package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func assertTaskContext(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("context changed: %s != %s", got, want)
	}
}

func TestTaskRolloutRestartCompressionAndContinuation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err = j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"context","version":2,"messages":[{"role":"user","content":"keep"}],"receipts":[{"id":"receipt","result":{"done":true}}]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", raw); err != nil {
		t.Fatal(err)
	}
	var inline bool
	if err = j.db.QueryRow("SELECT json_type(body,'$.context') IS NOT NULL FROM task_runs").Scan(&inline); err != nil || inline {
		t.Fatal("checkpoint still inline", inline, err)
	}
	before, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.AppendEvent(ctx, "runtime", "task", "run", core.AgentEvent{Generation: 1, TurnID: "run", Seq: 1, Update: core.Update{Type: "text_delta", Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	after, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.RolloutRef, after.RolloutRef) {
		t.Fatal("transcript update duplicated checkpoint")
	}
	if err = j.Finish(ctx, "runtime", "task", "run", Terminal{Outcome: core.Success, Text: "done"}); err != nil {
		t.Fatal(err)
	}
	if err = j.change(ctx, "runtime", "task", "run", func(run *Run) error { run.FinishedAtMS = time.Now().Add(-8 * 24 * time.Hour).UnixMilli(); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = j.db.Exec("UPDATE task_sessions SET activity_at=?", time.Now().Add(-8*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = j.db.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = openJournal(t, path, "runtime", "nextboot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(j.rolloutPath(*after)); err != nil {
		t.Fatal("startup unexpectedly rewrote cold rollout", err)
	}
	if err = j.CompressColdRollouts(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(j.rolloutPath(*after) + ".zst"); err != nil {
		t.Fatal(err)
	}
	restored, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, restored.Context, raw)
	launch.RunID = "next-run"
	continued, err := j.Accept(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, continued.Context, raw)
	if continued.RolloutRef == nil || j.rolloutPath(continued) != j.rolloutPath(*restored) {
		t.Fatal("continuation did not reuse task session")
	}
	if _, err = j.Claim(ctx, "runtime", "task", "next-run"); err != nil {
		t.Fatal(err)
	}
	updated := json.RawMessage(`{"type":"context","version":2,"messages":[{"role":"user","content":"keep"},{"role":"assistant","content":"new"}],"receipts":[]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "next-run", updated); err != nil {
		t.Fatal(err)
	}
	original, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, original.Context, raw)
}

func TestTaskRolloutSQLRollbackAndMissingCommittedFile(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err = j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	first := json.RawMessage(`{"type":"context","version":2,"messages":["one"]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", first); err != nil {
		t.Fatal(err)
	}
	headBefore, err := readTaskHead(ctx, j.db, "runtime", "task")
	if err != nil {
		t.Fatal(err)
	}
	runBefore, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback after append")
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		run, e := readRun(ctx, tx, "runtime", "task", "run")
		if e != nil {
			return e
		}
		run.Context = json.RawMessage(`{"type":"context","version":2,"messages":["one","uncommitted"]}`)
		if e = j.persistRun(ctx, tx, run); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	committed, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, committed.Context, first)
	headAfter, err := readTaskHead(ctx, j.db, "runtime", "task")
	if err != nil {
		t.Fatal(err)
	}
	if headAfter != headBefore || *committed.RolloutRef != *runBefore.RolloutRef {
		t.Fatal("rollback moved SQL task head or run ref")
	}
	second := json.RawMessage(`{"type":"context","version":2,"messages":["one","two"]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", second); err != nil {
		t.Fatal(err)
	}
	loaded, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, loaded.Context, second)
	headFinal, err := readTaskHead(ctx, j.db, "runtime", "task")
	if err != nil || headFinal != *loaded.RolloutRef {
		t.Fatal("subsequent append head mismatch", err)
	}
	historical := *runBefore
	historical.Context = nil
	if err = j.hydrateRun(&historical); err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, historical.Context, first)
	if err = os.Remove(j.rolloutPath(*loaded)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Inspect(ctx, "runtime", "task", "run"); err == nil {
		t.Fatal("missing rollout silently accepted")
	}
	if _, err = OpenJournal(j.db, t.TempDir(), "runtime", "nextboot"); err == nil {
		t.Fatal("startup accepted missing committed rollout")
	}
}

func TestTaskRolloutUpgradeExportsInlineContext(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	run, err := j.Accept(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"context","version":2,"messages":["` + strings.Repeat("legacy", 128<<10) + `"],"pending":[],"receipts":[]}`)
	run.SchemaVersion = 8
	run.Context = raw
	if err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		if err := writeRun(ctx, tx, run); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP TABLE task_sessions"); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM pot_migrations WHERE id LIKE '0017_task_sessions_tasks_%'"); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP TABLE task_rollout_migration"); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM pot_migrations WHERE id LIKE '0016_session_rollouts_tasks_%'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	j.db.Close()
	j, err = openJournal(t, path, "runtime", "newboot")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if restored.SchemaVersion != SchemaVersion || restored.RolloutRef == nil || restored.Phase != RecoveryRequired {
		t.Fatal(restored)
	}
	assertTaskContext(t, restored.Context, raw)
	var body string
	if err = j.db.QueryRow("SELECT body FROM task_runs").Scan(&body); err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err = json.Unmarshal([]byte(body), &record); err != nil {
		t.Fatal(err)
	}
	if _, ok := record["context"]; ok {
		t.Fatal("migration retained inline context")
	}
	var pending bool
	if err = j.db.QueryRow("SELECT reclaim_pending FROM task_rollout_migration WHERE singleton=1").Scan(&pending); err != nil || pending {
		t.Fatal("reclamation not completed", pending, err)
	}
	var free int
	if err = j.db.QueryRow("PRAGMA freelist_count").Scan(&free); err != nil || free != 0 {
		t.Fatal("migration left free pages", free, err)
	}
}

func TestTaskRolloutWaitRejectsUnsettledCheckpoint(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.Accept(ctx, launchFixture(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	state := json.RawMessage(`{"type":"context","version":2,"messages":[],"model_request_id":"pending-model"}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", state); err != nil {
		t.Fatal(err)
	}
	if err = j.Wait(ctx, "runtime", "task", "run", "allowance"); err == nil {
		t.Fatal("unsettled checkpoint was not loaded before allowance wait")
	}
}

func TestTaskRolloutReclamationRetriesButDoesNotRunEveryBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the durable migration receipt after import but before VACUUM.
	if _, err = j.db.Exec("UPDATE task_rollout_migration SET reclaim_pending=1"); err != nil {
		t.Fatal(err)
	}
	j.db.Close()
	j, err = openJournal(t, path, "runtime", "retryboot")
	if err != nil {
		t.Fatal(err)
	}
	var pending bool
	if err = j.db.QueryRow("SELECT reclaim_pending FROM task_rollout_migration").Scan(&pending); err != nil || pending {
		t.Fatal(pending, err)
	}
	// Free unrelated database pages after completed migration. Ordinary startup
	// must not run VACUUM merely because the database has reusable free pages.
	if _, err = j.db.Exec("INSERT INTO task_runs VALUES('dummy','dummy','dummy','dummy','complete','boot',?)", strings.Repeat("x", 256<<10)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.db.Exec("DELETE FROM task_runs WHERE control_id='dummy'"); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = j.db.QueryRow("PRAGMA freelist_count").Scan(&before); err != nil || before == 0 {
		t.Fatal(before, err)
	}
	j.db.Close()
	j, err = openJournal(t, path, "runtime", "ordinaryboot")
	if err != nil {
		t.Fatal(err)
	}
	var after int
	if err = j.db.QueryRow("PRAGMA freelist_count").Scan(&after); err != nil || after != before {
		t.Fatal("ordinary boot reclaimed unrelated pages", before, after, err)
	}
}

func TestTaskRolloutCompressionBatchesRotatePastIneligibleRuns(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 36; i++ {
		launch := launchFixture(t)
		launch.TaskID = fmt.Sprintf("task-%02d", i)
		if _, err = j.Accept(ctx, launch); err != nil {
			t.Fatal(err)
		}
		if _, err = j.Claim(ctx, "runtime", launch.TaskID, "run"); err != nil {
			t.Fatal(err)
		}
		if i >= 34 {
			if err = j.SaveContext(ctx, "runtime", launch.TaskID, "run", json.RawMessage(`{"type":"context","version":2,"messages":["keep"]}`)); err != nil {
				t.Fatal(err)
			}
		}
		if err = j.Finish(ctx, "runtime", launch.TaskID, "run", Terminal{Outcome: core.Success, Text: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	next, count, err := j.CompressRolloutBatch(ctx, time.Now().Add(time.Hour), "", 100, 0)
	if err != nil || next == "" || count != 0 {
		t.Fatal("first batch must stop after 32 ineligible runs", next, count, err)
	}
	next, count, err = j.CompressRolloutBatch(ctx, time.Now().Add(time.Hour), next, 1, 0)
	if err != nil || next == "" || count != 1 {
		t.Fatal("compression budget exceeded or later run starved", next, count, err)
	}
	next, count, err = j.CompressRolloutBatch(ctx, time.Now().Add(time.Hour), next, 1, 0)
	if err != nil || next == "" || count != 1 {
		t.Fatal("remaining eligible run starved", next, count, err)
	}
	saved := next
	if next, count, err = j.CompressRolloutBatch(ctx, time.Now(), saved, 0, 0); err != nil || next != saved || count != 0 {
		t.Fatal("zero budget performed work", next, count, err)
	}
	next, count, err = j.CompressRolloutBatch(ctx, time.Now().Add(time.Hour), next, 2, 0)
	if err != nil || next != "" || count != 0 {
		t.Fatal("end did not reset cursor", next, count, err)
	}
	if _, _, err = j.CompressRolloutBatch(ctx, time.Now(), "invalid", 32, 0); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}

func TestTaskRolloutFinishedCallbackOnlyAfterNewDurableTerminal(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	j.SetOnFinished(func() {
		calls.Add(1)
		// A query in the callback proves it does not run inside the single-pool tx.
		if _, err := j.Inspect(ctx, "runtime", "task", "run"); err != nil {
			t.Error(err)
		}
	})
	if _, err = j.Accept(ctx, launchFixture(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if err = j.Wait(ctx, "runtime", "task", "run", "allowance"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("waiting triggered finish callback")
	}
	if err = j.Resume(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	result := Terminal{Outcome: core.Success, Text: "done"}
	if err = j.Finish(ctx, "runtime", "task", "run", result); err != nil {
		t.Fatal(err)
	}
	if err = j.Finish(ctx, "runtime", "task", "run", result); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate finish triggered callback", calls.Load())
	}
	recovery := launchFixture(t)
	recovery.TaskID = "recover"
	if _, err = j.Accept(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	if err = j.Fence(ctx, "runtime", "recover", "run"); err != nil {
		t.Fatal(err)
	}
	if err = j.ReconcileRecovered(ctx, "runtime", "recover", "run", nil, true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("recovery terminal did not wake maintenance", calls.Load())
	}
	j.SetOnFinished(nil)
}

func TestTaskRolloutCompressionDoesNotHoldDatabaseDuringPreparation(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.Accept(ctx, launchFixture(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"context","version":2,"messages":["` + strings.Repeat("padding", 8192) + `"]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", raw); err != nil {
		t.Fatal(err)
	}
	if err = j.Finish(ctx, "runtime", "task", "run", Terminal{Outcome: core.Success}); err != nil {
		t.Fatal(err)
	}
	slow, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := j.CompressRolloutBatch(slow, time.Now().Add(time.Hour), "", 32, 1024)
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	probe, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	if _, err = j.Inspect(probe, "runtime", "task", "run"); err != nil {
		t.Fatal("compression blocked foreground inspect", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("compression ignored cancellation")
	}
}

func TestTaskSessionPreservesHistoryAcrossResetAndRejectsStaleWriter(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err = j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	first := json.RawMessage(`{"type":"context","version":2,"messages":["original"]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", first); err != nil {
		t.Fatal(err)
	}
	old, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.Finish(ctx, "runtime", "task", "run", Terminal{Outcome: core.Success, Text: "done"}); err != nil {
		t.Fatal(err)
	}
	launch.RunID = "second"
	launch.Generation++
	next, err := j.Accept(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	if next.RolloutRef != nil || len(next.Context) != 0 {
		t.Fatal("incompatible continuation inherited model state")
	}
	if n, c, e := j.CompressRolloutBatch(ctx, time.Now().Add(time.Hour), "", 2, 0); e != nil || c != 0 {
		t.Fatal("active continuation compressed", n, c, e)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "second"); err != nil {
		t.Fatal(err)
	}
	second := json.RawMessage(`{"type":"context","version":2,"messages":["reset"]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "second", second); err != nil {
		t.Fatal(err)
	}
	newer, err := j.Inspect(ctx, "runtime", "task", "second")
	if err != nil {
		t.Fatal(err)
	}
	if newer.RolloutRef.Sequence <= old.RolloutRef.Sequence {
		t.Fatal("reset did not append after old task head")
	}
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error { return j.persistRun(ctx, tx, old) })
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("old checkpoint accepted", err)
	}
	historical, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, historical.Context, first)
	current, err := j.Inspect(ctx, "runtime", "task", "second")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, current.Context, second)
	files, err := filepath.Glob(filepath.Join(j.rolloutDirectory, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal("task has multiple sessions", files, err)
	}
}

func TestTaskSessionRejectsHeadBehindHistoricalCheckpoint(t *testing.T) {
	ctx := context.Background()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.Accept(ctx, launchFixture(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if err = j.SaveContext(ctx, "runtime", "task", "run", json.RawMessage(`{"type":"context","version":2,"messages":["one"]}`)); err != nil {
		t.Fatal(err)
	}
	first, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.SaveContext(ctx, "runtime", "task", "run", json.RawMessage(`{"type":"context","version":2,"messages":["one","two"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.db.Exec("UPDATE task_sessions SET sequence=?,hash=?", first.RolloutRef.Sequence, first.RolloutRef.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenJournal(j.db, t.TempDir(), "runtime", "nextboot"); err == nil || !strings.Contains(err.Error(), "precedes") {
		t.Fatal("stale head accepted", err)
	}
}

func TestTaskSessionInlineUpgradeOrdersHistoricalRuns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	first, err := j.Accept(ctx, launchFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	first.SchemaVersion = 8
	first.Phase = Complete
	first.Result = &Terminal{Outcome: core.Success, Text: "done"}
	first.CleanupConfirmed = true
	first.FinishedAtMS = 1
	first.Context = json.RawMessage(`{"type":"context","version":2,"messages":["first"]}`)
	second := first
	second.Sequence = 2
	second.Launch.RunID = "second"
	second.LaunchDigest, err = second.Launch.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second.Context = json.RawMessage(`{"type":"context","version":2,"messages":["first","second"]}`)
	if err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		if _, e := tx.Exec("DELETE FROM task_runs"); e != nil {
			return e
		}
		if e := writeRun(ctx, tx, second); e != nil {
			return e
		}
		if e := writeRun(ctx, tx, first); e != nil {
			return e
		}
		if _, e := tx.Exec("DROP TABLE task_sessions"); e != nil {
			return e
		}
		if _, e := tx.Exec("DROP TABLE task_rollout_migration"); e != nil {
			return e
		}
		_, e := tx.Exec("DELETE FROM pot_migrations WHERE id LIKE '0016_session_rollouts_tasks_%' OR id LIKE '0017_task_sessions_tasks_%'")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	j.db.Close()
	j, err = openJournal(t, path, "runtime", "newboot")
	if err != nil {
		t.Fatal(err)
	}
	a, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	b, err := j.Inspect(ctx, "runtime", "task", "second")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, a.Context, first.Context)
	assertTaskContext(t, b.Context, second.Context)
	if a.RolloutRef.Sequence >= b.RolloutRef.Sequence {
		t.Fatal("legacy runs imported out of sequence")
	}
	head, err := readTaskHead(ctx, j.db, "runtime", "task")
	if err != nil || head != *b.RolloutRef {
		t.Fatal("head does not match latest imported checkpoint", err)
	}
}

func TestTaskSessionContinuationDuringCompressionPreventsPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	j, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err = j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"context","version":2,"messages":["` + strings.Repeat("payload", 20000) + `"]}`)
	if err = j.SaveContext(ctx, "runtime", "task", "run", raw); err != nil {
		t.Fatal(err)
	}
	if err = j.Finish(ctx, "runtime", "task", "run", Terminal{Outcome: core.Success}); err != nil {
		t.Fatal(err)
	}
	head, err := readTaskHead(ctx, j.db, "runtime", "task")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		count int
		err   error
	}
	done := make(chan result, 1)
	go func() {
		_, n, e := j.CompressRolloutBatch(ctx, time.Now().Add(time.Hour), "", 2, 128<<10)
		done <- result{n, e}
	}()
	// Temp creation follows the first paced replay. The second paced copy is
	// still in progress, so accepting the continuation races with preparation.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		files, e := filepath.Glob(filepath.Join(j.rolloutDirectory, ".rollout-compress-*"))
		if e != nil {
			t.Fatal(e)
		}
		if len(files) > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("compression completed before synchronization: %+v", r)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	launch.RunID = "second"
	launch.Generation++
	continued, err := j.Accept(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	if continued.RolloutRef != nil || len(continued.Context) != 0 {
		t.Fatal("reset continuation has checkpoint")
	}
	unchanged, err := readTaskHead(ctx, j.db, "runtime", "task")
	if err != nil || unchanged != head {
		t.Fatal("continuation changed head", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.count != 0 {
			t.Fatal("published after continuation", r)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = os.Stat(j.rolloutPath(continued) + ".zst"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("archive published", err)
	}
	restored, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	assertTaskContext(t, restored.Context, raw)
}
