package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

type DeleteAssistantRequest struct {
	From        protocol.AgentRef `json:"from"`
	AssistantID string            `json:"assistant_id"`
	Revision    uint64            `json:"revision"`
}

type AssistantDeletion struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// AssistantDeletionBackend retires an idle identity; it grants no execution or
// filesystem configuration authority. Only authenticated user callers enter it.
type AssistantDeletionBackend interface {
	DeleteAssistant(context.Context, DeleteAssistantRequest) error
}

func (s *Service) DeleteAssistant(ctx context.Context, link string, input DeleteAssistantRequest) (AssistantDeletion, error) {
	if input.From.Kind != "user" || !input.From.Valid() || input.From.NodeID != s.options.HostIdentity.NodeID {
		return AssistantDeletion{}, ErrUnauthorized
	}
	c, err := s.Get(ctx, link)
	if err != nil {
		return AssistantDeletion{}, err
	}
	session, err := s.businessSession(ctx, c)
	if err != nil {
		return AssistantDeletion{}, err
	}
	var result AssistantDeletion
	err = s.call(ctx, session, "assistant.delete", input, &result)
	if err == nil && (result.ID != input.AssistantID || !result.Deleted) {
		err = ErrConflict
	}
	if err == nil {
		// A successful retirement remains successful if a later directory read fails.
		_, _ = s.Directory(ctx, link)
	}
	return result, err
}
