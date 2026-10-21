package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
)

var ErrCapacity = errors.New("agent_link_capacity_exceeded")
var ErrFenced = errors.New("agent_link_execution_fenced")
var ErrUnsupported = errors.New("agent_link_capability_unsupported")
var ErrDisconnected = errors.New("agent_link_disconnected")

func validateDirectory(d Directory, node string) error {
	if d.NodeID != node || len(d.Assistants) > 256 || d.Name == "" || (d.Adapter != "pot" && d.Adapter != "codex") {
		return ErrConflict
	}
	seen := map[string]bool{}
	for _, a := range d.Assistants {
		if !validIdentity(a) || a.Address.Kind != "assistant" || a.Address.NodeID != node || a.Adapter != d.Adapter || seen[a.Address.AgentID] {
			return ErrConflict
		}
		seen[a.Address.AgentID] = true
	}
	if d.DefaultAssistantID != "" && !seen[d.DefaultAssistantID] {
		return ErrConflict
	}
	return nil
}
func supports(d Directory, capability string) bool {
	for _, c := range d.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}
func (s *Service) localDirectory(ctx context.Context) (Directory, error) {
	if s.options.Backend == nil {
		return Directory{}, ErrUnsupported
	}
	d, e := s.options.Backend.Directory(ctx)
	if e == nil {
		e = validateDirectory(d, s.options.HostIdentity.NodeID)
	}
	return d, e
}
func (s *Service) pending(ctx context.Context, c Connection) ([]protocol.Envelope, error) {
	return s.options.Mailbox.PendingNode(ctx, c.Local.Address.NodeID, c.PeerHost.NodeID)
}

// Directory refreshes the remote node's advertised assistants. It grants no
// access to its owner APIs, files, credentials or execution transport.
func (s *Service) Directory(ctx context.Context, id string) (Directory, error) {
	c, e := s.Get(ctx, id)
	if e != nil {
		return Directory{}, e
	}
	session, e := s.businessSession(ctx, c)
	if e != nil {
		return Directory{}, e
	}
	var d Directory
	e = s.call(ctx, session, "directory", struct{}{}, &d)
	if e == nil {
		e = validateDirectory(d, c.PeerHost.NodeID)
	}
	if e == nil {
		s.mu.Lock()
		current, err := s.Get(ctx, id)
		if err == nil {
			current.Directory = &d
			err = s.update(ctx, current)
		}
		s.mu.Unlock()
		e = err
	}
	return d, e
}
func (s *Service) businessSession(ctx context.Context, c Connection) (*peer.Session, error) {
	if c.State == "revoked" {
		return nil, ErrRevoked
	}
	if c.State == "disconnected" {
		return nil, ErrDisconnected
	}
	return s.ensureSession(ctx, c)
}

type taskLookup struct {
	From protocol.AgentRef `json:"from"`
	ID   string            `json:"id"`
}

