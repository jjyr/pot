package tasks

import (
	"context"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakePort struct {
	mu       sync.Mutex
	runs     map[string]runner.Run
	launches int
	stops    int
	lose     bool
}

func (p *fakePort) Launch(_ context.Context, l runner.Launch) (runner.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.launches++
	hash, _ := l.Digest()
	run := runner.Run{SchemaVersion: runner.SchemaVersion, Launch: l, LaunchDigest: hash, BootID: "boot", Phase: runner.Running}
	p.runs[l.RunID] = run
	if p.lose {
		return runner.Run{}, errors.New("ack lost")
	}
	return run, nil
}
func (p *fakePort) Inspect(_ context.Context, control, task, run string) (*runner.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.runs[run]
	if !ok {
		return nil, nil
	}
	if r.Launch.ControlID != control || r.Launch.TaskID != task {
		return nil, fmt.Errorf("bad scope")
	}
	return &r, nil
}
func (p *fakePort) Stop(_ context.Context, control, task, run, mode string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	r := p.runs[run]
	r.Phase = runner.Cancelled
	r.CleanupConfirmed = true
	r.Result = &runner.Terminal{Outcome: core.Cancelled}
	p.runs[run] = r
	return nil
}
func selection(t *testing.T) Selection {
	t.Helper()
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{}
	p := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	config, err := p.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return Selection{Config: config, Plugins: []string{}}
}
func fixture(t *testing.T) (*Service, *fakePort, *core.ExecutionTarget, string) {
	t.Helper()
	p := &fakePort{runs: map[string]runner.Run{}}
	target := core.RuntimeTarget("machine")
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	selected := selection(t)
	s, err := Open(path, Options{ControlID: "control", RuntimeID: "machine", Port: p, ResolveModel: func(context.Context, string, protocol.CreateTask) (Selection, error) { return selected, nil }, ResolveTarget: func(context.Context, string) (core.ExecutionTarget, error) { return target, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p, &target, path
}
func TestCooperativeSharedResourcesHoldOriginalMachineLease(t *testing.T) {
	ctx := context.Background()
	s, p, _, _ := fixture(t)
	first, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "first", Text: "one", Resource: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "second", Text: "two", Resource: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	blocked, _ := s.Get(ctx, "assistant", second.Info.ID)
	if p.launches != 1 || blocked.Launch != nil {
		t.Fatal("shared task did not retain exclusive cooperative lease")
	}
	if _, err = s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "ordinary", Text: "ordinary"}); err != nil {
		t.Fatal(err)
	}
	s.Tick(ctx)
	if p.launches != 2 {
		t.Fatal("cooperative lease blocked unrelated task")
	}
	if err = s.Stop(ctx, "assistant", first.Info.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	s.Tick(ctx)
	s.Tick(ctx)
	if p.launches != 3 {
		t.Fatal("settled shared task did not release cooperative lease")
	}
}
func TestLostLaunchAckInspectsOriginalAndPinsCurrentDispatchPolicy(t *testing.T) {
	ctx := context.Background()
	s, p, target, path := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	*target = core.RuntimeTarget("machine")
	p.lose = true
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, "assistant", record.Info.ID)
	if p.launches != 1 || !current.Info.RemoteWaiting || current.Info.ExecutionTarget.RuntimeID != "machine" {
		t.Fatal(current, p.launches)
	}
	*target = core.RuntimeTarget("other")
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Get(ctx, "assistant", record.Info.ID)
	if p.launches != 1 || current.Info.ExecutionTarget.RuntimeID != "machine" || current.Info.Phase != protocol.TaskRunning {
		t.Fatal(current, p.launches)
	}
	options := s.options
	s.Close()
	restored, err := Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if p.launches != 1 {
		t.Fatal("restart relaunched", p.launches)
	}
}
func TestIdempotencyOwnershipGuidanceReferencesAndStop(t *testing.T) {
	ctx := context.Background()
	s, p, _, _ := fixture(t)
	request := protocol.CreateTask{RequestID: "create", Text: "work"}
	record, err := s.Create(ctx, "assistant", request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Create(ctx, "assistant", request)
	if err != nil || again.Info.ID != record.Info.ID {
		t.Fatal(again, err)
	}
	request.Text = "changed"
	if _, err := s.Create(ctx, "assistant", request); err == nil {
		t.Fatal("changed identity accepted")
	}
	if other, err := s.Get(ctx, "other", record.Info.ID); err != nil || other != nil {
		t.Fatal("owner leaked", other, err)
	}
	if err := s.Guide(ctx, "assistant", record.Info.ID, "guidance", "new work"); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	steering, err := s.Steering(ctx, "assistant", record.Info.ID, record.RunID, false)
	if err != nil || len(steering) != 1 {
		t.Fatal(steering, err)
	}
	if err := s.AcknowledgeInputs(ctx, "assistant", record.Info.ID, record.RunID, []string{"guidance"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "assistant", record.Info.ID, "interrupt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Guide(ctx, "assistant", record.Info.ID, "late", "unsafe"); err == nil {
		t.Fatal("guidance during stop accepted")
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, "assistant", record.Info.ID)
	if current.Info.Phase != protocol.TaskInterrupted || p.stops != 1 {
		t.Fatal(current, p.stops)
	}
	if err := s.SendReference(ctx, "assistant", record.Info.ID, "reference", "trusted-sender", "data only"); err != nil {
		t.Fatal(err)
	}
	inputs, err := s.Inputs(ctx, "assistant", record.Info.ID)
	if err != nil || inputs[len(inputs)-1].State != "retained" {
		t.Fatal(inputs, err)
	}
	if err := s.Stop(ctx, "assistant", record.Info.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Get(ctx, "assistant", record.Info.ID)
	if current.Info.Phase != protocol.TaskCancelled {
		t.Fatal(current.Info.Phase)
	}
}
func TestTaskWaitCursorSuppressesUnchangedAndWakesOnCompletion(t *testing.T) {
	ctx := context.Background()
	s, p, _, _ := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, "assistant", record.Info.ID)
	done := make(chan []WaitItem, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		items, err := s.Wait(ctx, "assistant", []WaitTarget{{record.Info.ID, current.Revision}})
		if err != nil {
			t.Error(err)
		}
		done <- items
	}()
	p.mu.Lock()
	run := p.runs[record.RunID]
	run.Phase = runner.Complete
	run.Result = &runner.Terminal{Outcome: core.Success, Text: "done"}
	run.CleanupConfirmed = true
	p.runs[record.RunID] = run
	p.mu.Unlock()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case items := <-done:
		if len(items) != 1 || items[0].Info.Phase != protocol.TaskComplete {
			t.Fatal(items)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		again, err := s.Wait(ctx, "assistant", []WaitTarget{{record.Info.ID, items[0].Cursor}})
		if err != nil || len(again) != 1 || again[0].Cursor != items[0].Cursor {
			t.Fatal(again, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not wake")
	}
}
func TestCreateRollbackDoesNotLeaveReceiptOrInput(t *testing.T) {
	s, _, _, _ := fixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_tasks BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'disk fixture');END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background(), "assistant", protocol.CreateTask{RequestID: "create", Text: "work"}); err == nil {
		t.Fatal("failure hidden")
	}
	for _, table := range []string{"tasks", "task_writes", "task_inputs"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}
