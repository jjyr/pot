package assistant

import (
	"context"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestSteerAfterCompletedTurnStartsNewInputAndRetriesIdempotently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "steer-after.sqlite")
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, path, "boot", func(_ context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("done"), nil
	}, &cleanup)
	original, err := c.Send(ctx, protocol.SendMessage{RequestID: "first-turn", ConversationID: "main", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) >= 2 })
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 model call, got %d", calls.Load())
	}
	request := SteerRequest{RequestID: "late-steer", UserMessageID: original.MessageID, Text: "guidance after completion"}
	receipt, err := c.Steer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MessageID == "" {
		t.Fatal("expected a queued message id")
	}
	if receipt.Duplicate {
		t.Fatal("first submission should not be duplicate")
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn == nil && len(s.Messages) >= 4 && s.Messages[len(s.Messages)-1].Text == "done"
	})
	if calls.Load() != 2 {
		t.Fatalf("late steer must start a new main turn, got %d calls", calls.Load())
	}
	retry, err := c.Steer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Duplicate || retry.MessageID != receipt.MessageID {
		t.Fatalf("retry must keep same receipt: %+v", retry)
	}
	if calls.Load() != 2 {
		t.Fatalf("retry must not trigger model, got %d calls", calls.Load())
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: request.RequestID, ConversationID: "main", Text: request.Text}); err == nil {
		t.Fatal("ordinary send adopted queued guidance identity")
	}
	q := protocol.SendReceiptQuery{RequestID: request.RequestID, ConversationID: "main", UserMessageID: request.UserMessageID}
	if value, err := c.SendReceipt(ctx, q); err != nil || value.State != "accepted" || value.MessageID != receipt.MessageID {
		t.Fatal(value, err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := conversationFixture(t, path, "boot2", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		t.Error("retry after restart must not call model")
		return replySSE("unexpected"), nil
	}, &cleanup)
	post, err := restarted.Steer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !post.Duplicate || post.MessageID != receipt.MessageID {
		t.Fatalf("restart retry must keep same message id: %+v vs %s", post, receipt.MessageID)
	}
}

func TestSteerDuringRetiringTurnQueuesInsteadOfSteering(t *testing.T) {
	for _, finalizing := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelling", true: "finalizing"}[finalizing], func(t *testing.T) { testSteerRetiringQueues(t, finalizing) })
	}
}

func testSteerRetiringQueues(t *testing.T, finalizing bool) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "steer-cancel.sqlite")
	started := make(chan struct{})
	block := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-block:
		default:
			close(block)
		}
	})
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, path, "boot", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-block:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return replySSE("first"), nil
		}
		return replySSE("second"), nil
	}, &cleanup)
	original, err := c.Send(ctx, protocol.SendMessage{RequestID: "active", ConversationID: "main", Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// Mark the turn as cancelling synchronously so the Steer path takes the queue branch.
	c.mu.Lock()
	if c.turn == nil {
		c.mu.Unlock()
		t.Fatal("no active turn")
	}
	c.turn.Cancelling = !finalizing
	c.finalizing = finalizing
	c.mu.Unlock()
	receipt, err := c.Steer(ctx, SteerRequest{RequestID: "during-cancel", UserMessageID: original.MessageID, Text: "queued guidance"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MessageID == "" {
		t.Fatal("expected queued message id")
	}
	// Verify it is a chat_user_inbox row, not a steering row.
	var steeringAll int
	if err = c.store.DB().QueryRow("SELECT COUNT(*) FROM assistant_steering").Scan(&steeringAll); err != nil {
		t.Fatal(err)
	}
	if steeringAll != 0 {
		t.Fatalf("cancelling steer must not write steering rows, got %d", steeringAll)
	}
	var queued int
	if err = c.store.DB().QueryRow("SELECT COUNT(*) FROM chat_user_inbox").Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("expected one queued input, got %d err %v", queued, err)
	}
	retry, err := c.Steer(ctx, SteerRequest{RequestID: "during-cancel", UserMessageID: original.MessageID, Text: "queued guidance"})
	if err != nil || !retry.Duplicate || retry.MessageID != receipt.MessageID {
		t.Fatalf("queued retry must be idempotent: %+v err %v", retry, err)
	}
	// Retargeting the same RequestID to a different user message must be rejected.
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "during-cancel", UserMessageID: "some-other-user", Text: "queued guidance"}); err == nil {
		t.Fatal("retargeted queued steering must be rejected")
	}
	c.mu.Lock()
	c.turn.Cancelling = false
	c.finalizing = false
	c.mu.Unlock()
	close(block)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) >= 4 })
	if calls.Load() != 2 {
		t.Fatal("queued guidance did not run independently", calls.Load())
	}
}

