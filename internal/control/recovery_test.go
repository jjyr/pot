package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/executor"
	m "github.com/jjyr/pot/pkg/modelprotocol"
)

func recoveryFixture(t *testing.T) (*Service, runner.Launch, Operation) {
	t.Helper()
	s := taskControl(t, "runtime", "")
	l := runner.Launch{ControlID: "runtime", AssistantID: "main", RuntimeID: "runtime", TaskID: "task", RunID: "run", Generation: 1, ExecutionTarget: core.RuntimeTarget("runtime"), Config: taskConfig(t), Plugins: []string{}, Text: "original task"}
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
	wrong.ExecutionTarget = core.RuntimeTarget("other-runtime")
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
