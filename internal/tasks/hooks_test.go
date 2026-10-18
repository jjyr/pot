package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTerminalHooksCoverAllOutcomesAndPreserveFailure(t *testing.T) {
	for _, scenario := range []struct {
		phase   runner.Phase
		kind    hooks.Kind
		outcome core.Outcome
		text    string
	}{
		{runner.Complete, hooks.TaskCompleted, core.Success, "partial output"},
		{runner.Failed, hooks.TaskFailed, core.Outcome{Kind: "failed", Error: "model transport failed"}, "partial output"},
		{runner.Failed, hooks.TaskFailed, core.Outcome{Kind: "failed", Error: "preparation failed before model start"}, ""},
		{runner.Interrupted, hooks.TaskInterrupted, core.Cancelled, ""},
		{runner.Cancelled, hooks.TaskCancelled, core.Cancelled, ""},
	} {
		t.Run(string(scenario.kind), func(t *testing.T) {
			ctx := context.Background()
			s, port, _, _ := fixture(t)
			record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "work", Text: "actual work"})
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
			run.Result = &runner.Terminal{Outcome: scenario.outcome, Text: scenario.text}
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

func TestPredispatchStopHookIsAtomicAndNotRepeatedByArchive(t *testing.T) {
	for _, mode := range []string{"cancel", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, _, _, _ := fixture(t)
			record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "work", Text: "queued work"})
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
