package agentlink

import (
	"slices"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// MCPReceiver is trusted connection metadata. An ordinary MCPCaller cannot be
// converted to this type by a tool. Authentication must independently authorize
// receiver participation for this node before constructing it.
type MCPReceiver struct {
	connection string
	node       string
}

func NewMCPReceiver(connection, node string) (MCPReceiver, error) {
	if !core.Identifier(connection) || !NodeTarget(node).Valid() {
		return MCPReceiver{}, ErrUnauthorized
	}
	return MCPReceiver{connection, node}, nil
}
func (r MCPReceiver) ConnectionID() string { return r.connection }
func (r MCPReceiver) NodeID() string       { return r.node }
func (r MCPReceiver) validate() error {
	_, err := NewMCPReceiver(r.connection, r.node)
	return err
}

// MCPReceiverRegistration registers one endpoint on the authenticated node.
// Multiple endpoints use separate registrations. Identity/Description retain
// their existing meanings; this does not change persisted Directory JSON.
type MCPReceiverRegistration struct {
	Version      int         `json:"version" required:"true"`
	Receiver     Identity    `json:"receiver" required:"true"`
	Capabilities Description `json:"capabilities" required:"true"`
	Epoch        string      `json:"epoch,omitempty"`
}

// ValidateAgainst requires the driver's actual effective capabilities. A remote
// advertisement alone cannot prove that a driver implements an operation.
func (r MCPReceiverRegistration) ValidateAgainst(ctx MCPReceiver, actual Description) error {
	if err := ctx.validate(); err != nil {
		return err
	}
	if r.Version != MCPContractVersion {
		return ErrUnsupported
	}
	if !validIdentity(r.Receiver) || !ValidTarget(r.Receiver.Address) || r.Receiver.Address.NodeID != ctx.node {
		return ErrUnauthorized
	}
	scope := "peer"
	if r.Receiver.Address.Kind == "node" {
		scope = "caller"
	}
	if r.Capabilities.Version != 1 || actual.Version != 1 || r.Capabilities.Scope != scope || actual.Scope != scope || len(r.Capabilities.Operations) > 64 {
		return ErrUnsupported
	}
	seen := map[Operation]bool{}
	for _, op := range r.Capabilities.Operations {
		if seen[op] || !mcpCollaborationOperation(op) || !actual.Supports(op) {
			return ErrUnsupported
		}
		seen[op] = true
	}
	return nil
}

// This is the existing peer/caller collaboration boundary, not a configurable
// grant/ACL system. Owner configuration and execution launch remain excluded.
// Approvals and native profile/delete describe wired human channels, never
// receiver/model decision or management authority.
func mcpCollaborationOperation(op Operation) bool {
	return slices.Contains(PeerOperations(), op) || op == TaskInput || op == TaskEvents || op == TaskFiles || op == Approvals || op == AssistantProfile || op == AssistantDelete || op == AssistantManagement
}

// MCPDispatchBinding identifies stable caller-side work before the execution
// backend has admitted it. Task is the queue's business resource, not an invented
// execution task/run. The eventual real execution mapping is stored separately.
type MCPDispatchBinding struct {
	Task     protocol.AgentRef `json:"task" required:"true"`
	Receiver protocol.AgentRef `json:"receiver" required:"true"`
}

func (b MCPDispatchBinding) Validate() error {
	if !b.Task.Valid() || b.Task.Kind != "task" || !ValidTarget(b.Receiver) {
		return ErrInvalid
	}
	return nil
}

// MCPRunBinding identifies the real execution-side Task/run. The dispatch claim
// retains the caller-side resource when its ID or node differs from this Task.
type MCPRunBinding struct {
	Task     protocol.AgentRef `json:"task" required:"true"`
	RunID    string            `json:"run_id" required:"true"`
	Receiver protocol.AgentRef `json:"receiver" required:"true"`
}

func (b MCPRunBinding) Validate() error {
	if !b.Task.Valid() || b.Task.Kind != "task" || !core.Identifier(b.RunID) || !ValidTarget(b.Receiver) || b.Task.NodeID != b.Receiver.NodeID {
		return ErrInvalid
	}
	return nil
}

// MCPWorkDelivery is internal receiver data, never a model-visible tool input.
// Request.From is injected by the authenticated gateway. The queue verifies
// original caller ownership before constructing or returning this envelope.
type MCPWorkDelivery struct {
	Version    int                `json:"version" required:"true"`
	DeliveryID string             `json:"delivery_id" required:"true"`
	Binding    MCPDispatchBinding `json:"binding" required:"true"`
	Request    TaskRequest        `json:"request" required:"true"`
	Message    *protocol.Envelope `json:"message,omitempty"`
}

func (d MCPWorkDelivery) Validate() error {
	if d.Version != MCPContractVersion {
		return ErrUnsupported
	}
	if !core.Identifier(d.DeliveryID) || d.Binding.Validate() != nil || !taskCaller(d.Request.From) || d.Request.Target != d.Binding.Receiver || !core.Identifier(d.Request.RequestID) || !mcpText(d.Request.Text, MCPMaxTextBytes) {
		return ErrInvalid
	}
	if d.Message != nil {
		if d.Message.Validate() != nil || d.Message.Kind != "message" || d.Message.From != d.Request.From || d.Message.To != d.Request.Target || d.Message.Text != d.Request.Text || d.Message.To.Kind != "assistant" {
			return ErrInvalid
		}
	}
	if d.Request.Configuration != nil || d.Request.Workspace != "" {
		return ErrUnsupported
	}
	return nil
}

// MCPControlDelivery has exactly one typed control. It cannot carry arbitrary
// operations, launch parameters or caller-selected owner metadata. Approval
// controls originate only at the existing authenticated human control plane.
type MCPControlDelivery struct {
	Version    int                  `json:"version" required:"true"`
	DeliveryID string               `json:"delivery_id" required:"true"`
	Dispatch   MCPDispatchBinding   `json:"dispatch" required:"true"`
	Binding    MCPRunBinding        `json:"binding" required:"true"`
	Caller     protocol.AgentRef    `json:"caller" required:"true"`
	Input      *MCPTaskInput        `json:"input,omitempty"`
	Cancel     *MCPCancelInput      `json:"cancel,omitempty"`
	Approval   *MCPApprovalDecision `json:"approval,omitempty"`
}

func (d MCPControlDelivery) Validate() error {
	if d.Version != MCPContractVersion {
		return ErrUnsupported
	}
	if !core.Identifier(d.DeliveryID) || d.Dispatch.Validate() != nil || d.Binding.Validate() != nil || d.Dispatch.Receiver != d.Binding.Receiver || !taskCaller(d.Caller) || controlCount(d.Input, d.Cancel, d.Approval) != 1 {
		return ErrInvalid
	}
	if d.Input != nil {
		if err := d.Input.Validate(); err != nil {
			return err
		}
		if d.Input.TaskID != d.Dispatch.Task.AgentID || d.Input.RunID != d.Binding.RunID {
			return ErrFenced
		}
	} else if d.Cancel != nil {
		if err := d.Cancel.Validate(); err != nil {
			return err
		}
		if d.Cancel.TaskID != d.Dispatch.Task.AgentID || d.Cancel.RunID != d.Binding.RunID {
			return ErrFenced
		}
	} else if d.Approval.Validate() != nil {
		return ErrInvalid
	}
	return nil
}

// MCPClaim is transport association, not a Task phase or authority token. The
// queue stores it before returning a claim; each ack/event must match that exact
// current claim and the authenticated connection/node. Expiry/loss of liveness
// never permits automatic reassignment of possibly executed work.
type MCPClaim struct {
	ConnectionID string             `json:"connection_id" required:"true"`
	DeliveryID   string             `json:"delivery_id" required:"true"`
	ClaimID      string             `json:"claim_id" required:"true"`
	Binding      MCPDispatchBinding `json:"binding" required:"true"`
}

func (c MCPClaim) Validate() error {
	if !core.Identifier(c.ConnectionID) || !core.Identifier(c.DeliveryID) || !core.Identifier(c.ClaimID) || c.Binding.Validate() != nil {
		return ErrInvalid
	}
	return nil
}
func (c MCPClaim) ValidateAgainst(ctx MCPReceiver, current MCPClaim) error {
	if err := ctx.validate(); err != nil {
		return err
	}
	if c.Validate() != nil || current.Validate() != nil {
		return ErrInvalid
	}
	if current.ConnectionID != ctx.connection || current.Binding.Receiver.NodeID != ctx.node {
		return ErrUnauthorized
	}
	if c != current {
		return ErrFenced
	}
	return nil
}

// MCPDeliveryAck means durable receiver admission or rejection, not start or
// completion. Repeated identical acknowledgements return their saved receipt.
// Accepted work remains fenced by the existing Host launch/cleanup journal.
type MCPDeliveryAck struct {
	Claim    MCPClaim  `json:"claim" required:"true"`
	Accepted bool      `json:"accepted" required:"true"`
	Error    *MCPError `json:"error,omitempty"`
}

// MCPRunAdmission is emitted after durable backend acceptance exposes a real
// execution Task/run, possibly after an earlier run-less acceptance ack. The
// Host derives it from the backend's persisted admission and reports it before
// execution events. Native launch remains fenced by that backend's journal.
// Continuation uses a new claim and compares PreviousRunID to stored execution.
type MCPRunAdmission struct {
	Claim         MCPClaim      `json:"claim" required:"true"`
	Binding       MCPRunBinding `json:"binding" required:"true"`
	PreviousRunID string        `json:"previous_run_id,omitempty"`
}

func (a MCPRunAdmission) Validate() error {
	if a.Claim.Validate() != nil || a.Binding.Validate() != nil || a.Binding.Receiver != a.Claim.Binding.Receiver || a.PreviousRunID != "" && !core.Identifier(a.PreviousRunID) {
		return ErrInvalid
	}
	return nil
}

func (a MCPRunAdmission) ValidateAgainst(ctx MCPReceiver, current MCPClaim, previous *MCPRunBinding) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := a.Claim.ValidateAgainst(ctx, current); err != nil {
		return err
	}
	if previous == nil {
		if a.PreviousRunID != "" {
			return ErrFenced
		}
	} else if previous.Validate() != nil || a.PreviousRunID != previous.RunID || a.Binding.Task != previous.Task || a.Binding.Receiver != previous.Receiver || a.Binding.RunID == previous.RunID {
		return ErrFenced
	}
	return nil
}

