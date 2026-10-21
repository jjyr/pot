package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

// Directory describes the assistants and operations owned by an autonomous node.
// DefaultAssistantID is persisted by the adapter and initially selects its first assistant.
type Directory struct {
	NodeID             string                 `json:"node_id"`
	Name               string                 `json:"name"`
	Adapter            string                 `json:"adapter"`
	Assistants         []Identity             `json:"assistants"`
	DefaultAssistantID string                 `json:"default_assistant_id"`
	Operations         map[string]Description `json:"operations"`
}

// TaskRequest is an immutable, idempotent delegation. From is authenticated by
// Agent Link; the receiving adapter owns permissions, workspace and execution.
type TaskRequest struct {
	// Configuration is an explicit task.configure operation. It uses the same
	// serializable contract as ordinary tasks, but requires owner authority.
	Configuration *protocol.CreateTask `json:"configuration,omitempty"`
	RequestID     string               `json:"request_id"`
	From          protocol.AgentRef    `json:"from"`
	AssistantID   string               `json:"assistant_id,omitempty"`
	Text          string               `json:"text"`
	Workspace     string               `json:"workspace,omitempty"`
}
type Task struct {
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
	Assistant Identity  `json:"assistant"`
	State     string    `json:"state"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Backend is the receiver execution port shared by in-process and remote calls.
// Adapters enforce per-assistant operations and original caller ownership. They
// must resolve accepted retries before current-directory admission checks,
// durably deduplicate new work, and fence interrupted work with unknown effects.
type Backend interface {
	Directory(context.Context) (Directory, error)
	Observe(context.Context, string) (Observation, error)
	Submit(context.Context, TaskRequest) (Task, error)
	Status(context.Context, protocol.AgentRef, string) (Task, error)
	Cancel(context.Context, protocol.AgentRef, string) (Task, error)
	ChatStatus(context.Context, ChatLookup) (ChatStatus, error)
}

func (s *Service) localIdentity(ctx context.Context) (Identity, error) {
	return s.options.Identity(ctx, "")
}
