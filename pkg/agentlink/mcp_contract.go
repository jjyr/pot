package agentlink

import (
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// MCPContractVersion versions Pot's receiver extension, independently of MCP.
const MCPContractVersion = 1

const (
	MCPMaxTextBytes   = protocol.MaxMailText
	MCPMaxInputBytes  = 24000
	MCPMaxResultBytes = 1024 * 1024
	// MCPMaxPayloadBytes bounds receiver extension payloads. HTTP handlers must
	// bound the body before decoding, including for registration and events.
	MCPMaxPayloadBytes = 24 * 1024 * 1024
)

// MCPExecutionBinding is trusted execution metadata, never tool arguments.
// The execution boundary must verify the active actor/task/run before binding it.
type MCPExecutionBinding struct {
	Actor protocol.AgentRef `json:"actor" required:"true"`
	Task  protocol.AgentRef `json:"task" required:"true"`
	RunID string            `json:"run_id" required:"true"`
}

func (b MCPExecutionBinding) Validate() error {
	if !mcpActor(b.Actor) || !b.Task.Valid() || b.Task.Kind != "task" || b.Actor.NodeID != b.Task.NodeID || !core.Identifier(b.RunID) || b.Actor.Kind == "task" && b.Actor != b.Task {
		return ErrUnauthorized
	}
	return nil
}

// MCPCaller is constructed only after authentication. Its private fields are
// deliberately unavailable to a tool JSON decoder. Constructors validate shape,
// not credentials, connection consent, revocation or execution liveness.
type MCPCaller struct {
	connection string
	actor      protocol.AgentRef
	execution  *MCPExecutionBinding
}

// NewMCPCaller binds a provisioned ordinary caller. User-kind is an identity,
// not owner authority; this context never authorizes owner APIs or approvals.
func NewMCPCaller(connection string, actor protocol.AgentRef) (MCPCaller, error) {
	if !core.Identifier(connection) || !actor.Valid() || actor.Kind != "user" {
		return MCPCaller{}, ErrUnauthorized
	}
	return MCPCaller{connection: connection, actor: actor}, nil
}

// NewMCPNodeCaller reuses an authenticated node's existing trust over its own
// assistants/tasks. It cannot assert a user, owner or another node's actor.
func NewMCPNodeCaller(connection, node string, actor protocol.AgentRef, execution *MCPExecutionBinding) (MCPCaller, error) {
	if !core.Identifier(connection) || !mcpActor(actor) || actor.NodeID != node || actor.Kind == "task" && execution == nil {
		return MCPCaller{}, ErrUnauthorized
	}
	if execution != nil {
		if execution.Validate() != nil || execution.Actor != actor {
			return MCPCaller{}, ErrUnauthorized
		}
		copy := *execution
		execution = &copy
	}
	return MCPCaller{connection, actor, execution}, nil
}

func (c MCPCaller) Actor() protocol.AgentRef { return c.actor }
func (c MCPCaller) ConnectionID() string     { return c.connection }
func (c MCPCaller) Execution() *MCPExecutionBinding {
	if c.execution == nil {
		return nil
	}
	copy := *c.execution
	return &copy
}
func (c MCPCaller) validate() error {
	if c.actor.Kind == "user" && c.execution == nil {
		_, err := NewMCPCaller(c.connection, c.actor)
		return err
	}
	_, err := NewMCPNodeCaller(c.connection, c.actor.NodeID, c.actor, c.execution)
	return err
}
func mcpActor(actor protocol.AgentRef) bool {
	return actor.Valid() && (actor.Kind == "assistant" || actor.Kind == "task")
}
func mcpText(text string, limit int) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" && len(text) <= limit
}

// MCPSubmitInput is model-visible. From and execution configuration are absent.
type MCPSubmitInput struct {
	RequestID string            `json:"request_id" required:"true"`
	Target    protocol.AgentRef `json:"target" required:"true"`
	Text      string            `json:"text" required:"true"`
}

func (r MCPSubmitInput) Validate() error {
	if !core.Identifier(r.RequestID) || !ValidTarget(r.Target) || !mcpText(r.Text, MCPMaxTextBytes) {
		return ErrInvalid
	}
	return nil
}
func (r MCPSubmitInput) Bind(c MCPCaller) (TaskRequest, error) {
	if err := c.validate(); err != nil {
		return TaskRequest{}, err
	}
	if err := r.Validate(); err != nil {
		return TaskRequest{}, err
	}
	return TaskRequest{RequestID: r.RequestID, Target: r.Target, Text: r.Text, From: c.actor}, nil
}
func (r MCPSubmitInput) Digest(c MCPCaller) (string, error) {
	if _, err := r.Bind(c); err != nil {
		return "", err
	}
	return mcpDigest(c, TaskSubmit, r)
}

// MCPTaskInput enqueues owner guidance. RunID fences admission to the mailbox;
// it does not require immediate steering or pin the later consumption run.
type MCPTaskInput struct {
	RequestID string `json:"request_id" required:"true"`
	TaskID    string `json:"task_id" required:"true"`
	RunID     string `json:"run_id" required:"true"`
	Text      string `json:"text" required:"true"`
}

