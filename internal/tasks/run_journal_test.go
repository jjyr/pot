package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutionStartAndCleanupProofSurviveResumeAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	launch := launchFixture(t)
	accepted, err := j.Accept(ctx, launch)
	if err != nil || accepted.StartedAtMS != 0 {
		t.Fatal("acceptance must not start the clock", accepted, err)
	}
	before := time.Now().UnixMilli()
	if claimed, err := j.Claim(ctx, "runtime", "task", "run"); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	first, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || first.StartedAtMS < before || first.StartedAtMS > time.Now().UnixMilli() {
		t.Fatal("claim must persist its actual start", first, err)
	}
	// Use a distinct historical value so this detects resets without sleeps.
	started := first.StartedAtMS - 60000
	if err := j.change(ctx, "runtime", "task", "run", func(run *Run) error { run.StartedAtMS = started; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := j.Wait(ctx, "runtime", "task", "run", "allowance"); err != nil {
		t.Fatal(err)
	}
	if err := j.Resume(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := j.Claim(ctx, "runtime", "task", "run"); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	duplicate, err := j.Accept(ctx, launch)
	if err != nil || duplicate.StartedAtMS != started || duplicate.FinishedAtMS != 0 {
		t.Fatal("resume/duplicate acceptance reset start", duplicate, err)
	}
	if err := j.RecordCleanup(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if err := j.db.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = openJournal(t, path, "runtime", "nextboot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	restored, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || restored.StartedAtMS != started || restored.Phase != RecoveryRequired || !restored.CleanupConfirmed {
		t.Fatal("restart lost execution start or cleanup proof", restored, err)
	}
	if err := j.ReconcileRecovered(ctx, "runtime", "task", "run", nil, true); err != nil {
		t.Fatal(err)
	}
}

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
	return Launch{ControlID: "runtime", AssistantID: "assistant", RuntimeID: "runtime", TaskID: "task", RunID: "run", Generation: 1, ExecutionTarget: core.RuntimeTarget("runtime"), Config: spec, Plugins: []string{}, Text: "do work"}
}
func TestImmutableLaunchDuplicateClaimsAndTerminalReceipt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	a, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	b, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer b.db.Close()
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
			won, err := j.Claim(ctx, "runtime", "task", "run")
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
	terminal := Terminal{Outcome: core.Success, Text: "done"}
	if err := a.Finish(ctx, "runtime", "task", "run", terminal); err != nil {
		t.Fatal(err)
	}
	if err := b.Finish(ctx, "runtime", "task", "run", terminal); err != nil {
		t.Fatal("duplicate final receipt", err)
	}
}
func TestRestartFencesOriginalRunAndNeverReclaims(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot-one")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err := j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}

	j.db.Close()
	j, err = openJournal(t, path, "runtime", "boot-two")
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	record, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || record.Phase != RecoveryRequired {
		t.Fatal(record, err)
	}
	if err := j.CheckPolicyCleanup(ctx); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal("fresh boot acknowledged unconfirmed policy cleanup", err)
	}
	if claimed, err := j.Claim(ctx, "runtime", "task", "run"); err != nil || claimed {
		t.Fatal(claimed, err)
	}
	next := launch
	next.RunID = "replacement"
	if _, err := j.Accept(ctx, next); err == nil {
		t.Fatal("unsettled Task replaced")
	}
	if err := j.ReconcileRecovered(ctx, "runtime", "task", "run", nil, false); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(err)
	}
	if err := j.ReconcileRecovered(ctx, "runtime", "task", "run", nil, true); err != nil {
		t.Fatal(err)
	}
	if err := j.CheckPolicyCleanup(ctx); err != nil {
		t.Fatal("authoritative reconciliation did not clear policy fence", err)
	}
	record, _ = j.Inspect(ctx, "runtime", "task", "run")
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

func TestTerminalTimeSurvivesDuplicateFinishAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err := j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	result := Terminal{Outcome: core.Success, Text: "done"}
	before := time.Now().UnixMilli()
	if err := j.Finish(ctx, "runtime", "task", "run", result); err != nil {
		t.Fatal(err)
	}
	first, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || first.FinishedAtMS < before || first.FinishedAtMS > time.Now().UnixMilli() {
		t.Fatal(first, err)
	}
	if err := j.Finish(ctx, "runtime", "task", "run", result); err != nil {
		t.Fatal(err)
	}
	j.db.Close()
	j, err = openJournal(t, path, "runtime", "nextboot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.db.Close()
	final, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || final.FinishedAtMS != first.FinishedAtMS {
		t.Fatal(final, first, err)
	}
}
