package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/storage"
)

// RecoveryCommand contains identity and intent only. Clients cannot supply a
// replacement result, cleanup acknowledgment or a different execution target.
type RecoveryCommand struct {
	Action    string `json:"action"`
	ControlID string `json:"control_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
}
type RecoveryReceipt struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	State  string `json:"state"`
}
type RecoverySnapshot struct {
	ControlID        string            `json:"control_id"`
	TaskID           string            `json:"task_id"`
	RunID            string            `json:"run_id"`
	Phase            runner.Phase      `json:"phase"`
	CleanupConfirmed bool              `json:"cleanup_confirmed"`
	Reconciled       bool              `json:"reconciled"`
	Receipts         []RecoveryReceipt `json:"receipts"`
	Unresolved       []string          `json:"unresolved"`
}

func recoveryAction(action string) error {
	if action != "inspect" && action != "reconcile" {
		return errors.New("recovery action must be inspect or reconcile")
	}
	return nil
}

// TaskRecovery always routes to the destination in the original accepted launch.
func (s *Service) TaskRecovery(ctx context.Context, owner, task, run, action string) (RecoverySnapshot, error) {
	if err := recoveryAction(action); err != nil {
		return RecoverySnapshot{}, err
	}
	record, err := s.Tasks.Get(ctx, owner, task)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if record == nil || record.Launch == nil || record.RunID != run {
		return RecoverySnapshot{}, errors.New("recovery requires original accepted Task/run")
	}
	l := *record.Launch
	if l.RuntimeID == s.options.RuntimeID {
		return s.RecoverRuntimeRun(ctx, RecoveryCommand{action, l.ControlID, l.TaskID, l.RunID})
	}
	peer, err := s.Machines.Peer(l.RuntimeID)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	var snapshot RecoverySnapshot
	err = s.remoteCall(ctx, peer, binding(l), "task.recovery", "recovery:"+storage.ID(), struct {
		Action string `json:"action"`
	}{action}, &snapshot)
	if err == nil {
		s.wakeTasks()
		s.Changes.Notify("tasks")
	}
	return snapshot, err
}

// RecoverRuntimeRun is a native/private owner port; browser forwarding excludes
// it. A joined Runner still obtains operation evidence from its original Control.
func (s *Service) RecoverRuntimeRun(ctx context.Context, q RecoveryCommand) (RecoverySnapshot, error) {
	if err := recoveryAction(q.Action); err != nil {
		return RecoverySnapshot{}, err
	}
	if !core.Identifier(q.ControlID) || !core.Identifier(q.TaskID) || !core.Identifier(q.RunID) {
		return RecoverySnapshot{}, errors.New("invalid original recovery identity")
	}
	record, err := s.RunnerJournal.Inspect(ctx, q.ControlID, q.TaskID, q.RunID)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if record == nil {
		return RecoverySnapshot{}, errors.New("original run not found")
	}
	return s.recoverRun(ctx, *record, q.Action)
}

func (s *Service) recoveryOperations(ctx context.Context, l runner.Launch) ([]OperationRecord, error) {
	if l.ControlID == s.options.RuntimeID {
		return s.Operations.RecoveryRecords(ctx, l.TaskID, l.RunID)
	}
	membership := s.Machines.Membership()
	if membership == nil || membership.Enrollment.Control.RuntimeID != l.ControlID {
		return nil, errors.New("original Control membership unavailable")
	}
	peer, err := s.Machines.ControlPeer()
	if err != nil {
		return nil, err
	}
	var records []OperationRecord
	err = s.remoteCall(ctx, peer, binding(l), "task.recovery.operations", "recovery-operations:"+storage.ID(), struct{}{}, &records)
	return records, err
}
func validateRecoveryOperation(l runner.Launch, r OperationRecord) error {
	op := r.Operation
	key, err := op.Key()
	if err != nil {
		return err
	}
	hash, err := core.HashJSON(op)
	if err != nil {
		return err
	}
	if op.Owner != l.TaskID || op.Turn != l.RunID || op.Generation != l.Generation || op.Target != l.ExecutionTarget || !op.Worker || key != r.Receipt.ID || hash != r.Receipt.InputHash {
		return errors.New("original operation differs from fixed Task/run/target")
	}
	return nil
}
func (s *Service) settleRecoveryOperations(ctx context.Context, l runner.Launch, records []OperationRecord) error {
	if l.ControlID != s.options.RuntimeID {
		peer, err := s.Machines.ControlPeer()
		if err != nil {
			return err
		}
		return s.remoteCall(ctx, peer, binding(l), "task.recovery.settle", "recovery-settle:"+storage.ID(), records, nil)
	}
	originals, err := s.Operations.RecoveryRecords(ctx, l.TaskID, l.RunID)
	if err != nil {
		return err
	}
	expected := map[string]OperationRecord{}
	for _, original := range originals {
		expected[original.Receipt.ID] = original
	}
	for _, record := range records {
		if err = validateRecoveryOperation(l, record); err != nil {
			return err
		}
		original, ok := expected[record.Receipt.ID]
		if !ok || original.Receipt.InputHash != record.Receipt.InputHash || record.Receipt.State != "complete" {
			return errors.New("original operation evidence missing or uncertain")
		}
		var operationErr error
		if record.Receipt.Error != "" {
			operationErr = errors.New(record.Receipt.Error)
		}
		if err = s.Operations.ReconcileKnown(ctx, record.Operation, record.Receipt.Result, operationErr); err != nil {
			return err
		}
	}
	return s.settleTaskOperations(ctx, l.TaskID)
}
func agentRecoveryValue(receipt Receipt) json.RawMessage {
	if receipt.Error != "" {
		value, _ := json.Marshal(map[string]any{"isError": true, "error": receipt.Error})
		return value
	}
	return receipt.Result
}

func (s *Service) recoverRun(ctx context.Context, record runner.Run, action string) (RecoverySnapshot, error) {
	l := record.Launch
	out := RecoverySnapshot{ControlID: l.ControlID, TaskID: l.TaskID, RunID: l.RunID, Phase: record.Phase, CleanupConfirmed: record.CleanupConfirmed, Receipts: []RecoveryReceipt{}, Unresolved: []string{}}
	if record.Phase != runner.RecoveryRequired && !record.Phase.Ended() {
		return out, errors.New("original run is still active or safely waiting")
	}
	if !s.Runner.RecoveryIdle(l.ControlID, l.TaskID, l.RunID) {
		return out, errors.New("original invocation is still stopping")
	}
	operations, err := s.recoveryOperations(ctx, l)
	if err != nil {
		return out, err
	}
	byCall := map[string]int{}
	for i, op := range operations {
		if err = validateRecoveryOperation(l, op); err != nil {
			return out, err
		}
		if _, ok := byCall[op.Operation.CallID]; ok {
			return out, errors.New("duplicate original operation call")
		}
		byCall[op.Operation.CallID] = i
	}
	commands, err := s.RunnerJournal.Commands(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return out, err
	}
	replies := map[string]json.RawMessage{}
	for _, command := range commands {
		var input remoteExecute
		if err = core.DecodeJSON(command.Input, &input); err != nil {
			return out, errors.New("original command input missing or invalid")
		}
		i, ok := byCall[command.ID]
		if !ok {
			return out, errors.New("original command has no Control operation")
		}
		operation := &operations[i]
		hash, _ := core.HashJSON(input.Operation)
		if hash != operation.Receipt.InputHash || input.Request.ID != command.ID {
			return out, errors.New("original command differs from Control operation")
		}
		if command.Phase == "complete" {
			var reply executeReply
			if err = core.DecodeJSON(command.Reply, &reply); err != nil || reply.CleanupUnconfirmed || !json.Valid(reply.Value) {
				return out, errors.New("original command result unavailable or cleanup uncertain")
			}
			candidate := *operation
			candidate.Receipt.State = "complete"
			candidate.Receipt.Result = reply.Value
			candidate.Receipt.Error = reply.Error
			if operation.Receipt.State == "complete" {
				a, _ := core.HashJSON(operation.Receipt.Result)
				b, _ := core.HashJSON(reply.Value)
				if a != b || operation.Receipt.Error != reply.Error {
					return out, errors.New("original command conflicts with Control receipt")
				}
			}
			*operation = candidate
			replies[command.ID] = command.Reply
		}
	}
	for i := range operations {
		operation := &operations[i]

		out.Receipts = append(out.Receipts, RecoveryReceipt{operation.Operation.CallID, "control_operation", operation.Receipt.State})
		if operation.Receipt.State != "complete" {
			out.Unresolved = append(out.Unresolved, "original operation result unavailable: "+operation.Operation.CallID)
		}
	}
	for _, command := range commands {
		if _, ok := replies[command.ID]; !ok {
			operation := operations[byCall[command.ID]]
			if operation.Receipt.State == "complete" {
				replies[command.ID], err = json.Marshal(executeReply{Value: operation.Receipt.Result, Error: operation.Receipt.Error})
				if err != nil {
					return out, err
				}
			}
		}
		state := "unknown"
		if _, ok := replies[command.ID]; ok {
			state = "complete"
		}
		out.Receipts = append(out.Receipts, RecoveryReceipt{command.ID, "runner_command", state})
		if state != "complete" {
			out.Unresolved = append(out.Unresolved, "original Runner command result unavailable: "+command.ID)
		}
	}
	values := map[string]json.RawMessage{}
	for _, operation := range operations {
		if operation.Receipt.State == "complete" {
			values[operation.Operation.CallID] = agentRecoveryValue(operation.Receipt)
		}
	}
	if len(record.Context) > 0 {
		var checkpoint agent.Checkpoint
		if err = core.DecodeJSON(record.Context, &checkpoint); err != nil {
			return out, err
		}
		if checkpoint.Started != nil {
			if _, ok := values[*checkpoint.Started]; !ok {
				out.Unresolved = append(out.Unresolved, "started Agent operation has no original result: "+*checkpoint.Started)
			}
		}
	}
	if record.Phase.Ended() {
		if action == "reconcile" && len(out.Unresolved) == 0 {
			err = s.settleRecoveryOperations(ctx, l, operations)
			out.Reconciled = err == nil
		}
		return out, err
	}
	if action == "inspect" || len(out.Unresolved) > 0 {
		return out, nil
	}
	if !record.CleanupConfirmed {
		if err = s.proveRecoveryCleanup(ctx, record, len(operations) > 0 || len(commands) > 0); err != nil {
			out.Unresolved = append(out.Unresolved, err.Error())
			return out, nil
		}
	}
	if err = s.settleRecoveryOperations(ctx, l, operations); err != nil {
		return out, err
	}
	if err = s.RunnerJournal.ReconcileRecovered(ctx, l.ControlID, l.TaskID, l.RunID, replies, values, true); err != nil {
		return out, err
	}
	if err = s.options.Manager.ReconcileInterrupted(execution.Identity{Owner: l.TaskID, RequestID: l.RunID, Generation: l.Generation, Role: "task"}); err != nil {
		return out, err
	}
	out.Phase = runner.Interrupted
	out.CleanupConfirmed = true
	out.Reconciled = true
	s.wakeTasks()
	s.Changes.Notify("tasks")
	if peer, e := s.Machines.ControlPeer(); e == nil {
		_ = peer.Notify(ctx, controlplane.Request{ID: "recovered:" + storage.ID(), Method: "event.runner.changed", Params: json.RawMessage(`{}`)})
	}
	return out, nil
}
func (s *Service) proveRecoveryCleanup(ctx context.Context, record runner.Run, issued bool) error {
	l := record.Launch
	if !issued {
		return nil
	}

	if record.BootID != s.options.BootID {
		return errors.New("previous-boot Host cleanup has no original proof; recovery remains fenced")
	}
	s.mu.Lock()
	engine := s.hosts[l.TaskID]
	generation := s.hostGenerations[l.TaskID]
	s.mu.Unlock()
	if engine == nil || generation != l.Generation {
		return errors.New("original Host cleanup engine unavailable")
	}
	if err := engine.Drain(); err != nil {
		return fmt.Errorf("original Host cleanup unconfirmed: %w", err)
	}
	return nil
}