func (s *Service) Submit(ctx context.Context, id string, r TaskRequest) (Task, error) {
	if r.From.NodeID != s.options.HostIdentity.NodeID {
		return Task{}, ErrUnauthorized
	}
	c, e := s.Get(ctx, id)
	if e != nil {
		return Task{}, e
	}
	session, e := s.businessSession(ctx, c)
	if e != nil {
		return Task{}, e
	}
	var t Task
	e = s.call(ctx, session, "task.submit", r, &t)
	if e == nil {
		e = validateTask(t, c.PeerHost.NodeID, "", r.RequestID)
		if r.AssistantID != "" && t.Assistant.Address.AgentID != r.AssistantID {
			e = ErrConflict
		}
	}
	return t, e
}
func (s *Service) Status(ctx context.Context, id string, from protocol.AgentRef, task string) (Task, error) {
	return s.lookup(ctx, id, from, task, "task.status")
}
func (s *Service) Cancel(ctx context.Context, id string, from protocol.AgentRef, task string) (Task, error) {
	return s.lookup(ctx, id, from, task, "task.cancel")
}
func (s *Service) lookup(ctx context.Context, id string, from protocol.AgentRef, task, method string) (Task, error) {
	if from.NodeID != s.options.HostIdentity.NodeID {
		return Task{}, ErrUnauthorized
	}
	c, e := s.Get(ctx, id)
	if e != nil {
		return Task{}, e
	}
	session, e := s.businessSession(ctx, c)
	if e != nil {
		return Task{}, e
	}
	var t Task
	e = s.call(ctx, session, method, taskLookup{from, task}, &t)
	if e == nil {
		e = validateTask(t, c.PeerHost.NodeID, task, "")
	}
	return t, e
}
func (s *Service) business(ctx context.Context, c Connection, remote peer.Identity, r peer.Request) (json.RawMessage, error) {
	if !s.admit(c.ID) {
		return nil, ErrCapacity
	}
	defer s.release(c.ID)
	d, e := s.localDirectory(ctx)
	if e != nil {
		return nil, e
	}
	var result any
	switch r.Method {
	case "observe":
		var empty struct{}
		if decodeJSON(r.Params, &empty) != nil {
			return nil, ErrInvalid
		}
		result, e = s.localObservation(ctx, remote.NodeID)
	case "directory":
		var empty struct{}
		if decodeJSON(r.Params, &empty) != nil {
			return nil, ErrInvalid
		}
		result = d
	case "task.submit":
		if !supports(d, "tasks") {
			return nil, ErrUnsupported
		}
		var input TaskRequest
		if decodeJSON(r.Params, &input) != nil || !input.From.Valid() || input.From.Kind != "assistant" || input.From.NodeID != remote.NodeID || strings.TrimSpace(input.RequestID) == "" || len(input.RequestID) > 256 || strings.TrimSpace(input.Text) == "" || len(input.Text) > 1024*1024 {
			return nil, ErrInvalid
		}

		result, e = s.options.Backend.Submit(ctx, input)
	case "task.status", "task.cancel":
		if !supports(d, "tasks") || r.Method == "task.cancel" && !supports(d, "cancel") {
			return nil, ErrUnsupported
		}
		var input taskLookup
		if decodeJSON(r.Params, &input) != nil || !input.From.Valid() || input.From.NodeID != remote.NodeID || input.ID == "" {
			return nil, ErrInvalid
		}
		if r.Method == "task.status" {
			result, e = s.options.Backend.Status(ctx, input.From, input.ID)
		} else {
			result, e = s.options.Backend.Cancel(ctx, input.From, input.ID)
		}
	default:
		return nil, ErrUnsupported
	}
	if e != nil {
		return nil, e
	}
	return json.Marshal(result)
}

// Disconnect pauses communication without deleting keys, history or work.
func (s *Service) Disconnect(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.Get(ctx, id)
	if e != nil {
		return e
	}
	if c.State == "revoked" {
		return ErrRevoked
	}
	c.State = "disconnected"
	c.LastError = ""
	e = s.update(ctx, c)
	if session := s.sessions[id]; session != nil {
		session.Close()
	}
	return e
}
func (s *Service) Reconnect(ctx context.Context, id string) error {
	s.mu.Lock()
	c, e := s.Get(ctx, id)
	if e == nil && c.State == "revoked" {
		e = ErrRevoked
	}
	if e == nil {
		c.State = "offline"
		raw, _ := json.Marshal(c)
		_, e = s.options.DB.ExecContext(ctx, "UPDATE agent_link_connections SET state=?,metadata=? WHERE id=? AND state!='revoked'", c.State, string(raw), c.ID)
	}
	s.mu.Unlock()
	if e != nil {
		return e
	}
	if c.Endpoint == "" {
		if s.options.Changed != nil {
			s.options.Changed()
		}
		return nil
	}
	return s.Sync(ctx, id)
}

// Delete leaves a revocation tombstone so a stale peer cannot restore the link.
// The saved directory is removed; business history and running work are retained.
func (s *Service) Delete(ctx context.Context, id string) error { return s.Revoke(ctx, id) }

func validateTask(t Task, node, id, request string) error {
	if t.ID == "" || len(t.ID) > 256 || !validIdentity(t.Assistant) || t.Assistant.Address.Kind != "assistant" || t.Assistant.Address.NodeID != node || id != "" && t.ID != id || request != "" && t.RequestID != request || len(t.Result) > 1024*1024 || len(t.Error) > 65536 {
		return ErrConflict
	}
	switch t.State {
	case "queued", "waiting", "running", "waiting_approval", "cancelling", "complete", "completed", "failed", "cancelled", "interrupted", "unknown":
		return nil
	}
	return ErrConflict
}
