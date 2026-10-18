package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// RetryFailed resumes a failed model boundary after execution cleanup. It keeps
// the original user message, checkpoint, uploads and tool receipts, discarding
// incomplete model output. Uncertain tool effects retain the recovery fence.
func (c *Conversation) RetryFailed(ctx context.Context, id, target string) (protocol.SendMessageAccepted, error) {
	return c.retryFailed(ctx, id, target, "")
}

// RetryContinuation also binds a UI action to the exact displayed interruption.
func (c *Conversation) RetryContinuation(ctx context.Context, id, target, attempt string) (protocol.SendMessageAccepted, error) {
	return c.retryFailed(ctx, id, target, attempt)
}

func (c *Conversation) retryFailed(ctx context.Context, id, target, attempt string) (protocol.SendMessageAccepted, error) {
	if !core.Identifier(id) || !core.Identifier(target) {
		return protocol.SendMessageAccepted{}, errors.New("invalid retry identity")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := "conversation.retry.receipt." + id
	if prior, found, err := c.store.Get(ctx, key); err != nil {
		return protocol.SendMessageAccepted{}, err
	} else if found {
		if prior != target {
			return protocol.SendMessageAccepted{}, errors.New("retry identity has different target")
		}
		return protocol.SendMessageAccepted{MessageID: target, Duplicate: true}, nil
	}
	if c.closed || c.turn != nil {
		return protocol.SendMessageAccepted{}, errors.New("conversation cannot retry this turn")
	}
	if c.blocked {
		if attempt != "" && (c.recovery == nil || c.recovery.UserMessageID != target || c.recovery.attemptID() != attempt) {
			return protocol.SendMessageAccepted{}, errors.New("continuation attempt changed")
		}
		return protocol.SendMessageAccepted{}, agent.ErrUncertainEffects
	}
	raw, found, err := c.store.Get(ctx, "conversation.retry")
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if !found || raw == "" {
		return protocol.SendMessageAccepted{}, errors.New("no confirmed failed turn to retry")
	}
	var turn savedTurn
	if err = json.Unmarshal([]byte(raw), &turn); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if turn.UserMessageID != target {
		return protocol.SendMessageAccepted{}, errors.New("retry target changed")
	}
	if attempt != "" && turn.attemptID() != attempt {
		return protocol.SendMessageAccepted{}, errors.New("continuation attempt changed")
	}
	body, err := c.store.LoadContext(ctx)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	var checkpoint agent.Checkpoint
	if len(body) > 0 {
		if err = json.Unmarshal(body, &checkpoint); err != nil {
			return protocol.SendMessageAccepted{}, err
		}
	}
	if checkpoint.Started != nil || checkpoint.ModelRequestID != nil {
		return protocol.SendMessageAccepted{}, agent.ErrUncertainEffects
	}
	for i := len(c.messages) - 1; i >= 0; i-- {
		message := c.messages[i]
		if message.Role == protocol.User && !message.Steering {
			if !turn.includes(message.ID) {
				return protocol.SendMessageAccepted{}, errors.New("conversation advanced after failed turn")
			}
			break
		}
	}
	if err = c.applyConfigurationBoundaryLocked(ctx); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if err = c.validateAttachmentsLocked(ctx, turn.Request.Attachments); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	for _, message := range checkpoint.Messages {
		if message.Kind.Key == turn.Request.RequestID {
			if err = c.validateAttachmentsLocked(ctx, message.Attachments); err != nil {
				return protocol.SendMessageAccepted{}, err
			}
		}
	}
	if c.thread == nil {
		return protocol.SendMessageAccepted{}, errors.New("assistant configuration unavailable")
	}
	turn.Resuming = checkpoint.LastRequest == turn.Request.RequestID
	turn.AttemptID = id
	turn.BootID = c.options.BootID
	turn.Generation = c.thread.Generation
	turn.State = "queued"
	turn.Paused = false
	turn.Cancelling = false
	rawTurn, _ := json.Marshal(turn)
	var input protocol.Message
	for _, message := range c.messages {
		if message.ID == target {
			input = message
			break
		}
	}
	if input.ID == "" {
		return protocol.SendMessageAccepted{}, errors.New("original input unavailable")
	}
	input.Status = protocol.Accepted
	previousError := c.lastError
	c.turn = &turn
	c.lastError = nil
	if err = c.putMessageLocked(ctx, input, map[string]string{"conversation.turn": string(rawTurn), key: target, "conversation.retry": ""}); err != nil {
		c.turn = nil
		c.lastError = previousError
		return protocol.SendMessageAccepted{}, err
	}
	c.input <- turn.Request
	return protocol.SendMessageAccepted{MessageID: target}, nil
}

func (c *Conversation) continuationLocked() *protocol.Continuation {
	if c.turn != nil {
		return nil
	}
	turn := c.recovery
	if !c.blocked {
		raw, exists, err := c.store.Get(context.WithoutCancel(c.ctx), "conversation.retry")
		if err != nil || !exists || raw == "" {
			return nil
		}
		var retry savedTurn
		if json.Unmarshal([]byte(raw), &retry) != nil {
			return nil
		}
		turn = &retry
	}
	if turn == nil || strings.HasPrefix(turn.Request.RequestID, "pot-result-") {
		return nil
	}
	// Old failed inputs must not become retryable again after newer input.
	for i := len(c.messages) - 1; i >= 0; i-- {
		message := c.messages[i]
		if message.Role == protocol.User && !message.Steering && !message.Queued {
			if !turn.includes(message.ID) {
				return nil
			}
			return &protocol.Continuation{UserMessageID: turn.UserMessageID, AttemptID: turn.attemptID(), RecoveryRequired: c.blocked}
		}
	}
	return nil
}
