package agentlink

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

const maxBody = 5 * 1024 * 1024

type exchangeRequest struct {
	ID       string              `json:"id"`
	Messages []protocol.Envelope `json:"messages"`
	Receipts []protocol.Receipt  `json:"receipts"`
	AckOnly  bool                `json:"ack_only,omitempty"`
}
type exchangeResponse struct {
	Messages []protocol.Envelope `json:"messages"`
	Receipts []protocol.Receipt  `json:"receipts"`
}
type revokeRequest struct {
	ID string `json:"id"`
}

func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	code, status := "agent_link_failed", http.StatusInternalServerError
	for _, item := range []struct {
		err    error
		status int
	}{{ErrInvalid, 400}, {ErrNotFound, 404}, {ErrUnauthorized, 401}, {ErrExpired, 410}, {ErrConflict, 409}, {ErrRevoked, 410}, {ErrUnavailable, 503}} {
		if errors.Is(err, item.err) {
			code, status = item.err.Error(), item.status
			break
		}
	}
	writeJSON(w, status, map[string]string{"error": code})
}
func decodeRequest(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	data, err := io.ReadAll(r.Body)
	if err != nil || decodeJSON(data, value) != nil {
		return ErrInvalid
	}
	return nil
}

// core's shape checker deliberately models structs as objects. Connection
// timestamps use time.Time's string encoding, so validate duplicate fields and
// syntax as raw JSON first, then decode the typed shape with unknown rejection.
func decodeJSON(data []byte, value any) error {
	var raw json.RawMessage
	if err := protocol.DecodeStrict(data, &raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(value)
}
func (s *Service) authenticate(ctx context.Context, id, token string) (Connection, error) {
	if !validID(id) || !validToken(token) {
		return Connection{}, ErrUnauthorized
	}
	var hash string
	if s.options.DB.QueryRowContext(ctx, `SELECT token_hash FROM agent_link_connections WHERE id=?`, id).Scan(&hash) != nil {
		return Connection{}, ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(digest([]byte(token)))) != 1 {
		return Connection{}, ErrUnauthorized
	}
	connection, err := s.Get(ctx, id)
	if err != nil {
		return connection, err
	}
	if connection.State == "revoked" {
		return connection, ErrRevoked
	}
	current, err := s.options.Identity(ctx, connection.Local.Address.AgentID)
	if err != nil || !equalIdentity(current, connection.Local) {
		return connection, ErrConflict
	}
	return connection, nil
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

func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, ErrNotFound)
		return
	}
	// Deliberately no CORS and no cookie authentication: this is not an owner API.
	if r.Header.Get("Origin") != "" {
		writeError(w, ErrUnauthorized)
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !validToken(provided) {
		writeError(w, ErrUnauthorized)
		return
	}
	switch r.URL.Path {
	case Prefix + "/pair":
		var request pairRequest
		if err := decodeRequest(w, r, &request); err != nil {
			writeError(w, err)
			return
		}
		connection, err := s.accept(r.Context(), request, provided)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, connection)
	case Prefix + "/exchange":
		var request exchangeRequest
		if err := decodeRequest(w, r, &request); err != nil {
			writeError(w, err)
			return
		}
		connection, err := s.authenticate(r.Context(), request.ID, provided)
		if err != nil {
			writeError(w, err)
			return
		}
		if !s.admit(request.ID) {
			w.Header().Set("Retry-After", "2")
			writeJSON(w, 429, map[string]string{"error": "agent_link_busy"})
			return
		}
		defer s.release(request.ID)
		response, err := s.exchange(r.Context(), connection, request)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, response)
	case Prefix + "/revoke":
		var request revokeRequest
		if err := decodeRequest(w, r, &request); err != nil {
			writeError(w, err)
			return
		}
		connection, err := s.authenticate(r.Context(), request.ID, provided)
		if err != nil && !errors.Is(err, ErrRevoked) {
			writeError(w, err)
			return
		}
		if err = s.revokeLocal(r.Context(), connection, false); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "revoked"})
	default:
		writeError(w, ErrNotFound)
	}
}
func validateMessage(value protocol.Envelope, from, to protocol.AgentRef) error {
	if value.Validate() != nil || value.Kind != "message" || value.From != from || value.To != to {
		return ErrConflict
	}
	return nil
}
func (s *Service) exchange(ctx context.Context, connection Connection, request exchangeRequest) (exchangeResponse, error) {
	response := exchangeResponse{Messages: []protocol.Envelope{}, Receipts: []protocol.Receipt{}}
	if len(request.Messages) > protocol.MailPageSize || len(request.Receipts) > protocol.MailPageSize || request.AckOnly && len(request.Messages) > 0 {
		return response, ErrInvalid
	}
	// Validate the complete batch before mutating any business message.
	for _, value := range request.Messages {
		if err := validateMessage(value, connection.Peer.Address, connection.Local.Address); err != nil {
			return response, err
		}
	}
	for _, value := range request.Receipts {
		if err := validateMessage(value.Message, connection.Local.Address, connection.Peer.Address); err != nil {
			return response, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Get(ctx, connection.ID)
	if err != nil {
		return response, err
	}
	if current.State == "revoked" {
		return response, ErrRevoked
	}
	for _, value := range request.Receipts {
		if err = s.options.Mailbox.Acknowledge(ctx, value); err != nil {
			return response, ErrConflict
		}
	}
	for _, value := range request.Messages {
		receipt, err := s.options.Mailbox.Deliver(ctx, value)
		if err != nil {
			return response, err
		}
		if !reflect.DeepEqual(receipt.Message, value) {
			return response, ErrConflict
		}
		response.Receipts = append(response.Receipts, receipt)
	}
	if !request.AckOnly {
		response.Messages, err = s.options.Mailbox.Pending(ctx, connection.Local.Address, connection.Peer.Address)
		if err != nil {
			return response, err
		}
		if len(response.Messages) > protocol.MailPageSize {
			return response, ErrInvalid
		}
		for _, value := range response.Messages {
			if err = validateMessage(value, connection.Local.Address, connection.Peer.Address); err != nil {
				return response, err
			}
		}
	}
	now := time.Now().UTC()
	current.State = "ready"
	current.LastSeen = &now
	current.LastError = ""
	return response, s.update(ctx, current)
}

func (s *Service) post(ctx context.Context, endpoint, path, token string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+Prefix+path, bytes.NewReader(data))
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return ErrUnavailable
	}
	if response.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		for _, known := range []error{ErrInvalid, ErrNotFound, ErrUnauthorized, ErrExpired, ErrConflict, ErrRevoked} {
			if known.Error() == failure.Error {
				return known
			}
		}
		return ErrUnavailable
	}
	if output != nil && decodeJSON(data, output) != nil {
		return ErrConflict
	}
	return nil
}

