package assistant

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (c *Conversation) uiContextLocked(ctx context.Context, messages []protocol.UIMessage) ([]protocol.UIMessage, error) {
	seen := map[string]bool{}
	for _, m := range messages {
		seen[m.ID] = true
	}
	// Inputs and composition ancestors are presentation dependencies, not an
	// invitation to fetch an entire turn. Each window has a strict context cap.
	maxMessages := len(messages) + 32
	for i := 0; i < len(messages) && len(messages) < maxMessages; i++ {
		m := messages[i]
		id := m.ReplyTo
		if m.Tool != nil {
			if m.Tool.ParentID != "" { // tool IDs need not equal their message ID
				var parent string
				err := c.store.DB().QueryRowContext(ctx, `SELECT id FROM messages WHERE json_extract(body,'$.role')='tool' AND json_extract(body,'$.tool.turn_id')=? AND json_extract(body,'$.tool.id')=? LIMIT 1`, m.Tool.TurnID, m.Tool.ParentID).Scan(&parent)
				if err == nil {
					id = parent
				} else if !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
			} else {
				var input string
				err := c.store.DB().QueryRowContext(ctx, "SELECT id FROM messages WHERE request_id=?", m.Tool.TurnID).Scan(&input)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				id = input
			}
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		parent, err := c.store.UIMessage(ctx, id)
		if err != nil {
			return nil, err
		}
		if parent != nil {
			parent.ContextOnly = true
			messages = append(messages, *parent)
		}
	}
	if c.turn != nil && !seen[c.turn.UserMessageID] {
		m, err := c.store.UIMessage(ctx, c.turn.UserMessageID)
		if err != nil {
			return nil, err
		}
		if m != nil {
			m.ContextOnly = true
			messages = append(messages, *m)
		}
	}
	for i := range messages {
		messages[i].Version = c.cursor
	}
	if c.options.ProjectUI != nil {
		if err := c.options.ProjectUI(ctx, messages); err != nil {
			return nil, err
		}
	}
	return messages, nil
}

func (c *Conversation) UIHistory(ctx context.Context, before, after int64, around string, limit int) (protocol.UIPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	messages, meta, err := c.store.UIWindow(ctx, before, after, around, limit)
	if err != nil {
		return protocol.UIPage{}, err
	}
	messages, err = c.uiContextLocked(ctx, messages)
	return protocol.UIPage{BootID: c.options.BootID, ConversationID: c.options.ConversationID, Cursor: c.cursor, Messages: messages, History: meta}, err
}

func (c *Conversation) UIFrame(ctx context.Context, after int64, reset bool) (protocol.UIFrame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return protocol.UIFrame{}, errors.New("conversation closed")
	}
	state := c.stateLocked()
	state.Messages = []protocol.Message{}
	frame := protocol.UIFrame{Kind: "delta", BaseCursor: after, UISnapshot: protocol.UISnapshot{Snapshot: state}}
	var err error
	if reset || c.finishError != nil || after > c.cursor || c.cursor-after > storage.UIMaxChanges {
		frame.Kind = "reset"
		frame.Messages, frame.History, err = c.store.UIWindow(ctx, 0, 0, "", storage.UIPageSize)
	} else {
		frame.Messages, err = c.store.UIChanges(ctx, after, c.cursor)
	}
	if err == nil {
		frame.Messages, err = c.uiContextLocked(ctx, frame.Messages)
	}
	// Derive outcomes from authoritative history, not a partial delta that may
	// omit an earlier failure or an intermediate output. Emit only dependencies
	// of this frame; an empty cleanup delta still settles the latest input.
	inputs := map[string]bool{}
	for _, m := range frame.Messages {
		inputs[m.ID] = true
	}
	replies := protocol.ProjectChatReplies(c.messages, state.Turn)
	if len(frame.Messages) == 0 && len(replies) > 0 {
		inputs[replies[len(replies)-1].InputID] = true
	}
	if state.Turn != nil {
		inputs[state.Turn.UserMessageID] = true
	}
	frame.Replies = []protocol.ChatReply{}
	for _, reply := range replies {
		if inputs[reply.InputID] {
			if c.finishError != nil && state.Turn != nil && reply.InputID == state.Turn.UserMessageID {
				reply.Error = c.finishError.Error()
			}
			frame.Replies = append(frame.Replies, reply)
		}
	}
	return frame, err
}

// Register before capturing the initial frame. Capacity one coalesces wakeups;
// a slow reader detects an oversized journal gap and receives a bounded reset.
func (c *Conversation) SubscribeUI(ctx context.Context) (<-chan struct{}, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, errors.New("conversation closed")
	}
	if c.uiSubscribers == nil {
		c.uiSubscribers = map[chan struct{}]struct{}{}
	}
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	c.uiSubscribers[ch] = struct{}{}
	var once sync.Once
	removed := make(chan struct{})
	remove := func() {
		once.Do(func() { c.mu.Lock(); delete(c.uiSubscribers, ch); close(ch); c.mu.Unlock(); close(removed) })
	}
	go func() {
		select {
		case <-ctx.Done():
			remove()
		case <-c.done:
			remove()
		case <-removed:
		}
	}()
	return ch, remove, nil
}
