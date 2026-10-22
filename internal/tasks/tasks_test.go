package tasks

import (
	"context"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakePort struct {
	mu          sync.Mutex
	runs        map[string]Run
	launches    int
	inspections int
	stops       int
	lose        bool
}

func (p *fakePort) Launch(_ context.Context, l Launch) (Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.launches++
	hash, _ := l.Digest()
	run := Run{SchemaVersion: SchemaVersion, Launch: l, LaunchDigest: hash, BootID: "boot", Phase: Running, StartedAtMS: time.Now().UnixMilli()}
	p.runs[l.RunID] = run
	if p.lose {
		return Run{}, errors.New("ack lost")
	}
	return run, nil
}
func (p *fakePort) Inspect(_ context.Context, control, task, run string) (*Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inspections++
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
	r.Phase = Cancelled
	r.CleanupConfirmed = true
	r.Result = &Terminal{Outcome: core.Cancelled}
	p.runs[run] = r
	return nil
}
func (p *fakePort) Resume(_ context.Context, node, task, run string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.runs[run]
	if !ok || r.Launch.ControlID != node || r.Launch.TaskID != task {
		return fmt.Errorf("original run unavailable")
	}
	r.Phase = Accepted
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
func fixture(t *testing.T) (*Service, *fakePort, string) {
	t.Helper()
	p := &fakePort{runs: map[string]Run{}}
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	selected := selection(t)
	s, err := Open(path, Options{NodeID: "machine", Executor: p, ResolveModel: func(context.Context, string, protocol.CreateTask) (Selection, error) { return selected, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p, path
}
func TestOriginalStartTimeFromDelayedReceiptsSurvivesRestart(t *testing.T) {
	for _, phase := range []Phase{Running, Waiting, Complete} {
		t.Run(string(phase), func(t *testing.T) {
			ctx := context.Background()
			s, p, path := fixture(t)
			record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "clock", Text: "work"})
			if err != nil || record.Info.StartedAtMS != 0 {
				t.Fatal("queued Task must not start the clock", record, err)
			}
			p.lose = true
			if err := s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			current, err := s.Get(ctx, "assistant", record.Info.ID)
			if err != nil || current.Info.StartedAtMS != 0 {
				t.Fatal("unknown launch receipt invented a start", current, err)
			}
			run := p.runs[record.RunID]
			run.StartedAtMS = record.Info.CreatedAtMS + 60000
			run.Phase = phase
			if phase == Complete {
				run.Result = &Terminal{Outcome: core.Success, Text: "done"}
				run.CleanupConfirmed = true
			}
			p.runs[record.RunID] = run
			if err := s.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			current, err = s.Get(ctx, "assistant", record.Info.ID)
			if err != nil || current.Info.StartedAtMS != run.StartedAtMS {
				t.Fatal("delayed receipt lost execution start", current, err)
			}
			options := s.options
			s.Close()
			restored, err := Open(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			current, err = restored.Get(ctx, "assistant", record.Info.ID)
			if err != nil || current.Info.StartedAtMS != run.StartedAtMS {
				t.Fatal("Control restart lost execution start", current, err)
			}
		})
	}
}

func TestTasksOnSameMachineLaunchIndependently(t *testing.T) {
	ctx := context.Background()
	s, p, _ := fixture(t)
	for _, requestID := range []string{"first", "second"} {
		if _, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: requestID, Text: "work"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if p.launches != 2 {
		t.Fatal("independent tasks on the same machine did not both launch", p.launches)
	}
}
func TestUnconfirmedLocalLaunchInspectsOriginalAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s, p, path := fixture(t)
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	p.lose = true
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, "assistant", record.Info.ID)
	if p.launches != 1 || !current.Info.RemoteWaiting || current.Info.ExecutionTarget.RuntimeID != "machine" {
		t.Fatal(current, p.launches)
	}
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
	s, p, _ := fixture(t)
	request := protocol.CreateTask{RequestID: "create", Text: "work"}
	record, err := s.AcceptLocal(ctx, "assistant", request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AcceptLocal(ctx, "assistant", request)
	if err != nil || again.Info.ID != record.Info.ID {
		t.Fatal(again, err)
	}
	request.Text = "changed"
	if _, err := s.AcceptLocal(ctx, "assistant", request); err == nil {
		t.Fatal("changed identity accepted")
	}
	if other, err := s.Get(ctx, "other", record.Info.ID); err != nil || other != nil {
		t.Fatal("owner leaked", other, err)
	}
	if err := s.SendMessage(ctx, "assistant", record.Info.ID, "guidance", protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "assistant"}, "new work"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendMessage(ctx, "assistant", record.Info.ID, "operator-guidance", protocol.AgentRef{NodeID: s.NodeID(), Kind: "user", AgentID: "assistant"}, "operator direction"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendMessage(ctx, "assistant", record.Info.ID, "operator-guidance", protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "assistant"}, "operator direction"); err == nil {
		t.Fatal("assistant retry changed operator provenance")
	}
	inputsBefore, err := s.Inputs(ctx, "assistant", record.Info.ID)
	if err != nil || len(inputsBefore) != 3 || inputsBefore[1].Kind != "guidance" || inputsBefore[2].Kind != "user_guidance" {
		t.Fatal(inputsBefore, err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	steering, err := s.Steering(ctx, "assistant", record.Info.ID, record.RunID, false)
	if err != nil || len(steering) != 2 {
		t.Fatal(steering, err)
	}
	if err := s.AcknowledgeInputs(ctx, "assistant", record.Info.ID, record.RunID, []string{"guidance", "operator-guidance"}); err != nil {
		t.Fatal(err)
	}
	afterGuidance, err := s.Get(ctx, "assistant", record.Info.ID)
	if err != nil || afterGuidance.Info.Pending != 0 {
		t.Fatal("operator guidance did not settle pending count", afterGuidance, err)
	}
	if err := s.Stop(ctx, "assistant", record.Info.ID, "interrupt"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendMessage(ctx, "assistant", record.Info.ID, "late", protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "assistant"}, "unsafe"); err == nil {
		t.Fatal("guidance during stop accepted")
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(ctx, "assistant", record.Info.ID)
	if current.Info.Phase != protocol.TaskInterrupted || p.stops != 1 {
		t.Fatal(current, p.stops)
	}
	if err := s.SendMessage(ctx, "assistant", record.Info.ID, "reference", protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "trusted-sender"}, "data only"); err != nil {
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
	s, p, _ := fixture(t)
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
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
	run.Phase = Complete
	run.Result = &Terminal{Outcome: core.Success, Text: "done"}
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

func TestTaskApprovalStateWakesWaitAndResumesOriginalRun(t *testing.T) {
	ctx := context.Background()
	s, p, _ := fixture(t)
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "approval", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	pending := false
	s.options.AwaitingApproval = func(owner, task, run string, generation uint64) bool {
		if owner != "assistant" || task != record.Info.ID || run != record.RunID || generation != record.Generation {
			t.Fatal("approval consulted another run")
		}
		return pending
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	running, _ := s.Get(ctx, "assistant", record.Info.ID)
	pending = true
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	items, err := s.Wait(wait, "assistant", []WaitTarget{{record.Info.ID, running.Revision}})
	if err != nil || wait.Err() != nil || len(items) != 1 || items[0].Info.Phase != protocol.TaskWaitingApproval {
		t.Fatalf("approval did not wake wait: %+v %v", items, err)
	}
	phase := protocol.TaskWaitingApproval
	page, err := s.Page(ctx, "assistant", protocol.TaskQuery{Phase: &phase, Scope: "active"})
	if err != nil || page.Total != 1 || page.Counts.Active != 1 {
		t.Fatalf("approval missing from active tasks: %+v %v", page, err)
	}
	repeat, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stop()
	_, err = s.Wait(repeat, "assistant", []WaitTarget{{record.Info.ID, items[0].Cursor}})
	if err != nil || repeat.Err() == nil {
		t.Fatal("unchanged approval should be suppressed by its cursor", err)
	}
	pending = false
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.Get(ctx, "assistant", record.Info.ID)
	if err != nil || resumed.Info.Phase != protocol.TaskRunning || resumed.RunID != record.RunID || p.launches != 1 {
		t.Fatalf("approval resolution replaced the original run: %+v %v", resumed, err)
	}
}
func TestCreateRollbackDoesNotLeaveReceiptOrInput(t *testing.T) {
	s, _, _ := fixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_tasks BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'disk fixture');END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptLocal(context.Background(), "assistant", protocol.CreateTask{RequestID: "create", Text: "work"}); err == nil {
		t.Fatal("failure hidden")
	}
	for _, table := range []string{"tasks", "task_writes", "task_inputs"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}
