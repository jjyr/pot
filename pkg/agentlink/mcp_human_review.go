package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

// These internal receiver RPCs replace the reverse direction of existing
// delegated human review. They are never model tools. User is provenance from
// the directing node's authenticated human port, not a node caller identity.
type MCPHumanReviewRead struct {
	Caller protocol.AgentRef `json:"caller" required:"true"`
}
type MCPHumanReview struct {
	Caller        protocol.AgentRef `json:"caller" required:"true"`
	RequestID     string            `json:"request_id" required:"true"`
	TaskID        string            `json:"task_id" required:"true"`
	ExecutionTask protocol.AgentRef `json:"execution_task" required:"true"`
	RunID         string            `json:"run_id" required:"true"`
	Generation    uint64            `json:"generation" required:"true"`
	Epoch         string            `json:"epoch" required:"true"`
	Approval      ExecutionApproval `json:"approval" required:"true"`
}
type MCPHumanReviewState struct {
	Pending []MCPHumanReview `json:"pending" required:"true"`
}
type MCPHumanReviewDecision struct {
	Caller        protocol.AgentRef `json:"caller" required:"true"`
	RequestID     string            `json:"request_id" required:"true"`
	TaskID        string            `json:"task_id" required:"true"`
	ExecutionTask protocol.AgentRef `json:"execution_task" required:"true"`
	RunID         string            `json:"run_id" required:"true"`
	Generation    uint64            `json:"generation" required:"true"`
	Epoch         string            `json:"epoch" required:"true"`
	ApprovalID    string            `json:"approval_id" required:"true"`
	User          protocol.AgentRef `json:"user" required:"true"`
	Action        string            `json:"action" required:"true"`
}

// Implementations must use the existing pending native/PermissionStore request,
// original accepted delegation/caller, current exact Task/run/generation and
// process epoch. Decision consumes only that pending request, grants no rule or
// policy, and fails after completion/cancellation/restart/revocation. A registered
// node or a supplied User field alone is never sufficient authorization.
type MCPHumanReviewBackend interface {
	MCPHumanReviews(context.Context, MCPReceiver, MCPCaller, MCPHumanReviewRead) (MCPHumanReviewState, error)
	MCPHumanDecision(context.Context, MCPReceiver, MCPCaller, MCPHumanReviewDecision) error
}
