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

func TestCatalogueKeepsLastCompletedReplyAcrossActiveTurnAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup atomic.Int64
	gate := make(chan struct{})
	c := conversationFixture(t, path, "first", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		select {
		case <-gate:
			return replySSE(" Hello\n 世界 "), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, &cleanup)
	request := protocol.SendMessage{RequestID: "one", ConversationID: "main", Text: "First"}
	if _, err := c.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 2 })
	first, err := c.Catalogue(ctx)
	if err != nil || first.ReplyCount != 1 || first.ReplyID == "" || first.Preview != "Hello 世界" || first.Running {
		t.Fatal(first, err)
	}
	if _, err := c.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	c.Close()
	gate = make(chan struct{})
	c = conversationFixture(t, path, "second", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		select {
		case <-gate:
			return replySSE("Next"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, &cleanup)
	restored, err := c.Catalogue(ctx)
	if err != nil || restored != first {
		t.Fatal(restored, first, err)
	}
	request.RequestID = "two"
	if _, err := c.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	active, err := c.Catalogue(ctx)
	if err != nil || !active.Responding || !active.Running || active.ReplyCount != 1 || active.ReplyID != first.ReplyID || active.Preview != first.Preview {
		t.Fatal(active, err)
	}
	close(gate)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 4 })
	last, err := c.Catalogue(ctx)
	if err != nil || last.ReplyCount != 2 || last.ReplyID == first.ReplyID || last.Preview != "Next" || last.Running {
		t.Fatal(last, err)
	}
}
