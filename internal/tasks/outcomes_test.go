package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestOutcomeCursorSkipsArchiveAndReadsBoundedOwnerTransitions(t *testing.T) {
	ctx := context.Background()
	s, _, _, path := fixture(t)
	config := selection(t).Config
	if err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		for i := range 1000 {
			r := Record{SchemaVersion: 5, AssistantID: "assistant", Config: config, Plugins: []string{}, RunID: fmt.Sprint("run", i), Generation: 1,
				Info:    protocol.TaskInfo{ID: fmt.Sprint("task", i), AssistantID: "assistant", Title: "History", Phase: protocol.TaskComplete},
				Request: protocol.CreateTask{Text: "private task goal"}}
			if err := write(ctx, tx, &r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.Outcomes(ctx, "assistant", nil)
	if err != nil || baseline.Cursor != 1000 || len(baseline.Items) != 0 || baseline.HasMore {
		t.Fatal(baseline, err)
	}
	publications := 0
	s.options.OutcomesChanged = func() { publications++ }
	s.Notify()
	s.Notify()
	if publications != 1 {
		t.Fatal("metadata invalidation repeated outcome publication", publications)
	}
	// Existing archive rows need not be decoded at all to query outcomes.
	if _, err := s.db.Exec("UPDATE tasks SET body='not JSON' WHERE id='task999'"); err != nil {
		t.Fatal(err)
	}
	for i := range 105 {
		r, err := s.Create(ctx, "other", protocol.CreateTask{RequestID: fmt.Sprint(i), Text: "Other owner"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Stop(ctx, "other", r.Info.ID, "interrupt"); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Outcomes(ctx, "assistant", &baseline.Cursor)
	if err != nil || len(page.Items) != 0 || page.Cursor != baseline.Cursor {
		t.Fatal("other owner leaked into cursor", page, err)
	}
	zero := uint64(0)
	first, err := s.Outcomes(ctx, "other", &zero)
	if err != nil || len(first.Items) != 100 || !first.HasMore {
		t.Fatal(first, err)
	}
	last, err := s.Outcomes(ctx, "other", &first.Cursor)
	if err != nil || len(last.Items) != 5 || last.HasMore || last.Items[0].Sequence <= first.Cursor {
		t.Fatal(last, err)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "before dispatch") || strings.Contains(string(encoded), "private task goal") {
		t.Fatal("outcome projection leaked execution text", string(encoded))
	}
	// Restore the unrelated fixture before reopening its durable projection.
	if _, err := s.db.Exec("DELETE FROM tasks WHERE id='task999'"); err != nil {
		t.Fatal(err)
	}
	options := s.options
	s.Close()
	reopened, err := Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.Outcomes(ctx, "other", nil)
	if err != nil || restored.Cursor != last.Cursor || len(restored.Items) != 0 {
		t.Fatal("restart lost cursor", restored, err)
	}
}

func TestOutcomeInsertRollsBackTaskAndDoesNotRepeatSamePhase(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := fixture(t)
	r, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "work", Text: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_outcome BEFORE INSERT ON task_outcomes BEGIN SELECT RAISE(ABORT,'outcome rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "assistant", r.Info.ID, "interrupt"); err == nil {
		t.Fatal("failed outcome insertion committed Task")
	}
	stored, err := s.Get(ctx, "assistant", r.Info.ID)
	if err != nil || stored.Info.Phase != protocol.TaskQueued {
		t.Fatal(stored, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_outcome"); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "assistant", r.Info.ID, "interrupt"); err != nil {
		t.Fatal(err)
	}
	if err := s.change(ctx, "assistant", r.Info.ID, func(_ *sql.Tx, r *Record) error { r.Error = "metadata change"; return nil }); err != nil {
		t.Fatal(err)
	}
	zero := uint64(0)
	page, err := s.Outcomes(ctx, "assistant", &zero)
	if err != nil || len(page.Items) != 1 || page.Items[0].Phase != protocol.TaskInterrupted {
		t.Fatal("same phase repeated outcome", page, err)
	}
	for _, cursor := range []uint64{9007199254740992, ^uint64(0)} {
		if _, err := s.Outcomes(ctx, "assistant", &cursor); err == nil {
			t.Fatal("unsafe cursor accepted", cursor)
		}
	}
}

func TestScheduleValidationOutcomePublishesOnlyAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := fixture(t)
	r, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "scheduled", Text: "Scheduled work"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	s.options.OutcomesChanged = func() { count++ }
	if err := s.change(ctx, "assistant", r.Info.ID, func(_ *sql.Tx, r *Record) error {
		r.Info.Phase = protocol.TaskComplete
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.Outcomes(ctx, "assistant", nil)
	if err != nil || count != 1 {
		t.Fatal(baseline, count, err)
	}
	if err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		return s.FailResultTx(ctx, tx, "assistant", r.Info.ID, "Invalid structured result")
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("transaction published before caller's post-commit invalidation")
	}
	s.Notify()
	s.Notify()
	page, err := s.Outcomes(ctx, "assistant", &baseline.Cursor)
	if err != nil || count != 2 || len(page.Items) != 1 || page.Items[0].Phase != protocol.TaskFailed {
		t.Fatal("scheduled failure was lost or repeated", page, count, err)
	}
}
