package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"reflect"
)

var ErrCapacity = errors.New("agent_link_capacity_exceeded")
var ErrFenced = errors.New("agent_link_execution_fenced")
var ErrUnsupported = errors.New("agent_link_capability_unsupported")
var ErrDisconnected = errors.New("agent_link_disconnected")

func validateDirectory(d Directory, node string) error {
	if d.NodeID != node || len(d.Targets) > 257 || d.Name == "" || !core.Identifier(d.Adapter) {
		return ErrConflict
	}
	seen := map[protocol.AgentRef]bool{}
	for _, entry := range d.Targets {
		ref, description := entry.Address, entry.Capabilities
		scope := "peer"
		if ref.Kind == "node" {
			scope = "caller"
		}
		if !ValidTarget(ref) || !validIdentity(entry.Identity) || ref.NodeID != node || entry.Adapter != d.Adapter || seen[ref] || description.Version != 1 || description.Scope != scope || len(description.Operations) > 64 {
			return ErrConflict
		}
		seen[ref] = true
		operations := map[Operation]bool{}
		for _, op := range description.Operations {
			if op == "" || operations[op] {
				return ErrConflict
			}
			operations[op] = true
		}
	}
	if d.DefaultTarget != nil && !seen[*d.DefaultTarget] {
		return ErrConflict
	}

	return nil
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
	if id == "" {
		return s.localDirectory(ctx)
	}
	c, e := s.Get(ctx, id)
	if e != nil {
		return Directory{}, e
	}
	session, e := s.businessSession(ctx, c)
	if e != nil {
		return Directory{}, e
	}
	return s.readPeerDirectory(ctx, c, session)
}

