package runner

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func launchFixture(t *testing.T) Launch {
	t.Helper()
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{}
	profile := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return Launch{ControlID: "control", AssistantID: "assistant", RuntimeID: "runtime", TaskID: "task", RunID: "run", Generation: 1, ExecutionTarget: core.RuntimeTarget("runtime"), Config: spec, Plugins: []string{}, Text: "do work"}
}
func TestImmutableLaunchDuplicateClaimsAndPendingCommandFence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runner.sqlite")
	a, err := Open(path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	launch := launchFixture(t)
	accepted, err := a.Accept(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.Accept(ctx, launch)
	if err != nil || accepted.LaunchDigest != again.LaunchDigest {
		t.Fatal(again, err)
	}
	changed := launch
	changed.Text = "changed"
	if _, err := b.Accept(ctx, changed); err == nil {
		t.Fatal("changed launch accepted")
	}
	var winners atomic.Int32
	var group sync.WaitGroup
	for _, j := range []*Journal{a, b} {
		group.Add(1)
		go func() {
			defer group.Done()
			won, err := j.Claim(ctx, "control", "task", "run")
			if err != nil {
				t.Error(err)
			}
			if won {
				winners.Add(1)
			}
		}()
	}
	group.Wait()
	if winners.Load() != 1 {
		t.Fatal(winners.Load())
	}
	command, claim, err := a.BeginCommand(ctx, "control", "task", "run", "call", json.RawMessage(`{"cmd":"effect"}`))
	if err != nil || !claim || command.Phase != "pending" {
		t.Fatal(command, claim, err)
	}
	_, claim, err = b.BeginCommand(ctx, "control", "task", "run", "call", json.RawMessage(`{"cmd":"effect"}`))
	if err != nil || claim {
		t.Fatal("pending command reclaimed", claim, err)
	}
	if _, _, err := b.BeginCommand(ctx, "control", "task", "run", "call", json.RawMessage(`{"cmd":"other"}`)); err == nil {
		t.Fatal("command conflict accepted")
	}
	terminal := Terminal{Outcome: core.Success, Text: "done"}
	if err := a.Finish(ctx, "control", "task", "run", terminal); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("pending effects finalized", err)
	}
	if err := a.CompleteCommand(ctx, "control", "task", "run", "call", json.RawMessage(`{"effect":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(ctx, "control", "task", "run", terminal); err != nil {
		t.Fatal(err)
	}
	if err := b.Finish(ctx, "control", "task", "run", terminal); err != nil {
		t.Fatal("duplicate final receipt", err)
	}
}
func TestRestartFencesOriginalRunAndNeverReclaims(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runner.sqlite")
	j, err := Open(path, "runtime", "boot-one")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err := j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(ctx, "control", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.BeginCommand(ctx, "control", "task", "run", "effect", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	j.Close()
	j, err = Open(path, "runtime", "boot-two")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	record, err := j.Inspect(ctx, "control", "task", "run")
	if err != nil || record.Phase != RecoveryRequired {
		t.Fatal(record, err)
	}
	if err := j.CheckPolicyCleanup(ctx); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("fresh boot acknowledged unconfirmed policy cleanup", err)
	}
	if claimed, err := j.Claim(ctx, "control", "task", "run"); err != nil || claimed {
		t.Fatal(claimed, err)
	}
	next := launch
	next.RunID = "replacement"
	if _, err := j.Accept(ctx, next); err == nil {
		t.Fatal("unsettled Task replaced")
	}
	if err := j.ReconcileRecovered(ctx, "control", "task", "run", nil, nil, false); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(err)
	}
	if err := j.ReconcileRecovered(ctx, "control", "task", "run", nil, nil, true); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("missing original command receipt ignored", err)
	}
	if err := j.CompleteCommand(ctx, "control", "task", "run", "effect", json.RawMessage(`{"known":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := j.ReconcileRecovered(ctx, "control", "task", "run", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := j.CheckPolicyCleanup(ctx); err != nil {
		t.Fatal("authoritative reconciliation did not clear policy fence", err)
	}
	record, _ = j.Inspect(ctx, "control", "task", "run")
	if record.Phase != Interrupted || !record.CleanupConfirmed {
		t.Fatal(record)
	}
}
func TestWorkerCannotSelectAnotherTarget(t *testing.T) {
	target := core.RuntimeTarget("machine")
	for _, raw := range []string{`{"execution_target":"host"}`, `{"execution_target":{"runtime_id":"other"}}`} {
		if _, err := BindTarget(json.RawMessage(raw), target); err == nil {
			t.Fatal("worker retargeted execution", raw)
		}
	}
	raw := json.RawMessage(`{"cmd":"pwd"}`)
	bound, err := BindTarget(raw, target)
	if err != nil || string(bound) != string(raw) {
		t.Fatal(string(bound), err)
	}
}

func TestRestartRetainsOriginalCleanupProofAndCommandInput(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runner.sqlite")
	j, err := Open(path, "runtime", "original-boot")
	if err != nil {
		t.Fatal(err)
	}
	l := launchFixture(t)
	if _, err = j.Accept(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"original":"immutable"}`)
	if _, _, err = j.BeginCommand(ctx, l.ControlID, l.TaskID, l.RunID, "call", input); err != nil {
		t.Fatal(err)
	}
	if err = j.CompleteCommand(ctx, l.ControlID, l.TaskID, l.RunID, "call", json.RawMessage(`{"known":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = j.RecordCleanup(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = Open(path, "runtime", "next-boot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	run, err := j.Inspect(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil || run.Phase != RecoveryRequired || !run.CleanupConfirmed {
		t.Fatal(run, err)
	}
	commands, err := j.Commands(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil || len(commands) != 1 || string(commands[0].Input) != string(input) || commands[0].Phase != "complete" {
		t.Fatal(commands, err)
	}
	if err = j.ReconcileRecovered(ctx, l.ControlID, l.TaskID, l.RunID, nil, nil, true); err != nil {
		t.Fatal(err)
	}
}
