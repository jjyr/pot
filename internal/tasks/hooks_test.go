package tasks

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTerminalHooksCoverAllOutcomesAndPreserveFailure(t *testing.T) {
	for _, scenario := range []struct {
		phase   Phase
		kind    hooks.Kind
		outcome core.Outcome
		text    string
	}{
		{Complete, hooks.TaskCompleted, core.Success, "partial output"},
		{Failed, hooks.TaskFailed, core.Outcome{Kind: "failed", Error: "model transport failed"}, "partial output"},
		{Failed, hooks.TaskFailed, core.Outcome{Kind: "failed", Error: "preparation failed before model start"}, ""},
		{Interrupted, hooks.TaskInterrupted, core.Cancelled, ""},
		{Cancelled, hooks.TaskCancelled, core.Cancelled, ""},
	} {
		t.Run(string(scenario.kind), func(t *testing.T) {
			ctx := context.Background()
			s, port, _ := fixture(t)
			record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "work", Text: "actual work"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			port.mu.Lock()
			run := port.runs[record.RunID]
			run.Phase = scenario.phase
			run.CleanupConfirmed = true
			run.Result = &Terminal{Outcome: scenario.outcome, Text: scenario.text}
			port.runs[record.RunID] = run
			port.mu.Unlock()
			var delivered []hooks.Event
			s.options.PublishHook = func(event hooks.Event) {
				stored, err := s.Get(ctx, "assistant", record.Info.ID)
				if err != nil || stored.Info.Phase.Scope() == "active" {
					t.Error("hook preceded committed terminal state", err)
				}
				delivered = append(delivered, event)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if len(delivered) != 1 || delivered[0].Kind != scenario.kind {
				t.Fatal(delivered)
			}
			notices, err := s.Notifications(ctx, "assistant")
			if err != nil || len(notices) != 1 || notices[0].Text == "" || !strings.Contains(notices[0].Text, scenario.text) {
				t.Fatal(notices, err)
			}
			if scenario.outcome.Error != "" && !strings.Contains(notices[0].Text, scenario.outcome.Error) {
				t.Fatal("failure diagnostic lost", notices)
			}
			if err = s.AcknowledgeNotification(ctx, "assistant", notices[0].ID); err != nil {
				t.Fatal(err)
			}
			if err = s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			notices, err = s.Notifications(ctx, "assistant")
			if err != nil || len(notices) != 0 || len(delivered) != 1 {
				t.Fatal("terminal hook replayed", notices, delivered, err)
			}
		})
	}
}

func TestStartedHookRequiresRunnerStartAndIsUIOnlyAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s, port, path := fixture(t)
	var events []hooks.Event
	s.options.PublishHook = func(event hooks.Event) { events = append(events, event) }
	port.lose = true
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "start", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("launch intent emitted start", events)
	}
	port.mu.Lock()
	run := port.runs[record.RunID]
	run.Phase, run.StartedAtMS = Accepted, 0
	port.runs[record.RunID] = run
	port.mu.Unlock()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("queued receipt emitted start", events)
	}
	port.mu.Lock()
	run.Phase, run.StartedAtMS = Running, 1000
	port.runs[record.RunID] = run
	port.mu.Unlock()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != hooks.TaskStarted || events[0].Result != nil || len(events[0].Topics()) != 1 || events[0].Topics()[0] != "tasks" {
		t.Fatal(events)
	}
	if notices, err := s.Notifications(ctx, "assistant"); err != nil || len(notices) != 0 {
		t.Fatal(notices, err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := openTasks(t, path, Options{NodeID: "machine", Executor: port, PublishHook: func(event hooks.Event) { events = append(events, event) }})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	if err := restored.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatal("start replayed after restart", events)
	}
}

