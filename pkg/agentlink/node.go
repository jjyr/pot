package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

// Directory describes the assistants and operations owned by an autonomous node.
// DefaultAssistantID is persisted by the adapter and initially selects its first assistant.
type Directory struct {
	NodeID             string     `json:"node_id"`
	Name               string     `json:"name"`
	Adapter            string     `json:"adapter"`
	Assistants         []Identity `json:"assistants"`
	DefaultAssistantID string     `json:"default_assistant_id"`
	Capabilities       []string   `json:"capabilities"`
}

// TaskRequest is an immutable, idempotent delegation. From is authenticated by
// Agent Link; the receiving adapter owns permissions, workspace and execution.
type TaskRequest struct {
	// Native is receiver-owned configuration passed only by the in-process port.
	// It is never serialized or admitted from a remote node.
	Native      *protocol.CreateTask `json:"-"`
	RequestID   string               `json:"request_id"`
	From        protocol.AgentRef    `json:"from"`
	AssistantID string               `json:"assistant_id,omitempty"`
	Text        string               `json:"text"`
	Workspace   string               `json:"workspace,omitempty"`
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

// Backend is the business port shared by in-process and remote adapters. Task
// lookups are scoped to the originating node; adapters must durably deduplicate
// requests before starting work, and fence interrupted work with unknown effects.
type Backend interface {
	Directory(context.Context) (Directory, error)
	Observe(context.Context, string) (Observation, error)
	Submit(context.Context, TaskRequest) (Task, error)
	Status(context.Context, protocol.AgentRef, string) (Task, error)
	Cancel(context.Context, protocol.AgentRef, string) (Task, error)
}

func (s *Service) localIdentity(ctx context.Context, id string) (Identity, error) {
	if id == "" && s.options.Backend != nil {
		d, err := s.options.Backend.Directory(ctx)
		if err != nil {
			return Identity{}, err
		}
		if err = validateDirectory(d, s.options.HostIdentity.NodeID); err != nil {
			return Identity{}, err
		}
		id = d.DefaultAssistantID
	}
	return s.options.Identity(ctx, id)
}
