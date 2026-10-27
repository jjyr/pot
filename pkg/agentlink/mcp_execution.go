package agentlink

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// MCPExecutionContext is receiver-to-service metadata, never model arguments.
// Authentication, persisted admission and current-run liveness must all be
// checked before constructing a node caller. Shape validation grants no trust.
type MCPExecutionContext struct {
	Claim           MCPClaim            `json:"claim,omitempty,omitzero"`
	NodeExecutionID string              `json:"node_execution_id,omitempty"`
	Binding         MCPExecutionBinding `json:"binding" required:"true"`
}

func (c MCPExecutionContext) Validate() error {
	if c.Binding.Validate() != nil {
		return ErrUnauthorized
	}
	if c.NodeExecutionID != "" {
		if !core.Identifier(c.NodeExecutionID) || c.Claim != (MCPClaim{}) {
			return ErrUnauthorized
		}
		return nil
	}
	if c.Claim.Validate() != nil || c.Binding.Task.NodeID != c.Claim.Binding.Receiver.NodeID {
		return ErrUnauthorized
	}
	return nil
}

type MCPMessageInput struct {
	RequestID string            `json:"request_id" required:"true"`
	Message   protocol.SendMail `json:"message" required:"true"`
}
type MCPExecutionSubmit struct {
	Context MCPExecutionContext `json:"context" required:"true"`
	Input   MCPSubmitInput      `json:"input" required:"true"`
}
type MCPExecutionInput struct {
	Context MCPExecutionContext `json:"context" required:"true"`
	Input   MCPTaskInput        `json:"input" required:"true"`
}
type MCPExecutionCancel struct {
	Context MCPExecutionContext `json:"context" required:"true"`
	Input   MCPCancelInput      `json:"input" required:"true"`
}
type MCPExecutionMessage struct {
	Context MCPExecutionContext `json:"context" required:"true"`
	Input   MCPMessageInput     `json:"input" required:"true"`
}
type MCPExecutionRead struct {
	Context MCPExecutionContext `json:"context" required:"true"`
	TaskID  string              `json:"task_id" required:"true"`
}

// Pending approvals are native requests, not model decisions or policy grants.
// Epoch identifies the operated receiver process; restarting invalidates pending
// requests rather than reconstructing the native in-memory done channel.
type MCPApprovalPending struct {
	Context   MCPExecutionContext `json:"context" required:"true"`
	Epoch     string              `json:"epoch" required:"true"`
	RequestID string              `json:"request_id" required:"true"`
	Approval  ExecutionApproval   `json:"approval" required:"true"`
}

func (p MCPApprovalPending) Validate() error {
	if p.Context.Validate() != nil || !core.Identifier(p.Epoch) || !core.Identifier(p.RequestID) || !core.Identifier(p.Approval.ID) || p.Approval.JobID != p.Context.Binding.Task.AgentID || p.Approval.Method == "" || len(p.Approval.Method) > 128 || len(p.Approval.Params) > MCPMaxInputBytes || !json.Valid(p.Approval.Params) {
		return ErrInvalid
	}
	return nil
}

type MCPApprovalDecision struct {
	ApprovalID string            `json:"approval_id" required:"true"`
	Epoch      string            `json:"epoch" required:"true"`
	RequestID  string            `json:"request_id" required:"true"`
	User       protocol.AgentRef `json:"user" required:"true"`
	Action     string            `json:"action" required:"true"`
}

func (d MCPApprovalDecision) Validate() error {
	if !core.Identifier(d.ApprovalID) || !core.Identifier(d.Epoch) || !core.Identifier(d.RequestID) || !d.User.Valid() || d.User.Kind != "user" || (d.Action != "allow_once" && d.Action != "deny") {
		return ErrInvalid
	}
	return nil
}

// MCPNodeMail forwards an immutable mailbox envelope from a registered node's
// own assistant outside supervised execution. It is receiver metadata and must
// never be exposed as a model tool; task actors always require a live binding.
type MCPNodeMail struct {
	Message            protocol.Envelope `json:"message" required:"true"`
	ProcessingParentID string            `json:"processing_parent_id,omitempty"`
}

// MCPNodeExecutionAdmission is an owning node's attestation of its actual local
// active execution, outside remotely claimed Work. It uses the same receiver
// credential. ExecutionID is correlation, never a credential or launch request.
type MCPNodeExecutionAdmission struct {
	ExecutionID   string              `json:"execution_id" required:"true"`
	Binding       MCPExecutionBinding `json:"binding" required:"true"`
	PreviousRunID string              `json:"previous_run_id,omitempty"`
	ParentID      string              `json:"parent_id,omitempty"`
}
type MCPNodeExecutionEnd struct {
	ExecutionID string              `json:"execution_id" required:"true"`
	Binding     MCPExecutionBinding `json:"binding" required:"true"`
}

// MCPNodeSubmit is for a registered node's unsupervised root assistant. A local
// Task must instead use actual execution admission; it cannot become assistant.
type MCPNodeSubmit struct {
	Actor protocol.AgentRef `json:"actor" required:"true"`
	Input MCPSubmitInput    `json:"input" required:"true"`
}

type MCPNodeRead struct {
	Actor  protocol.AgentRef `json:"actor" required:"true"`
	TaskID string            `json:"task_id" required:"true"`
}
type MCPNodeInput struct {
	Actor protocol.AgentRef `json:"actor" required:"true"`
	Input MCPTaskInput      `json:"input" required:"true"`
}
type MCPNodeCancel struct {
	Actor protocol.AgentRef `json:"actor" required:"true"`
	Input MCPCancelInput    `json:"input" required:"true"`
}
