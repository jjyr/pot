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
	profile := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return Launch{ControlID: "control", AssistantID: "assistant", RuntimeID: "runtime", TaskID: "task", RunID: "run", Generation: 1, ExecutionTarget: core.HostTarget("runtime"), Config: spec, Plugins: []string{}, Text: "do work"}
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
	record, _ = j.Inspect(ctx, "control", "task", "run")
	if record.Phase != Interrupted || !record.CleanupConfirmed {
		t.Fatal(record)
	}
}
func TestTargetBindingRejectsMachineHostAndSandboxSubstitution(t *testing.T) {
	target := core.SandboxTarget("machine", "shared")
	for _, raw := range []string{`{"cmd":"x","execution_target":"host"}`, `{"cmd":"x","execution_target":{"runtime_id":"other","target":{"kind":"sandbox","sandbox_id":"shared"}}}`, `{"cmd":"x","sandbox_id":"shared"}`} {
		if _, err := BindTarget(json.RawMessage(raw), target); err == nil {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`{"cmd":"x"}`, `{"cmd":"x","execution_target":"sandbox:shared"}`} {
		data, err := BindTarget(json.RawMessage(raw), target)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]json.RawMessage
		_ = json.Unmarshal(data, &value)
		var restored core.ExecutionTarget
		if err := core.DecodeJSON(value["execution_target"], &restored); err != nil || restored != target {
			t.Fatal(restored, err)
		}
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
