package core

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const MessageReferenceCount = 8
const MessageQuoteBytes = 4096

// MessageReference identifies a message in the same owning conversation. Quote
// is the user's selected rendered text, not a copy of the source message.
type MessageReference struct {
	MessageID string `json:"message_id"`
	Quote     string `json:"quote,omitempty"`
}

func ValidateMessageReferences(refs []MessageReference) error {
	if len(refs) > MessageReferenceCount {
		return fmt.Errorf("too many message references")
	}
	seen := map[MessageReference]bool{}
	total := 0
	for _, ref := range refs {
		if !Identifier(ref.MessageID) || !utf8.ValidString(ref.Quote) || len(ref.Quote) > MessageQuoteBytes || ref.Quote != "" && strings.TrimSpace(ref.Quote) == "" || seen[ref] {
			return fmt.Errorf("invalid message reference")
		}
		seen[ref] = true
		total += len(ref.Quote)
	}
	if total > 16384 {
		return fmt.Errorf("message quotes exceed 16 KiB")
	}
	return nil
}

// ConversationInput preserves each accepted user's identity and references in a
// batch without combining them into another user's text.
type ConversationInput struct {
	ID          string
	Text        string
	Attachments []Attachment
	References  []MessageReference
}
