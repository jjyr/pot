package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func stoppedMainFixture(t *testing.T, checkpoint agent.Checkpoint, cleanupProofKnown bool) (*Conversation, *atomic.Int64) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	calls := &atomic.Int64{}
	cleanup := &atomic.Int64{}
	model := func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("new input completed"), nil
	}
	c := conversationFixture(t, path, "old-boot", model, cleanup)
	turn := savedTurn{Owner: "main", BootID: "old-boot", Generation: 1, Request: protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "original input"}, UserMessageID: "original-user", State: "running", Paused: true}
	requestID := turn.Request.RequestID
	if _, err := c.store.Save(ctx, protocol.Message{ID: turn.UserMessageID, RequestID: &requestID, ConversationID: "main", Role: protocol.User, Text: turn.Request.Text, Status: protocol.Streaming, CreatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(checkpoint)
	if err := c.store.SaveContext(ctx, raw); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(turn)
	if err := c.store.Set(ctx, "conversation.turn", string(raw)); err != nil {
		t.Fatal(err)
	}
	if cleanupProofKnown {
		if err := c.store.Set(ctx, "conversation.cleanup.original", cleanupProof(&turn)); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return conversationFixture(t, path, "new-boot", model, cleanup), calls
}
func recoveryCheckpoint() agent.Checkpoint {
	return agent.Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}, LastRequest: "original"}
}
func TestMainRecoverySafePausedBoundaryClosesOriginalWithoutReplay(t *testing.T) {
	checkpoint := recoveryCheckpoint()
	checkpoint.Pending = []m.ToolCall{{ID: "never-started", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"must not run"}`)}}
	c, calls := stoppedMainFixture(t, checkpoint, false)
	ctx := context.Background()
	state, err := c.Recovery(ctx)
	if err != nil || state.RequestID != "original" {
		t.Fatal(state, err)
	}
	if err = c.ReconcileRecovery(ctx, *state, nil, true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("old input replayed")
	}
	raw, _ := c.store.LoadContext(ctx)
	var closed agent.Checkpoint
	json.Unmarshal(raw, &closed)
	if len(closed.Pending) != 0 || len(closed.Messages) != 1 || closed.Messages[0].CallID != "never-started" {
		t.Fatal("never-started batch not closed", closed)
	}
	if recovered, err := c.Recovered(ctx, "original-user"); err != nil || !recovered {
		t.Fatal(recovered, err)
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "next", ConversationID: "main", Text: "fresh input"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestMainRecoveryUsesExactCheckpointAndOriginalResult(t *testing.T) {
	checkpoint := recoveryCheckpoint()
	id := "tool"
	checkpoint.Started = &id
	checkpoint.Pending = []m.ToolCall{{ID: id, Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"effect"}`)}}
	c, calls := stoppedMainFixture(t, checkpoint, true)
	ctx := context.Background()
	state, err := c.Recovery(ctx)
	if err != nil || !state.CleanupConfirmed {
		t.Fatal(state, err)
	}
	changed := *state
	changed.CheckpointHash = "different"
	if err = c.ReconcileRecovery(ctx, changed, map[string]json.RawMessage{id: json.RawMessage(`{"stdout":"known"}`)}, true); err == nil {
		t.Fatal("changed checkpoint accepted")
	}
	if err = c.ReconcileRecovery(ctx, *state, nil, true); !errors.Is(err, agent.ErrUncertainEffects) {
		t.Fatal("missing receipt accepted", err)
	}
	if err = c.ReconcileRecovery(ctx, *state, map[string]json.RawMessage{id: json.RawMessage(`{"stdout":"known"}`)}, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := c.store.LoadContext(ctx)
	var result agent.Checkpoint
	json.Unmarshal(raw, &result)
	if result.Started != nil || len(result.Receipts) != 1 || len(result.Pending) != 0 || calls.Load() != 0 {
		t.Fatal(result, calls.Load())
	}
}
func TestMainRecoveryAbandonsInterruptedModelAfterCleanup(t *testing.T) {
	checkpoint := recoveryCheckpoint()
	id := "model-original"
	checkpoint.ModelRequestID = &id
	c, calls := stoppedMainFixture(t, checkpoint, true)
	ctx := context.Background()
	state, err := c.Recovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ReconcileRecovery(ctx, *state, nil, false); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(err)
	}
	if err = c.ReconcileRecovery(ctx, *state, nil, true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("recovery resampled the old request")
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "new", ConversationID: "main", Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
}

func TestMainCleanupProofComesFromOriginalCleanupService(t *testing.T) {
	ctx := context.Background()
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		return nil, errors.New("original model transport lost")
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "input"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	proof, exists, err := c.store.Get(ctx, "conversation.cleanup.original")
	if err != nil || !exists || proof == "" || cleanup.Load() != 1 {
		t.Fatal(exists, cleanup.Load(), err)
	}
}

func TestCancelledModelCanContinueAfterRestartWithExactAttempt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup, calls atomic.Int64
	started := make(chan struct{})
	c := conversationFixture(t, path, "first-boot", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, &cleanup)
	accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "keep working"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := c.Cancel(ctx, protocol.CancelTurn{RequestID: "stop", UserMessageID: accepted.MessageID}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(snapshot protocol.Snapshot) bool { return snapshot.Turn == nil })
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "second-boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("continued"), nil
	}, &cleanup)
	snapshot, err := c.Snapshot(ctx)
	if err != nil || snapshot.Continuation == nil || snapshot.Continuation.RecoveryRequired || snapshot.Continuation.AttemptID != "original" {
		t.Fatal(snapshot, err)
	}
	if _, err := c.RetryContinuation(ctx, "stale", accepted.MessageID, "another-attempt"); err == nil {
		t.Fatal("stale attempt accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("stale action started a model", calls.Load())
	}
	if _, err := c.RetryContinuation(ctx, "continue", accepted.MessageID, "original"); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && snapshot.Agent.Phase == protocol.Ready
	})
	if calls.Load() != 2 {
		t.Fatal("cancelled input did not continue", calls.Load())
	}
}
