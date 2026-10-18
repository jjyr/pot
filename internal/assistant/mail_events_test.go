package assistant

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestCompletionInboxClaimsAndReportsTheSameResultOnce(t *testing.T) {
	ctx := context.Background()
	m, err := OpenMailbox(filepath.Join(t.TempDir(), "messages.sqlite"), "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	event := protocol.Envelope{ID: "completion", Kind: "task_result", From: protocol.AgentRef{ControlID: "control", Kind: "runtime", AgentID: "runtime"}, To: address("control", "main"), ConversationID: newConversation(), Text: "done", Result: &protocol.TaskResultEvent{TaskID: "task-a", RunID: newConversation(), Title: "A", Phase: protocol.TaskComplete, Text: "done"}}
	enqueue := func() error {
		return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error { return inbox.EnqueueTx(ctx, tx, event, "in", "queued") })
	}
	if err = enqueue(); err != nil {
		t.Fatal(err)
	}
	if err = enqueue(); err != nil {
		t.Fatal("duplicate event changed its receipt", err)
	}
	if err = m.ObserveTaskResult(ctx, "main", "query", "task-a"); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishChat(ctx, "main", "query", "", false); err != nil {
		t.Fatal(err)
	}
	if err = m.ClaimEvent(ctx, "main", event.ID); err != nil {
		t.Fatal("failed query swallowed notification", err)
	}
	if err = m.ClaimEvent(ctx, "main", event.ID); err == nil {
		t.Fatal("same notification was claimed twice")
	}
	if err = m.FinishChat(ctx, "main", "pot-result-completion", event.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = enqueue(); err != nil {
		t.Fatal(err)
	}
	if err = m.ClaimEvent(ctx, "main", event.ID); err == nil {
		t.Fatal("reported result was queued again")
	}
	event.ID = "queried-result"
	event.Result.TaskID = "task-b"
	if err = enqueue(); err != nil {
		t.Fatal(err)
	}
	if err = m.ObserveTaskResult(ctx, "main", "successful-query", "task-b"); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishChat(ctx, "main", "successful-query", "", true); err != nil {
		t.Fatal(err)
	}
	if err = m.ClaimEvent(ctx, "main", event.ID); err == nil {
		t.Fatal("successful query caused a duplicate automatic report")
	}
	event.ID = "forged-event"
	event.From.ControlID = "remote"
	if _, err = m.Deliver(ctx, event); err == nil {
		t.Fatal("remote transport forged a Runtime lifecycle event")
	}
}
