package control

import (
	"context"
	"sync"
	"time"
)

// Allow an in-flight local operation a bounded cleanup interval after its
// caller is cancelled. Settlement retains the original operation identity.
func settlementContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	var mu sync.Mutex
	var timer *time.Timer
	stop := context.AfterFunc(parent, func() { mu.Lock(); timer = time.AfterFunc(5*time.Second, cancel); mu.Unlock() })
	return ctx, func() {
		stop()
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		cancel()
	}
}
