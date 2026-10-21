package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

type idlePort struct{ modelPort }

func (idlePort) Compaction(context.Context, json.RawMessage) error { return nil }

func TestOrdinaryResponsePersistsCacheActivityAndRequestStart(t *testing.T) {
	ctx := context.Background()
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4096,\"completion_tokens\":2,\"prompt_cache_hit_tokens\":2048}}\n\ndata: [DONE]\n\n")), nil
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "cache-evidence", ConversationID: "main", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	var started, finished, read int64
	var write *int64
	if err := c.store.DB().QueryRow("SELECT started_at,finished_at,cache_read,cache_write FROM assistant_idle_compaction WHERE singleton=1").Scan(&started, &finished, &read, &write); err != nil || started <= 0 || started > finished || read != 2048 || write != nil {
		t.Fatal(started, finished, read, write, err)
	}
}

func TestQueuedPrecompactionRechecksExpiryBeforeStart(t *testing.T) {
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		t.Error("expired compact reached model")
		return replySSE(""), nil
	}, &cleanup)
	primeIdle(t, c, time.Now().Add(-14*time.Minute))
	c.mu.Lock()
	defer c.mu.Unlock()
	if eligible, err := c.idleEligibleLocked(context.Background(), time.Now()); err != nil || !eligible {
		t.Fatal(eligible, err)
	}
	if _, err := c.store.DB().Exec("UPDATE assistant_idle_compaction SET started_at=?", time.Now().Add(-15*time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := c.startIdleLocked(context.Background()); !errors.Is(err, ErrCompactDeferred) {
		t.Fatal(err)
	}
}

func TestCachePrecompactionRequiresActivityAndUnexpiredWindow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		read, write any
		age         time.Duration
		want        bool
	}{
		{"read", 512, nil, 27 * time.Minute / 2, true},
		{"write", 0, 1200, 27 * time.Minute / 2, true},
		{"unknown", nil, nil, 27 * time.Minute / 2, false},
		{"zero", 0, 0, 27 * time.Minute / 2, false},
		{"early", 512, nil, 13 * time.Minute, false},
		{"expired", 512, nil, 15 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cleanup atomic.Int64
			c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
				t.Error("unexpected model call")
				return replySSE(""), nil
			}, &cleanup)
			anchor := time.Now()
			primeIdle(t, c, anchor)
			c.mu.Lock()
			defer c.mu.Unlock()
			if _, err := c.store.DB().Exec("UPDATE assistant_idle_compaction SET cache_read=?,cache_write=?", tc.read, tc.write); err != nil {
				t.Fatal(err)
			}
			got, err := c.idleEligibleLocked(context.Background(), anchor.Add(tc.age))
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
			if tc.name == "expired" || tc.name == "zero" || tc.name == "unknown" {
				// Extending TTL does not resurrect a discarded opportunity.
				c.options.IdlePolicy = func(context.Context, core.Thread) (IdleCompactionPolicy, error) {
					return IdleCompactionPolicy{CacheTTL: 20 * time.Minute}, nil
				}
				got, err = c.idleEligibleLocked(context.Background(), anchor.Add(18*time.Minute))
				if err != nil || got {
					t.Fatal("discarded opportunity resurrected", got, err)
				}
			}
		})
	}
}

