package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"reflect"
	"time"
)

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

func validateMessage(value protocol.Envelope, from, to protocol.AgentRef) error {
	if value.Validate() != nil || value.Kind != "message" || value.From.NodeID != from.NodeID || value.To.NodeID != to.NodeID || value.From.Kind != "assistant" || value.To.Kind != "assistant" {
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
	if current.State == "revoked" || current.State == "disconnected" {
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
	now := time.Now().UTC()
	current.State = "ready"
	current.LastSeen = &now
	current.LastError = ""
	return response, s.update(ctx, current)
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
	if connection.State == "disconnected" {
		return ErrDisconnected
	}
	if connection.State == "revoked" {
		return ErrRevoked
	}
	if connection.PeerHost.Validate() != nil {
		return ErrReconnectRequired
	}
	previous := s.session(id)
	session, err := s.ensureSession(ctx, connection)
	if err != nil {
		return s.failed(ctx, connection, err)
	}
	fresh := previous != session || connection.State != "ready"
	if fresh || connection.LastSeen == nil {
		var accepted Connection
		err = s.call(ctx, session, "identity", struct{}{}, &accepted)
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
	if s.options.Backend != nil {
		var directory Directory
		if err = s.call(ctx, session, "directory", struct{}{}, &directory); err != nil {
			return s.failed(ctx, connection, err)
		}
		if err = validateDirectory(directory, connection.PeerHost.NodeID); err != nil {
			return err
		}
		connection.Directory = &directory
	}
	messages, err := s.pending(ctx, connection)
	if err != nil {
		return err
	}
	if len(messages) > protocol.MailPageSize {
		return ErrInvalid
	}
	if len(messages) == 0 && !fresh && connection.LastSeen != nil {
		return nil
	}
	for _, value := range messages {
		if err = validateMessage(value, connection.Local.Address, connection.Peer.Address); err != nil {
			return err
		}
	}
	var response exchangeResponse
	err = s.call(ctx, session, "exchange", exchangeRequest{ID: id, Messages: messages, Receipts: []protocol.Receipt{}}, &response)
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
	if err == nil && (current.State == "revoked" || current.State == "disconnected") {
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
		err = s.call(ctx, session, "exchange", exchangeRequest{ID: id, Messages: []protocol.Envelope{}, Receipts: receipts, AckOnly: true}, &ack)
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
	if (current.State == "revoked" || current.State == "disconnected") && pending && !current.RevocationPending {
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
	if session := s.sessions[current.ID]; session != nil {
		session.Close()
	}
	if s.options.Changed != nil {
		s.options.Changed()
	}
	return nil
}
func (s *Service) propagateRevoke(ctx context.Context, connection Connection) error {
	session, err := s.ensureSession(ctx, connection)
	if err != nil {
		if errors.Is(err, ErrRevoked) {
			return s.revokeLocal(ctx, connection, false)
		}
		return err
	}
	var response struct {
		Status string `json:"status"`
	}
	err = s.call(ctx, session, "revoke", revokeRequest{connection.ID}, &response)
	if err != nil && !errors.Is(err, ErrRevoked) {
		return err
	}
	return s.revokeLocal(ctx, connection, false)
}
