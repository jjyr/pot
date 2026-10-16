package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/tasks"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/agent"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/executor"
	m "github.com/jjyr/pot/internal/modelprotocol"
	"github.com/jjyr/pot/internal/runner"
)

func recoveryFixture(t *testing.T) (*Service, runner.Launch, Operation) {
	t.Helper()
	s := taskControl(t, "runtime", "")
	l := runner.Launch{ControlID: "runtime", AssistantID: "main", RuntimeID: "runtime", TaskID: "task", RunID: "run", Generation: 1, ExecutionTarget: core.HostTarget("runtime"), Config: taskConfig(t), Plugins: []string{}, Text: "original task"}
	ctx := context.Background()
	if _, err := s.RunnerJournal.Accept(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunnerJournal.Claim(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	op := Operation{Owner: l.TaskID, Turn: l.RunID, Generation: l.Generation, CallID: "original-call", Target: l.ExecutionTarget, Tool: "exec_command", Args: json.RawMessage(`{"cmd":"effect"}`), Worker: true}
	call := m.ToolCall{ID: op.CallID, Name: op.Tool, Arguments: op.Args}
	checkpoint := agent.Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{call}, Started: &call.ID, LastRequest: l.RunID, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}}
	raw, _ := json.Marshal(checkpoint)
	if err := s.RunnerJournal.SaveContext(ctx, l.ControlID, l.TaskID, l.RunID, raw); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.Operations.Begin(ctx, op); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	return s, l, op
}
func fenceRecovery(t *testing.T, s *Service, l runner.Launch) {
	t.Helper()
	ctx := context.Background()
	if err := s.RunnerJournal.Fence(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	_, result := s.options.Manager.Execute(ctx, execution.Identity{Owner: l.TaskID, RequestID: l.RunID, Generation: l.Generation, Role: "task"}, func(context.Context) execution.Result {
		return execution.Result{Phase: execution.Interrupted, Err: core.ErrCleanupUnconfirmed}
	})
	if result.Phase != execution.Interrupted {
		t.Fatal(result)
	}
}
func TestRecoverySettlesOriginalCommandAndCheckpointWithoutReplay(t *testing.T) {
	s, l, op := recoveryFixture(t)
	ctx := context.Background()
	key, _ := op.Key()
	if err := s.Operations.Unknown(ctx, key); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(remoteExecute{op, executor.Request{ID: op.CallID, Operation: op.Tool, CWD: "/original", Policy: l.Config.Params.Agent.Shell, Args: map[string]any{"cmd": "effect"}}})
	if _, _, err := s.RunnerJournal.BeginCommand(ctx, l.ControlID, l.TaskID, l.RunID, op.CallID, raw); err != nil {
		t.Fatal(err)
	}
	reply, _ := json.Marshal(executeReply{Value: json.RawMessage(`{"stdout":"original output"}`)})
	if err := s.RunnerJournal.CompleteCommand(ctx, l.ControlID, l.TaskID, l.RunID, op.CallID, reply); err != nil {
		t.Fatal(err)
	}
	if err := s.RunnerJournal.RecordCleanup(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	fenceRecovery(t, s, l)
	command := RecoveryCommand{"inspect", l.ControlID, l.TaskID, l.RunID}
	inspected, err := s.RecoverRuntimeRun(ctx, command)
	if err != nil || inspected.Reconciled || len(inspected.Unresolved) > 0 {
		t.Fatal(inspected, err)
	}
	receipt, _ := s.Operations.Get(ctx, key)
	if receipt.State != "unknown" {
		t.Fatal("inspection mutated original operation", receipt)
	}
	command.Action = "reconcile"
	recovered, err := s.RecoverRuntimeRun(ctx, command)
	if err != nil || !recovered.Reconciled || recovered.Phase != runner.Interrupted {
		t.Fatal(recovered, err)
	}
	receipt, _ = s.Operations.Get(ctx, key)
	if receipt.State != "complete" || !strings.Contains(string(receipt.Result), "original output") {
		t.Fatal(receipt)
	}
	run, _ := s.RunnerJournal.Inspect(ctx, l.ControlID, l.TaskID, l.RunID)
	var checkpoint agent.Checkpoint
	json.Unmarshal(run.Context, &checkpoint)
	if checkpoint.Started != nil || len(checkpoint.Pending) != 0 || len(checkpoint.Receipts) != 1 {
		t.Fatal(checkpoint)
	}
	for _, entry := range s.options.Manager.Snapshot() {
		if entry.Owner == l.TaskID && entry.Phase == execution.Interrupted {
			continue
		}
		if entry.Owner == l.TaskID {
			t.Fatal(entry)
		}
	}
	nextCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, next := s.options.Manager.Execute(nextCtx, execution.Identity{Owner: "next-task", RequestID: "next", Generation: 1, Role: "task"}, func(context.Context) execution.Result { return execution.Result{Phase: execution.Succeeded} })
	if next.Phase != execution.Succeeded {
		t.Fatal("proven recovery did not release held capacity", next)
	}
	// Duplicate owner reconciliation returns original settled state and never runs
	// Prepare, an Agent or the execution request whose command is only a fixture.
	again, err := s.RecoverRuntimeRun(ctx, command)
	if err != nil || !again.Reconciled {
		t.Fatal(again, err)
	}
}
func TestRecoveryUnknownOriginalReceiptKeepsRunAndCapacityFenced(t *testing.T) {
	s, l, op := recoveryFixture(t)
	ctx := context.Background()
	key, _ := op.Key()
	if err := s.Operations.Unknown(ctx, key); err != nil {
		t.Fatal(err)
	}
	fenceRecovery(t, s, l)
	out, err := s.RecoverRuntimeRun(ctx, RecoveryCommand{"reconcile", l.ControlID, l.TaskID, l.RunID})
	if err != nil || out.Reconciled || len(out.Unresolved) == 0 {
		t.Fatal(out, err)
	}
	run, _ := s.RunnerJournal.Inspect(ctx, l.ControlID, l.TaskID, l.RunID)
	if run.Phase != runner.RecoveryRequired {
		t.Fatal(run)
	}
	if _, _, err = s.Operations.Begin(ctx, Operation{Owner: l.TaskID, Turn: "replacement", Generation: 1, CallID: "other", Target: l.ExecutionTarget, Tool: "exec_command", Args: json.RawMessage(`{}`), Worker: true}); err == nil {
		t.Fatal("uncertainty fence bypassed")
	}
}
func TestRecoveryPriorBootHostRequiresOriginalCleanupProof(t *testing.T) {
	s, l, op := recoveryFixture(t)
	ctx := context.Background()
	key, _ := op.Key()
	if err := s.Operations.Complete(ctx, key, json.RawMessage(`{"stdout":"known"}`), nil); err != nil {
		t.Fatal(err)
	}
	fenceRecovery(t, s, l)
	// A new empty engine is not evidence that an old process or plugin stopped.
	s.options.BootID = "next-boot"
	if _, err := s.host(l.TaskID, l.Generation); err != nil {
		t.Fatal(err)
	}
	out, err := s.RecoverRuntimeRun(ctx, RecoveryCommand{"reconcile", l.ControlID, l.TaskID, l.RunID})
	if err != nil || out.Reconciled || !strings.Contains(strings.Join(out.Unresolved, " "), "previous-boot") {
		t.Fatal(out, err)
	}
}
func TestRecoveryOriginalOwnerRunAndTargetCannotBeRebound(t *testing.T) {
	s, l, op := recoveryFixture(t)
	ctx := context.Background()
	record := OperationRecord{Operation: op}
	record.Receipt.ID, _ = op.Key()
	record.Receipt.InputHash, _ = core.HashJSON(op)
	wrong := l
	wrong.ExecutionTarget = core.SandboxTarget(l.RuntimeID, "shared")
	if err := validateRecoveryOperation(wrong, record); err == nil {
		t.Fatal("target changed")
	}
	if _, err := s.RecoverRuntimeRun(ctx, RecoveryCommand{"reconcile", "another-control", l.TaskID, l.RunID}); err == nil {
		t.Fatal("control identity changed")
	}
	if _, err := s.RecoverRuntimeRun(ctx, RecoveryCommand{"reconcile", l.ControlID, l.TaskID, "another-run"}); err == nil {
		t.Fatal("run identity changed")
	}
	if _, err := s.TaskRecovery(ctx, "another-owner", l.TaskID, l.RunID, "reconcile"); err == nil {
		t.Fatal("owner identity changed")
	}
}

func TestRecoveryUsesSignedOriginalRunnerAndControlReceipts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	control := taskControl(t, "control", "")
	worker := taskControl(t, "worker", "")
	if err := control.Machines.EnableControl(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(control.ControlServer(nil))
	defer server.Close()
	invitation, err := control.Machines.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	connector := controlplane.Connector{Directory: worker.Machines}
	if _, err = connector.Enroll(ctx, server.URL, control.Machines.Identity(), invitation.Code, "recovery-enroll"); err != nil {
		t.Fatal(err)
	}
	peer, err := worker.RunnerConnect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	l := runner.Launch{ControlID: "control", AssistantID: "main", RuntimeID: "worker", TaskID: "remote-task", RunID: "original-run", Generation: 1, ExecutionTarget: core.HostTarget("worker"), Config: taskConfig(t), Plugins: []string{}, Text: "original"}
	// The Control identity record is already terminal here to keep its automatic
	// lifecycle from racing this deliberately incomplete destination fixture.
	task := tasks.Record{SchemaVersion: 4, AssistantID: l.AssistantID, Info: protocol.TaskInfo{ID: l.TaskID, AssistantID: l.AssistantID, MachineID: l.RuntimeID, Phase: protocol.TaskInterrupted, ExecutionTarget: &l.ExecutionTarget}, Config: l.Config, Plugins: []string{}, RunID: l.RunID, Generation: l.Generation, Launch: &l, LaunchSent: true}
	raw, _ := json.Marshal(task)
	if _, err = control.Tasks.DB().ExecContext(ctx, "INSERT INTO tasks VALUES(?,?,?,0,?)", l.AssistantID, l.TaskID, protocol.TaskInterrupted, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.RunnerJournal.Accept(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.RunnerJournal.Claim(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	op := Operation{Owner: l.TaskID, Turn: l.RunID, Generation: l.Generation, CallID: "command", Target: l.ExecutionTarget, Tool: "exec_command", Args: json.RawMessage(`{"cmd":"never replay"}`), Worker: true}
	receipt, _, err := control.Operations.Begin(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if err = control.Operations.Unknown(ctx, receipt.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(remoteExecute{op, executor.Request{ID: op.CallID, Operation: op.Tool, CWD: "/original", Policy: l.Config.Params.Agent.Shell, Args: map[string]any{"cmd": "never replay"}}})
	if _, _, err = worker.RunnerJournal.BeginCommand(ctx, l.ControlID, l.TaskID, l.RunID, op.CallID, raw); err != nil {
		t.Fatal(err)
	}
	reply, _ := json.Marshal(executeReply{Value: json.RawMessage(`{"stdout":"signed original result"}`)})
	if err = worker.RunnerJournal.CompleteCommand(ctx, l.ControlID, l.TaskID, l.RunID, op.CallID, reply); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunnerJournal.RecordCleanup(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunnerJournal.Fence(ctx, l.ControlID, l.TaskID, l.RunID); err != nil {
		t.Fatal(err)
	}
	out, err := control.TaskRecovery(ctx, l.AssistantID, l.TaskID, l.RunID, "reconcile")
	if err != nil || !out.Reconciled || out.Phase != runner.Interrupted {
		t.Fatal(out, err)
	}
	settled, err := control.Operations.Get(ctx, receipt.ID)
	if err != nil || settled.State != "complete" || !strings.Contains(string(settled.Result), "signed original result") {
		t.Fatal(settled, err)
	}
	// A fresh Task/run selector cannot reuse an authenticated old Runner result.
	if _, err = control.TaskRecovery(ctx, l.AssistantID, l.TaskID, "replacement-run", "reconcile"); err == nil {
		t.Fatal("original run rebound")
	}
}
