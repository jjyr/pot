package assistant

import (
	"context"
	"encoding/json"
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
	if _, err := c.store.DB().Exec(`UPDATE assistant_idle_compaction SET request_id='last-model',finished_at=?,route=?,prefix=?`, finished.UnixMilli(), idleRoute(c.thread.Config), string(prefix)); err != nil {
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
	anchor := time.Now().Add(-15 * time.Minute)
	primeIdle(t, c, anchor)
	if err := c.CheckIdleCompaction(ctx, anchor.Add(15*time.Minute-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("triggered early")
	}
	if err := c.CheckIdleCompaction(ctx, anchor.Add(15*time.Minute)); err != nil {
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
	policy := IdleCompactionPolicy{Delay: 20 * time.Minute, Source: "connection"}
	checks := 0
	c.mu.Lock()
	c.options.IdlePolicy = func(context.Context, core.Thread) (IdleCompactionPolicy, error) { return policy, nil }
	c.options.CompactGate = func(context.Context, func() error) error { checks++; return ErrCompactDeferred }
	c.mu.Unlock()
	if err := c.CheckIdleCompaction(ctx, anchor.Add(15*time.Minute)); err != nil || checks != 0 {
		t.Fatal("ignored connection delay", err, checks)
	}
	if err := c.CheckIdleCompaction(ctx, anchor.Add(20*time.Minute)); err != nil || checks != 1 {
		t.Fatal("missed exact deadline", err, checks)
	}
	c.mu.Lock()
	policy = IdleCompactionPolicy{Delay: 5 * time.Minute, Source: "model"}
	c.mu.Unlock()
	if err := c.CheckIdleCompaction(ctx, anchor.Add(6*time.Minute)); err != nil || checks != 2 {
		t.Fatal("did not recalculate original anchor", err, checks)
	}
	c.mu.Lock()
	c.thread.Config.Model = "new-route"
	c.mu.Unlock()
	if err := c.CheckIdleCompaction(ctx, anchor.Add(time.Hour)); err != nil || checks != 2 {
		t.Fatal("stale route timer fired", err, checks)
	}
}