// Each request captures an epoch before network I/O. A confirmed deletion or
// newer request fences its response, including the value returned to callers.
func (s *Service) readPeerDirectory(ctx context.Context, c Connection, session *peer.Session) (Directory, error) {
	s.mu.Lock()
	s.directoryEpoch[c.ID]++
	epoch := s.directoryEpoch[c.ID]
	s.mu.Unlock()
	var d Directory
	e := s.call(ctx, session, "directory", struct{}{}, &d)
	if e == nil {
		e = validateDirectory(d, c.PeerHost.NodeID)
		if d.Adapter != c.Peer.Adapter {
			e = ErrConflict
		}
	}
	if e == nil {
		s.mu.Lock()
		current, err := s.Get(ctx, c.ID)
		if err == nil && (current.State == "revoked" || current.State == "disconnected") {
			err = ErrDisconnected
		}
		if err == nil && s.directoryEpoch[c.ID] != epoch {
			if current.Directory == nil {
				err = ErrUnavailable
			} else {
				d = *current.Directory
			}
		} else if err == nil && !reflect.DeepEqual(current.Directory, &d) {
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
	if !taskCaller(r.From) || r.From.NodeID != s.options.HostIdentity.NodeID {
		return Task{}, ErrUnauthorized
	}
	if r.Configuration != nil && id != "" {
		return Task{}, ErrUnsupported
	}
	if !ValidTarget(r.Target) {
		return Task{}, ErrInvalid
	}
	if strings.TrimSpace(r.RequestID) == "" || len(r.RequestID) > 128 || strings.TrimSpace(r.Text) == "" || len(r.Text) > 65536 {
		return Task{}, ErrInvalid
	}
	if id == "" {
		if r.Target.NodeID != s.options.HostIdentity.NodeID {
			return Task{}, ErrConflict
		}
		if s.options.Backend == nil {
			return Task{}, ErrUnsupported
		}
		identity, err := s.localIdentity(ctx)
		if err != nil {
			return Task{}, err
		}
		result, err := s.options.Backend.Submit(ctx, r)
		if err == nil {
			err = validateSubmission(result, r, s.options.HostIdentity.NodeID, identity.Adapter)
		}
		return result, err
	}
	c, e := s.Get(ctx, id)
	if e != nil {
		return Task{}, e
	}
	if r.Target.NodeID != c.PeerHost.NodeID {
		return Task{}, ErrConflict
	}
	session, e := s.businessSession(ctx, c)
	if e != nil {
		return Task{}, e
	}
	var t Task
	e = s.call(ctx, session, "task.submit", r, &t)
	if e == nil {
		e = validateSubmission(t, r, c.PeerHost.NodeID, c.Peer.Adapter)
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
	if !taskCaller(from) || from.NodeID != s.options.HostIdentity.NodeID {
		return Task{}, ErrUnauthorized
	}
	if id == "" {
		if s.options.Backend == nil {
			return Task{}, ErrUnsupported
		}
		identity, err := s.localIdentity(ctx)
		if err != nil {
			return Task{}, err
		}
		var result Task
		if method == "task.cancel" {
			result, err = s.options.Backend.Cancel(ctx, from, task)
		} else {
			result, err = s.options.Backend.Status(ctx, from, task)
		}
		if err == nil {
			err = validateTask(result, s.options.HostIdentity.NodeID, identity.Adapter, task, "")
		}
		return result, err
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
		e = validateTask(t, c.PeerHost.NodeID, c.Peer.Adapter, task, "")
	}
	return t, e
}
func (s *Service) business(ctx context.Context, c Connection, remote peer.Identity, r peer.Request) (json.RawMessage, error) {
	if !s.admit(c.ID, businessAdmission) {
		return nil, ErrCapacity
	}
	defer s.release(c.ID, businessAdmission)
	d, e := s.localDirectory(ctx)
	if e != nil {
		return nil, e
	}
	var result any
	switch r.Method {
	case "execution.review":
		var input ReviewRequest
		if decodeJSON(r.Params, &input) != nil || !validReview(input, remote.NodeID) {
			return nil, ErrUnauthorized
		}
		backend, ok := s.options.Backend.(ReviewBackend)
		if !ok || !d.Capabilities(NodeTarget(d.NodeID)).Supports(Approvals) {
			return nil, ErrUnsupported
		}
		result, e = backend.Review(ctx, input)
	case "chat.status":
		result, e = s.chatBusiness(ctx, remote.NodeID, r.Params)
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
	case "assistant.management":
		var input AssistantManagementRequest
		if decodeJSON(r.Params, &input) != nil || !input.From.Valid() || input.From.Kind != "user" || input.From.NodeID != remote.NodeID {
			return nil, ErrUnauthorized
		}
		backend, ok := s.options.Backend.(AssistantManagementBackend)
		if !ok || !d.Capabilities(NodeTarget(d.NodeID)).Supports(AssistantManagement) {
			return nil, ErrUnsupported
		}
		result, e = backend.ManageAssistant(ctx, input)
	case "assistant.delete":
		var input DeleteAssistantRequest
		if decodeJSON(r.Params, &input) != nil || input.From.Kind != "user" || !input.From.Valid() || input.From.NodeID != remote.NodeID || input.AssistantID == "" || input.Revision == 0 {
			return nil, ErrInvalid
		}
		backend, ok := s.options.Backend.(AssistantDeletionBackend)
		if !ok || !d.Capabilities(AssistantTarget(d.NodeID, input.AssistantID)).Supports(AssistantDelete) {
			return nil, ErrUnsupported
		}
		e = backend.DeleteAssistant(ctx, input)
		result = AssistantDeletion{ID: input.AssistantID, Deleted: e == nil}
	case "assistant.profile":
		var input ProfileRequest
		if decodeJSON(r.Params, &input) != nil || input.From.Kind != "user" || !input.From.Valid() || input.From.NodeID != remote.NodeID || input.AssistantID == "" {
			return nil, ErrInvalid
		}
		backend, ok := s.options.Backend.(ProfileBackend)
		if !ok || !d.Capabilities(AssistantTarget(d.NodeID, input.AssistantID)).Supports(AssistantProfile) {
			return nil, ErrUnsupported
		}
		result, e = backend.Profile(ctx, input)
	case "task.submit":
		var input TaskRequest
		if decodeJSON(r.Params, &input) != nil || !taskCaller(input.From) || input.From.NodeID != remote.NodeID || strings.TrimSpace(input.RequestID) == "" || len(input.RequestID) > 128 || strings.TrimSpace(input.Text) == "" || len(input.Text) > 65536 {
			return nil, ErrInvalid
		}
		if !ValidTarget(input.Target) || input.Target.NodeID != s.options.HostIdentity.NodeID {
			return nil, ErrInvalid
		}
		if input.Configuration != nil {
			return nil, ErrUnsupported
		}

		result, e = s.options.Backend.Submit(ctx, input)
	case "task.input":
		var input TaskGuidance
		if decodeJSON(r.Params, &input) != nil || !validGuidance(input, remote.NodeID) {
			return nil, ErrInvalid
		}
		result, e = s.input(ctx, d, input)
	case "task.status", "task.cancel":
		var input taskLookup
		if decodeJSON(r.Params, &input) != nil || !taskCaller(input.From) || input.From.NodeID != remote.NodeID || input.ID == "" {
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

func taskCaller(from protocol.AgentRef) bool {
	return from.Valid() && (from.Kind == "assistant" || from.Kind == "task" || from.Kind == "user")
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

func validateSubmission(task Task, request TaskRequest, node, adapter string) error {
	if err := validateTask(task, node, adapter, "", request.RequestID); err != nil {
		return err
	}
	if request.Target.Kind == "node" && task.Receiver.Address.Kind != "task" || request.Target.Kind == "assistant" && task.Receiver.Address != request.Target {
		return ErrConflict
	}
	return nil
}

func validateTask(t Task, node, adapter, id, request string) error {
	receiver := t.Receiver
	if t.ID == "" || len(t.ID) > 256 || !validIdentity(receiver) || receiver.Adapter != adapter || (receiver.Address.Kind != "assistant" && (receiver.Address.Kind != "task" || receiver.Address.AgentID != t.ID)) || receiver.Address.NodeID != node || id != "" && t.ID != id || request != "" && t.RequestID != request || len(t.Result) > 1024*1024 || len(t.Error) > 65536 {
		return ErrConflict
	}
	switch t.State {
	case "queued", "waiting", "running", "waiting_approval", "cancelling", "complete", "completed", "failed", "cancelled", "interrupted", "unknown":
		return nil
	}
	return ErrConflict
}
