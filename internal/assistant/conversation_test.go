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

	"github.com/jjyr/pot/internal/agent"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/modelprotocol"
	"github.com/jjyr/pot/internal/protocol"
)

type modelPort struct {
	model func(context.Context, core.ModelRequest) (io.ReadCloser, error)
}

func (p modelPort) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	return p.model(ctx, r)
}
func (modelPort) RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error) {
	return nil, nil, errors.New("unexpected RPC")
}
func (modelPort) Compaction(context.Context, json.RawMessage) error {
	return errors.New("unexpected compaction")
}
func replySSE(text string) io.ReadCloser {
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2}})
	return io.NopCloser(strings.NewReader("data: " + string(body) + "\n\ndata: [DONE]\n\n"))
}
func conversationFixture(t *testing.T, path, boot string, model func(context.Context, core.ModelRequest) (io.ReadCloser, error), cleanup *atomic.Int64) *Conversation {
	t.Helper()
	manager, err := execution.New("runtime", boot, execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	spec := resolved(t, "model")
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.AutoCompact = false
	if err := spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	var c *Conversation
	options := ConversationOptions{Owner: "main", RuntimeID: "runtime", BootID: boot, Manager: manager, InitialSpec: &spec, MaxRequests: 10, MaxTurnRequests: 3}
	options.Prepare = func(_ context.Context, thread core.Thread, request core.TurnRequest) (RunOptions, error) {
		return RunOptions{Execution: core.ExecutionContext{Owner: thread.Owner, Role: core.Main}, Services: modelPort{model: func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
			if err := c.ClaimModel(ctx, request.RequestID, thread.Generation, r.ID); err != nil {
				return nil, err
			}
			return model(ctx, r)
		}}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { cleanup.Add(1); return nil }}, nil
	}
	c, err = OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}
