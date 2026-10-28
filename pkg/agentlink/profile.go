package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

// Profile contains only presentation identity, never execution configuration.
type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar"`
	Revision uint64 `json:"revision"`
}

type ProfileRequest struct {
	From        protocol.AgentRef `json:"from"`
	AssistantID string            `json:"assistant_id"`
	Action      string            `json:"action"`
	Name        *string           `json:"name,omitempty"`
	Avatar      *string           `json:"avatar,omitempty"`
	Revision    uint64            `json:"revision,omitempty"`
}

// ProfileBackend is an explicitly advertised peer profile editor. It never
// includes deletion, workspace/model changes or other owner configuration.
type ProfileBackend interface {
	Profile(context.Context, ProfileRequest) (Profile, error)
}
