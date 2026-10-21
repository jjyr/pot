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

func (s *Service) Profile(ctx context.Context, id string, input ProfileRequest) (Profile, error) {
	if input.From.Kind != "user" || !input.From.Valid() || input.From.NodeID != s.options.HostIdentity.NodeID {
		return Profile{}, ErrUnauthorized
	}
	c, err := s.Get(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	session, err := s.businessSession(ctx, c)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	err = s.call(ctx, session, "assistant.profile", input, &profile)
	if err == nil && profile.ID != input.AssistantID {
		err = ErrConflict
	}
	if err == nil {
		_, err = s.Directory(ctx, id)
	}
	return profile, err
}
