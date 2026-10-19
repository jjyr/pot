package agentlink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type Service struct {
	options Options
	client  *http.Client
	mu      sync.Mutex
	running sync.Mutex
	syncMu  sync.Mutex
	rates   map[string]rate
}
type rate struct {
	At     time.Time
	Count  int
	Active int
}
type address struct {
	Version    int      `json:"version"`
	Endpoint   string   `json:"endpoint"`
	Identity   Identity `json:"identity"`
	Invitation string   `json:"invitation"`
	Token      string   `json:"token"`
}
type credential struct {
	Token           string       `json:"token"`
	InvitationToken string       `json:"invitation_token,omitempty"`
	Pair            *pairRequest `json:"pair,omitempty"`
}
type pairRequest struct {
	ID         string   `json:"id"`
	Invitation string   `json:"invitation"`
	Local      Identity `json:"local"`
	Peer       Identity `json:"peer"`
	Token      string   `json:"token"`
}

func New(options Options) (*Service, error) {
	if options.DB == nil || options.Secrets == nil || options.Identity == nil || options.Mailbox == nil {
		return nil, ErrInvalid
	}
	if options.InvitationTTL == 0 {
		options.InvitationTTL = time.Hour
	}
	if options.InvitationTTL < time.Minute || options.InvitationTTL > 24*time.Hour {
		return nil, ErrInvalid
	}
	if options.PollInterval == 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.PollInterval < 100*time.Millisecond {
		return nil, ErrInvalid
	}
	return &Service{options: options, client: NewHTTPClient(options.HTTPClient), rates: map[string]rate{}}, nil
}

func validIdentity(identity Identity) bool {
	return identity.Address.Valid() && identity.Address.Kind == "assistant" && (identity.Adapter == "pot" || identity.Adapter == "codex") && strings.TrimSpace(identity.Name) != "" && utf8.ValidString(identity.Name) && len(identity.Name) <= 512
}
func equalIdentity(a, b Identity) bool { return a.Address == b.Address && a.Adapter == b.Adapter }
func newID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func validToken(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32
}
func digest(value []byte) string { hash := sha256.Sum256(value); return hex.EncodeToString(hash[:]) }
func route(local, peer protocol.AgentRef) string {
	value, _ := json.Marshal([]protocol.AgentRef{local, peer})
	return digest(value)
}
func validID(id string) bool { return len(id) == 48 && strings.Trim(id, "0123456789abcdef") == "" }
func parseAddress(ctx context.Context, raw string) (address, error) {
	var value address
	if len(raw) > 16384 {
		return value, ErrInvalid
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return value, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(u.Fragment)
	if err != nil || decodeJSON(data, &value) != nil || value.Version != 1 || !validIdentity(value.Identity) || !validID(value.Invitation) || !validToken(value.Token) {
		return address{}, ErrInvalid
	}
	origin, err := ValidateEndpoint(ctx, value.Endpoint)
	if err != nil || u.Scheme+"://"+u.Host+u.Path != origin+Prefix+"/connect" || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" || u.User != nil {
		return address{}, ErrInvalid
	}
	value.Endpoint = origin
	return value, nil
}

