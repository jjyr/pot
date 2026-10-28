package assistant

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// A lost HTTP acknowledgement is simulated by discarding Send's first result.
// Concurrent retries and process boots must consult the same business receipt;
// the transport's request lifetime is deliberately absent from this fixture.
func TestOutboxReceiptConcurrentRetriesRestartAndScope(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var calls, cleanup atomic.Int64
	model := func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return replySSE("synthetic reply"), nil
	}
	path := filepath.Join(root, "first.sqlite")
	c := conversationFixture(t, path, "boot-one", model, &cleanup)
	request := protocol.SendMessage{RequestID: "fixed-submission", ConversationID: "main", Text: "Synthetic queued input"}
	first, err := c.Send(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			value, err := c.Send(ctx, request)
			if err != nil || !value.Duplicate || value.MessageID != first.MessageID {
				t.Error("concurrent retry changed acceptance", value, err)
			}
		})
	}
	group.Wait()
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if calls.Load() != 1 {
		t.Fatal("duplicate model execution", calls.Load())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "boot-two", model, &cleanup)
	q := protocol.SendReceiptQuery{RequestID: request.RequestID, ConversationID: request.ConversationID}
	if receipt, err := c.SendReceipt(ctx, q); err != nil || receipt.State != "accepted" || receipt.MessageID != first.MessageID {
		t.Fatal("boot lost durable acceptance", receipt, err)
	}
	if value, err := c.Send(ctx, request); err != nil || !value.Duplicate || value.MessageID != first.MessageID || calls.Load() != 1 {
		t.Fatal("restart replayed accepted input", value, err, calls.Load())
	}
	changed := request
	changed.Text = "Different input under old identity"
	if _, err := c.Send(ctx, changed); err == nil {
		t.Fatal("same identity admitted changed content")
	}
	changed = request
	changed.ConversationID = "new-conversation"
	if _, err := c.Send(ctx, changed); err == nil {
		t.Fatal("old request retargeted a new conversation")
	}
	other := conversationFixture(t, filepath.Join(root, "independent-owner.sqlite"), "other-boot", model, &cleanup)
	if receipt, err := other.SendReceipt(ctx, q); err != nil || receipt.State != "unconfirmed" {
		t.Fatal("receipt leaked across independent owning databases", receipt, err)
	}
	request.Text = "Same ID in an independent owning database"
	if accepted, err := other.Send(ctx, request); err != nil || accepted.Duplicate || accepted.MessageID == first.MessageID {
		t.Fatal("independent scope shared a receipt", accepted, err)
	}
	waitSnapshot(t, other, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if calls.Load() != 2 {
		t.Fatal("scope did not execute independently", calls.Load())
	}
}
