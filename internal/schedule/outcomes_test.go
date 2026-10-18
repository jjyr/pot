package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func scheduledTask(t *testing.T, f fixture, spec protocol.ScheduleSpec) (protocol.ScheduleInfo, tasks.Record) {
	t.Helper()
	ctx := context.Background()
	at := millis("2026-10-16T00:00:00Z")
	info := create(t, f, spec, at)
	if err := f.schedule.Tick(ctx, at+60000); err != nil {
		t.Fatal(err)
	}
	page, err := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
	if err != nil || len(page.Runs) != 1 || page.Runs[0].TaskID == nil {
		t.Fatal(page, err)
	}
	task, err := f.tasks.Get(ctx, "assistant", *page.Runs[0].TaskID)
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	return info, *task
}

func finishScheduled(t *testing.T, f fixture, task tasks.Record, phase runner.Phase) {
	t.Helper()
	ctx := context.Background()
	if err := f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for id, run := range f.destination.runs {
		if run.Launch.TaskID != task.Info.ID {
			continue
		}
		run.Phase, run.CleanupConfirmed = phase, true
		run.Result = &runner.Terminal{Outcome: core.Success, Text: "partial output"}
		if phase == runner.Failed {
			run.Result.Outcome = core.Outcome{Kind: "failed", Error: "actual execution failure"}
		}
		f.destination.runs[id] = run
	}
	if err := f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
}

func taskEnvelope(t *testing.T, f fixture, task string) (protocol.Envelope, string) {
	t.Helper()
	var body []byte
	var state string
	err := f.tasks.DB().QueryRow(`SELECT envelope,state FROM agent_messages WHERE json_extract(envelope,'$.result.task_id')=?`, task).Scan(&body, &state)
	if err != nil {
		t.Fatal(err)
	}
	var event protocol.Envelope
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	return event, state
}

