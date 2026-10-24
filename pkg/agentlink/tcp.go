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
	if !s.enterRPC() {
		return nil, ErrUnavailable
	}
	defer s.rpcWG.Done()
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
	return s.protectHandler(func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
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
		case "directory", "observe", "task.submit", "task.status", "task.cancel", "task.input", "chat.status", "assistant.profile", "assistant.delete", "assistant.management", "execution.review":
			return s.business(ctx, connection, remote, request)
		case "exchange":
			var input exchangeRequest
			if decodeJSON(request.Params, &input) != nil || input.ID != id {
				return nil, ErrInvalid
			}
			if !s.admit(id, controlAdmission) {
				return nil, ErrUnavailable
			}
			defer s.release(id, controlAdmission)
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
	})
}

// Attach registers a successfully authenticated, long-lived session. The host
// wires this into peer.Server.OnSession for incoming connections.
func (s *Service) Attach(session *peer.Session) {
	if session == nil || session.Service() != ServiceName {
		return
	}
	var auth sessionAuth
	if decodeJSON(session.Auth(), &auth) != nil {
		session.Close()
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		session.Close()
		return
	}
	if auth.Preview != nil {
		s.trackPending(session, 5*time.Second)
		s.mu.Unlock()
		return
	}
	connection, err := s.Get(context.Background(), auth.ID)
	if errors.Is(err, ErrNotFound) && auth.Pair != nil {
		s.trackPending(session, 10*time.Second)
		s.mu.Unlock()
		return
	}
	if err != nil || connection.PeerHost != session.Remote() || connection.State == "disconnected" || connection.State == "revoked" {
		s.mu.Unlock()
		session.Close()
		return
	}
	delete(s.pendingSessions, session)
	s.mu.Unlock()
	f, err := s.forwarder(connection)
	if err != nil {
		session.Close()
		return
	}
	_ = f.transition(s.ctx, nil, session, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return ErrUnavailable
		}
		current, err := s.Get(s.ctx, auth.ID)
		if err != nil {
			return err
		}
		if current.State == "disconnected" || current.State == "revoked" || current.PeerHost != session.Remote() {
			return ErrDisconnected
		}
		return nil
	})
}

// Caller holds mu. Pending handshakes do not own a persisted connection yet.
func (s *Service) trackPending(session *peer.Session, lifetime time.Duration) {
	s.pendingSessions[session] = true
	timer := time.AfterFunc(lifetime, func() { s.expirePending(session) })
	s.pendingWG.Add(1)
	go func() {
		defer s.pendingWG.Done()
		<-session.Done()
		timer.Stop()
		s.mu.Lock()
		delete(s.pendingSessions, session)
		s.mu.Unlock()
	}()
}

// Promotion removes the pending marker. Its handshake timeout must then leave
// the selected, long-lived session alone.
func (s *Service) expirePending(session *peer.Session) {
	s.mu.Lock()
	pending := s.pendingSessions[session]
	delete(s.pendingSessions, session)
	s.mu.Unlock()
	if pending {
		session.Close()
	}
}