func TestSteerTargetingOldTurnWhileSuccessorRunsQueuesSeparately(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "steer-old.sqlite")
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	secondRelease := make(chan struct{})
	defer func() {
		select {
		case <-firstRelease:
		default:
			close(firstRelease)
		}
		select {
		case <-secondRelease:
		default:
			close(secondRelease)
		}
	}()
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, path, "boot", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		n := calls.Add(1)
		if n == 1 {
			close(firstStarted)
			select {
			case <-firstRelease:
			case <-ctx.Done():
			}
			return replySSE("one"), nil
		}
		if n == 2 {
			close(secondStarted)
			select {
			case <-secondRelease:
			case <-ctx.Done():
			}
			return replySSE("two"), nil
		}
		return replySSE("three"), nil
	}, &cleanup)
	first, err := c.Send(ctx, protocol.SendMessage{RequestID: "turn-one", ConversationID: "main", Text: "one"})
	if err != nil {
		t.Fatal(err)
	}
	<-firstStarted
	close(firstRelease)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) >= 2 })
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "turn-two", ConversationID: "main", Text: "two"}); err != nil {
		t.Fatal(err)
	}
	<-secondStarted
	// Target the ended first turn while the second turn's model is still running.
	receipt, err := c.Steer(ctx, SteerRequest{RequestID: "old-target", UserMessageID: first.MessageID, Text: "for the old turn"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MessageID == "" {
		t.Fatal("expected queued receipt")
	}
	var queued int
	if err = c.store.DB().QueryRow("SELECT COUNT(*) FROM chat_user_inbox").Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("old-target steer must queue, got %d err %v", queued, err)
	}
	// No mailbox message should be added to the running successor.
	var steering int
	if err = c.store.DB().QueryRow("SELECT COUNT(*) FROM assistant_steering").Scan(&steering); err != nil || steering != 0 {
		t.Fatalf("old-target steer must not touch steering mailbox, got %d err %v", steering, err)
	}
	retry, err := c.Steer(ctx, SteerRequest{RequestID: "old-target", UserMessageID: first.MessageID, Text: "for the old turn"})
	if err != nil || !retry.Duplicate || retry.MessageID != receipt.MessageID {
		t.Fatalf("old-target retry must be idempotent: %+v err %v", retry, err)
	}
	close(secondRelease)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn == nil && len(s.Messages) >= 6 && s.Messages[len(s.Messages)-1].Text == "three"
	})
	if calls.Load() != 3 {
		t.Fatalf("expected separate successor turn, got %d calls", calls.Load())
	}
}

func TestSteerRequestCollisionRejectedActiveAndLate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "steer-collide.sqlite")
	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, path, "boot", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return replySSE("ok"), nil
	}, &cleanup)
	original, err := c.Send(ctx, protocol.SendMessage{RequestID: "collide-req", ConversationID: "main", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	// Active-path collision: normal send already owns collide-req.
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "collide-req", UserMessageID: original.MessageID, Text: "steer with reused id"}); err == nil {
		t.Fatal("active collision must be rejected")
	}
	// A valid steer on a fresh request id must still succeed.
	receipt, err := c.Steer(ctx, SteerRequest{RequestID: "collide-ok", UserMessageID: original.MessageID, Text: "fresh steer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "collide-ok", ConversationID: "main", Text: "fresh steer"}); err == nil {
		t.Fatal("ordinary send adopted active steering identity")
	}
	// Reusing the steer request id against a different target or content must be rejected.
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "collide-ok", UserMessageID: "someone-else", Text: "fresh steer"}); err == nil {
		t.Fatal("retargeted steer must be rejected")
	}
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "collide-ok", UserMessageID: original.MessageID, Text: "different text"}); err == nil {
		t.Fatal("content-changed steer must be rejected")
	}
	close(release)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) >= 4 })
	// Late-path collision: reuse the original send request id after the turn ended.
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "collide-req", UserMessageID: original.MessageID, Text: "late reuse"}); err == nil {
		t.Fatal("late collision must be rejected")
	}
	// Late reuse of a previously accepted steering id must still match its original identity.
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "collide-ok", UserMessageID: original.MessageID, Text: "different text"}); err == nil {
		t.Fatal("late content-changed steer must be rejected")
	}
	dupe, err := c.Steer(ctx, SteerRequest{RequestID: "collide-ok", UserMessageID: original.MessageID, Text: "fresh steer"})
	if err != nil || !dupe.Duplicate || dupe.MessageID != receipt.MessageID {
		t.Fatalf("late identical steer must remain idempotent: %+v err %v", dupe, err)
	}
}
