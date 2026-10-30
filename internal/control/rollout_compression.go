package control

import (
	"context"
	"log/slog"
	"time"
)

type rolloutBatch func(context.Context, time.Time, string, int, int64) (string, int, error)

// Completion signals coalesce into one pending pass. Only this worker owns the
// scan cursors; exhausted pages wrap on a later completion, never a timer.
type rolloutCompressor struct {
	ctx     context.Context
	batches [2]rolloutBatch
	cursors [2]string
	first   int
	signal  chan struct{}
	done    chan struct{}
}

func newRolloutCompressor(ctx context.Context, assistants, tasks rolloutBatch) *rolloutCompressor {
	w := &rolloutCompressor{ctx: ctx, batches: [2]rolloutBatch{assistants, tasks}, signal: make(chan struct{}, 1), done: make(chan struct{})}
	go w.run()
	return w
}

func (w *rolloutCompressor) trigger() {
	if w == nil {
		return
	}
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

func (w *rolloutCompressor) wait() {
	if w != nil {
		<-w.done
	}
}

func (w *rolloutCompressor) run() {
	defer close(w.done)
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.signal:
			if w.ctx.Err() != nil {
				return
			}
			w.pass()
		}
	}
}

func (w *rolloutCompressor) pass() {
	before := time.Now().Add(-7 * 24 * time.Hour)
	remaining := 2
	first := w.first
	w.first = 1 - first
	for i := 0; i < 2 && remaining > 0 && w.ctx.Err() == nil; i++ {
		index := (first + i) % 2
		if w.batches[index] == nil {
			continue
		}
		next, count, err := w.batches[index](w.ctx, before, w.cursors[index], remaining, 8<<20)
		w.cursors[index] = next
		remaining -= count
		if err != nil && w.ctx.Err() == nil {
			slog.Warn("session rollout compression failed", "source", index, "error", err)
		}
	}
}
