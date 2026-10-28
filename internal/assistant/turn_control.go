package assistant

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"slices"
	"strings"
)

func steeringMessageID(request, target string) string {
	digest, _ := core.HashJSON([]string{"steering", request, target})
	return "steer-" + digest
}

type TurnControl struct {
	UserMessageID string `json:"user_message_id" required:"true"`
	Action        string `json:"action" required:"true"`
}
type SteerRequest struct {
	References    []core.MessageReference `json:"references,omitempty"`
	Attachments   []core.Attachment       `json:"attachments,omitempty"`
	RequestID     string                  `json:"request_id" required:"true"`
	UserMessageID string                  `json:"user_message_id" required:"true"`
	Text          string                  `json:"text" required:"true"`
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

// Steer atomically records guidance for the named turn, or queues it as a new
// user input when that turn can no longer consume it. Retries retain the same
// receipt and never steer a successor turn.
func (c *Conversation) Steer(ctx context.Context, r SteerRequest) (protocol.SendMessageAccepted, error) {
	r.Attachments = slices.Clone(r.Attachments)
	r.References = slices.Clone(r.References)
	if !core.Identifier(r.RequestID) || strings.HasPrefix(r.RequestID, "pot-result-") || isCompactRequest(r.RequestID) || !core.Identifier(r.UserMessageID) || (strings.TrimSpace(r.Text) == "" && len(r.Attachments) == 0) || len(r.Text) > 65536 {
		return protocol.SendMessageAccepted{}, errors.New("invalid steering input")
	}
	if err := core.ValidateAttachments(r.Attachments); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	source, exists, err := c.store.Get(ctx, "conversation.user_source."+r.RequestID)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if exists {
		old, err := c.store.Duplicate(ctx, r.RequestID)
		if err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if source != r.UserMessageID || old == nil || old.Role != protocol.User || old.Text != r.Text || (!slices.Equal(old.Attachments, r.Attachments) || !slices.Equal(old.References, r.References)) {
			return protocol.SendMessageAccepted{}, errors.New("steering identity has different target or content")
		}
		return protocol.SendMessageAccepted{MessageID: old.ID, Duplicate: true}, nil
	}
	if old, err := c.store.Duplicate(ctx, r.RequestID); err != nil {
		return protocol.SendMessageAccepted{}, err
	} else if old != nil {
		return protocol.SendMessageAccepted{}, errors.New("guidance identity already used")
	}
	messageID := steeringMessageID(r.RequestID, r.UserMessageID)
	var priorTurn, priorText string
	err = c.store.DB().QueryRowContext(ctx, "SELECT turn_id,text FROM assistant_steering WHERE id=?", r.RequestID).Scan(&priorTurn, &priorText)
	duplicate := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.SendMessageAccepted{}, err
	}
	if duplicate {
		target, err := c.store.Message(ctx, r.UserMessageID)
		if err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if target == nil || target.RequestID == nil || *target.RequestID != priorTurn || priorText != r.Text {
			return protocol.SendMessageAccepted{}, errors.New("steering request identity has different target or content")
		}
		m, err := c.store.Message(ctx, messageID)
		if err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if m != nil {
			if !slices.Equal(m.Attachments, r.Attachments) || !slices.Equal(m.References, r.References) {
				return protocol.SendMessageAccepted{}, errors.New("steering attachment identity changed")
			}
			return protocol.SendMessageAccepted{MessageID: messageID, Duplicate: true}, nil
		}
	}
	if !duplicate {
		if err := c.validateReferencesLocked(r.References); err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if err := c.validateAttachmentsLocked(ctx, r.Attachments); err != nil {
			return protocol.SendMessageAccepted{}, err
		}
	}
	if !duplicate {
		if c.closed || c.ctx.Err() != nil {
			return protocol.SendMessageAccepted{}, errors.New("conversation closed")
		}
		// Validate the captured target in durable storage, not a paged UI view.
		target, err := c.store.Message(ctx, r.UserMessageID)
		if err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if target == nil || target.ConversationID != c.options.ConversationID || target.RequestID == nil {
			return protocol.SendMessageAccepted{}, errors.New("unknown guidance target")
		}
		if c.turn == nil || c.turn.UserMessageID != r.UserMessageID || c.turn.Cancelling || c.finalizing || strings.HasPrefix(c.turn.Request.RequestID, "pot-result-") || isCompactRequest(c.turn.Request.RequestID) {
			request := protocol.SendMessage{RequestID: r.RequestID, ConversationID: c.options.ConversationID, Text: r.Text, Attachments: r.Attachments, References: r.References}
			receipt, err := c.queueUserFromLocked(ctx, request, r.UserMessageID)
			if err != nil {
				return receipt, err
			}
			// The queue commit is the acceptance boundary. A scheduling failure
			// remains recoverable from that inbox, including after restart.
			if err := c.startNextUserLocked(ctx); err != nil {
				return protocol.SendMessageAccepted{}, err
			}
			return receipt, nil
		}
	}
	var turn string
	if duplicate {
		turn = priorTurn
	} else {
		turn = c.turn.Request.RequestID
	}
	message := protocol.Message{ID: messageID, ConversationID: c.options.ConversationID, Role: protocol.User, Text: r.Text, Attachments: r.Attachments, References: r.References, Status: protocol.Accepted, CreatedAtMS: storage.Now(), Steering: true, AcceptedRequestID: r.RequestID, AcceptedTurnID: r.UserMessageID}
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
