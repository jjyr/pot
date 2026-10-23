package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errModelIdleTimeout = errors.New("model stream inactivity timeout")

// The timer runs while Model or Read is blocked. A last-activity check under the
// lock prevents an already queued timer callback from cancelling a renewed read.
type modelIdleContext struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	mu      sync.Mutex
	timer   *time.Timer
	timeout time.Duration
	last    time.Time
	closed  bool
}

func newModelIdleContext(parent context.Context, timeout time.Duration) *modelIdleContext {
	ctx, cancel := context.WithCancelCause(parent)
	i := &modelIdleContext{ctx: ctx, cancel: cancel, timeout: timeout, last: time.Now()}
	i.timer = time.AfterFunc(timeout, i.expire)
	return i
}

func (i *modelIdleContext) expire() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.ctx.Err() != nil {
		return
	}
	remaining := i.timeout - time.Since(i.last)
	if remaining > 0 {
		i.timer.Reset(remaining)
		return
	}
	i.cancel(errModelIdleTimeout)
}

func (i *modelIdleContext) touch() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.closed && i.ctx.Err() == nil {
		i.last = time.Now()
		i.timer.Reset(i.timeout)
	}
}

func (i *modelIdleContext) close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed = true
	i.timer.Stop()
	i.cancel(context.Canceled)
}