func primeIdle(t *testing.T, c *Conversation, finished time.Time) {
	t.Helper()
	ctx := context.Background()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thread.Config.Params.Agent.AutoCompact = true
	c.thread.Config.Params.Agent.UseProviderUsage = false
	if err := c.thread.Config.Rehash(); err != nil {
		t.Fatal(err)
	}
	threadBody, _ := json.Marshal(c.thread)
	if err := c.store.Set(ctx, ThreadKey, string(threadBody)); err != nil {
		t.Fatal(err)
	}
	state := agent.Checkpoint{Type: "context", Version: 2, Messages: []modelprotocol.Message{}, Pending: []modelprotocol.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}}
	for range 4 {
		state.Messages = append(state.Messages, modelprotocol.TextMessage("user", strings.Repeat("goal ", 1000)), modelprotocol.TextMessage("assistant", strings.Repeat("observed ", 1000)))
	}
	raw, _ := json.Marshal(state)
	if err := c.store.SaveContext(ctx, raw); err != nil {
		t.Fatal(err)
	}
	prefix, _ := json.Marshal(modelprotocol.Request{System: "stable system", Tools: []modelprotocol.ToolDefinition{}})
	if _, err := c.store.DB().Exec(`UPDATE assistant_idle_compaction SET request_id='last-model',finished_at=?,started_at=?,cache_read=512,route=?,prefix=?`, finished.UnixMilli(), finished.UnixMilli(), idleRoute(c.thread.Config), string(prefix)); err != nil {
		t.Fatal(err)
	}
	prepare := c.options.Prepare
	c.options.Prepare = func(ctx context.Context, thread core.Thread, r core.TurnRequest) (RunOptions, error) {
		options, err := prepare(ctx, thread, r)
		options.Services = idlePort{options.Services.(modelPort)}
		return options, err
	}
}
func TestIdleDeadlineDedupeAndQueuedUser(t *testing.T) {
	ctx := context.Background()
	var cleanup, calls atomic.Int64
	started := make(chan core.ModelRequest, 2)
	release := make(chan struct{})
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		started <- r
		if r.CompactionID != nil {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return replySSE("Goal: preserve authorization. Completed: observed work. Pending: next input."), nil
		}
		return replySSE("normal reply"), nil
	}, &cleanup)
	anchor := time.Now().Add(-27 * time.Minute / 2)
	primeIdle(t, c, anchor)
	if err := c.CheckIdleCompaction(ctx, anchor.Add(27*time.Minute/2-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("triggered early")
	}
	if err := c.CheckIdleCompaction(ctx, anchor.Add(27*time.Minute/2)); err != nil {
		t.Fatal(err)
	}
	var r core.ModelRequest
	select {
	case r = <-started:
	case <-time.After(3 * time.Second):
		snapshot, _ := c.Snapshot(ctx)
		raw, _ := c.store.LoadContext(ctx)
		t.Fatal("idle did not start", snapshot.Agent, string(raw)[:200])
	}
	if r.CompactionID == nil || !strings.Contains(string(r.Body), "stable system") || !strings.Contains(string(r.Body), "pot_compact_request") {
		t.Fatal("wrong compact request", string(r.Body))
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "next", ConversationID: "main", Text: "continue normally"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("foreground preempted compact")
	}
	if err := c.CheckIdleCompaction(ctx, anchor.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case r = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("queued input lost")
	}
	if r.CompactionID != nil || strings.Contains(string(r.Body), "<pot_compact_request>") || !strings.Contains(string(r.Body), "continue normally") {
		t.Fatal("compact mode leaked", string(r.Body))
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	snapshot, _ := c.Snapshot(ctx)
	found := false
	for _, m := range snapshot.Messages {
		if m.Compaction != nil {
			found = true
			if m.Compaction.State != "complete" {
				t.Fatal(m)
			}
		}
		if m.Role == protocol.User && m.Text == "" {
			t.Fatal("synthetic compact user")
		}
	}
	if !found {
		t.Fatal("no visible compact operation")
	}
}
func TestInterruptedIdleRestoresHistoryAndRejectsReservedInputs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	var cleanup, calls atomic.Int64
	c := conversationFixture(t, path, "old", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("done"), nil
	}, &cleanup)
	primeIdle(t, c, time.Now())
	for _, send := range []protocol.SendMessage{{RequestID: compactPrefix + "spoof", ConversationID: "main", Text: "spoof"}} {
		if _, err := c.Send(ctx, send); err == nil {
			t.Fatal("accepted maintenance identity")
		}
	}
	if _, err := c.Steer(ctx, SteerRequest{RequestID: compactPrefix + "spoof", UserMessageID: "target", Text: "spoof"}); err == nil {
		t.Fatal("accepted compact steering identity")
	}
	raw, _ := c.store.LoadContext(ctx)
	var checkpoint agent.Checkpoint
	_ = json.Unmarshal(raw, &checkpoint)
	id := "unknown-compact-model"
	checkpoint.ModelRequestID = &id
	raw, _ = json.Marshal(checkpoint)
	_ = c.store.SaveContext(ctx, raw)
	turn, _ := json.Marshal(savedTurn{Owner: "main", BootID: "old", Generation: c.thread.Generation, Request: protocol.SendMessage{RequestID: compactPrefix + "interrupted", ConversationID: "main"}, State: "running"})
	_ = c.store.Set(ctx, "conversation.turn", string(turn))
	c.Close()
	c = conversationFixture(t, path, "new", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("done"), nil
	}, &cleanup)
	snapshot, _ := c.Snapshot(ctx)
	if snapshot.Agent.Phase == protocol.AgentFailed || snapshot.Turn != nil || calls.Load() != 0 {
		t.Fatal("maintenance required tool recovery", snapshot)
	}
	raw, _ = c.store.LoadContext(ctx)
	_ = json.Unmarshal(raw, &checkpoint)
	if checkpoint.ModelRequestID != nil || len(checkpoint.Messages) != 8 {
		t.Fatal("history changed", checkpoint)
	}
}

func TestIdleTimingRecalculatesAndRouteChangeInvalidates(t *testing.T) {
	ctx := context.Background()
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		t.Error("deferred timer reached model")
		return replySSE(""), nil
	}, &cleanup)
	anchor := time.Now()
	primeIdle(t, c, anchor)
	policy := IdleCompactionPolicy{CacheTTL: 20 * time.Minute, Source: "connection"}
	checks := 0
	c.mu.Lock()
	c.options.IdlePolicy = func(context.Context, core.Thread) (IdleCompactionPolicy, error) { return policy, nil }
	c.options.CompactGate = func(context.Context, func() error) error { checks++; return ErrCompactDeferred }
	c.mu.Unlock()
	if err := c.CheckIdleCompaction(ctx, anchor.Add(27*time.Minute/2)); err != nil || checks != 0 {
		t.Fatal("ignored connection delay", err, checks)
	}
	if err := c.CheckIdleCompaction(ctx, anchor.Add(18*time.Minute)); err != nil || checks != 1 {
		t.Fatal("missed exact deadline", err, checks)
	}
	c.mu.Lock()
	policy = IdleCompactionPolicy{CacheTTL: 10 * time.Minute, Source: "model"}
	c.mu.Unlock()
	if err := c.CheckIdleCompaction(ctx, anchor.Add(9*time.Minute)); err != nil || checks != 2 {
		t.Fatal("did not recalculate original anchor", err, checks)
	}
	c.mu.Lock()
	c.thread.Config.Model = "new-route"
	c.mu.Unlock()
	if err := c.CheckIdleCompaction(ctx, anchor.Add(time.Hour)); err != nil || checks != 2 {
		t.Fatal("stale route timer fired", err, checks)
	}
}
