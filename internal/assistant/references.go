package assistant

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"slices"
)

func (c *Conversation) validateReferencesLocked(refs []core.MessageReference) error {
	if err := core.ValidateMessageReferences(refs); err != nil {
		return err
	}
	for _, ref := range refs {
		found := slices.ContainsFunc(c.messages, func(m protocol.Message) bool {
			return m.ID == ref.MessageID && m.ConversationID == c.options.ConversationID && (m.Role == protocol.User || m.Role == protocol.Assistant) && m.Text != ""
		})
		if !found {
			return errors.New("referenced message is unavailable in this conversation")
		}
	}
	return nil
}

// ReadMessages is bound to this conversation. Callers cannot supply another
// assistant, Control or conversation to widen the read.
func (c *Conversation) ReadMessages(ctx context.Context, ids []string) ([]protocol.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(ids) == 0 || len(ids) > core.MessageReferenceCount {
		return nil, errors.New("request one to eight message IDs")
	}
	out := make([]protocol.Message, 0, len(ids))
	for _, id := range ids {
		found := false
		for _, m := range c.messages {
			if m.ID == id && (m.Role == protocol.User || m.Role == protocol.Assistant) {
				m.Attachments = slices.Clone(m.Attachments)
				m.References = slices.Clone(m.References)
				out = append(out, m)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("referenced message is unavailable in this conversation")
		}
	}
	return out, nil
}

func (f *turnFeedback) messageID(ctx context.Context) (string, error) {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return "", err
	}
	for _, message := range c.messages {
		if message.ID == c.turn.ReplyMessageID && (message.Status == protocol.Streaming || (c.turn.Resuming || message.Status == protocol.Failed) && message.Text == "") {
			return message.ID, nil
		}
	}
	return storage.ID(), nil
}
