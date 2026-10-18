package control

import (
	"context"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type ContinueMainCommand struct {
	RequestID     string `json:"request_id" required:"true"`
	UserMessageID string `json:"user_message_id" required:"true"`
	AttemptID     string `json:"attempt_id" required:"true"`
}

// ContinueMain is the owner-facing continuation entry point. The client supplies
// only intent and original input identity; receipts and cleanup remain trusted
// service evidence. It never blindly repeats a started tool.
func (s *Service) ContinueMain(ctx context.Context, owner string, q ContinueMainCommand) (protocol.SendMessageAccepted, error) {
	if !core.Identifier(q.RequestID) || !core.Identifier(q.UserMessageID) || !core.Identifier(q.AttemptID) {
		return protocol.SendMessageAccepted{}, protocol.APIError{Code: "turn_continuation_unavailable", Message: "Invalid continuation identity"}
	}
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	accepted, retryErr := c.RetryContinuation(ctx, q.RequestID, q.UserMessageID, q.AttemptID)
	if retryErr == nil {
		return accepted, nil
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		return accepted, err
	}
	if snapshot.Continuation == nil || snapshot.Continuation.UserMessageID != q.UserMessageID || snapshot.Continuation.AttemptID != q.AttemptID || !errors.Is(retryErr, agent.ErrUncertainEffects) {
		return accepted, protocol.APIError{Code: "turn_continuation_unavailable", Message: retryErr.Error()}
	}
	if snapshot.Continuation.RecoveryRequired {
		recovered, err := s.RecoverMain(ctx, owner, MainRecoveryCommand{Action: "reconcile", UserMessageID: q.UserMessageID})
		if err != nil {
			return accepted, err
		}
		if !recovered.Reconciled {
			return accepted, protocol.APIError{Code: "turn_recovery_required", Message: strings.Join(recovered.Unresolved, "; ")}
		}
	}
	return c.RetryContinuation(ctx, q.RequestID, q.UserMessageID, q.AttemptID)
}