func waitSnapshot(t *testing.T, c *Conversation, predicate func(protocol.Snapshot) bool) protocol.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	updates, remove, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	for {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatal("conversation stopped")
			}
			if predicate(snapshot) {
				return snapshot
			}
		case <-ctx.Done():
			snapshot, _ := c.Snapshot(context.Background())
			t.Fatalf("timeout waiting for snapshot: %+v", snapshot)
		}
	}
}
func TestConversationRealAgentStreamDedupRestartAndCounts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, path, "boot-one", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("Hello 世界"), nil
	}, &cleanup)
	request := protocol.SendMessage{RequestID: "request", ConversationID: "main", Text: "  用户原文\n"}
	first, err := c.Send(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := c.Send(ctx, request)
	if err != nil || !duplicate.Duplicate || duplicate.MessageID != first.MessageID {
		t.Fatal(duplicate, err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 2 })
	if calls.Load() != 1 || cleanup.Load() != 1 || snapshot.Messages[0].Text != request.Text || snapshot.Messages[1].Text != "Hello 世界" || snapshot.Messages[1].Status != protocol.Complete || snapshot.Budget.Used != 1 {
		t.Fatal(snapshot, calls.Load(), cleanup.Load())
	}
	events, err := c.Events(ctx, 0)
	if err != nil || len(events) < 5 {
		t.Fatal(events, err)
	}
	c.Close()
	c = conversationFixture(t, path, "boot-two", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("second"), nil
	}, &cleanup)
	duplicate, err = c.Send(ctx, request)
	if err != nil || !duplicate.Duplicate || calls.Load() != 1 {
		t.Fatal(duplicate, err, calls.Load())
	}
	request.RequestID = "next"
	request.Text = "new"
	if _, err = c.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	snapshot = waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 4 })
	if snapshot.Budget.Used != 2 || calls.Load() != 2 {
		t.Fatal(snapshot, calls.Load())
	}
}
func TestConversationWaitingResumeUsesOriginalInput(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, path, "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			return nil, &protocol.APIError{Code: "pot_token_limit", Message: "allowance unavailable"}
		}
		return replySSE("resumed"), nil
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "original input"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn != nil && s.Agent.Detail != nil && strings.Contains(*s.Agent.Detail, "Waiting")
	})
	if err := c.ResumeOriginal(ctx, "another-turn"); err == nil {
		t.Fatal("stale allowance turn resumed current input")
	}
	if err := c.ResumeOriginal(ctx, "turn"); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if len(snapshot.Messages) != 2 || snapshot.Messages[1].Text != "resumed" || snapshot.Agent.Detail != nil || cleanup.Load() != 2 {
		t.Fatal(snapshot, cleanup.Load())
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	inputs := 0
	for _, m := range checkpoint.Messages {
		if m.Text == "original input" {
			inputs++
		}
	}
	if inputs != 1 {
		t.Fatal("input replayed on resume", inputs)
	}
}
func TestConversationCancelFencesUnknownModelAndOldCancelCannotStopNewTurn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup atomic.Int64
	started := make(chan struct{})
	c := conversationFixture(t, path, "boot", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, &cleanup)
	accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "input"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel := protocol.CancelTurn{RequestID: "cancel", UserMessageID: accepted.MessageID}
	if err := c.Cancel(ctx, cancel); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if snapshot.Messages[0].Status != protocol.MessageCancelled || cleanup.Load() != 1 {
		t.Fatal(snapshot, cleanup.Load())
	}
	if err := c.Cancel(ctx, cancel); err != nil {
		t.Fatal("cancellation not idempotent", err)
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "new", ConversationID: "main", Text: "no replay"}); !errors.Is(err, agent.ErrUncertainEffects) {
		t.Fatal("uncertain model state silently reused", err)
	}
}
func TestConversationCrashTurnNeverReplayedAndOwnerIsolation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup, calls atomic.Int64
	c := conversationFixture(t, path, "boot-one", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("done"), nil
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "input"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if err := c.store.Set(ctx, "conversation.turn", `{"owner":"main","boot_id":"old","generation":1,"request":{"request_id":"uncertain","conversation_id":"main","text":"never replay"},"user_message_id":"existing","state":"running","cancelling":false,"resuming":false,"reply_message_id":""}`); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c = conversationFixture(t, path, "boot-two", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("bad"), nil
	}, &cleanup)
	snapshot, _ := c.Snapshot(ctx)
	if snapshot.Agent.Phase != protocol.AgentFailed || calls.Load() != 1 {
		t.Fatal(snapshot, calls.Load())
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "new", ConversationID: "main", Text: "not allowed"}); !errors.Is(err, agent.ErrUncertainEffects) {
		t.Fatal(err)
	}
	options := c.options
	options.Owner = "other"
	if other, err := OpenConversation(path, options); err == nil {
		other.Close()
		t.Fatal("conversation rebound to another owner")
	}
}

func TestConversationWaitingCheckpointSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup, calls atomic.Int64
	c := conversationFixture(t, path, "old-boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return nil, protocol.APIError{Code: "pot_token_limit", Message: "waiting"}
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "original"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn != nil && s.Agent.Detail != nil && strings.Contains(*s.Agent.Detail, "Waiting")
	})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "new-boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("after restart"), nil
	}, &cleanup)
	before, _ := c.Snapshot(ctx)
	if before.Turn == nil || calls.Load() != 1 {
		t.Fatal("waiting work automatically replayed", before, calls.Load())
	}
	if err := c.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	after := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if len(after.Messages) != 2 || after.Messages[1].Text != "after restart" || calls.Load() != 2 {
		t.Fatal(after, calls.Load())
	}
}

func TestConversationSteeringSavesBeforeAckAndContinues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup, calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	c := conversationFixture(t, path, "boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return replySSE("first"), nil
		}
		if !strings.Contains(string(r.Body), "durable guidance") {
			t.Error("model did not receive steering", string(r.Body))
		}
		return replySSE("after guidance"), nil
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "original"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := c.QueueSteering(ctx, "guide", "durable guidance"); err != nil {
		t.Fatal(err)
	}
	close(release)
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if calls.Load() != 2 || snapshot.Messages[len(snapshot.Messages)-1].Text != "after guidance" {
		t.Fatal(snapshot, calls.Load())
	}
	var acknowledged bool
	if err := c.store.DB().QueryRow("SELECT acknowledged FROM assistant_steering WHERE id='guide'").Scan(&acknowledged); err != nil || !acknowledged {
		t.Fatal("steering not acknowledged", acknowledged, err)
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range checkpoint.SteeringIDs {
		if id == "guide" {
			found = true
		}
	}
	if !found {
		t.Fatal("ack preceded durable checkpoint")
	}
	if err := c.QueueSteering(ctx, "guide", "durable guidance"); err != nil {
		t.Fatal("durable steering retry failed", err)
	}
}

