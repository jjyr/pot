package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type feedback struct {
	settleErr error
	journal   *Journal
}

func (f feedback) LoadContext(ctx context.Context) (json.RawMessage, error) {
	if f.journal == nil {
		return nil, nil
	}
	r, e := f.journal.Inspect(ctx, "runtime", "task", "run")
	if e != nil {
		return nil, e
	}
	return r.Context, nil
}
func (f feedback) SaveContext(ctx context.Context, raw json.RawMessage) error {
	if f.journal == nil {
		return nil
	}
	return f.journal.SaveContext(ctx, "runtime", "task", "run", raw)
}
func (feedback) RecordTurn(context.Context, string) error                      { return nil }
func (feedback) InputStreaming(context.Context, string) error                  { return nil }
func (feedback) Ready(context.Context) error                                   { return nil }
func (feedback) Steering(context.Context, bool) ([]agent.SteeringInput, error) { return nil, nil }
func (feedback) AcknowledgeSteering(context.Context, []string) error           { return nil }
func (feedback) Observe(context.Context, agent.Observation) error              { return nil }
func (f feedback) Settle(context.Context) error                                { return f.settleErr }

type models struct {
	calls  atomic.Int32
	before func(core.ModelRequest) error
}

func (s *models) Model(_ context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	s.calls.Add(1)
	if s.before != nil {
		if err := s.before(request); err != nil {
			return nil, err
		}
	}
	return io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")), nil
}
func (*models) RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error) {
	return nil, nil, nil
}
func (*models) Compaction(context.Context, json.RawMessage) error { return nil }

func TestExecutorRejectsForeignCoordinatorAndRetiredRun(t *testing.T) {
	ctx := context.Background()
	j, err := OpenJournal(filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	manager, err := execution.New("runtime", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	prepared := false
	e := &Executor{Journal: j, Executions: manager, Context: ctx, Prepare: func(context.Context, Run) (Prepared, error) {
		prepared = true
		return Prepared{}, errors.New("foreign configuration reached local preparation")
	}}
	l := launchFixture(t)
	l.ControlID = "retired-coordinator"
	if _, err := e.Launch(ctx, l); err == nil {
		t.Fatal("accepted foreign coordinator")
	}
	if runs, err := j.Runs(ctx); err != nil || len(runs) != 0 {
		t.Fatal(runs, err)
	}
	// The journal can retain historical evidence, but execution cannot resume it.
	if _, err := j.Accept(ctx, l); err == nil {
		t.Fatal("journal admitted a new foreign launch")
	}
	hash, _ := l.Digest()
	original := Run{SchemaVersion: SchemaVersion, Launch: l, LaunchDigest: hash, BootID: "boot", Phase: Accepted, Sequence: 1}
	raw, _ := json.Marshal(original)
	if _, err := j.db.Exec("INSERT INTO task_runs VALUES(?,?,?,?,?,?,?)", l.ControlID, l.TaskID, l.RunID, hash, original.Phase, original.BootID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if retained, err := j.Accept(ctx, l); err != nil || retained.LaunchDigest != hash {
		t.Fatal("historical retry lost its receipt", retained, err)
	}
	if _, err := e.Execute(ctx, l.ControlID, l.TaskID, l.RunID); err == nil {
		t.Fatal("executed retired run")
	}
	if err := e.Resume(ctx, l.ControlID, l.TaskID, l.RunID); err == nil {
		t.Fatal("resumed retired run")
	}
	if err := e.Stop(ctx, l.ControlID, l.TaskID, l.RunID, "cancel"); err == nil {
		t.Fatal("changed retired run")
	}
	if prepared || len(manager.Snapshot()) != 0 {
		t.Fatal("foreign run acquired local execution services")
	}
	r, err := j.Inspect(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil || r.Phase != Accepted || r.StopRequested != "" {
		t.Fatal(r, err)
	}
}

func TestUnsettledEffectsRetainsSharedExecutionCapacity(t *testing.T) {
	ctx := context.Background()
	j, err := OpenJournal(filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	manager, err := execution.New("runtime", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err := j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	model := &models{}
	engine := &Executor{Journal: j, Executions: manager, Context: ctx, Prepare: func(_ context.Context, run Run) (Prepared, error) {
		a := &agent.Agent{Services: model, Feedback: feedback{journal: j, settleErr: core.ErrCleanupUnconfirmed}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
		return Prepared{Agent: a, Context: core.ExecutionContext{Owner: run.Launch.TaskID, Role: core.Worker, Workspace: t.TempDir()}, Thread: core.Thread{Owner: run.Launch.TaskID, Generation: run.Launch.Generation, Config: run.Launch.Config}, ResourceDigest: pinTestResources(t, j, run)}, nil
	}}
	result, err := engine.Execute(ctx, "runtime", "task", "run")
	if result != nil || !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(result, err)
	}
	stored, _ := j.Inspect(ctx, "runtime", "task", "run")
	if stored.Phase != RecoveryRequired {
		t.Fatal(stored)
	}
	if !errors.Is(manager.Drain(ctx), execution.ErrUnconfirmed) {
		t.Fatal("unknown receipt released shared execution capacity")
	}
}
func TestExecutorUsesSharedCapacityAndJournalFeedbackWithoutResampling(t *testing.T) {
	ctx := context.Background()
	j, err := OpenJournal(filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	manager, err := execution.New("runtime", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err := j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	model := &models{}
	engine := &Executor{Journal: j, Executions: manager, Context: ctx}
	engine.Prepare = func(_ context.Context, run Run) (Prepared, error) {
		active := manager.Snapshot()
		if len(active) != 1 || active[0].Phase != execution.Running {
			t.Fatal("Agent preparation bypassed shared capacity", active)
		}
		a := &agent.Agent{Services: model, Feedback: feedback{journal: j}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
		return Prepared{Agent: a, Context: core.ExecutionContext{Owner: run.Launch.TaskID, Role: core.Worker, Workspace: t.TempDir()}, Thread: core.Thread{Owner: run.Launch.TaskID, Generation: run.Launch.Generation, Config: run.Launch.Config}, ResourceDigest: pinTestResources(t, j, run)}, nil
	}
	result, err := engine.Execute(ctx, "runtime", "task", "run")
	if err != nil || result == nil || result.Outcome.Kind != "success" || result.Text != "done" {
		t.Fatal(result, err)
	}
	stored, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || stored.Phase != Complete || !stored.CleanupConfirmed || len(stored.Context) == 0 {
		t.Fatal(stored, err)
	}
	if err := manager.Drain(ctx); err != nil {
		t.Fatal("capacity leaked", err)
	}
	again, err := engine.Execute(ctx, "runtime", "task", "run")
	if err != nil || again == nil || model.calls.Load() != 1 {
		t.Fatal(again, err, model.calls.Load())
	}
}

func pinTestResources(t *testing.T, j *Journal, run Run) string {
	t.Helper()
	_, digest, err := j.PinResources(context.Background(), run.Launch.ControlID, run.Launch.TaskID, run.Launch.RunID, testResourceBody(run))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func testResourceBody(run Run) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"config": run.Launch.Config})
	return raw
}
