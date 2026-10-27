package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) potMCPRemoteTask(ctx context.Context, task string) bool {
	var present int
	if e := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM mcp_receiver_journal WHERE execution_task=?", task).Scan(&present); e == nil && present > 0 {
		return true
	}
	// The other direction executes in this MCP service's local Task backend.
	// These are actual Task mappings, not inferred connection/display identities.
	var public string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT id FROM agent_link_received_tasks WHERE task_id=?", task).Scan(&public)
	if e != nil {
		return false
	}
	e = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM mcp_local_mutations WHERE operation=? AND state='accepted' AND json_extract(receipt,'$.task.id')=?", agentlink.TaskSubmit, public).Scan(&present)
	return e == nil && present > 0
}
func (p *potMCPReceiver) BeforeSnapshot(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	if r.Admission == nil || potMCPTerminal(r.LastPhase) {
		return nil
	}
	record, e := p.s.Tasks.Get(ctx, r.Claim.Binding.Receiver.AgentID, r.Admission.Binding.Task.AgentID)
	if e != nil {
		return e
	}
	if record == nil || record.RunID != r.Admission.Binding.RunID {
		return agentlink.ErrFenced
	}
	for _, approval := range p.s.Permissions.Pending(record.AssistantID) {
		request := approval.Request
		if request.Task != record.Info.ID || request.Turn != record.RunID || request.Generation != record.Generation {
			continue
		}
		if p.remote == nil {
			return agentlink.ErrUnavailable
		}
		original, e := p.initialWork(ctx, r)
		if e != nil {
			return e
		}
		if original.Message != nil {
			return agentlink.ErrUnauthorized
		}
		pending := agentlink.MCPApprovalPending{Context: agentlink.MCPExecutionContext{Claim: r.Claim, Binding: agentlink.MCPExecutionBinding{Actor: r.Admission.Binding.Task, Task: r.Admission.Binding.Task, RunID: record.RunID}}, Epoch: p.s.options.BootID, RequestID: original.Request.RequestID, Approval: agentlink.ExecutionApproval{ID: approval.ID, JobID: record.Info.ID, Method: request.Tool, Params: json.RawMessage(request.Args)}}
		var accepted agentlink.MCPApprovalPending
		if e = p.remote.Call(ctx, "pot_receiver_approval_pending", pending, &accepted); e != nil {
			var failure agentlink.MCPError
			if errors.As(e, &failure) && failure.Code == agentlink.MCPFenced {
				// An explicit origin fence expires the existing request. Transport loss or
				// disconnect alone never does so, and no expiry can authorize execution.
				p.s.Permissions.mu.Lock()
				waiting := p.s.Permissions.pending[approval.ID]
				p.s.Permissions.mu.Unlock()
				if waiting != nil {
					p.s.Permissions.expireApproval(approval.ID, waiting)
				}
			}
			return e
		}
		actualDigest, hashError := core.HashJSON(accepted)
		expectedDigest, expectedError := core.HashJSON(pending)
		if hashError != nil || expectedError != nil || actualDigest != expectedDigest {
			return agentlink.ErrConflict
		}
	}
	return nil
}
func (p *potMCPReceiver) initialWork(ctx context.Context, r *agentlink.MCPReceiverRecord) (agentlink.MCPWorkDelivery, error) {
	if r.Work != nil {
		return *r.Work, nil
	}
	var raw string
	var work agentlink.MCPWorkDelivery
	e := p.s.Tasks.DB().QueryRowContext(ctx, "SELECT json_extract(body,'$.work') FROM mcp_receiver_journal WHERE connection_id=? AND json_extract(body,'$.claim.binding.task.agent_id')=? AND json_extract(body,'$.work') IS NOT NULL ORDER BY rowid LIMIT 1", r.Claim.ConnectionID, r.Claim.Binding.Task.AgentID).Scan(&raw)
	if e != nil {
		return work, e
	}
	e = mcpRead(raw, &work)
	return work, e
}
func (p *potMCPReceiver) Approval(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	if r.Control == nil || r.Control.Approval == nil {
		return agentlink.ErrInvalid
	}
	decision := r.Control.Approval
	if decision.Epoch != p.s.options.BootID || decision.Validate() != nil || decision.User.NodeID != r.Control.Caller.NodeID {
		return agentlink.ErrUnauthorized
	}
	original, e := p.initialWork(ctx, r)
	if e != nil {
		return e
	}
	if original.Message != nil || decision.RequestID != original.Request.RequestID || original.Request.From != r.Control.Caller {
		return agentlink.ErrUnauthorized
	}
	if original.Request.From.Kind == "user" && decision.User != original.Request.From {
		return agentlink.ErrUnauthorized
	}
	permissions := p.s.Permissions
	// Lock the pending request before taking the Tasks writer. Native expiry and
	// local decisions cannot race the exact run comparison and decision intent.
	permissions.mu.Lock()
	defer permissions.mu.Unlock()
	pending := permissions.pending[decision.ApprovalID]
	if pending == nil || pending.invalid {
		return agentlink.ErrFenced
	}
	if pending.resolved && pending.action != decision.Action {
		return agentlink.ErrConflict
	}
	e = storage.Transaction(ctx, p.s.Tasks.DB(), func(tx *sql.Tx) error {
		current, e := mcpreceiver.Read(ctx, tx, r.Claim.ConnectionID, r.Claim.DeliveryID)
		if e != nil {
			return e
		}
		if current.Claim != r.Claim || mcpJSON(current.Control) != mcpJSON(r.Control) {
			return agentlink.ErrFenced
		}
		if _, e = p.prior(ctx, tx, r.Control); e != nil {
			return e
		}
		record, e := p.s.Tasks.ReadReceiverTx(ctx, tx, r.Claim.Binding.Receiver.AgentID, r.Control.Binding.Task.AgentID)
		if e != nil {
			return e
		}
		if record == nil || record.Execution != nil || record.RunID != r.Control.Binding.RunID || record.Info.Phase.Scope() != "active" || record.StopMode != "" {
			return agentlink.ErrFenced
		}
		request := pending.approval.Request
		if request.Owner != record.AssistantID || request.Task != record.Info.ID || request.Turn != record.RunID || request.Generation != record.Generation {
			return agentlink.ErrFenced
		}
		*r = current
		r.ControlApplied = true
		return mcpreceiver.Save(ctx, tx, r)
	})
	if e != nil {
		return e
	}
	if !pending.resolved {
		pending.action = decision.Action
		pending.allow = decision.Action == "allow_once"
		pending.resolved = true
		close(pending.done)
		permissions.approvalHookLocked(hooks.ApprovalResolved, pending)
	}
	return nil
}
