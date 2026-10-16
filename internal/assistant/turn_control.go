package assistant

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"strings"
)

type TurnControl struct {
	UserMessageID string `json:"user_message_id" required:"true"`
	Action        string `json:"action" required:"true"`
}
type SteerRequest struct {
	RequestID     string `json:"request_id" required:"true"`
	UserMessageID string `json:"user_message_id" required:"true"`
	Text          string `json:"text" required:"true"`
}

// Control pauses only at safe Agent model boundaries. The in-flight model/tool
// is allowed to settle normally; resuming never creates a replacement turn.
func (c *Conversation) Control(ctx context.Context, r TurnControl) error {
	if !core.Identifier(r.UserMessageID) || (r.Action != "pause" && r.Action != "resume") {
		return errors.New("invalid turn control")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.turn == nil || c.turn.UserMessageID != r.UserMessageID || c.turn.Cancelling || c.finalizing {
		return errors.New("turn ended or is stopping")
	}
	paused := r.Action == "pause"
	if c.turn.Paused == paused {
		return nil
	}
	previous := c.turn.Paused
	c.turn.Paused = paused
	if err := c.appendTurnLocked(ctx); err != nil {
		c.turn.Paused = previous
		return err
	}
	close(c.controlChanged)
	c.controlChanged = make(chan struct{})
	return nil
}

// Steer atomically records exact-turn guidance and its visible user message.
// Repeating the original receipt cannot steer a later turn with the same text.
func (c *Conversation) Steer(ctx context.Context, r SteerRequest) (protocol.SendMessageAccepted, error) {
	if !core.Identifier(r.RequestID) || !core.Identifier(r.UserMessageID) || strings.TrimSpace(r.Text) == "" || len(r.Text) > 65536 {
		return protocol.SendMessageAccepted{}, errors.New("invalid steering input")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	digest, _ := core.HashJSON([]string{"steering", r.RequestID, r.UserMessageID})
	messageID := "steer-" + digest
	var priorTurn, priorText string
	err := c.store.DB().QueryRowContext(ctx, "SELECT turn_id,text FROM assistant_steering WHERE id=?", r.RequestID).Scan(&priorTurn, &priorText)
	duplicate := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.SendMessageAccepted{}, err
	}
	if duplicate {
		validTarget := false
		for _, m := range c.messages {
			if m.ID == r.UserMessageID && m.RequestID != nil && *m.RequestID == priorTurn {
				validTarget = true
				break
			}
		}
		if !validTarget || priorText != r.Text {
			return protocol.SendMessageAccepted{}, errors.New("steering request identity has different target or content")
		}
		for _, m := range c.messages {
			if m.ID == messageID {
				return protocol.SendMessageAccepted{MessageID: messageID, Duplicate: true}, nil
			}
		}
	} else if c.closed || c.turn == nil || c.turn.UserMessageID != r.UserMessageID || c.turn.Cancelling || c.finalizing {
		return protocol.SendMessageAccepted{}, errors.New("turn is not accepting steering")
	}
	var turn string
	if duplicate {
		turn = priorTurn
	} else {
		turn = c.turn.Request.RequestID
	}
	message := protocol.Message{ID: messageID, ConversationID: c.options.ConversationID, Role: protocol.User, Text: r.Text, Status: protocol.Accepted, CreatedAtMS: storage.Now(), Steering: true}
	event, err := c.store.SaveWithMutation(ctx, message, func(tx *sql.Tx) error {
		if duplicate {
			return nil
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_steering WHERE turn_id=? AND acknowledged=0", turn).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return errors.New("steering mailbox is full")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO assistant_steering(id,turn_id,text,at)VALUES(?,?,?,?)", r.RequestID, turn, r.Text, message.CreatedAtMS)
		return err
	})
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	c.messages = append(c.messages, message)
	c.cursor = *event.Cursor
	c.changedLocked()
	return protocol.SendMessageAccepted{MessageID: messageID, Duplicate: duplicate}, nil
}