func (a MCPDeliveryAck) Validate() error {
	if a.Claim.Validate() != nil || a.Accepted && a.Error != nil || !a.Accepted && a.Error == nil {
		return ErrInvalid
	}
	if a.Error != nil {
		return a.Error.Validate()
	}
	return nil
}

// MCPExecutionEvent reuses business phases. ThreadID is the receiver's native
// thread/session, never the business task ID. Terminal results and artifacts
// retain the binding's Task/run ownership; paths never imply remote file access.
type MCPExecutionEvent struct {
	Claim    MCPClaim                  `json:"claim" required:"true"`
	Binding  MCPRunBinding             `json:"binding" required:"true"`
	Sequence uint64                    `json:"sequence" required:"true"`
	Phase    protocol.TaskPhase        `json:"phase" required:"true"`
	ThreadID string                    `json:"thread_id,omitempty"`
	Result   *protocol.TaskResultEvent `json:"result,omitempty"`
}

func (e MCPExecutionEvent) Validate() error {
	if e.Claim.Validate() != nil || e.Binding.Validate() != nil || e.Binding.Receiver != e.Claim.Binding.Receiver || e.Sequence == 0 || e.Sequence > core.MaxSafeInteger || len(e.ThreadID) > 4096 || !utf8.ValidString(e.ThreadID) {
		return ErrInvalid
	}
	terminal := false
	switch e.Phase {
	case protocol.TaskQueued, protocol.TaskWaiting, protocol.TaskRunning, protocol.TaskWaitingApproval:
	case protocol.TaskComplete, protocol.TaskFailed, protocol.TaskCancelled, protocol.TaskInterrupted:
		terminal = true
	default:
		return ErrInvalid
	}
	if terminal != (e.Result != nil) {
		return ErrInvalid
	}
	if e.Result != nil {
		r := e.Result
		if r.TaskID != e.Binding.Task.AgentID || r.RunID != e.Binding.RunID || r.Phase != e.Phase || !mcpText(r.Title, MCPMaxTextBytes) || len(r.Text) > MCPMaxResultBytes || len(r.Error) > MCPMaxTextBytes || !utf8.ValidString(r.Text) || !utf8.ValidString(r.Error) || r.Origin.Validate() != nil {
			return ErrInvalid
		}
	}
	data, err := protocol.CanonicalJSON(e)
	if err != nil || len(data) > MCPMaxPayloadBytes {
		return ErrInvalid
	}
	return nil
}

