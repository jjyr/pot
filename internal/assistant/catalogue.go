package assistant

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"strings"
)

type CatalogueState struct {
	ReplyID            string `json:"reply_id"`
	ReplyCount         uint64 `json:"reply_count"`
	Preview            string `json:"preview"`
	Responding         bool   `json:"responding"`
	Running            bool   `json:"running"`
	Failed             bool   `json:"failed"`
	NeedsConfiguration bool   `json:"needs_configuration"`
}

// Catalogue avoids serializing every conversation message for sidebar updates.
func (c *Conversation) Catalogue(ctx context.Context) (CatalogueState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := CatalogueState{Responding: c.turn != nil, Running: c.turn != nil, Failed: c.blocked || c.lastError != nil, NeedsConfiguration: c.thread == nil}
	count, err := c.store.NotificationCount(ctx)
	if err != nil {
		return out, err
	}
	if count < 0 || uint64(count) > core.MaxSafeInteger {
		return out, errors.New("reply count exceeds supported range")
	}
	out.ReplyCount = uint64(count)
	reply, err := c.store.LastCompletedReply(ctx)
	if err != nil {
		return out, err
	}
	if reply != nil {
		out.ReplyID = reply.ID
		text := []rune(strings.Join(strings.Fields(reply.Text), " "))
		out.Preview = string(text[:min(120, len(text))])
	}
	return out, nil
}
