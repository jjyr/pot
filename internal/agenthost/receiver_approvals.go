package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
)

type receiverApprovalRemote interface {
	ApprovalPending(context.Context, agentlink.MCPApprovalPending) error
}

func (p *receiverPump) reportApprovals(ctx context.Context, r *receiverRecord) error {
	remote, ok := p.remote.(receiverApprovalRemote)
	if !ok || r.Admission == nil || p.epoch == "" {
		return nil
	}
	requestID := p.originalRequestID(ctx, r)
	p.backend.mu.Lock()
	var pending []agentlink.MCPApprovalPending
	for _, review := range p.backend.reviews {
		if review.resolved || review.ctx.Err() != nil || review.job.Task.ID != r.Admission.Binding.Task.AgentID || review.job.Task.RunID != r.Admission.Binding.RunID {
			continue
		}
		actor := r.Admission.Binding.Task
		pending = append(pending, agentlink.MCPApprovalPending{Context: agentlink.MCPExecutionContext{Claim: r.Claim, Binding: agentlink.MCPExecutionBinding{Actor: actor, Task: actor, RunID: r.Admission.Binding.RunID}}, Epoch: p.epoch, RequestID: requestID, Approval: review.value})
	}
	p.backend.mu.Unlock()
	for _, request := range pending {
		if err := remote.ApprovalPending(ctx, request); err != nil {
			var problem agentlink.MCPError
			if errors.Is(err, agentlink.ErrFenced) || errors.As(err, &problem) && problem.Code == agentlink.MCPFenced {
				// The service explicitly invalidated this pending native channel
				// (for example after restart). Fail the waiting turn closed;
				// transport loss/disconnect alone never takes this path.
				p.backend.mu.Lock()
				cancel := p.backend.active[request.Context.Binding.Task.AgentID]
				p.backend.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				continue
			}
			return err
		}
	}
	return nil
}
func (p *receiverPump) originalRequestID(ctx context.Context, r *receiverRecord) string {
	if r.Work != nil {
		return r.Work.Request.RequestID
	}
	var raw string
	if p.backend.db.QueryRowContext(ctx, "SELECT body FROM mcp_receiver_journal WHERE connection_id=? AND json_extract(body,'$.claim.binding.task.agent_id')=? AND json_type(body,'$.work') IS NOT NULL ORDER BY rowid LIMIT 1", p.identity.ConnectionID(), r.Claim.Binding.Task.AgentID).Scan(&raw) != nil {
		return ""
	}
	var original receiverRecord
	if agentlink.DecodeMCPContract([]byte(raw), &original) != nil || original.Work == nil {
		return ""
	}
	return original.Work.Request.RequestID
}
func (p *receiverPump) applyApproval(ctx context.Context, r *receiverRecord) error {
	control := r.Control
	decision := control.Approval
	if decision == nil || decision.Epoch != p.epoch {
		return agentlink.ErrFenced
	}
	p.backend.mu.Lock()
	defer p.backend.mu.Unlock()
	pending := p.backend.reviews[decision.ApprovalID]
	if pending == nil || p.backend.active[control.Binding.Task.AgentID] == nil || pending.resolved || pending.ctx.Err() != nil || pending.job.Request.From != control.Caller || pending.job.Task.ID != control.Binding.Task.AgentID || pending.job.Task.RunID != control.Binding.RunID {
		return agentlink.ErrFenced
	}
	if decision.RequestID != p.originalRequestID(ctx, r) {
		return agentlink.ErrUnauthorized
	}
	if control.Caller.Kind == "user" && decision.User != control.Caller {
		return agentlink.ErrUnauthorized
	}
	// Save intent before touching an in-memory channel. After an uncertain save
	// or process exit this one-time decision is not replayed into a native request.
	err := storage.Transaction(ctx, p.backend.db, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE id=? AND caller=?", control.Binding.Task.AgentID, callerKey(control.Caller)).Scan(&raw); err != nil {
			return agentlink.ErrFenced
		}
		var current job
		if json.Unmarshal([]byte(raw), &current) != nil || current.Task.RunID != control.Binding.RunID || current.Task.State != "waiting_approval" {
			return agentlink.ErrFenced
		}
		r.ControlApplied = true
		return saveReceiverRecord(ctx, tx, r)
	})
	if err != nil {
		return err
	}
	pending.resolved = true
	pending.done <- decision.Action
	return nil
}

func (c *receiverClient) ApprovalPending(ctx context.Context, in agentlink.MCPApprovalPending) error {
	var out agentlink.MCPApprovalPending
	if err := c.call(ctx, "pot_receiver_approval_pending", in, &out); err != nil {
		return err
	}
	if encoded(out) != encoded(in) {
		return agentlink.ErrConflict
	}
	return nil
}
