package agentlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
)

type sessionAuth struct {
	ID      string          `json:"id"`
	Pair    *pairRequest    `json:"pair,omitempty"`
	Preview *previewRequest `json:"preview,omitempty"`
}

func decodeJSON(data []byte, value any) error {
	var raw json.RawMessage
	if err := protocol.DecodeStrict(data, &raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

// Authorize is mounted in the host's shared peer.Server. It runs only after the
// TLS transport has proved possession of the remote host's Ed25519 key.
func (s *Service) Authorize(ctx context.Context, remote peer.Identity, service string, raw json.RawMessage) (peer.Handler, error) {
	if service != ServiceName || remote.Validate() != nil {
		return nil, ErrUnauthorized
	}
	var auth sessionAuth
	if decodeJSON(raw, &auth) != nil {
		return nil, ErrInvalid
	}
	if auth.Preview != nil {
		if auth.ID != "" || auth.Pair != nil || auth.Preview.Code != "" || !validID(auth.Preview.Invitation) || !validNodeIdentity(auth.Preview.Target) || auth.Preview.Target.Address.NodeID != s.options.HostIdentity.NodeID {
			return nil, ErrInvalid
		}
		return s.previewHandler(*auth.Preview), nil
	}
	if !validID(auth.ID) {
		return nil, ErrInvalid
	}
	if auth.Pair != nil {
		if auth.Pair.ID != auth.ID || auth.Pair.Code != "" || !validID(auth.Pair.Invitation) || !validNodeIdentity(auth.Pair.Local) || !validNodeIdentity(auth.Pair.Peer) || auth.Pair.Local.Address.NodeID != remote.NodeID || auth.Pair.Peer.Address.NodeID != s.options.HostIdentity.NodeID {
			return nil, ErrConflict
		}
		return s.pairHandler(*auth.Pair, remote), nil
	}
	if _, err := s.authenticate(ctx, auth.ID, remote); err != nil {
		return nil, err
	}
	return s.handler(auth.ID, remote), nil
}

func (s *Service) authenticate(ctx context.Context, id string, remote peer.Identity) (Connection, error) {
	connection, err := s.Get(ctx, id)
	if err != nil {
		return Connection{}, ErrUnauthorized
	}
	if connection.SchemaVersion != 5 || connection.PeerHost.Validate() != nil {
		return connection, ErrReconnectRequired
	}
	if connection.PeerHost != remote || connection.Peer.Address.NodeID != remote.NodeID {
		return connection, ErrUnauthorized
	}
	if connection.State == "revoked" {
		return connection, ErrRevoked
	}
	if connection.State == "disconnected" {
		return connection, ErrDisconnected
	}
	return connection, nil
}

func (s *Service) handler(id string, remote peer.Identity) peer.Handler {
	return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
		connection, err := s.authenticate(ctx, id, remote)
		if err != nil && !(errors.Is(err, ErrRevoked) && request.Method == "revoke") {
			return nil, err
		}
		var result any
		switch request.Method {
		case "identity":
			var input struct{}
			if decodeJSON(request.Params, &input) != nil {
				return nil, ErrInvalid
			}
			result = connection
		case "directory", "observe", "task.submit", "task.status", "task.cancel", "task.input", "chat.status", "assistant.profile", "assistant.delete", "execution.review":
			return s.business(ctx, connection, remote, request)
		case "exchange":
			var input exchangeRequest
			if decodeJSON(request.Params, &input) != nil || input.ID != id {
				return nil, ErrInvalid
			}
			if !s.admit(id) {
				return nil, ErrUnavailable
			}
			defer s.release(id)
			result, err = s.exchange(ctx, connection, input)
		case "revoke":
			var input revokeRequest
			if decodeJSON(request.Params, &input) != nil || input.ID != id {
				return nil, ErrInvalid
			}
			err = s.revokeLocal(ctx, connection, false)
			result = map[string]string{"status": "revoked"}
		default:
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
}

// Attach registers a successfully authenticated, long-lived session. The host
// wires this into peer.Server.OnSession for incoming connections.
func (s *Service) Attach(session *peer.Session) {
	if session == nil || session.Service() != ServiceName {
		return
	}
	var auth sessionAuth
	if decodeJSON(session.Auth(), &auth) != nil {
		return
	}
	if auth.Preview != nil {
		time.AfterFunc(5*time.Second, session.Close)
		return
	}
	connection, err := s.Get(context.Background(), auth.ID)
	if errors.Is(err, ErrNotFound) && auth.Pair != nil {
		s.mu.Lock()
		s.pendingSessions[session] = true
		s.mu.Unlock()
		time.AfterFunc(10*time.Second, func() {
			s.mu.Lock()
			pending := s.pendingSessions[session]
			delete(s.pendingSessions, session)
			s.mu.Unlock()
			if pending {
				session.Close()
			}
		})
		go func() { <-session.Done(); s.mu.Lock(); delete(s.pendingSessions, session); s.mu.Unlock() }()
		return
	}
	if err != nil || connection.PeerHost != session.Remote() {
		session.Close()
		return
	}
	s.mu.Lock()
	delete(s.pendingSessions, session)
	previous := s.sessions[auth.ID]
	s.sessions[auth.ID] = session
	s.mu.Unlock()
	if previous != nil && previous != session {
		previous.Close()
	}
	go func() {
		<-session.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sessions[auth.ID] != session {
			return
		}
		delete(s.sessions, auth.ID)
		current, err := s.Get(ctx, auth.ID)
		if err == nil && current.State != "revoked" && current.State != "disconnected" {
			current.State = "offline"
			current.LastError = ErrUnavailable.Error()
			_ = s.update(ctx, current)
		}
	}()
}

func (s *Service) ensureSession(ctx context.Context, connection Connection) (*peer.Session, error) {
	if session := s.session(connection.ID); session != nil {
		return session, nil
	}
	if connection.SchemaVersion != 5 || connection.PeerHost.Validate() != nil {
		return nil, ErrReconnectRequired
	}
	if connection.Endpoint == "" {
		return nil, ErrUnavailable
	}
	auth := sessionAuth{ID: connection.ID}
	var originalPair *pairRequest
	if connection.LastSeen == nil {
		credential, err := s.secret(ctx, connection.ID)
		if err != nil {
			return nil, err
		}
		originalPair = credential.Pair
		if originalPair != nil {
			metadata := *originalPair
			metadata.Code = ""
			auth.Pair = &metadata
		}
	}
	raw, _ := json.Marshal(auth)
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := peer.Dial(dialCtx, connection.Endpoint, peer.DialOptions{Identity: s.options.HostIdentity, PrivateKey: s.options.HostKey, Expected: &connection.PeerHost, Service: ServiceName, Auth: raw, Handler: s.handler(connection.ID, connection.PeerHost)})
	if err != nil {
		return nil, transportError(err)
	}
	if originalPair != nil {
		var accepted Connection
		err = s.call(ctx, session, "pair", struct {
			Code string `json:"code"`
		}{Code: originalPair.Code}, &accepted)
		if err == nil && (accepted.ID != connection.ID || !equalIdentity(accepted.Local, connection.Peer) || !equalIdentity(accepted.Peer, connection.Local) || accepted.PeerHost != s.options.HostIdentity) {
			err = ErrConflict
		}
		if err != nil {
			session.Close()
			return nil, err
		}
	}
	s.Attach(session)
	return session, nil
}

func (s *Service) call(ctx context.Context, session *peer.Session, method string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := session.Call(callCtx, peer.Request{ID: newID(), Method: method, Params: data})
	if err != nil {
		return transportError(err)
	}
	if output != nil && decodeJSON(response, output) != nil {
		return ErrConflict
	}
	return nil
}

func transportError(err error) error {
	if errors.Is(err, peer.ErrUnauthorized) {
		return ErrUnauthorized
	}
	for _, known := range []error{ErrAssistantBusy, ErrAssistantChanged, ErrInvalid, ErrNotFound, ErrUnauthorized, ErrExpired, ErrUsed, ErrConflict, ErrRevoked, ErrReconnectRequired, ErrDisconnected, ErrUnsupported, ErrCapacity, ErrFenced} {
		if errors.Is(err, known) || strings.Contains(err.Error(), known.Error()) {
			return known
		}
	}
	return ErrUnavailable
}

func (s *Service) session(id string) *peer.Session {
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	if session != nil {
		select {
		case <-session.Done():
		default:
			return session
		}
	}
	return nil
}

func (s *Service) closeSessions() {
	s.mu.Lock()
	sessions := make([]*peer.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	for session := range s.pendingSessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

func (s *Service) admit(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	value := s.rates[id]
	if now.Sub(value.At) > time.Minute {
		value.At = now
		value.Count = 0
	}
	if value.Count >= 180 || value.Active >= 1 {
		return false
	}
	value.Count++
	value.Active++
	s.rates[id] = value
	return true
}
func (s *Service) release(id string) {
	s.mu.Lock()
	value := s.rates[id]
	value.Active--
	s.rates[id] = value
	s.mu.Unlock()
}