// Sync performs one bounded delivery pass. Durable pending envelopes are the
// recovery cursor: an interrupted pass resends the original, immutable message.
func (s *Service) Sync(ctx context.Context, id string) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	connection, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if connection.State == "revoked" {
		return ErrRevoked
	}
	if connection.Endpoint == "" {
		return nil
	}
	credential, err := s.secret(ctx, id)
	if err != nil {
		return err
	}
	if connection.LastSeen == nil && credential.Pair != nil {
		var accepted Connection
		err = s.post(ctx, connection.Endpoint, "/pair", credential.InvitationToken, *credential.Pair, &accepted)
		if err == nil && accepted.State == "revoked" {
			err = ErrRevoked
		}
		if err == nil && (accepted.ID != connection.ID || !equalIdentity(accepted.Local, connection.Peer) || !equalIdentity(accepted.Peer, connection.Local)) {
			err = ErrConflict
		}
		if err != nil {
			return s.failed(ctx, connection, err)
		}
	}
	messages, err := s.options.Mailbox.Pending(ctx, connection.Local.Address, connection.Peer.Address)
	if err != nil {
		return err
	}
	if len(messages) > protocol.MailPageSize {
		return ErrInvalid
	}
	for _, value := range messages {
		if err = validateMessage(value, connection.Local.Address, connection.Peer.Address); err != nil {
			return err
		}
	}
	var response exchangeResponse
	err = s.post(ctx, connection.Endpoint, "/exchange", credential.Token, exchangeRequest{ID: id, Messages: messages, Receipts: []protocol.Receipt{}}, &response)
	if err != nil {
		return s.failed(ctx, connection, err)
	}
	if len(response.Messages) > protocol.MailPageSize || len(response.Receipts) > len(messages) {
		return s.failed(ctx, connection, ErrConflict)
	}
	for _, value := range response.Messages {
		if err = validateMessage(value, connection.Peer.Address, connection.Local.Address); err != nil {
			return s.failed(ctx, connection, err)
		}
	}
	for _, receipt := range response.Receipts {
		matched := false
		for _, value := range messages {
			if reflect.DeepEqual(value, receipt.Message) {
				matched = true
				break
			}
		}
		if !matched {
			return s.failed(ctx, connection, ErrConflict)
		}
	}
	// Check cancellation/revocation again after network I/O and before admission.
	s.mu.Lock()
	current, err := s.Get(ctx, id)
	if err == nil && current.State == "revoked" {
		err = ErrRevoked
	}
	if err == nil {
		for _, receipt := range response.Receipts {
			if err = s.options.Mailbox.Acknowledge(ctx, receipt); err != nil {
				break
			}
		}
	}
	receipts := []protocol.Receipt{}
	if err == nil {
		for _, value := range response.Messages {
			var receipt protocol.Receipt
			receipt, err = s.options.Mailbox.Deliver(ctx, value)
			if err != nil {
				break
			}
			if !reflect.DeepEqual(value, receipt.Message) {
				err = ErrConflict
				break
			}
			receipts = append(receipts, receipt)
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if len(receipts) > 0 {
		var ack exchangeResponse
		err = s.post(ctx, connection.Endpoint, "/exchange", credential.Token, exchangeRequest{ID: id, Messages: []protocol.Envelope{}, Receipts: receipts, AckOnly: true}, &ack)
		if err != nil {
			return s.failed(ctx, connection, err)
		}
		if len(ack.Messages) != 0 || len(ack.Receipts) != 0 {
			return s.failed(ctx, connection, ErrConflict)
		}
	}
	now := time.Now().UTC()
	connection.LastSeen = &now
	connection.State = "ready"
	connection.LastError = ""
	return s.update(ctx, connection)
}
func (s *Service) failed(ctx context.Context, connection Connection, err error) error {
	if errors.Is(err, ErrRevoked) {
		_ = s.revokeLocal(ctx, connection, false)
		return err
	}
	connection.State = "offline"
	connection.LastError = ErrUnavailable.Error()
	for _, known := range []error{ErrConflict, ErrUnauthorized, ErrExpired, ErrInvalid} {
		if errors.Is(err, known) {
			connection.LastError = known.Error()
		}
	}
	_ = s.update(ctx, connection)
	return err
}

// Revoke fences local delivery first. If offline, the encrypted credential is
// retained only to notify the peer later; revocation never deletes mail history.
func (s *Service) Revoke(ctx context.Context, id string) error {
	connection, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err = s.revokeLocal(ctx, connection, connection.Endpoint != ""); err != nil {
		return err
	}
	if connection.Endpoint != "" {
		connection, err = s.Get(ctx, id)
		if err == nil {
			_ = s.propagateRevoke(ctx, connection)
		}
	}
	return nil
}
func (s *Service) revokeLocal(ctx context.Context, connection Connection, pending bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Get(ctx, connection.ID)
	if err != nil {
		return err
	}
	if current.State == "revoked" && pending && !current.RevocationPending {
		return nil
	}
	current.State = "revoked"
	current.LastError = ""
	current.RevocationPending = pending
	current.UpdatedAt = time.Now().UTC()
	raw, _ := json.Marshal(current)
	_, err = s.options.DB.ExecContext(ctx, `UPDATE agent_link_connections SET state='revoked',metadata=? WHERE id=?`, string(raw), current.ID)
	if err != nil {
		return err
	}
	if !pending {
		if err = s.options.Secrets.Delete(ctx, "agent-link/connection/"+current.ID); err != nil {
			return err
		}
	}
	if s.options.Changed != nil {
		s.options.Changed()
	}
	return nil
}
func (s *Service) propagateRevoke(ctx context.Context, connection Connection) error {
	credential, err := s.secret(ctx, connection.ID)
	if err != nil {
		return err
	}
	var response struct {
		Status string `json:"status"`
	}
	err = s.post(ctx, connection.Endpoint, "/revoke", credential.Token, revokeRequest{connection.ID}, &response)
	if err != nil && !errors.Is(err, ErrRevoked) {
		return err
	}
	return s.revokeLocal(ctx, connection, false)
}