func TestScheduledOutcomesUseSharedInboxAndPolicy(t *testing.T) {
	for _, policy := range []string{"changes", "all", "failures"} {
		for _, scenario := range []string{"first", "unchanged", "changed", "failed", "invalid", "interrupted", "cancelled"} {
			t.Run(policy+"/"+scenario, func(t *testing.T) {
				ctx := context.Background()
				f := setup(t)
				spec := defaultSpec()
				spec.Notify = policy
				info, task := scheduledTask(t, f, spec)
				if scenario == "unchanged" || scenario == "changed" {
					baseline := "facts-v1"
					if scenario == "changed" {
						baseline = "older-facts"
					}
					if _, err := f.tasks.DB().Exec(`UPDATE schedules SET body=json_set(body,'$.info.baseline',?) WHERE id=?`, baseline, info.ID); err != nil {
						t.Fatal(err)
					}
				}
				phase := protocol.TaskComplete
				switch scenario {
				case "failed":
					phase = protocol.TaskFailed
					finishScheduled(t, f, task, runner.Failed)
				case "invalid":
					phase = protocol.TaskFailed
					f.reader.body = json.RawMessage(`{"summary":"bad","fingerprint":"x","evidence":[]}`)
					finishScheduled(t, f, task, runner.Complete)
				case "interrupted", "cancelled":
					mode := "interrupt"
					phase = protocol.TaskInterrupted
					if scenario == "cancelled" {
						mode, phase = "cancel", protocol.TaskCancelled
					}
					if err := f.tasks.Stop(ctx, "assistant", task.Info.ID, mode); err != nil {
						t.Fatal(err)
					}
				default:
					f.reader.body = json.RawMessage(`{"summary":"Observed facts","fingerprint":"facts-v1","evidence":["https://example.com/source"],"artifacts":["report.txt"]}`)
					finishScheduled(t, f, task, runner.Complete)
				}
				event, state := taskEnvelope(t, f, task.Info.ID)
				notify := policy == "all" || (phase == protocol.TaskFailed || phase == protocol.TaskInterrupted) || policy == "changes" && scenario != "unchanged" && scenario != "cancelled"
				if (state == "queued") != notify || !notify && (state != "suppressed" || event.Result.SuppressionReason == "") {
					t.Fatal(state, event)
				}
				if event.Result.Phase != phase || event.Result.Origin.Kind != "schedule" || event.Result.Origin.ScheduleID != info.ID {
					t.Fatal(event)
				}
				stored, err := f.tasks.Get(ctx, "assistant", task.Info.ID)
				if err != nil || stored.Info.Phase != phase || stored.Outcome == nil || stored.Outcome.Text != event.Text {
					t.Fatal(stored, err)
				}
				page, err := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
				if err != nil || page.Runs[0].State != string(phase) || page.Runs[0].FinishedAtMS == nil {
					t.Fatal(page, err)
				}
				if scenario == "failed" && !strings.Contains(event.Text, "actual execution failure") {
					t.Fatal(event)
				}
				if scenario == "invalid" && !strings.Contains(event.Text, "Structured result missing or invalid") {
					t.Fatal(event)
				}
				if phase == protocol.TaskComplete && (len(event.Result.Evidence) != 1 || len(event.Result.Artifacts) != 1) {
					t.Fatal(event)
				}
				terminal, started := 0, 0
				for _, hook := range *f.events {
					if hook.Kind == hooks.TaskStarted {
						started++
					}
					if hook.Result != nil {
						terminal++
						if hook.Result.Phase != phase {
							t.Fatal(hook)
						}
					}
				}
				if terminal != 1 {
					t.Fatal("expected one final hook", *f.events)
				}
				wantStarted := 1
				if scenario == "interrupted" || scenario == "cancelled" {
					wantStarted = 0
				}
				if started != wantStarted {
					t.Fatal("incorrect scheduled start hooks", *f.events)
				}
				if err := f.tasks.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := f.tasks.DB().QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.result.task_id')=?`, task.Info.ID).Scan(&count); err != nil || count != 1 {
					t.Fatal(count, err)
				}
				var tables int
				if err := f.tasks.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='schedule_notifications'`).Scan(&tables); err != nil || tables != 0 {
					t.Fatal(tables, err)
				}
			})
		}
	}
}

func TestScheduledOutcomeAndBaselineRollbackWithHook(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	info, task := scheduledTask(t, f, defaultSpec())
	if err := f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	f.destination.complete(task.Info.ID)
	if _, err := f.tasks.DB().Exec(`CREATE TRIGGER reject_outcome BEFORE INSERT ON agent_messages WHEN json_extract(NEW.envelope,'$.kind')='task_result' BEGIN SELECT RAISE(ABORT,'reject outcome'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Tick(ctx); err == nil {
		t.Fatal("result insertion failure did not roll back")
	}
	stored, _ := f.tasks.Get(ctx, "assistant", task.Info.ID)
	page, _ := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
	if stored.Info.Phase.Scope() != "active" || stored.Outcome != nil || page.Selected.Baseline != nil || page.Runs[0].FinishedAtMS != nil {
		t.Fatal(stored, page)
	}
	for _, event := range *f.events {
		if event.Result != nil {
			t.Fatal("hook published before commit", event)
		}
	}
	if _, err := f.tasks.DB().Exec("DROP TRIGGER reject_outcome"); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	event, state := taskEnvelope(t, f, task.Info.ID)
	if event.Result.Phase != protocol.TaskComplete || state != "queued" {
		t.Fatal(event, state)
	}
}

func TestPendingScheduledResultRecoversWithoutPrematureHook(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	info, task := scheduledTask(t, f, defaultSpec())
	f.reader.err = tasks.ErrResultPending
	finishScheduled(t, f, task, runner.Complete)
	stored, _ := f.tasks.Get(ctx, "assistant", task.Info.ID)
	page, _ := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
	if stored.Info.Phase.Scope() != "active" || page.Runs[0].FinishedAtMS != nil {
		t.Fatal(stored, page)
	}
	for _, event := range *f.events {
		if event.Result != nil {
			t.Fatal("premature outcome", event)
		}
	}
	f.reader.err = nil
	if err := f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	event, _ := taskEnvelope(t, f, task.Info.ID)
	if event.Result.Phase != protocol.TaskComplete || f.destination.launches != 1 {
		t.Fatal(event, f.destination.launches)
	}
}

func TestMissingScheduledReportEmitsOnlyFailure(t *testing.T) {
	f := setup(t)
	_, task := scheduledTask(t, f, defaultSpec())
	f.reader.err = errors.New("report does not exist")
	finishScheduled(t, f, task, runner.Complete)
	for _, event := range *f.events {
		if event.Result != nil && event.Kind != hooks.TaskFailed {
			t.Fatal(event)
		}
	}
	event, _ := taskEnvelope(t, f, task.Info.ID)
	if !strings.Contains(event.Result.Error, "report does not exist") {
		t.Fatal(event)
	}
}

func TestScheduledPolicySnapshotAndPendingResultSurviveRestart(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	spec := defaultSpec()
	spec.Notify = "all"
	info, task := scheduledTask(t, f, spec)
	f.reader.err = tasks.ErrResultPending
	finishScheduled(t, f, task, runner.Complete)
	// Editing a definition changes its generation and policy, not this occurrence.
	spec.Notify, spec.Goal = "failures", "A new observation goal"
	if _, err := f.schedule.Manage(ctx, "assistant", Request{RequestID: "edit", Action: "update", ScheduleID: info.ID, Spec: &spec}, millis("2026-10-16T00:02:00Z")); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var database, path string
	if err := f.tasks.DB().QueryRow("PRAGMA database_list").Scan(&sequence, &database, &path); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Close(); err != nil {
		t.Fatal(err)
	}
	var schedules *Service
	restored, err := tasks.Open(path, tasks.Options{ControlID: "control", RuntimeID: "machine", Port: f.destination,
		ResolveTarget: func(context.Context, string) (core.ExecutionTarget, error) { return core.RuntimeTarget("machine"), nil },
		PrepareOutcome: func(ctx context.Context, task tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
			return schedules.PrepareOutcome(ctx, task, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	schedules, err = New(Options{Tasks: restored, Ready: func(context.Context, string) (bool, error) { return true, nil }, Results: f.reader})
	if err != nil {
		t.Fatal(err)
	}
	f.tasks, f.schedule = restored, schedules
	f.reader.err = nil
	if err := restored.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	event, state := taskEnvelope(t, f, task.Info.ID)
	if state != "queued" || event.Result.Phase != protocol.TaskComplete {
		t.Fatal("occurrence policy changed after edit/restart", state, event)
	}
	page, err := schedules.Page(ctx, "assistant", Query{Selected: info.ID})
	if err != nil || page.Selected.Baseline != nil || page.Runs[0].Notify != "all" || f.destination.launches != 1 {
		t.Fatal(page, err, f.destination.launches)
	}
}