func TestPreparedOutcomeCannotCommitAfterConcurrentTaskChange(t *testing.T) {
	ctx := context.Background()
	s, port, _ := fixture(t)
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "stale", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	port.mu.Lock()
	run := port.runs[record.RunID]
	run.Phase, run.CleanupConfirmed = Complete, true
	run.Result = &Terminal{Outcome: core.Success, Text: "done"}
	port.runs[record.RunID] = run
	port.mu.Unlock()
	commits := 0
	s.options.PrepareOutcome = func(ctx context.Context, snapshot Record, event protocol.TaskResultEvent) (OutcomeCommit, error) {
		if err := s.SendMessage(ctx, "assistant", record.Info.ID, "reference", protocol.AgentRef{NodeID: "machine", Kind: "task", AgentID: "peer"}, "late reference"); err != nil {
			return nil, err
		}
		return func(context.Context, *sql.Tx, *protocol.TaskResultEvent) error { commits++; return nil }, nil
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.Get(ctx, "assistant", record.Info.ID)
	if stored.Info.Phase.Scope() != "active" || commits != 0 {
		t.Fatal(stored, commits)
	}
	s.options.PrepareOutcome = nil
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Get(ctx, "assistant", record.Info.ID)
	if stored.Info.Phase != protocol.TaskComplete {
		t.Fatal(stored)
	}
}

func TestTaskStoreRejectsPreviousSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := openTasks(t, path, Options{NodeID: "machine"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("DROP TABLE pot_migrations; CREATE TABLE task_schema(version INTEGER NOT NULL); INSERT INTO task_schema VALUES(6)"); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	_, err = openTasks(t, path, Options{NodeID: "machine"})
	if err == nil || !strings.Contains(err.Error(), "unsupported pre-release or untracked schema") {
		t.Fatal(err)
	}
}

func TestOutcomePreparationFailureLeavesTaskActive(t *testing.T) {
	ctx := context.Background()
	s, _, _ := fixture(t)
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "blocked", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("outcome preparation failed")
	s.options.PrepareOutcome = func(context.Context, Record, protocol.TaskResultEvent) (OutcomeCommit, error) { return nil, want }
	if err := s.Stop(ctx, "assistant", record.Info.ID, "cancel"); !errors.Is(err, want) {
		t.Fatal(err)
	}
	stored, _ := s.Get(ctx, "assistant", record.Info.ID)
	if stored.Info.Phase != protocol.TaskQueued || stored.Outcome != nil {
		t.Fatal(stored)
	}
}

func TestOutcomeProcessorCannotRebindTaskOrReopenExecution(t *testing.T) {
	for _, field := range []string{"task", "run", "origin", "phase"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			s, _, _ := fixture(t)
			record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "binding", Text: "work"})
			if err != nil {
				t.Fatal(err)
			}
			s.options.PrepareOutcome = func(context.Context, Record, protocol.TaskResultEvent) (OutcomeCommit, error) {
				return func(_ context.Context, _ *sql.Tx, event *protocol.TaskResultEvent) error {
					switch field {
					case "task":
						event.TaskID = "another-task"
					case "run":
						event.RunID = ID()
					case "origin":
						event.Origin = protocol.TaskOrigin{Kind: "schedule", ScheduleID: "schedule", OccurrenceID: "run"}
					case "phase":
						event.Phase = protocol.TaskRunning
					}
					return nil
				}, nil
			}
			if err := s.Stop(ctx, "assistant", record.Info.ID, "cancel"); err == nil {
				t.Fatal("invalid prepared outcome committed")
			}
			stored, _ := s.Get(ctx, "assistant", record.Info.ID)
			if stored.Info.Phase != protocol.TaskQueued || stored.Outcome != nil {
				t.Fatal(stored)
			}
		})
	}
}

func TestPredispatchStopHookIsAtomicAndNotRepeatedByArchive(t *testing.T) {
	for _, mode := range []string{"cancel", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, _, _ := fixture(t)
			record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "work", Text: "queued work"})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			s.options.PublishHook = func(hooks.Event) { count++ }
			_, err = s.DB().Exec(`CREATE TRIGGER reject_hook BEFORE INSERT ON agent_messages WHEN json_extract(NEW.envelope,'$.kind')='task_result' BEGIN SELECT RAISE(ABORT,'hook rejected'); END`)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Stop(ctx, "assistant", record.Info.ID, mode); err == nil {
				t.Fatal("failed hook insert committed task state")
			}
			stored, err := s.Get(ctx, "assistant", record.Info.ID)
			if err != nil || stored.Info.Phase != protocol.TaskQueued || count != 0 {
				t.Fatal(stored, count, err)
			}
			if _, err = s.DB().Exec("DROP TRIGGER reject_hook"); err != nil {
				t.Fatal(err)
			}
			if err = s.Stop(ctx, "assistant", record.Info.ID, mode); err != nil {
				t.Fatal(err)
			}
			notices, err := s.Notifications(ctx, "assistant")
			if err != nil || len(notices) != 1 || count != 1 || !strings.Contains(notices[0].Text, "before dispatch") {
				t.Fatal(notices, count, err)
			}
			if err = s.Stop(ctx, "assistant", record.Info.ID, "cancel"); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatal("archiving an ended run emitted another outcome")
			}
		})
	}
}
