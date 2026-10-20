package control

import (
	"context"
	"sync"
	"time"
)

// During cancellation a command already sent receives a bounded chance to return
// its original cleanup receipt. No replacement command is issued.
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