func (s *Service) Invite(ctx context.Context, assistantID, endpoint string) (Invitation, error) {
	identity, err := s.options.Identity(ctx, assistantID)
	if err != nil {
		return Invitation{}, err
	}
	if !validIdentity(identity) {
		return Invitation{}, ErrInvalid
	}
	endpoint, err = ValidateEndpoint(ctx, endpoint)
	if err != nil {
		return Invitation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(identity)
	var id, ref string
	var expires int64
	err = s.options.DB.QueryRowContext(ctx, `SELECT id,secret_ref,expires_at FROM agent_link_invitations WHERE identity=? AND endpoint=? AND expires_at>? AND claim_hash='' ORDER BY expires_at DESC LIMIT 1`, string(raw), endpoint, time.Now().UnixMilli()).Scan(&id, &ref, &expires)
	var secret []byte
	if errors.Is(err, sql.ErrNoRows) {
		id = newID()
		ref = "agent-link/invitation/" + id
		secret = []byte(token())
		expires = time.Now().Add(s.options.InvitationTTL).UnixMilli()
		if err = s.options.Secrets.Put(ctx, ref, secret); err != nil {
			return Invitation{}, err
		}
		_, err = s.options.DB.ExecContext(ctx, `INSERT INTO agent_link_invitations(id,identity,endpoint,expires_at,secret_ref)VALUES(?,?,?,?,?)`, id, string(raw), endpoint, expires, ref)
	} else if err == nil {
		secret, err = s.options.Secrets.Get(ctx, ref)
	}
	if err != nil {
		return Invitation{}, err
	}
	defer clear(secret)
	value, _ := json.Marshal(address{1, endpoint, identity, id, string(secret)})
	return Invitation{Address: endpoint + Prefix + "/connect#" + base64.RawURLEncoding.EncodeToString(value), ExpiresAt: time.UnixMilli(expires).UTC()}, nil
}

func (s *Service) List(ctx context.Context) ([]Connection, error) {
	rows, err := s.options.DB.QueryContext(ctx, `SELECT metadata FROM agent_link_connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []Connection{}
	for rows.Next() {
		var raw []byte
		var value Connection
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = decodeJSON(raw, &value); err != nil {
			return nil, err
		}
		connections = append(connections, value)
	}
	return connections, rows.Err()
}
func (s *Service) Get(ctx context.Context, id string) (Connection, error) {
	var raw []byte
	var value Connection
	err := s.options.DB.QueryRowContext(ctx, `SELECT metadata FROM agent_link_connections WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrNotFound
	}
	if err != nil {
		return value, err
	}
	err = decodeJSON(raw, &value)
	return value, err
}
func (s *Service) save(ctx context.Context, c Connection, cred credential) error {
	ref := "agent-link/connection/" + c.ID
	rawSecret, _ := json.Marshal(cred)
	if err := s.options.Secrets.Put(ctx, ref, rawSecret); err != nil {
		return err
	}
	raw, _ := json.Marshal(c)
	_, err := s.options.DB.ExecContext(ctx, `INSERT INTO agent_link_connections(id,route,state,metadata,secret_ref,token_hash)VALUES(?,?,?,?,?,?)`, c.ID, route(c.Local.Address, c.Peer.Address), c.State, string(raw), ref, digest([]byte(cred.Token)))
	return err
}
func (s *Service) secret(ctx context.Context, id string) (credential, error) {
	var ref string
	var value credential
	err := s.options.DB.QueryRowContext(ctx, `SELECT secret_ref FROM agent_link_connections WHERE id=?`, id).Scan(&ref)
	if err != nil {
		return value, err
	}
	raw, err := s.options.Secrets.Get(ctx, ref)
	if err != nil {
		return value, err
	}
	defer clear(raw)
	err = decodeJSON(raw, &value)
	return value, err
}
func (s *Service) update(ctx context.Context, c Connection) error {
	c.UpdatedAt = time.Now().UTC()
	raw, _ := json.Marshal(c)
	result, err := s.options.DB.ExecContext(ctx, `UPDATE agent_link_connections SET state=?,metadata=? WHERE id=? AND state!='revoked'`, c.State, string(raw), c.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrRevoked
	}
	if err == nil && s.options.Changed != nil {
		s.options.Changed()
	}
	return err
}

func (s *Service) Connect(ctx context.Context, assistantID, raw string) (Connection, error) {
	address, err := parseAddress(ctx, raw)
	if err != nil {
		return Connection{}, err
	}
	local, err := s.options.Identity(ctx, assistantID)
	if err != nil {
		return Connection{}, err
	}
	if !validIdentity(local) || local.Address.ControlID == address.Identity.Address.ControlID {
		return Connection{}, ErrInvalid
	}
	s.mu.Lock()
	var id string
	err = s.options.DB.QueryRowContext(ctx, `SELECT id FROM agent_link_connections WHERE route=? AND state!='revoked'`, route(local.Address, address.Identity.Address)).Scan(&id)
	var value Connection
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC()
		value = Connection{ID: newID(), Local: local, Peer: address.Identity, Endpoint: address.Endpoint, State: "connecting", CreatedAt: now, UpdatedAt: now}
		request := pairRequest{ID: value.ID, Invitation: address.Invitation, Local: local, Peer: address.Identity, Token: token()}
		err = s.save(ctx, value, credential{Token: request.Token, InvitationToken: address.Token, Pair: &request})
	} else if err == nil {
		value, err = s.Get(ctx, id)
		// A definitively expired invitation never established a remote binding.
		// A newly copied invitation may replace that failed intent; ambiguous
		// network failures keep their original idempotent handshake instead.
		if err == nil && value.LastSeen == nil && value.LastError == ErrExpired.Error() {
			previous, secretErr := s.secret(ctx, value.ID)
			if secretErr != nil {
				err = secretErr
			} else if previous.Pair != nil && previous.Pair.Invitation != address.Invitation {
				value.State = "revoked"
				err = s.update(ctx, value)
				if err == nil {
					err = s.options.Secrets.Delete(ctx, "agent-link/connection/"+value.ID)
				}
				if err == nil {
					now := time.Now().UTC()
					value = Connection{ID: newID(), Local: local, Peer: address.Identity, Endpoint: address.Endpoint, State: "connecting", CreatedAt: now, UpdatedAt: now}
					request := pairRequest{ID: value.ID, Invitation: address.Invitation, Local: local, Peer: address.Identity, Token: token()}
					err = s.save(ctx, value, credential{Token: request.Token, InvitationToken: address.Token, Pair: &request})
				}
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return value, err
	}
	if err = s.Sync(ctx, value.ID); err != nil {
		return value, err
	}
	return s.Get(ctx, value.ID)
}

func (s *Service) accept(ctx context.Context, request pairRequest, provided string) (Connection, error) {
	if !validID(request.ID) || !validID(request.Invitation) || !validIdentity(request.Local) || !validIdentity(request.Peer) || !validToken(request.Token) || !validToken(provided) || request.Local.Address.ControlID == request.Peer.Address.ControlID {
		return Connection{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rawIdentity []byte
	var endpoint, ref, claim, connectionID string
	var expiry int64
	err := s.options.DB.QueryRowContext(ctx, `SELECT identity,endpoint,expires_at,secret_ref,claim_hash,connection_id FROM agent_link_invitations WHERE id=?`, request.Invitation).Scan(&rawIdentity, &endpoint, &expiry, &ref, &claim, &connectionID)
	if err != nil {
		return Connection{}, ErrUnauthorized
	}
	var identity Identity
	if decodeJSON(rawIdentity, &identity) != nil || !equalIdentity(identity, request.Peer) {
		return Connection{}, ErrConflict
	}
	current, err := s.options.Identity(ctx, identity.Address.AgentID)
	if err != nil || !equalIdentity(current, identity) {
		return Connection{}, ErrConflict
	}
	secret, err := s.options.Secrets.Get(ctx, ref)
	if err != nil {
		return Connection{}, err
	}
	defer clear(secret)
	if subtle.ConstantTimeCompare(secret, []byte(provided)) != 1 {
		return Connection{}, ErrUnauthorized
	}
	requestRaw, _ := json.Marshal(request)
	hash := digest(requestRaw)
	if claim != "" {
		if claim != hash {
			return Connection{}, ErrConflict
		}
		return s.Get(ctx, connectionID)
	}
	if expiry <= time.Now().UnixMilli() {
		return Connection{}, ErrExpired
	}
	now := time.Now().UTC()
	value := Connection{ID: request.ID, Local: current, Peer: request.Local, State: "connecting", CreatedAt: now, UpdatedAt: now}
	credential := credential{Token: request.Token}
	secretRaw, _ := json.Marshal(credential)
	secretRef := "agent-link/connection/" + value.ID
	if err = s.options.Secrets.Put(ctx, secretRef, secretRaw); err != nil {
		return Connection{}, err
	}
	metadata, _ := json.Marshal(value)
	err = storage.Transaction(ctx, s.options.DB, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_link_connections(id,route,state,metadata,secret_ref,token_hash)VALUES(?,?,?,?,?,?)`, value.ID, route(value.Local.Address, value.Peer.Address), value.State, string(metadata), secretRef, digest([]byte(request.Token)))
		if err != nil {
			return ErrConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE agent_link_invitations SET claim_hash=?,connection_id=? WHERE id=? AND claim_hash=''`, hash, value.ID, request.Invitation)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			return ErrConflict
		}
		return err
	})
	if err == nil && s.options.Changed != nil {
		s.options.Changed()
	}
	return value, err
}

// Run supervises connections belonging to this host independently of its UI.
// Only the side with Endpoint dials; both message directions use that channel.
func (s *Service) Run(ctx context.Context) {
	if !s.running.TryLock() {
		return
	}
	defer s.running.Unlock()
	ticker := time.NewTicker(s.options.PollInterval)
	defer ticker.Stop()
	for {
		values, err := s.List(ctx)
		if err == nil {
			for _, c := range values {
				if ctx.Err() != nil {
					return
				}
				if c.Endpoint != "" {
					if c.State != "revoked" {
						_ = s.Sync(ctx, c.ID)
					} else if c.RevocationPending {
						_ = s.propagateRevoke(ctx, c)
					}
				} else if c.State == "ready" {
					s.expireListener(ctx, c.ID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) expireListener(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Get(ctx, id)
	if err == nil && current.State == "ready" && current.LastSeen != nil && time.Since(*current.LastSeen) > 30*time.Second {
		current.State = "offline"
		current.LastError = ErrUnavailable.Error()
		_ = s.update(ctx, current)
	}
}
