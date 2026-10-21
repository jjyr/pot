package assistant

import (
	"context"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/protocol"
)

// RequireUserTool binds owner configuration writes to an active ordinary user
// turn. Inbox reference data, peer conversations and expired turns grant no
// configuration authority. A new user request during a summary runs separately.
func (c *Conversation) RequireUserTool(ctx context.Context, turn string, generation uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.turn == nil || c.thread == nil || c.thread.Generation != generation || c.turn.Request.RequestID != turn || strings.HasPrefix(turn, "pot-result-") || isCompactRequest(turn) {
		return errors.New("notification_filters_require_user_turn")
	}
	message, err := c.store.Duplicate(ctx, turn)
	if err != nil {
		return err
	}
	if message == nil || message.Role != protocol.User {
		return errors.New("notification_filters_require_user_turn")
	}
	return nil
}
