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
