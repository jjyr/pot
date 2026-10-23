package assistant

import (
	"context"
	"errors"
)

type messageSenderKey struct{}
type messageSenderGuard func(context.Context, func() (any, error)) (any, error)

// GuardMessageSend uses the authenticated execution's in-memory attempt binding.
// Native producers outside a model execution have their own sender validation.
func GuardMessageSend(ctx context.Context, send func() (any, error)) (any, error) {
	if guard, ok := ctx.Value(messageSenderKey{}).(messageSenderGuard); ok {
		return guard(ctx, send)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return send()
}

// Called with the conversation locked. Only the short durable ingress operation
// runs under this guard; recipient model execution is never part of acceptance.
func (c *Conversation) bindMessageSenderLocked(ctx context.Context) context.Context {
	turn := c.turn
	guard := messageSenderGuard(func(callCtx context.Context, send func() (any, error)) (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed || c.turn != turn || turn.Cancelling || ctx.Err() != nil {
			return nil, errors.New("stale assistant message sender binding")
		}
		if err := callCtx.Err(); err != nil {
			return nil, err
		}
		return send()
	})
	return context.WithValue(ctx, messageSenderKey{}, guard)
}
