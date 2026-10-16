package runner

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

type feedback struct{}

func (feedback) LoadContext(context.Context) (json.RawMessage, error)          { return nil, nil }
func (feedback) SaveContext(context.Context, json.RawMessage) error            { return nil }
func (feedback) RecordTurn(context.Context, string) error                      { return nil }
func (feedback) InputStreaming(context.Context, string) error                  { return nil }
func (feedback) Ready(context.Context) error                                   { return nil }
func (feedback) Steering(context.Context, bool) ([]agent.SteeringInput, error) { return nil, nil }
func (feedback) AcknowledgeSteering(context.Context, []string) error           { return nil }
func (feedback) Observe(context.Context, agent.Observation) error              { return nil }
func (feedback) Settle(context.Context) error                                  { return nil }

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
func TestUnknownCommandReceiptRetainsSharedExecutionCapacity(t *testing.T) {
	ctx := context.Background()
	j, err := Open(filepath.Join(t.TempDir(), "runner.sqlite"), "runtime", "boot")
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
	model := &models{before: func(core.ModelRequest) error {
		_, _, err := j.BeginCommand(ctx, "control", "task", "run", "unconfirmed", json.RawMessage(`{"effect":"uncertain"}`))
		return err
	}}
	engine := &Executor{Journal: j, Executions: manager, Context: ctx, Prepare: func(_ context.Context, run Run) (Prepared, error) {
		a := &agent.Agent{Services: model, Feedback: feedback{}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
		return Prepared{Agent: a, Context: core.ExecutionContext{Owner: run.Launch.TaskID, Role: core.Worker, Workspace: t.TempDir()}, Thread: core.Thread{Owner: run.Launch.TaskID, Generation: run.Launch.Generation, Config: run.Launch.Config}, ResourceDigest: pinTestResources(t, j, run)}, nil
	}}
	result, err := engine.Execute(ctx, "control", "task", "run")
	if result != nil || !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(result, err)
	}
	stored, _ := j.Inspect(ctx, "control", "task", "run")
	if stored.Phase != RecoveryRequired {
		t.Fatal(stored)
	}
	if !errors.Is(manager.Drain(ctx), execution.ErrUnconfirmed) {
		t.Fatal("unknown receipt released shared execution capacity")
	}
}
func TestExecutorUsesSharedCapacityAndJournalFeedbackWithoutResampling(t *testing.T) {
	ctx := context.Background()
	j, err := Open(filepath.Join(t.TempDir(), "runner.sqlite"), "runtime", "boot")
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
		a := &agent.Agent{Services: model, Feedback: feedback{}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
		return Prepared{Agent: a, Context: core.ExecutionContext{Owner: run.Launch.TaskID, Role: core.Worker, Workspace: t.TempDir()}, Thread: core.Thread{Owner: run.Launch.TaskID, Generation: run.Launch.Generation, Config: run.Launch.Config}, ResourceDigest: pinTestResources(t, j, run)}, nil
	}
	result, err := engine.Execute(ctx, "control", "task", "run")
	if err != nil || result == nil || result.Outcome.Kind != "success" || result.Text != "done" {
		t.Fatal(result, err)
	}
	stored, err := j.Inspect(ctx, "control", "task", "run")
	if err != nil || stored.Phase != Complete || !stored.CleanupConfirmed || len(stored.Context) == 0 {
		t.Fatal(stored, err)
	}
	if err := manager.Drain(ctx); err != nil {
		t.Fatal("capacity leaked", err)
	}
	again, err := engine.Execute(ctx, "control", "task", "run")
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
