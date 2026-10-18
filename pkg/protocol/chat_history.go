package protocol

import (
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"strconv"
)

// UIMessage adds a durable insertion sequence and a read version to a message
// projection. Neither field participates in model history or persistence.
type UIMessage struct {
	Message     `json:"message" required:"true"`
	Sequence    string `json:"sequence"`
	Version     int64  `json:"version"`
	ContextOnly bool   `json:"context_only,omitempty"`
}

type UIHistory struct {
	Before   string `json:"before"`
	After    string `json:"after"`
	HasMore  bool   `json:"has_more"`
	HasNewer bool   `json:"has_newer"`
}

type UIPage struct {
	BootID         string      `json:"boot_id"`
	ConversationID string      `json:"conversation_id"`
	Cursor         int64       `json:"cursor"`
	Messages       []UIMessage `json:"messages"`
	History        UIHistory   `json:"history"`
}

// UISnapshot is exclusively the client projection. Snapshot remains the complete
// internal read contract used by agent services, recovery and reference lookup.
type UISnapshot struct {
	Snapshot `json:"state" required:"true"`
	Messages []UIMessage `json:"messages"`
	History  UIHistory   `json:"history"`
}

type UIFrame struct {
	Kind       string `json:"kind"` // reset or delta
	BaseCursor int64  `json:"base_cursor"`
	UISnapshot `json:"view" required:"true"`
}

func (s UISnapshot) Validate() error {
	if err := s.Snapshot.Validate(); err != nil {
		return err
	}
	for _, m := range s.Messages {
		if err := m.Message.Validate(); err != nil {
			return err
		}
		if m.ConversationID != s.ConversationID {
			return fmt.Errorf("cross-conversation UI message")
		}
		n, err := strconv.ParseInt(m.Sequence, 10, 64)
		if err != nil || n <= 0 || uint64(n) > core.MaxSafeInteger || m.Version < 0 || m.Version > s.Cursor {
			return fmt.Errorf("invalid UI message version")
		}
	}
	return nil
}
func (f UIFrame) Validate() error {
	if f.Kind != "reset" && f.Kind != "delta" || f.BaseCursor < 0 || uint64(f.BaseCursor) > core.MaxSafeInteger {
		return fmt.Errorf("invalid UI frame")
	}
	return f.UISnapshot.Validate()
}
