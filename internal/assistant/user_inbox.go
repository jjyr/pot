package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/inbox"
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
	message := protocol.Message{Queued: true, ID: storage.ID(), ConversationID: request.ConversationID, Role: protocol.User, Text: request.Text, Attachments: request.Attachments, References: request.References, Status: protocol.Accepted, CreatedAtMS: storage.Now(), RequestID: &request.RequestID}
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
	var raw, message string
	err := c.store.DB().QueryRowContext(ctx, "SELECT request,message FROM chat_user_inbox ORDER BY sequence LIMIT 1").Scan(&raw, &message)
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
	if err = c.aggregateUsersLocked(ctx); err != nil {
		c.turn = nil
		return err
	}
	c.lastError = nil
	c.input <- request
	return nil
}

// Membership is persisted with queue removal and the original projections. The
// anchor request remains the execution identity; individual receipts stay intact.
func (c *Conversation) aggregateUsersLocked(ctx context.Context) error {
	next := *c.turn
	next.Inputs = append([]protocol.SendMessage(nil), c.turn.Inputs...)
	next.UserMessages = append([]string(nil), c.turn.UserMessages...)
	if len(next.Inputs) == 0 {
		next.Inputs = []protocol.SendMessage{next.Request}
		next.UserMessages = []string{next.UserMessageID}
	}
	size := 0
	for _, input := range next.Inputs {
		size += len(input.Text)
	}
	rows, err := c.store.DB().QueryContext(ctx, "SELECT sequence,request,message FROM chat_user_inbox ORDER BY sequence LIMIT ?", inbox.MaxBatchMessages)
	if err != nil {
		return err
	}
	type queued struct {
		sequence int64
		request  protocol.SendMessage
		message  string
	}
	var chosen []queued
	for rows.Next() {
		var item queued
		var raw string
		if err = rows.Scan(&item.sequence, &raw, &item.message); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &item.request); err != nil {
			rows.Close()
			return err
		}
		if err = item.request.Validate(); err != nil || item.request.ConversationID != c.options.ConversationID {
			rows.Close()
			return errors.New("queued user identity changed")
		}
		if item.message != next.UserMessageID {
			if len(item.request.Attachments) > 0 || len(next.Request.Attachments) > 0 {
				break
			}
			if len(next.Inputs) >= inbox.MaxBatchMessages || size+len(item.request.Text) > inbox.MaxBatchBytes {
				break
			}
			next.Inputs = append(next.Inputs, item.request)
			next.UserMessages = append(next.UserMessages, item.message)
			size += len(item.request.Text)
		}
		chosen = append(chosen, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(chosen) == 0 {
		return nil
	}
	selected := make([]protocol.Message, 0, len(chosen))
	for _, item := range chosen {
		var found *protocol.Message
		for _, message := range c.messages {
			if message.ID == item.message {
				copy := message
				found = &copy
				break
			}
		}
		if found == nil || !found.Queued || found.Text != item.request.Text || found.RequestID == nil || *found.RequestID != item.request.RequestID {
			return errors.New("queued input projection is missing or changed")
		}
		found.Queued = false
		selected = append(selected, *found)
	}
	rawTurn, _ := json.Marshal(next)
	events, err := c.store.SaveMessagesWithMetadataMutation(ctx, selected, map[string]string{"conversation.turn": string(rawTurn)}, func(tx *sql.Tx) error {
		for _, item := range chosen {
			if _, err := tx.ExecContext(ctx, "DELETE FROM chat_user_inbox WHERE sequence=?", item.sequence); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.turn = &next
	for _, selected := range selected {
		for i, message := range c.messages {
			if message.ID == selected.ID {
				c.messages[i] = selected
				break
			}
		}
	}
	c.cursor = *events[len(events)-1].Cursor
	c.changedLocked()
	return nil
}

// StartPendingUsers is called after the owning adapter has bound its Prepare
// closure to the opened conversation. Uncertain original turns remain fenced.
func (c *Conversation) StartPendingUsers(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startNextUserLocked(ctx)
}
