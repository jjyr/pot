package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/sandbox"
)

type MainRecoveryCommand struct {
	Action        string `json:"action" required:"true"`
	UserMessageID string `json:"user_message_id,omitempty"`
}
type MainRecoverySnapshot struct {
	UserMessageID    string            `json:"user_message_id"`
	RequestID        string            `json:"request_id"`
	Generation       uint64            `json:"generation"`
	CleanupConfirmed bool              `json:"cleanup_confirmed"`
	Reconciled       bool              `json:"reconciled"`
	Receipts         []RecoveryReceipt `json:"receipts"`
	Unresolved       []string          `json:"unresolved"`
}

// RecoverMain is a private owner operation. It closes only the original stopped
// turn, using service-held receipts; no caller can supply outcomes or cleanup.
func (s *Service) RecoverMain(ctx context.Context, owner string, q MainRecoveryCommand) (MainRecoverySnapshot, error) {
	out := MainRecoverySnapshot{Receipts: []RecoveryReceipt{}, Unresolved: []string{}}
	if err := recoveryAction(q.Action); err != nil {
		return out, err
	}
	conversation, err := s.Conversation(ctx, owner)
	if err != nil {
		return out, err
	}
	if q.UserMessageID != "" {
		recovered, e := conversation.RecoveredState(ctx, q.UserMessageID)
		if e != nil {
			return out, e
		}
		if recovered != nil {
			if q.Action == "reconcile" {
				if e = s.finishMainRecovery(ctx, owner, *recovered); e != nil {
					return out, e
				}
			}
			out.UserMessageID = recovered.UserMessageID
			out.RequestID = recovered.RequestID
			out.Generation = recovered.Generation
			out.Reconciled = true
			out.CleanupConfirmed = true
			return out, nil
		}
	}
	state, err := conversation.Recovery(ctx)
	if err != nil {
		return out, err
	}
	out.UserMessageID = state.UserMessageID
	out.RequestID = state.RequestID
	out.Generation = state.Generation
	out.CleanupConfirmed = state.CleanupConfirmed
	if q.UserMessageID != "" && q.UserMessageID != state.UserMessageID {
		return out, errors.New("recovery target differs from original user message")
	}
	if q.Action == "reconcile" && q.UserMessageID == "" {
		return out, errors.New("reconcile requires original user_message_id")
	}
	operations, err := s.Operations.RecoveryRecords(ctx, owner, state.RequestID)
	if err != nil {
		return out, err
	}
	values := map[string]json.RawMessage{}
	for i := range operations {
		record := &operations[i]
		op := record.Operation
		key, e := op.Key()
		if e != nil {
			return out, e
		}
		hash, e := core.HashJSON(op)
		if e != nil {
			return out, e
		}
		if op.Owner != owner || op.Turn != state.RequestID || op.Generation != state.Generation || op.Worker || !op.Target.IsLocalTo(s.options.RuntimeID) || key != record.Receipt.ID || hash != record.Receipt.InputHash {
			return out, errors.New("original Main operation binding changed")
		}
		if record.Receipt.State != "complete" && op.Target.Target.Kind == "sandbox" {
			value, e := s.inspectMainSandboxOperation(ctx, *state, op)
			if e == nil {
				record.Receipt.State = "complete"
				record.Receipt.Result = value
				record.Receipt.Error = ""
			}
		}
		out.Receipts = append(out.Receipts, RecoveryReceipt{ID: op.CallID, Source: "original_operation", State: record.Receipt.State})
		if record.Receipt.State != "complete" {
			out.Unresolved = append(out.Unresolved, "original Main operation result unavailable: "+op.CallID)
		} else {
			values[op.CallID] = agentRecoveryValue(record.Receipt)
		}
	}
	var checkpoint agent.Checkpoint
	if len(state.Context) > 0 {
		if err = core.DecodeJSON(state.Context, &checkpoint); err != nil {
			return out, err
		}
		if (checkpoint.Started != nil || len(operations) > 0) && checkpoint.LastRequest != state.RequestID {
			out.Unresolved = append(out.Unresolved, "checkpoint differs from original recovery turn")
		}
		if checkpoint.ModelRequestID != nil {
			out.Unresolved = append(out.Unresolved, "original model outcome is uncertain; request remains fenced")
		}
		if checkpoint.Started != nil {
			if _, known := values[*checkpoint.Started]; !known {
				out.Unresolved = append(out.Unresolved, "started Main operation has no original result: "+*checkpoint.Started)
			}
		}
	}
	if q.Action == "inspect" || len(out.Unresolved) > 0 {
		return out, nil
	}
	if !state.CleanupConfirmed {
		if err = s.proveMainRecoveryCleanup(ctx, owner, *state, operations); err != nil {
			out.Unresolved = append(out.Unresolved, err.Error())
			return out, nil
		}
	}
	for _, record := range operations {
		var operationErr error
		if record.Receipt.Error != "" {
			operationErr = errors.New(record.Receipt.Error)
		}
		if err = s.Operations.ReconcileKnown(ctx, record.Operation, record.Receipt.Result, operationErr); err != nil {
			return out, err
		}
	}
	if err = s.settleTaskOperations(ctx, owner); err != nil {
		return out, err
	}
	if err = s.finishMainRecovery(ctx, owner, *state); err != nil {
		return out, err
	}
	if err = conversation.ReconcileRecovery(ctx, *state, values, true); err != nil {
		return out, err
	}
	out.Reconciled = true
	out.CleanupConfirmed = true
	s.Changes.Notify("assistants")
	s.Changes.Notify("usage")
	return out, nil
}
func (s *Service) inspectMainSandboxOperation(ctx context.Context, state assistant.RecoveryState, op Operation) (json.RawMessage, error) {
	if s.options.Sandboxes == nil {
		return nil, errors.New("original Sandbox Manager unavailable")
	}
	var args map[string]any
	if err := core.DecodeJSON(op.Args, &args); err != nil {
		return nil, err
	}
	checker := Dispatcher{Spec: state.Config}
	name, prepared, err := checker.prepare(op.Tool, args, op.Target)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(prepared)
	if err != nil {
		return nil, err
	}
	grant := sandbox.AccessGrant{Target: op.Target, Owner: op.Owner, Turn: op.Turn, ThreadGeneration: op.Generation, Policy: state.Config.Params.Agent.Shell, Worker: false, WorkspaceOwner: op.Owner}
	return s.options.Sandboxes.InspectPreparedReceipt(ctx, grant, op.CallID, name, raw)
}
func (s *Service) proveMainRecoveryCleanup(ctx context.Context, owner string, state assistant.RecoveryState, operations []OperationRecord) error {
	if len(operations) == 0 {
		return nil
	}
	hasHost := false
	for _, record := range operations {
		if record.Operation.Target.Target.Kind == "host" {
			hasHost = true
		}
	}
	if hasHost {
		if state.BootID != s.options.BootID {
			return errors.New("previous-boot Host cleanup has no original proof; Main recovery remains fenced")
		}
		s.mu.Lock()
		engine := s.hosts[owner]
		generation := s.hostGenerations[owner]
		s.mu.Unlock()
		if engine == nil || generation != state.Generation {
			return errors.New("original Main Host cleanup engine unavailable")
		}
		if err := engine.Drain(); err != nil {
			return err
		}
	}
	if s.options.CleanupTarget != nil {
		return s.options.CleanupTarget(ctx, owner, true)
	}
	for _, record := range operations {
		if record.Operation.Target.Target.Kind == "sandbox" {
			return errors.New("original Main Sandbox cleanup unavailable")
		}
	}
	return nil
}

// Completing admission/wait cleanup is idempotent under the original identity,
// including retries after the durable conversation recovery receipt was saved.
func (s *Service) finishMainRecovery(ctx context.Context, owner string, state assistant.RecoveryState) error {
	role := "assistant"
	if strings.HasPrefix(state.RequestID, "pot-result-") {
		role = "summary"
	}
	if err := s.options.Manager.ReconcileInterrupted(execution.Identity{Owner: owner, RequestID: state.RequestID, Generation: state.Generation, Role: role}); err != nil {
		return err
	}
	return s.Usage.ClearWaitForTurn(ctx, owner, state.RequestID)
}
