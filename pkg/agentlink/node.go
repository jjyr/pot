package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

// Endpoint publishes one receiver address together with its effective peer capabilities.
type Endpoint struct {
	Identity
	Capabilities Description `json:"capabilities"`
}

// Directory is a node-owned catalogue. Connection routes are not receiver identities.
type Directory struct {
	NodeID        string             `json:"node_id"`
	Name          string             `json:"name"`
	Adapter       string             `json:"adapter"`
	Targets       []Endpoint         `json:"targets"`
	DefaultTarget *protocol.AgentRef `json:"default_target,omitempty"`
}

func NodeTarget(node string) protocol.AgentRef {
	return protocol.AgentRef{NodeID: node, Kind: "node", AgentID: node}
}
func AssistantTarget(node, assistant string) protocol.AgentRef {
	return protocol.AgentRef{NodeID: node, Kind: "assistant", AgentID: assistant}
}
func ValidTarget(target protocol.AgentRef) bool {
	return target.Valid() && (target.Kind == "assistant" || target.Kind == "node" && target.AgentID == target.NodeID)
}
func (d Directory) Capabilities(target protocol.AgentRef) Description {
	for _, entry := range d.Targets {
		if entry.Address == target {
			return entry.Capabilities
		}
	}
	return Description{}
}
func (d Directory) Assistants() []Endpoint {
	result := []Endpoint{}
	for _, entry := range d.Targets {
		if entry.Address.Kind == "assistant" {
			result = append(result, entry)
		}
	}
	return result
}
func (d Directory) DefaultAssistant() string {
	if d.DefaultTarget != nil && d.DefaultTarget.Kind == "assistant" {
		return d.DefaultTarget.AgentID
	}
	return ""
}

// TaskRequest is an immutable, idempotent delegation. From is authenticated by
// Agent Link; the receiving adapter owns permissions, workspace and execution.
type TaskRequest struct {
	Target protocol.AgentRef `json:"target"`
	// Configuration is an explicit task.configure operation. It uses the same
	// serializable contract as ordinary tasks, but requires owner authority.
	Configuration *protocol.CreateTask `json:"configuration,omitempty"`
	RequestID     string               `json:"request_id"`
	From          protocol.AgentRef    `json:"from"`
	Text          string               `json:"text"`
	Workspace     string               `json:"workspace,omitempty"`
}
type Task struct {
	RunID     string    `json:"run_id,omitempty"`
	Receiver  Identity  `json:"receiver"`
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
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
}

func (s *Service) localIdentity(ctx context.Context) (Identity, error) {
	return s.options.Identity(ctx, "")
}
