package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// User requests have a separate durable queue, ahead of background inbox events.
// A request received during a read-only summary must start a Main turn rather
// than inheriting the summary's restricted tools and instruction role.
func (c *Conversation) queueUserLocked(ctx context.Context, request protocol.SendMessage) (protocol.SendMessageAccepted, error) {
	return c.queueUserFromLocked(ctx, request, "")
}

func (c *Conversation) queueUserFromLocked(ctx context.Context, request protocol.SendMessage, source string) (protocol.SendMessageAccepted, error) {
	if c.thread == nil {
		return protocol.SendMessageAccepted{}, errors.New("configure an assistant before sending messages")
	}
	var count int
	if err := c.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chat_user_inbox").Scan(&count); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if count >= 100 {
		return protocol.SendMessageAccepted{}, errors.New("user inbox is full")
	}
	message := protocol.Message{Queued: true, ID: storage.ID(), ConversationID: request.ConversationID, Role: protocol.User, Text: request.Text, Status: protocol.Accepted, CreatedAtMS: storage.Now(), RequestID: &request.RequestID}
	raw, _ := json.Marshal(request)
	var metadata map[string]string
	if source != "" {
		metadata = map[string]string{"conversation.user_source." + request.RequestID: source}
	}
	event, err := c.store.SaveWithMetadataMutation(ctx, message, metadata, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO chat_user_inbox(request,message)VALUES(?,?)", string(raw), message.ID)
		return err
	})
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	c.messages = append(c.messages, message)
	c.cursor = *event.Cursor
	c.changedLocked()
	return protocol.SendMessageAccepted{MessageID: message.ID}, nil
}

func (c *Conversation) startNextUserLocked(ctx context.Context) error {
	if c.closed || c.blocked || c.turn != nil {
		return nil
	}
	var sequence int64
	var raw, message string
	err := c.store.DB().QueryRowContext(ctx, "SELECT sequence,request,message FROM chat_user_inbox ORDER BY sequence LIMIT 1").Scan(&sequence, &raw, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var request protocol.SendMessage
	if err = json.Unmarshal([]byte(raw), &request); err != nil {
		return err
	}
	if err = c.applyConfigurationBoundaryLocked(ctx); err != nil {
		return err
	}
	if c.thread == nil {
		return errors.New("queued user request has no assistant configuration")
	}
	c.turn = &savedTurn{Owner: c.options.Owner, BootID: c.options.BootID, Generation: c.thread.Generation, Request: request, UserMessageID: message, State: "queued"}
	var selected protocol.Message
	for _, m := range c.messages {
		if m.ID == message {
			selected = m
			break
		}
	}
	if selected.ID == "" || !selected.Queued {
		c.turn = nil
		return errors.New("queued input projection is missing")
	}
	selected.Queued = false
	turn, _ := json.Marshal(c.turn)
	event, err := c.store.SaveWithMetadataMutation(ctx, selected, map[string]string{"conversation.turn": string(turn)}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM chat_user_inbox WHERE sequence=?", sequence)
		return err
	})
	if err != nil {
		c.turn = nil
		return err
	}
	for i, m := range c.messages {
		if m.ID == selected.ID {
			c.messages[i] = selected
			break
		}
	}
	c.cursor = *event.Cursor
	c.changedLocked()
	c.lastError = nil
	c.input <- request
	return nil
}

// StartPendingUsers is called after the owning adapter has bound its Prepare
// closure to the opened conversation. Uncertain original turns remain fenced.
func (c *Conversation) StartPendingUsers(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startNextUserLocked(ctx)
}
