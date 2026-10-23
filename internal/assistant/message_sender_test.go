package assistant

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMessageSenderAttemptDoesNotReviveWithContinuation(t *testing.T) {
	ctx := context.Background()
	var cleanup, calls atomic.Int64
	contexts := make(chan context.Context, 2)
	release := make(chan struct{})
	defer close(release)
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(ctx context.Context, _ core.ModelRequest) (io.ReadCloser, error) {
		contexts <- ctx
		if calls.Add(1) == 1 {
			return nil, errors.New("confirmed model rejection")
		}
		select {
		case <-release:
			return replySSE("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, &cleanup)
	accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	old := <-contexts
	waitSnapshot(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil })
	if _, err = c.RetryFailed(ctx, "continue", accepted.MessageID); err != nil {
		t.Fatal(err)
	}
	active := <-contexts
	m, _ := mailbox(t, "local")
	effect := func(id string) func() (any, error) {
		return func() (any, error) {
			return m.Send(ctx, id, address("local", "main"), protocol.SendMail{To: address("local", "tony"), Text: "reply"}, nil)
		}
	}
	if _, err = GuardMessageSend(context.WithoutCancel(old), effect("stale")); err == nil {
		t.Fatal("same turn/generation revived the old sender")
	}
	if _, err = GuardMessageSend(active, effect("active")); err != nil {
		t.Fatal("active continuation could not enqueue", err)
	}
	if err = c.Cancel(ctx, protocol.CancelTurn{RequestID: "cancel", UserMessageID: accepted.MessageID}); err != nil {
		t.Fatal(err)
	}
	if _, err = GuardMessageSend(context.WithoutCancel(active), effect("cancelled")); err == nil {
		t.Fatal("cancelled sender accepted new mail")
	}
	var count int
	if err = m.db.QueryRow("SELECT COUNT(*) FROM agent_messages").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
