package control

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRolloutCompressionBudgetAndFairness(t *testing.T) {
	w := &rolloutCompressor{ctx: context.Background()}
	var calls []string
	for i := range w.batches {
		w.batches[i] = func(_ context.Context, before time.Time, cursor string, limit int, rate int64) (string, int, error) {
			if limit != 2 || rate != 8<<20 || time.Since(before) < 7*24*time.Hour {
				t.Fatalf("invalid bounds: %d %d %v", limit, rate, before)
			}
			calls = append(calls, fmt.Sprintf("%d:%s", i, cursor))
			return "next", 2, nil
		}
	}
	w.pass()
	w.pass()
	w.pass()
	if fmt.Sprint(calls) != "[0: 1: 0:next]" {
		t.Fatal(calls)
	}
}

func TestRolloutCompressionSharesRemainingBudget(t *testing.T) {
	w := &rolloutCompressor{ctx: context.Background()}
	w.batches[0] = func(context.Context, time.Time, string, int, int64) (string, int, error) { return "a", 1, nil }
	w.batches[1] = func(_ context.Context, _ time.Time, cursor string, limit int, _ int64) (string, int, error) {
		if limit != 1 || cursor != "" {
			t.Fatalf("remaining=%d cursor=%s", limit, cursor)
		}
		return "b", 1, nil
	}
	w.pass()
	if w.cursors != [2]string{"a", "b"} {
		t.Fatal(w.cursors)
	}
}

func TestRolloutCompressionCoalescesAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	batch := func(ctx context.Context, _ time.Time, _ string, _ int, _ int64) (string, int, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "", 2, ctx.Err()
	}
	w := newRolloutCompressor(ctx, batch, batch)
	defer w.wait()
	defer cancel()
	// A pass requires an explicit completion signal.
	select {
	case <-started:
		t.Fatal("startup compression")
	default:
	}
	w.trigger()
	await := func() {
		t.Helper()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
		}
	}
	await()
	for range 1000 {
		w.trigger()
	}
	if len(w.signal) != 1 {
		t.Fatal("completion signals were not coalesced")
	}
	release <- struct{}{}
	await()
	if len(w.signal) != 0 {
		t.Fatal("unexpected queued pass")
	}
	cancel()
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	select {
	case <-started:
		t.Fatal("unexpected third pass")
	default:
	}
}
