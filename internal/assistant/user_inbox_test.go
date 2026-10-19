package assistant

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestUserSteeringDuringSummaryQueuesIndependentMainTurn(t *testing.T) {
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, err
		}
		system := body.Messages[0].Content
		if calls.Add(1) == 1 {
			if !strings.Contains(system, "Process background updates") || len(body.Tools) != 0 {
				t.Error("summary must have its own read-only role")
			}
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return replySSE("A completed"), nil
		}
		if strings.Contains(system, "Process background updates") {
			t.Error("user request inherited summary role")
		}
		if !strings.Contains(string(r.Body), "start B") {
			t.Error("queued user input was lost")
		}
		return replySSE("handled B"), nil
	}, &cleanup)
	if _, err := c.Report(ctx, "pot-result-a", "Task A completed"); err != nil {
		t.Fatal(err)
	}
	<-entered
	snapshot, _ := c.Snapshot(ctx)
	request := SteerRequest{RequestID: "user-b", UserMessageID: snapshot.Turn.UserMessageID, Text: "start B"}
	receipt, err := c.Steer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := c.Steer(ctx, SteerRequest{RequestID: "user-b", UserMessageID: snapshot.Turn.UserMessageID, Text: "start B"})
	if err != nil || !retry.Duplicate || retry.MessageID != receipt.MessageID {
		t.Fatal("queued retry was not idempotent", retry, err)
	}
	if _, err := c.Report(ctx, "pot-result-other", "Other result"); err == nil {
		t.Fatal("background notification overtook accepted user input")
	}
	close(release)
	snapshot = waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn == nil && len(s.Messages) >= 4 && s.Messages[len(s.Messages)-1].Text == "handled B"
	})
	if calls.Load() != 2 {
		t.Fatal("unexpected model attempts", calls.Load())
	}
	for _, m := range snapshot.Messages {
		if m.ID == receipt.MessageID && (m.Queued || m.Status != protocol.Complete) {
			t.Fatal("queued user request was not applied", m)
		}
	}
	var pending int
	if err := c.store.DB().QueryRow("SELECT COUNT(*) FROM chat_user_inbox").Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
	retry, err = c.Steer(ctx, request)
	if err != nil || !retry.Duplicate || retry.MessageID != receipt.MessageID {
		t.Fatal("completed queued steering retry lost its receipt", retry, err)
	}
	request.UserMessageID = "different-summary"
	if _, err = c.Steer(ctx, request); err == nil {
		t.Fatal("queued steering retry changed its original target")
	}
}

func TestAcceptedUserInboxStartsAfterRecovery(t *testing.T) {
	c, calls := stoppedMainFixture(t, recoveryCheckpoint(), true)
	ctx := context.Background()
	c.mu.Lock()
	receipt, err := c.queueUserLocked(ctx, protocol.SendMessage{RequestID: "accepted-next", ConversationID: "main", Text: "separate accepted input"})
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.StartPendingUsers(ctx); err != nil || calls.Load() != 0 {
		t.Fatal("uncertain original turn must retain its fence", err, calls.Load())
	}
	state, err := c.Recovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ReconcileRecovery(ctx, *state, nil, true); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn == nil && len(s.Messages) > 0 && s.Messages[len(s.Messages)-1].Text == "new input completed"
	})
	if calls.Load() != 1 {
		t.Fatal("original input was replayed or accepted input was stranded", calls.Load())
	}
	for _, message := range snapshot.Messages {
		if message.ID == receipt.MessageID && (message.Queued || message.Status != protocol.Complete) {
			t.Fatal("accepted input was not completed", message)
		}
	}
}

func TestUnstartedUserInboxSurvivesRestartAndRunsOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chat.sqlite")
	var cleanup, calls atomic.Int64
	model := func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("queued work handled"), nil
	}
	c := conversationFixture(t, path, "boot-one", model, &cleanup)
	c.mu.Lock()
	receipt, err := c.queueUserLocked(ctx, protocol.SendMessage{RequestID: "queued-user", ConversationID: "main", Text: "safe unstarted work"})
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "boot-two", model, &cleanup)
	if err = c.StartPendingUsers(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn == nil && len(s.Messages) == 2 && s.Messages[1].Text == "queued work handled"
	})
	if snapshot.Messages[0].ID != receipt.MessageID || snapshot.Messages[0].Status != protocol.Complete || calls.Load() != 1 {
		t.Fatal("pending input was lost or replayed", snapshot, calls.Load())
	}
	retry, err := c.Send(ctx, protocol.SendMessage{RequestID: "queued-user", ConversationID: "main", Text: "safe unstarted work"})
	if err != nil || !retry.Duplicate {
		t.Fatal(retry, err)
	}
}
