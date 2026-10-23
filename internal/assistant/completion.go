package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
)

// A completed invocation may only resume its persistence, never Execute. The
// original turn is retained until its local state and inbox acknowledgement settle.
type turnCompletion struct {
	Turn  savedTurn       `json:"turn"`
	Phase execution.Phase `json:"phase"`
	Error string          `json:"error"`
}

func (p turnCompletion) result() execution.Result {
	var err error
	if p.Error != "" {
		err = errors.New(p.Error)
	}
	return execution.Result{Phase: p.Phase, Err: err}
}

func (p turnCompletion) validate(options ConversationOptions, thread *core.Thread) error {
	t := p.Turn
	if thread == nil || t.Owner != options.Owner || t.Generation != thread.Generation || t.UserMessageID == "" || t.Request.ConversationID != options.ConversationID || validateConversationRequest(t.Request) != nil {
		return errors.New("persisted completion turn binding mismatch")
	}
	switch p.Phase {
	case execution.Succeeded, execution.Failed, execution.Cancelled, execution.Interrupted:
		return nil
	}
	return errors.New("persisted completion has no terminal execution result")
}

func (c *Conversation) recordCompletionLocked(ctx context.Context, result execution.Result) error {
	if c.completion != nil {
		return nil
	}
	pending := turnCompletion{Turn: *c.turn, Phase: result.Phase}
	if result.Err != nil {
		pending.Error = result.Err.Error()
	}
	body, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if err = c.store.Set(ctx, "conversation.completion", string(body)); err != nil {
		return err
	}
	c.completion = &pending
	return nil
}

func (c *Conversation) FinalizationFailed(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finishError, c.lastError = err, err
	// Persist a wakeup when possible. A storage failure can prevent advancing
	// the durable cursor; UIFrame then sends a reset at the retained cursor.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), time.Second)
	defer cancel()
	if c.appendTurnLocked(ctx) != nil {
		c.changedLocked()
	}
}
