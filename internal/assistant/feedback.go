package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type turnFeedback struct {
	conversation *Conversation
	thread       core.Thread
	request      protocol.SendMessage
	options      RunOptions
	gate         *core.EventGate
	reply        string
	toolMessages map[string]string
}

func (f *turnFeedback) checkLocked() error {
	c := f.conversation
	if c.turn == nil || c.thread == nil || c.turn.Owner != f.thread.Owner || c.turn.Generation != f.thread.Generation || c.thread.Config.SpecHash != f.thread.Config.SpecHash || c.turn.Request.RequestID != f.request.RequestID || c.turn.BootID != c.options.BootID {
		return errors.New("stale assistant feedback binding")
	}
	return nil
}
func (f *turnFeedback) LoadContext(ctx context.Context) (json.RawMessage, error) {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return nil, err
	}
	return c.store.LoadContext(ctx)
}
func (f *turnFeedback) SaveContext(ctx context.Context, body json.RawMessage) error {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return err
	}
	return c.store.SaveContext(ctx, body)
}
func (f *turnFeedback) RecordTurn(ctx context.Context, id string) error {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return err
	}
	if id != f.request.RequestID {
		return errors.New("recorded turn differs from accepted input")
	}
	if c.turn.Cancelling {
		return context.Canceled
	}
	c.turn.State = "running"
	return c.appendTurnLocked(ctx)
}
func (f *turnFeedback) InputStreaming(ctx context.Context, id string) error {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return err
	}
	if id != f.request.RequestID {
		return errors.New("streaming input binding mismatch")
	}
	found := false
	for _, m := range c.messages {
		if c.turn.includes(m.ID) {
			found = true
			m.Status = protocol.Streaming
			if err := c.putMessageLocked(ctx, m, nil); err != nil {
				return err
			}
		}
	}
	if !found {
		return errors.New("accepted input is missing")
	}
	return nil
}
func (f *turnFeedback) Ready(ctx context.Context) error {
	c := f.conversation
	for {
		c.mu.Lock()
		err := f.checkLocked()
		if err == nil && c.turn.Cancelling {
			err = context.Canceled
		}
		paused := err == nil && c.turn.Paused
		changed := c.controlChanged
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !paused {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
	if f.options.Ready != nil {
		return f.options.Ready(ctx)
	}
	return nil
}
func (f *turnFeedback) Steering(ctx context.Context, finalizing bool) ([]agent.SteeringInput, error) {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return nil, err
	}
	rows, err := c.store.DB().QueryContext(ctx, "SELECT id,text FROM assistant_steering WHERE turn_id=? AND acknowledged=0 ORDER BY at,rowid LIMIT 100", f.request.RequestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agent.SteeringInput{}
	for rows.Next() {
		var input agent.SteeringInput
		if err := rows.Scan(&input.ID, &input.Text); err != nil {
			return nil, err
		}
		for _, message := range c.messages {
			if message.ID == steeringMessageID(input.ID, c.turn.UserMessageID) {
				input.MessageID = message.ID
				input.References = slices.Clone(message.References)
				input.Attachments = message.Attachments
				break
			}
		}
		out = append(out, input)
	}
	c.finalizing = finalizing && len(out) == 0
	return out, rows.Err()
}
func (f *turnFeedback) AcknowledgeSteering(ctx context.Context, ids []string) error {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return err
	}
	body, err := c.store.LoadContext(ctx)
	if err != nil {
		return err
	}
	var saved struct {
		SteeringIDs []string `json:"steering_ids"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.Contains(saved.SteeringIDs, id) {
			return errors.New("steering was not durably saved before acknowledgement")
		}
	}
	for _, id := range ids {
		acknowledge := func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE assistant_steering SET acknowledged=1 WHERE id=? AND turn_id=?", id, f.request.RequestID)
			return err
		}
		digest, _ := core.HashJSON([]string{"steering", id, c.turn.UserMessageID})
		messageID := "steer-" + digest
		index := -1
		for i, m := range c.messages {
			if m.ID == messageID {
				index = i
				break
			}
		}
		if index < 0 || c.messages[index].Status == protocol.Complete {
			if err := storage.Transaction(ctx, c.store.DB(), acknowledge); err != nil {
				return err
			}
			continue
		}
		message := c.messages[index]
		message.Status = protocol.Complete
		event, err := c.store.SaveWithMutation(ctx, message, acknowledge)
		if err != nil {
			return err
		}
		c.messages[index] = message
		c.cursor = *event.Cursor
		c.changedLocked()
	}
	return nil
}
func (f *turnFeedback) Observe(ctx context.Context, observation agent.Observation) error {
	c := f.conversation
	c.mu.Lock()
	if err := f.checkLocked(); err != nil {
		c.mu.Unlock()
		return err
	}
	body, err := json.Marshal(observation)
	if err == nil {
		_, err = c.store.DB().ExecContext(ctx, "INSERT INTO assistant_observations(turn_id,generation,body) VALUES (?,?,?)", f.request.RequestID, f.thread.Generation, string(body))
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if f.options.Observe != nil {
		return f.options.Observe(ctx, observation)
	}
	return nil
}
func (f *turnFeedback) Settle(ctx context.Context) error {
	c := f.conversation
	c.mu.Lock()
	err := f.checkLocked()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if f.options.Settle != nil {
		return f.options.Settle(ctx)
	}
	return nil
}
func (f *turnFeedback) event(event core.AgentEvent) error {
	c := f.conversation
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := f.checkLocked(); err != nil {
		return err
	}
	if !f.gate.Accept(event) {
		return errors.New("stale or duplicate assistant event")
	}
	ctx := context.WithoutCancel(c.ctx)
	update := event.Update
	switch update.Type {
	case "message_started":
		for _, message := range c.messages {
			if message.ID == c.turn.ReplyMessageID && (message.Status == protocol.Streaming || (c.turn.Resuming || message.Status == protocol.Failed) && message.Text == "") {
				f.reply = message.ID
				message.Status = protocol.Streaming
				message.Error = ""
				return c.putMessageLocked(ctx, message, nil)
			}
		}
		f.reply = update.ID
		if f.reply == "" {
			return errors.New("assistant response identity missing")
		}
		c.turn.ReplyMessageID = f.reply
		metadata, _ := json.Marshal(c.turn)
		return c.putMessageLocked(ctx, protocol.Message{ID: f.reply, ConversationID: c.options.ConversationID, Role: protocol.Assistant, ReplyTo: c.turn.UserMessageID, Status: protocol.Streaming, CreatedAtMS: storage.Now()}, map[string]string{"conversation.turn": string(metadata)})
	case "text_delta", "message_finished":
		if f.reply == "" {
			return errors.New("assistant output arrived before message start")
		}
		for _, m := range c.messages {
			if m.ID == f.reply {
				if update.Type == "text_delta" {
					m.Text += update.Text
				} else {
					m.Text = update.Text
					m.Status = protocol.Complete
				}
				return c.putMessageLocked(ctx, m, nil)
			}
		}
		return errors.New("assistant output projection is missing")
	case "tool_started":
		id := storage.ID()
		f.toolMessages[update.ID] = id
		tool := &protocol.ToolCall{}
		tool.Apply(update)
		tool.TurnID = f.request.RequestID
		return c.putMessageLocked(ctx, protocol.Message{ID: id, ConversationID: c.options.ConversationID, Role: protocol.Tool, Tool: tool, Status: protocol.Streaming, CreatedAtMS: update.AtMS}, nil)
	case "tool_state", "tool_finished", "tool_output":
		id := f.toolMessages[update.ID]
		if id == "" {
			return errors.New("tool update arrived before tool start")
		}
		for _, m := range c.messages {
			if m.ID == id {
				if m.Tool == nil {
					return errors.New("tool details missing")
				}
				// Copy before mutation: a previously delivered snapshot owns its value.
				tool := *m.Tool
				tool.Output = slices.Clone(tool.Output)
				tool.Apply(update)
				m.Tool = &tool
				if update.Type == "tool_finished" {
					m.Status = protocol.Complete
					if update.Failed {
						m.Status = protocol.Failed
					}
				}
				return c.putMessageLocked(ctx, m, nil)
			}
		}
		return errors.New("tool projection missing")
	case "turn_finished":
		for _, message := range slices.Clone(c.messages) {
			if message.Tool != nil && message.Status == protocol.Streaming {
				tool := *message.Tool
				tool.Apply(update)
				message.Tool = &tool
				message.Status = protocol.Failed
				if tool.Phase == "stopped" {
					message.Status = protocol.MessageCancelled
				}
				if err := c.putMessageLocked(ctx, message, nil); err != nil {
					return err
				}
			}
		}
		if update.Outcome != nil && update.Outcome.Error != "" {
			c.lastError = errors.New(update.Outcome.Error)
		} else if update.Outcome != nil && update.Outcome.Kind == "success" {
			c.lastError = nil
		}
		return nil
	case "compaction_observed", "input_applied":
		return nil
	default:
		return errors.New("unsupported assistant event")
	}
}

// QueueSteering accepts reference/guidance at model boundaries, never a new
// authority grant. The Agent durably incorporates IDs before acknowledging them.
func (c *Conversation) QueueSteering(ctx context.Context, id, text string) error {
	if !core.Identifier(id) || strings.TrimSpace(text) == "" || len(text) > 65536 {
		return errors.New("invalid steering input")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var previousTurn, previousText string
	err := c.store.DB().QueryRowContext(ctx, "SELECT turn_id,text FROM assistant_steering WHERE id=?", id).Scan(&previousTurn, &previousText)
	if err == nil {
		if previousText != text {
			return errors.New("steering identity has different content")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if c.closed || c.turn == nil || c.turn.Cancelling || c.finalizing {
		return errors.New("turn is not accepting steering")
	}
	return storage.Transaction(ctx, c.store.DB(), func(tx *sql.Tx) error {
		var oldTurn, oldText string
		err := tx.QueryRowContext(ctx, "SELECT turn_id,text FROM assistant_steering WHERE id=?", id).Scan(&oldTurn, &oldText)
		if err == nil {
			if oldTurn != c.turn.Request.RequestID || oldText != text {
				return errors.New("steering identity has different content")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_steering WHERE turn_id=? AND acknowledged=0", c.turn.Request.RequestID).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return errors.New("steering mailbox is full")
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_steering(id,turn_id,text,at) VALUES (?,?,?,?)", id, c.turn.Request.RequestID, text, storage.Now())
		return err
	})
}
