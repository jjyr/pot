package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
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
	Phase            tasks.Phase       `json:"phase"`
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
	return RecoverySnapshot{}, protocol.APIError{Code: "original_execution_unavailable"}
}

// RecoverRuntimeRun is a native/private owner port; browser forwarding excludes
// it. Reconciliation reads original local operations and execution receipts only.
func (s *Service) RecoverRuntimeRun(ctx context.Context, q RecoveryCommand) (RecoverySnapshot, error) {
	if err := recoveryAction(q.Action); err != nil {
		return RecoverySnapshot{}, err
	}
	if !core.Identifier(q.ControlID) || !core.Identifier(q.TaskID) || !core.Identifier(q.RunID) {
		return RecoverySnapshot{}, errors.New("invalid original recovery identity")
	}
	record, err := s.RunJournal.Inspect(ctx, q.ControlID, q.TaskID, q.RunID)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if record == nil {
		return RecoverySnapshot{}, errors.New("original run not found")
	}
	return s.recoverRun(ctx, *record, q.Action)
}

func (s *Service) recoveryOperations(ctx context.Context, l tasks.Launch) ([]OperationRecord, error) {
	if l.ControlID != s.options.RuntimeID {
		return nil, protocol.APIError{Code: "original_execution_unavailable"}
	}
	records, err := s.Operations.RecoveryRecords(ctx, l.TaskID, l.RunID)
	if err != nil {
		return nil, err
	}
	old, err := s.RunJournal.OriginalOperations(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i, r := range records {
		index[r.Operation.CallID] = i
	}
	for _, v := range old {
		i, ok := index[v.CallID]
		if !ok {
			return nil, errors.New("archived execution has no original operation")
		}
		r := &records[i]
		hash, _ := core.HashJSON(r.Operation)
		if hash != v.OperationHash {
			return nil, errors.New("archived operation differs from original effect")
		}
		if v.Known {
			if r.Receipt.State == "complete" {
				a, _ := core.HashJSON(r.Receipt.Result)
				b, _ := core.HashJSON(v.Result)
				if a != b || r.Receipt.Error != v.Error {
					return nil, errors.New("archived result conflicts with original effect")
				}
			}
			r.Receipt.State, r.Receipt.Result, r.Receipt.Error = "complete", v.Result, v.Error
		}
	}
	return records, nil
}
func validateRecoveryOperation(l tasks.Launch, r OperationRecord) error {
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
func (s *Service) settleRecoveryOperations(ctx context.Context, l tasks.Launch, records []OperationRecord) error {
	if l.ControlID != s.options.RuntimeID {
		return protocol.APIError{Code: "original_execution_unavailable"}
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

func (s *Service) recoverRun(ctx context.Context, record tasks.Run, action string) (RecoverySnapshot, error) {
	l := record.Launch
	out := RecoverySnapshot{ControlID: l.ControlID, TaskID: l.TaskID, RunID: l.RunID, Phase: record.Phase, CleanupConfirmed: record.CleanupConfirmed, Receipts: []RecoveryReceipt{}, Unresolved: []string{}}
	if record.Phase != tasks.RecoveryRequired && !record.Phase.Ended() {
		return out, errors.New("original run is still active or safely waiting")
	}
	if !s.TaskRuns.RecoveryIdle(l.ControlID, l.TaskID, l.RunID) {
		return out, errors.New("original invocation is still stopping")
	}
	operations, err := s.recoveryOperations(ctx, l)
	if err != nil {
		return out, err
	}
	for _, operation := range operations {
		if err = validateRecoveryOperation(l, operation); err != nil {
			return out, err
		}
		out.Receipts = append(out.Receipts, RecoveryReceipt{operation.Operation.CallID, "operation", operation.Receipt.State})
		if operation.Receipt.State != "complete" {
			out.Unresolved = append(out.Unresolved, "original operation result unavailable: "+operation.Operation.CallID)
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
		if err = s.proveRecoveryCleanup(ctx, record, len(operations) > 0); err != nil {
			out.Unresolved = append(out.Unresolved, err.Error())
			return out, nil
		}
	}
	if err = s.settleRecoveryOperations(ctx, l, operations); err != nil {
		return out, err
	}
	if err = s.RunJournal.ReconcileRecovered(ctx, l.ControlID, l.TaskID, l.RunID, values, true); err != nil {
		return out, err
	}
	if err = s.options.Manager.ReconcileInterrupted(execution.Identity{Owner: l.TaskID, RequestID: l.RunID, Generation: l.Generation, Role: "task"}); err != nil {
		return out, err
	}
	out.Phase = tasks.Interrupted
	out.CleanupConfirmed = true
	out.Reconciled = true
	s.wakeTasks()
	s.Changes.Notify("tasks")
	return out, nil
}
func (s *Service) proveRecoveryCleanup(ctx context.Context, record tasks.Run, issued bool) error {
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
