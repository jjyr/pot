package assistant

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"slices"
	"strings"
	"unicode/utf8"
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

const referenceContextBytes = 8192

func (f *turnFeedback) resolveReferences(ctx context.Context, messages []modelprotocol.Message) ([]modelprotocol.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return nil, err
	}
	out := slices.Clone(messages)
	var sources map[string]protocol.Message
	for i, message := range out {
		out[i].ReferenceContents = nil
		if err := core.ValidateMessageReferences(message.References); err != nil {
			return nil, err
		}
		if len(message.References) > 0 && sources == nil {
			sources = make(map[string]protocol.Message)
			for _, source := range c.messages {
				if source.ConversationID == c.options.ConversationID && (source.Role == protocol.User || source.Role == protocol.Assistant) && source.Text != "" {
					sources[source.ID] = source
				}
			}
		}
		for _, ref := range message.References {
			source, ok := sources[ref.MessageID]
			if !ok {
				return nil, errors.New("referenced message is unavailable in this conversation")
			}
			text, truncated := referenceContext(source.Text, ref.Quote)
			out[i].ReferenceContents = append(out[i].ReferenceContents, modelprotocol.ReferenceContent{Role: string(source.Role), Quote: ref.Quote, Context: text, Truncated: truncated})
		}
	}
	return out, nil
}

// Prefer context surrounding a literal selection. Rendered Markdown selections
// need not match source bytes, so retain the quote separately and fall back to
// the source's opening text when no literal match exists.
func referenceContext(text, quote string) (string, bool) {
	if len(text) <= referenceContextBytes {
		return text, false
	}
	start := 0
	if quote != "" {
		if at := strings.Index(text, quote); at >= 0 {
			start = max(0, at-(referenceContextBytes-len(quote))/2)
		}
	}
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	end := min(len(text), start+referenceContextBytes)
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[start:end], true
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
