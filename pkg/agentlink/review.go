package agentlink

import (
	"context"
	"encoding/json"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// Review accepts human decisions only for tasks directly delegated by the caller.
type ReviewRequest struct {
	From   protocol.AgentRef `json:"from"`
	Caller protocol.AgentRef `json:"caller"`
	ID     string            `json:"id,omitempty"`
	Action string            `json:"action"`
}

type ExecutionApproval struct {
	ID     string          `json:"id"`
	JobID  string          `json:"job_id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type ReviewState struct {
	NodeID    string              `json:"node_id"`
	Host      string              `json:"host"`
	CanDecide bool                `json:"can_decide"`
	Pending   []ExecutionApproval `json:"pending"`
}

type ReviewBackend interface {
	Review(context.Context, ReviewRequest) (ReviewState, error)
}

func validReview(r ReviewRequest, node string) bool {
	return r.From.Valid() && r.From.Kind == "user" && r.From.NodeID == node &&
		r.Caller.Valid() && r.Caller.NodeID == node && taskCaller(r.Caller) &&
		r.Caller.Kind == "assistant" && ((r.Action == "read" && r.ID == "") ||
		((r.Action == "allow_once" || r.Action == "deny") && core.Identifier(r.ID)))
}

func (s *Service) Review(ctx context.Context, link string, r ReviewRequest) (ReviewState, error) {
	if !validReview(r, s.options.HostIdentity.NodeID) {
		return ReviewState{}, ErrUnauthorized
	}
	c, err := s.Get(ctx, link)
	if err != nil {
		return ReviewState{}, err
	}
	if c.Directory == nil || c.Directory.TaskExecutor == nil || !c.Directory.TaskExecutor.Supports(Approvals) {
		return ReviewState{}, ErrUnsupported
	}
	session, err := s.businessSession(ctx, c)
	if err != nil {
		return ReviewState{}, err
	}
	var result ReviewState
	err = s.call(ctx, session, "execution.review", r, &result)
	if err == nil && (result.NodeID != c.PeerHost.NodeID || len(result.Pending) > 32) {
		return ReviewState{}, ErrConflict
	}
	return result, err
}