// ValidateAfter checks correlation and a new sequence. A persistent consumer
// first resolves an exact duplicate (same claim/sequence/canonical event digest)
// to its saved receipt. Conflicting reuse is ErrConflict, older events ErrFenced.
// Apply this check and the business projection atomically with expected-run CAS.
func (e MCPExecutionEvent) ValidateAfter(ctx MCPReceiver, current MCPClaim, execution MCPRunBinding, last uint64) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if err := e.Claim.ValidateAgainst(ctx, current); err != nil {
		return err
	}
	if execution.Validate() != nil || e.Binding != execution || e.Sequence <= last {
		return ErrFenced
	}
	return nil
}

func (e MCPExecutionEvent) Digest() (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	return core.HashJSON(e)
}

// MCPEventReceipt acknowledges a committed event projection. Replaying the same
// report is safe; executing the work again is not. Store this with that projection.
type MCPEventReceipt struct {
	Claim     MCPClaim `json:"claim" required:"true"`
	Sequence  uint64   `json:"sequence" required:"true"`
	Digest    string   `json:"digest" required:"true"`
	Duplicate bool     `json:"duplicate" required:"true"`
}

func (r MCPEventReceipt) Validate() error {
	if r.Claim.Validate() != nil || r.Sequence == 0 || r.Sequence > core.MaxSafeInteger {
		return ErrInvalid
	}
	return (MCPMutationReceipt{RequestID: r.Claim.DeliveryID, Digest: r.Digest}).Validate()
}

func (r MCPEventReceipt) Retry(event MCPExecutionEvent) (MCPEventReceipt, error) {
	if err := r.Validate(); err != nil {
		return MCPEventReceipt{}, err
	}
	digest, err := event.Digest()
	if err != nil {
		return MCPEventReceipt{}, err
	}
	if event.Claim != r.Claim || event.Sequence != r.Sequence || digest != r.Digest {
		return MCPEventReceipt{}, ErrConflict
	}
	r.Duplicate = true
	return r, nil
}

// DecodeMCPContract is the bounded, duplicate/unknown-field-rejecting decoder
// for these extension DTOs. Context-specific validation still follows decoding.
func DecodeMCPContract(data []byte, destination any) error {
	if len(data) > MCPMaxPayloadBytes {
		return ErrInvalid
	}
	return protocol.DecodeStrict(data, destination)
}

func controlCount(input *MCPTaskInput, cancel *MCPCancelInput, approval *MCPApprovalDecision) int {
	n := 0
	if input != nil {
		n++
	}
	if cancel != nil {
		n++
	}
	if approval != nil {
		n++
	}
	return n
}
