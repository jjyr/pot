package hooks

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestApprovalRoutesNeverEnterAgentInbox(t *testing.T) {
	db, err := storage.OpenDatabase(filepath.Join(t.TempDir(), "inbox.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []Kind{ApprovalRequested, ApprovalResolved, ApprovalExpired} {
		event := Approval(kind, "approval", "assistant", "task", "run", 1, "")
		if err = event.Validate(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(event.Topics(), []string{"permissions", "tasks"}) {
			t.Fatal(event.Topics())
		}
		err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error { return EnqueueTx(context.Background(), tx, "control", "runtime", event) })
		if err == nil {
			t.Fatal("approval could wake an assistant model")
		}
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM agent_messages").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestOutcomeHookRejectsMismatchedTypeAndRun(t *testing.T) {
	event := Task("assistant", 1, protocol.TaskResultEvent{Origin: protocol.TaskOrigin{Kind: "manual"}, TaskID: "task", RunID: "run", Title: "Work", Phase: protocol.TaskFailed, Text: "failed"})
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.Kind = TaskCompleted
	if err := event.Validate(); err == nil {
		t.Fatal("failure relabeled as success")
	}
	event.Kind = TaskFailed
	event.RunID = "replacement-run"
	if err := event.Validate(); err == nil {
		t.Fatal("outcome rebound to another run")
	}
}

func TestStartedHookCannotEnterAgentInbox(t *testing.T) {
	event := Started("assistant", "task", "run", 1)
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(event.Topics(), []string{"tasks"}) {
		t.Fatal(event.Topics())
	}
	if err := EnqueueTx(context.Background(), nil, "control", "runtime", event); err == nil {
		t.Fatal("start hook entered summary inbox")
	}
	event.Result = &protocol.TaskResultEvent{}
	if err := event.Validate(); err == nil {
		t.Fatal("start hook accepted a terminal result")
	}
}