func (s *Service) forwarder(c Connection) (*connectionForwarder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrUnavailable
	}
	if f := s.forwarders[c.ID]; f != nil {
		return f, nil
	}
	f := newConnectionForwarder(s.ctx, func(ctx context.Context) (connectionTransport, error) {
		session, err := s.dialSession(ctx, c)
		if err != nil {
			return nil, err
		}
		return session, nil
	}, func(transport connectionTransport) {
		session := transport.(*peer.Session)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			session.Close()
			return
		}
		s.sessions[c.ID] = session
	}, func(transport connectionTransport) {
		session := transport.(*peer.Session)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sessions[c.ID] != session {
			return
		}
		delete(s.sessions, c.ID)
		if s.closed {
			return
		}
		current, err := s.Get(context.Background(), c.ID)
		if err == nil && current.State != "revoked" && current.State != "disconnected" {
			current.State = "offline"
			current.LastError = ErrUnavailable.Error()
			_ = s.update(context.Background(), current)
		}
	})
	s.forwarders[c.ID] = f
	s.forwardWG.Add(1)
	go func() { defer s.forwardWG.Done(); <-f.done }()
	return f, nil
}
func (s *Service) callConnection(ctx context.Context, c Connection, method string, input, output any) error {
	control := method == "identity" || method == "exchange" || method == "revoke"
	if c.State == "disconnected" {
		return ErrDisconnected
	}
	if c.State == "revoked" && method != "revoke" {
		return ErrRevoked
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if len(data) > peer.MaxPayload {
		return ErrInvalid
	}
	f, err := s.forwarder(c)
	if err != nil {
		return err
	}
	result, err := f.call(ctx, peer.Request{ID: newID(), Method: method, Params: data}, control)
	if err != nil {
		return transportError(err)
	}
	if output != nil && decodeJSON(result.data, output) != nil {
		return ErrConflict
	}
	return nil
}

// Only transport integration tests inspect a connection directly. Production
// callers submit typed requests through callConnection.
func (s *Service) ensureSession(ctx context.Context, c Connection) (*peer.Session, error) {
	f, err := s.forwarder(c)
	if err != nil {
		return nil, err
	}
	result, err := f.call(ctx, peer.Request{}, false)
	if err != nil {
		return nil, err
	}
	return result.session.(*peer.Session), nil
}
func (s *Service) dialSession(ctx context.Context, connection Connection) (*peer.Session, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	current, err := s.Get(ctx, connection.ID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	connection = current
	if connection.State == "disconnected" {
		s.mu.Unlock()
		return nil, ErrDisconnected
	}
	if connection.SchemaVersion != 5 || connection.PeerHost.Validate() != nil {
		s.mu.Unlock()
		return nil, ErrReconnectRequired
	}
	if connection.Endpoint == "" {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	auth := sessionAuth{ID: connection.ID}
	var originalPair *pairRequest
	if connection.LastSeen == nil {
		credential, e := s.secret(ctx, connection.ID)
		if e != nil {
			s.mu.Unlock()
			return nil, e
		}
		originalPair = credential.Pair
		if originalPair != nil {
			metadata := *originalPair
			metadata.Code = ""
			auth.Pair = &metadata
		}
	}
	s.mu.Unlock()
	raw, _ := json.Marshal(auth)
	session, err := peer.Dial(ctx, connection.Endpoint, peer.DialOptions{Identity: s.options.HostIdentity, PrivateKey: s.options.HostKey, Expected: &connection.PeerHost, Service: ServiceName, Auth: raw, Handler: s.handler(connection.ID, connection.PeerHost), Context: s.ctx})
	if err != nil {
		return nil, transportError(err)
	}
	if originalPair != nil {
		var accepted Connection
		err = s.call(ctx, session, "pair", struct {
			Code string `json:"code"`
		}{originalPair.Code}, &accepted)
		if err == nil && (accepted.ID != connection.ID || !equalIdentity(accepted.Local, connection.Peer) || !equalIdentity(accepted.Peer, connection.Local) || accepted.PeerHost != s.options.HostIdentity) {
			err = ErrConflict
		}
		if err != nil {
			session.Close()
			return nil, err
		}
	}
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
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, peer.ErrUncertain) {
		return err
	}
	if errors.Is(err, peer.ErrUnauthorized) {
		return ErrUnauthorized
	}
	for _, known := range []error{ErrModelUnavailable, ErrWorkspaceUnavailable, ErrAssistantBusy, ErrAssistantChanged, ErrInvalid, ErrNotFound, ErrUnauthorized, ErrExpired, ErrUsed, ErrConflict, ErrRevoked, ErrReconnectRequired, ErrDisconnected, ErrUnsupported, ErrCapacity, ErrFenced} {
		if errors.Is(err, known) || strings.Contains(err.Error(), known.Error()) {
			return known
		}
	}
	return ErrUnavailable
}

func (s *Service) session(id string) *peer.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	session := s.sessions[id]
	if session != nil {
		select {
		case <-session.Done():
		default:
			return session
		}
	}
	return nil
}

// Close fences persisted callbacks and joins owned request/dial workers before
// the caller releases databases. Hosts stop Run and incoming listeners first.
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		pending := make([]*peer.Session, 0, len(s.pendingSessions))
		for session := range s.pendingSessions {
			pending = append(pending, session)
		}
		s.mu.Unlock()
		s.cancel()
		for _, session := range pending {
			session.Close()
		}
		s.forwardWG.Wait()
		s.pendingWG.Wait()
		s.rpcWG.Wait()
	})
}
func (s *Service) closeSessions() {
	s.mu.Lock()
	values := make([]*connectionForwarder, 0, len(s.forwarders))
	for _, f := range s.forwarders {
		values = append(values, f)
	}
	pending := make([]*peer.Session, 0, len(s.pendingSessions))
	for session := range s.pendingSessions {
		pending = append(pending, session)
	}
	s.mu.Unlock()
	for _, f := range values {
		_ = f.change(context.Background(), ErrUnavailable, nil)
	}
	for _, session := range pending {
		session.Close()
	}
}
func (s *Service) changeConnection(ctx context.Context, c Connection, reason error) error {
	f, err := s.forwarder(c)
	if err != nil {
		return err
	}
	return f.change(ctx, reason, nil)
}
func (s *Service) enterRPC() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.rpcWG.Add(1)
	return true
}
func (s *Service) protectHandler(handler peer.Handler) peer.Handler {
	return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
		if !s.enterRPC() {
			return nil, ErrUnavailable
		}
		defer s.rpcWG.Done()
		return handler(ctx, request)
	}
}

type admissionKind int

const (
	controlAdmission admissionKind = iota
	businessAdmission

	maxConcurrentBusinessRequests = 32
	maxPreviewRequestsPerMinute   = 180
)

func (s *Service) admit(id string, kind admissionKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.admissions[id]
	limit := 1
	if kind == businessAdmission {
		limit = maxConcurrentBusinessRequests
	}
	if value.Active[kind] >= limit {
		return false
	}
	value.Active[kind]++
	s.admissions[id] = value
	return true
}
func (s *Service) release(id string, kind admissionKind) {
	s.mu.Lock()
	value := s.admissions[id]
	value.Active[kind]--
	s.admissions[id] = value
	s.mu.Unlock()
}

func (s *Service) admitPreview(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.previewRates == nil {
		s.previewRates = map[string]previewRate{}
	}
	value := s.previewRates[id]
	now := time.Now()
	if now.Sub(value.At) >= time.Minute {
		value.At = now
		value.Count = 0
	}
	if value.Active >= 1 || value.Count >= maxPreviewRequestsPerMinute {
		return false
	}
	value.Active++
	value.Count++
	s.previewRates[id] = value
	return true
}
func (s *Service) releasePreview(id string) {
	s.mu.Lock()
	value := s.previewRates[id]
	value.Active--
	s.previewRates[id] = value
	s.mu.Unlock()
}
