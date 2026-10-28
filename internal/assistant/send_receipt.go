package assistant

import (
	"context"
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// SendReceipt reads durable storage independently of transcript paging. An absent
// receipt is unknown while the original request can still arrive, including
// guidance that will join the durable inbox after its original turn ends.
func (c *Conversation) SendReceipt(ctx context.Context, q protocol.SendReceiptQuery) (protocol.SendReceipt, error) {
	result := protocol.SendReceipt{SendReceiptQuery: q, State: "unconfirmed", RuntimeID: c.options.RuntimeID, OwnerID: c.options.Owner}
	if !core.Identifier(q.RequestID) || q.ConversationID != c.options.ConversationID ||
		(q.UserMessageID != "" && !core.Identifier(q.UserMessageID)) ||
		(q.RetryUserMessageID != "" && (!core.Identifier(q.RetryUserMessageID) || q.UserMessageID != "")) {
		return result, errors.New("invalid send receipt identity")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var message *protocol.Message
	var err error
	switch {
	case q.RetryUserMessageID != "":
		target, found, e := c.store.Get(ctx, "conversation.retry.receipt."+q.RequestID)
		if e != nil {
			return result, e
		}
		if found && target == q.RetryUserMessageID {
			result.State = "accepted"
			result.MessageID = target
		}
		return result, nil
	case q.UserMessageID != "":
		message, err = c.store.Message(ctx, steeringMessageID(q.RequestID, q.UserMessageID))
		if err == nil && message == nil {
			source, found, e := c.store.Get(ctx, "conversation.user_source."+q.RequestID)
			if e != nil {
				return result, e
			}
			if found && source == q.UserMessageID {
				message, err = c.store.Duplicate(ctx, q.RequestID)
			}
		}
	default:
		source, found, e := c.store.Get(ctx, "conversation.user_source."+q.RequestID)
		if e != nil {
			return result, e
		}
		if found && source != "" {
			return result, nil
		}
		message, err = c.store.Duplicate(ctx, q.RequestID)
		if message != nil && message.Steering {
			message = nil
		}
	}
	if err != nil {
		return result, err
	}
	if message != nil && message.Role == protocol.User && message.ConversationID == q.ConversationID {
		result.State = "accepted"
		result.MessageID = message.ID
	} else if q.UserMessageID != "" {
		target, err := c.store.Message(ctx, q.UserMessageID)
		if err != nil {
			return result, err
		}
		if target == nil || target.RequestID == nil || target.ConversationID != q.ConversationID {
			result.State = "rejected"
		}
	}
	return result, nil
}