func (r MCPTaskInput) Validate() error {
	if !core.Identifier(r.RequestID) || !core.Identifier(r.TaskID) || !core.Identifier(r.RunID) || !mcpText(r.Text, MCPMaxInputBytes) {
		return ErrInvalid
	}
	return nil
}
func (r MCPTaskInput) Guidance(c MCPCaller, current Task) (TaskGuidance, error) {
	if err := c.validate(); err != nil {
		return TaskGuidance{}, err
	}
	if err := r.Validate(); err != nil {
		return TaskGuidance{}, err
	}
	if current.ID != r.TaskID || current.RunID != r.RunID {
		return TaskGuidance{}, ErrFenced
	}
	return TaskGuidance{From: c.actor, ID: r.TaskID, RequestID: r.RequestID, Text: r.Text}, nil
}
func (r MCPTaskInput) Digest(c MCPCaller) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	return mcpDigest(c, TaskInput, r)
}

// MCPCancelInput records intent. Only execution evidence confirms cancellation.
type MCPCancelInput struct {
	RequestID string `json:"request_id" required:"true"`
	TaskID    string `json:"task_id" required:"true"`
	RunID     string `json:"run_id" required:"true"`
}

func (r MCPCancelInput) Validate() error {
	if !core.Identifier(r.RequestID) || !core.Identifier(r.TaskID) || !core.Identifier(r.RunID) {
		return ErrInvalid
	}
	return nil
}
func (r MCPCancelInput) Digest(c MCPCaller) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	return mcpDigest(c, TaskCancel, r)
}

// This private helper accepts only the typed mutation methods above. It is not
// an operation dispatcher and must not be exposed as an arbitrary JSON tool.
func mcpDigest(c MCPCaller, op Operation, input any) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	return core.HashJSON(struct {
		Version    int                  `json:"version"`
		Connection string               `json:"connection_id"`
		Actor      protocol.AgentRef    `json:"actor"`
		Execution  *MCPExecutionBinding `json:"execution"`
		Operation  Operation            `json:"operation"`
		Input      any                  `json:"input"`
	}{MCPContractVersion, c.connection, c.actor, c.execution, op, input})
}

// MCPMutationReceipt is durable acceptance, not execution completion. Persist it
// with the existing business receipt under connection/actor/operation/request ID.
type MCPMutationReceipt struct {
	RequestID string `json:"request_id" required:"true"`
	Digest    string `json:"digest" required:"true"`
	Duplicate bool   `json:"duplicate" required:"true"`
}

func (r MCPMutationReceipt) Validate() error {
	raw, err := hex.DecodeString(r.Digest)
	if !core.Identifier(r.RequestID) || err != nil || len(raw) != 32 || strings.ToLower(r.Digest) != r.Digest {
		return ErrInvalid
	}
	return nil
}

// Retry returns the original immutable acceptance for an exact retry. Call this
// before fresh capability/admission checks; it does not permit re-execution.
func (r MCPMutationReceipt) Retry(request, digest string) (MCPMutationReceipt, error) {
	if err := r.Validate(); err != nil {
		return MCPMutationReceipt{}, err
	}
	if request != r.RequestID || digest != r.Digest {
		return MCPMutationReceipt{}, ErrConflict
	}
	r.Duplicate = true
	return r, nil
}

type MCPErrorCode string
type MCPRetry string

const (
	MCPInvalid      MCPErrorCode = "invalid"
	MCPUnauthorized MCPErrorCode = "unauthorized"
	MCPUnsupported  MCPErrorCode = "unsupported_capability"
	MCPConflict     MCPErrorCode = "request_conflict"
	MCPFenced       MCPErrorCode = "fenced"
	MCPUnavailable  MCPErrorCode = "unavailable"
	MCPUnknown      MCPErrorCode = "outcome_unknown"
	MCPDoNotRetry   MCPRetry     = "do_not_retry"
	MCPRetrySame    MCPRetry     = "retry_same_request"
	MCPReconcile    MCPRetry     = "reconcile"
)

// MCPError contains machine codes only. Presentation layers localize messages.
// Unavailable is safe only when admission is known not to have happened. Any
// uncertainty after mutation dispatch is outcome_unknown, including response loss.
type MCPError struct {
	Code MCPErrorCode `json:"code" required:"true"`
}

func (e MCPError) Error() string { return string(e.Code) }
func (e MCPError) Validate() error {
	switch e.Code {
	case MCPInvalid, MCPUnauthorized, MCPUnsupported, MCPConflict, MCPFenced, MCPUnavailable, MCPUnknown:
		return nil
	}
	return ErrInvalid
}
func (e MCPError) Retry() MCPRetry {
	switch e.Code {
	case MCPUnavailable:
		return MCPRetrySame
	case MCPUnknown:
		return MCPReconcile
	default:
		return MCPDoNotRetry
	}
}
