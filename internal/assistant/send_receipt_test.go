package assistant

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestSendReceiptDurableIdentityAndOrderedSteering(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var calls, cleanup atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	c := conversationFixture(t, path, "boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return replySSE("first"), nil
		}
		body := string(r.Body)
		if strings.Count(body, "first guidance") != 1 || strings.Count(body, "second guidance") != 1 || strings.Index(body, "first guidance") > strings.Index(body, "second guidance") {
			t.Error("guidance was reordered or duplicated", body)
		}
		return replySSE("continued"), nil
	}, &cleanup)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	original, err := c.Send(ctx, protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	q := SendReceiptQuery{RequestID: "first", ConversationID: "main", UserMessageID: original.MessageID}
	if value, err := c.SendReceipt(ctx, q); err != nil || value.State != "unconfirmed" {
		t.Fatal(value, err)
	}
	first := SteerRequest{RequestID: q.RequestID, UserMessageID: q.UserMessageID, Text: "first guidance"}
	receipt, err := c.Steer(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := c.SendReceipt(ctx, q); err != nil || value.State != "accepted" || value.MessageID != receipt.MessageID || value.RuntimeID != "runtime" || value.OwnerID != "main" {
		t.Fatal(value, err)
	}
	snapshot, _ := c.Snapshot(ctx)
	found := false
	for _, m := range snapshot.Messages {
		if m.ID == receipt.MessageID {
			found = m.AcceptedRequestID == q.RequestID && m.AcceptedTurnID == q.UserMessageID && m.RequestID == nil
		}
	}
	if !found {
		t.Fatal("steering acceptance identity absent or confused with a new turn")
	}
	// Simulate a transcript page without the accepted bubble. Lookup still uses
	// the durable journal, including entries written before identity fields existed.
	c.mu.Lock()
	saved := c.messages
	c.messages = nil
	c.mu.Unlock()
	if value, err := c.SendReceipt(ctx, q); err != nil || value.State != "accepted" {
		t.Fatal(value, err)
	}
	c.mu.Lock()
	c.messages = saved
	c.mu.Unlock()
	if duplicate, err := c.Steer(ctx, first); err != nil || !duplicate.Duplicate || duplicate.MessageID != receipt.MessageID {
		t.Fatal(duplicate, err)
	}
	if _, err = c.Steer(ctx, SteerRequest{RequestID: "second", UserMessageID: q.UserMessageID, Text: "second guidance"}); err != nil {
		t.Fatal(err)
	}
	wrong := q
	wrong.UserMessageID = "another-turn"
	if value, err := c.SendReceipt(ctx, wrong); err != nil || value.State != "rejected" {
		t.Fatal("receipt retargeted", value, err)
	}
	wrong = q
	wrong.ConversationID = "other"
	if _, err = c.SendReceipt(ctx, wrong); err == nil {
		t.Fatal("receipt crossed conversations")
	}
	ordinary := SendReceiptQuery{RequestID: "original", ConversationID: "main"}
	if value, err := c.SendReceipt(ctx, ordinary); err != nil || value.State != "accepted" || value.MessageID != original.MessageID {
		t.Fatal(value, err)
	}
	close(release)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if calls.Load() != 2 {
		t.Fatal("receipt lookup or duplicate replay started another model request", calls.Load())
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := conversationFixture(t, path, "new-boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		t.Error("receipt lookup executed a model")
		return replySSE("unexpected"), nil
	}, &cleanup)
	if value, err := restarted.SendReceipt(ctx, q); err != nil || value.State != "accepted" || value.MessageID != receipt.MessageID {
		t.Fatal(value, err)
	}
	q.RequestID = "never-accepted"
	if value, err := restarted.SendReceipt(ctx, q); err != nil || value.State != "rejected" {
		t.Fatal("ended target should reject an absent steer", value, err)
	}
	q.UserMessageID = ""
	if value, err := restarted.SendReceipt(ctx, q); err != nil || value.State != "unconfirmed" {
		t.Fatal("missing ordinary acceptance is not proof of rejection", value, err)
	}
}