func TestConversationPersistsToolStreamAndFinalReceipt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup, calls, effects atomic.Int64
	c := conversationFixture(t, path, "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			return io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"fixture\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")), nil
		}
		return replySSE("observed result"), nil
	}, &cleanup)
	prepare := c.options.Prepare
	c.options.Prepare = func(ctx context.Context, thread core.Thread, request core.TurnRequest) (RunOptions, error) {
		run, err := prepare(ctx, thread, request)
		run.Tools = []modelprotocol.ToolDefinition{{Name: "fixture", Description: "Fixture effect", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}
		run.Dispatch = func(_ context.Context, _ core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
			effects.Add(1)
			if err := emit(core.Update{Type: "tool_output", ID: call.ID, Stream: "stdout", Text: "streamed"}); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"final receipt"}]}`), nil
		}
		return run, err
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "run fixture"}); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if effects.Load() != 1 || calls.Load() != 2 || cleanup.Load() != 1 {
		t.Fatal(snapshot, effects.Load(), calls.Load(), cleanup.Load())
	}
	var tool *protocol.Message
	for i := range snapshot.Messages {
		if snapshot.Messages[i].Role == protocol.Tool {
			tool = &snapshot.Messages[i]
		}
	}
	if tool == nil || tool.Status != protocol.Complete || !strings.Contains(tool.Text, "final receipt") {
		t.Fatal("tool result projection missing", tool)
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Receipts) != 1 || checkpoint.Receipts[0].ID != "call" || checkpoint.Started != nil {
		t.Fatal(checkpoint)
	}
}

func TestTurnControlPausesAtBoundaryAndSteeringBindsOriginalReceipt(t *testing.T) {
	ctx := context.Background()
	var cleanup, calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	second := make(chan struct{})
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
			return replySSE("first"), nil
		case 2:
			close(second)
			if !strings.Contains(string(r.Body), "visible guidance") {
				t.Error("guidance absent from continued model request")
			}
			return replySSE("after guidance"), nil
		}
		return replySSE("later"), nil
	}, &cleanup)
	receipt, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn", ConversationID: "main", Text: "original"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = c.Control(ctx, TurnControl{receipt.MessageID, "pause"}); err != nil {
		t.Fatal(err)
	}
	guidance, err := c.Steer(ctx, SteerRequest{"guide", receipt.MessageID, "visible guidance"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := c.Snapshot(ctx)
	if snapshot.Turn == nil || !snapshot.Turn.Paused {
		t.Fatal(snapshot)
	}
	found := false
	for _, m := range snapshot.Messages {
		if m.ID == guidance.MessageID && m.Text == "visible guidance" && m.Role == protocol.User && m.Status == protocol.Accepted {
			found = true
		}
	}
	if !found {
		t.Fatal("accepted steering absent from visible transcript")
	}
	close(release)
	select {
	case <-second:
		t.Fatal("paused turn sampled the next model")
	case <-time.After(60 * time.Millisecond):
	}
	if err = c.Control(ctx, TurnControl{receipt.MessageID, "resume"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	again, err := c.Steer(ctx, SteerRequest{"guide", receipt.MessageID, "visible guidance"})
	if err != nil || !again.Duplicate || again.MessageID != guidance.MessageID {
		t.Fatal(again, err)
	}
	if _, err = c.Steer(ctx, SteerRequest{"guide", "another-user", "visible guidance"}); err == nil {
		t.Fatal("steering receipt retargeted")
	}
	if err = c.Control(ctx, TurnControl{receipt.MessageID, "pause"}); err == nil {
		t.Fatal("ended turn paused")
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
