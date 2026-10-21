package agentlink

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/protocol"
	"slices"
)

// Operation names describe behavior, never an adapter type or machine location.
// Payloads use typed operation DTOs. Execution configuration remains owner-only;
// adapters may explicitly advertise presentation-only peer profile editing.
type Operation string

const (
	ChatRead           Operation = "chat.read"
	ChatSend           Operation = "chat.send"
	ChatReceipt        Operation = "chat.receipt"
	ChatHistory        Operation = "chat.history"
	ChatSubscribe      Operation = "chat.subscribe"
	ChatCancel         Operation = "chat.cancel"
	ChatSteer          Operation = "chat.steer"
	ChatControl        Operation = "chat.control"
	ChatRetry          Operation = "chat.retry"
	ChatContinue       Operation = "chat.continue"
	ChatAttachments    Operation = "chat.attachments"
	ChatReferences     Operation = "chat.references"
	ChatCompletions    Operation = "chat.completions"
	AssistantConfigure Operation = "assistant.configure"
	AssistantRules     Operation = "assistant.rules"
	AssistantProfile   Operation = "assistant.profile"
	AssistantDelete    Operation = "assistant.delete"
	AssistantUsage     Operation = "assistant.usage"
	Approvals          Operation = "approvals.manage"
	TaskSubmit         Operation = "task.submit"
	TaskRead           Operation = "task.read"
	TaskList           Operation = "task.list"
	TaskCancel         Operation = "task.cancel"
	TaskInterrupt      Operation = "task.interrupt"
	TaskControl        Operation = "task.control"
	TaskInput          Operation = "task.input"
	TaskEvents         Operation = "task.events"
	TaskFiles          Operation = "task.files"
	TaskRecovery       Operation = "task.recovery"
	TaskResume         Operation = "task.resume"
	TaskArchive        Operation = "task.archive"
	TaskOutcomes       Operation = "task.outcomes"
	TaskConfigure      Operation = "task.configure"
	Schedules          Operation = "schedules.manage"
)

type Target struct {
	Locale      string `json:"locale,omitempty"`
	LinkID      string `json:"link_id,omitempty"`
	AssistantID string `json:"assistant_id"`
}

// Call parameters are operation-specific DTOs plus bounded read cursors. ID is
// a task resource, not a path. Action distinguishes reading and adding inputs.
type Call struct {
	ID     string            `json:"id,omitempty"`
	Action string            `json:"action,omitempty"`
	Query  map[string]string `json:"query,omitempty"`
	Body   json.RawMessage   `json:"body,omitempty"`
}

type Description struct {
	Version    int         `json:"version"`
	Scope      string      `json:"scope"`
	Operations []Operation `json:"operations"`
}

func (d Description) Supports(op Operation) bool {
	return d.Version == 1 && slices.Contains(d.Operations, op)
}

// Port is the product boundary used by chat, tasks, schedules and agent tools.
// The host binds caller identity before invoking it. A target cannot select a
// sender, an authorization scope or an arbitrary operator HTTP route.
type Port interface {
	Describe(context.Context, Target) (Description, error)
	Call(context.Context, Target, Operation, Call) (any, error)
	Subscribe(context.Context, Target) (Subscription, error)
}

// Subscription coalesces wakeups; Frame reads a bounded cursor-based update.
// Close releases it without changing conversation or execution lifetime.
type Subscription struct {
	Updates <-chan struct{}
	Frame   func(context.Context, int64, bool) (protocol.UIFrame, error)
	Close   func()
}

func PeerOperations() []Operation {
	return []Operation{ChatRead, ChatSend, ChatReceipt, ChatSubscribe, TaskSubmit, TaskRead, TaskCancel}
}

func OwnerOperations() []Operation {
	return []Operation{ChatRead, ChatSend, ChatReceipt, ChatHistory, ChatSubscribe,
		ChatCancel, ChatSteer, ChatControl, ChatRetry, ChatContinue, ChatAttachments,
		ChatReferences, ChatCompletions, AssistantConfigure, AssistantRules, AssistantProfile, AssistantDelete, AssistantUsage, Approvals,
		TaskSubmit, TaskRead, TaskList, TaskCancel, TaskInterrupt, TaskControl, TaskInput,
		TaskEvents, TaskFiles, TaskRecovery, TaskResume, TaskArchive, TaskOutcomes,
		TaskConfigure, Schedules}
}
