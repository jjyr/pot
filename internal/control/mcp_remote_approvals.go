package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (a *MCPAccess) approvalTools() {
	a.tool("pot_receiver_approval_pending", mcpContractSchema(agentlink.MCPApprovalPending{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPApprovalPending
		if agentlink.DecodeMCPContract(raw, &in) != nil || in.Validate() != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		if _, _, e = a.executionCaller(ctx, receiver, in.Context); e != nil {
			return nil, e
		}
		return in, a.options.Queue.approvalPending(ctx, receiver, in)
	})
}
func (q *MCPAccessQueue) approvalPending(ctx context.Context, receiver agentlink.MCPReceiver, in agentlink.MCPApprovalPending) error {
	return storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		d, e := loadMCPDelivery(ctx, tx, receiver, in.Context.Claim)
		if e != nil {
			return e
		}
		if d.terminal || !acceptedMCPDelivery(d) {
			return agentlink.ErrFenced
		}
		var registrationRaw string
		if e = tx.QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), mcpJSON(in.Context.Claim.Binding.Receiver)).Scan(&registrationRaw); e != nil {
			return e
		}
		var registration agentlink.MCPReceiverRegistration
		if mcpRead(registrationRaw, &registration) != nil || registration.Epoch != in.Epoch || !registration.Capabilities.Supports(agentlink.Approvals) {
			return agentlink.ErrUnsupported
		}
		var work agentlink.MCPWorkDelivery
		if d.work == "" {
			if e = tx.QueryRowContext(ctx, "SELECT work FROM mcp_service_deliveries WHERE task_id=? AND work!='' ORDER BY rowid LIMIT 1", in.Context.Claim.Binding.Task.AgentID).Scan(&d.work); e != nil {
				return e
			}
		}
		if mcpRead(d.work, &work) != nil || work.Message != nil || work.Request.RequestID != in.RequestID {
			return agentlink.ErrUnauthorized
		}
		var body, state string
		e = tx.QueryRowContext(ctx, "SELECT body,state FROM mcp_remote_approvals WHERE connection_id=? AND approval_id=?", receiver.ConnectionID(), in.Approval.ID).Scan(&body, &state)
		if e == nil {
			if body != mcpJSON(in) {
				return agentlink.ErrConflict
			}
			if state == "invalidated" {
				return agentlink.ErrFenced
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_remote_approvals(connection_id,approval_id,task_id,run_id,epoch,caller,body,state) VALUES(?,?,?,?,?,?,?,'pending')", receiver.ConnectionID(), in.Approval.ID, in.Context.Claim.Binding.Task.AgentID, in.Context.Binding.RunID, in.Epoch, mcpJSON(work.Request.From), mcpJSON(in))
		return e
	})
}

type mcpHumanReview struct {
	Connection, Task string
	Caller           protocol.AgentRef
	Pending          agentlink.MCPApprovalPending
}

func (a *MCPAccess) humanReviews(ctx context.Context, owner string) []mcpHumanReview {
	result := []mcpHumanReview{}
	rows, e := a.options.Queue.DB.QueryContext(ctx, `SELECT p.connection_id,p.task_id,p.caller,p.body FROM mcp_remote_approvals p JOIN mcp_service_tasks t ON t.id=p.task_id WHERE p.state='pending' AND json_extract(t.task,'$.run_id')=p.run_id AND json_extract(t.task,'$.state') IN ('running','waiting_approval','queued')`)
	if e != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var item mcpHumanReview
		var caller, body string
		if rows.Scan(&item.Connection, &item.Task, &caller, &body) != nil {
			continue
		}
		if mcpRead(caller, &item.Caller) != nil || mcpRead(body, &item.Pending) != nil {
			continue
		}
		// Assistant delegations stay with their owner. User-kind OAuth submissions
		// stay user-kind; the local human control plane may review them without
		// manufacturing an assistant execution identity.
		if item.Caller.Kind == "assistant" && item.Caller.AgentID != owner {
			continue
		}
		result = append(result, item)
	}
	return result
}
func mcpHumanApprovalID(item mcpHumanReview) string {
	hash, _ := core.HashJSON([]string{item.Connection, item.Pending.Approval.ID})
	return "linked:mcp:" + hash
}

// This method is called only by the existing human review port. It is never an
// MCP/model tool and cannot be reached with a caller/receiver access credential.
func (a *MCPAccess) decideHumanApproval(ctx context.Context, item mcpHumanReview, user protocol.AgentRef, action string) error {
	a.options.Queue.dispatchMu.Lock()
	defer a.options.Queue.dispatchMu.Unlock()
	if a.options.Queue.CheckConnection != nil {
		if e := a.options.Queue.CheckConnection(ctx, item.Connection); e != nil {
			return e
		}
	}
	if !user.Valid() || user.Kind != "user" || (action != "allow_once" && action != "deny") {
		return agentlink.ErrUnauthorized
	}
	return storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		var body, state, caller, raw, bindingRaw, current string
		e := tx.QueryRowContext(ctx, "SELECT p.body,p.state,p.caller,t.task,t.execution,t.current_claim FROM mcp_remote_approvals p JOIN mcp_service_tasks t ON t.id=p.task_id WHERE p.connection_id=? AND p.approval_id=?", item.Connection, item.Pending.Approval.ID).Scan(&body, &state, &caller, &raw, &bindingRaw, &current)
		if e != nil {
			return mcpMissing(e)
		}
		if body != mcpJSON(item.Pending) || caller != mcpJSON(item.Caller) || state != "pending" {
			return agentlink.ErrFenced
		}
		var registrationRaw string
		var registration agentlink.MCPReceiverRegistration
		if tx.QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", item.Connection, mcpJSON(item.Pending.Context.Claim.Binding.Receiver)).Scan(&registrationRaw) != nil || mcpRead(registrationRaw, &registration) != nil || registration.Epoch != item.Pending.Epoch {
			return agentlink.ErrFenced
		}
		var task agentlink.Task
		var binding agentlink.MCPRunBinding
		if mcpRead(raw, &task) != nil || mcpRead(bindingRaw, &binding) != nil || binding.Task != item.Pending.Context.Binding.Task || binding.RunID != item.Pending.Context.Binding.RunID || current != item.Pending.Context.Claim.ClaimID {
			return agentlink.ErrFenced
		}
		switch task.State {
		case "running", "waiting_approval", "queued":
		default:
			return agentlink.ErrFenced
		}
		if item.Caller.Kind == "user" && user != item.Caller {
			return agentlink.ErrUnauthorized
		}
		decision := agentlink.MCPApprovalDecision{ApprovalID: item.Pending.Approval.ID, Epoch: item.Pending.Epoch, RequestID: item.Pending.RequestID, User: user, Action: action}
		delivery := agentlink.MCPControlDelivery{Version: agentlink.MCPContractVersion, DeliveryID: rand.Text(), Dispatch: item.Pending.Context.Claim.Binding, Binding: binding, Caller: item.Caller, Approval: &decision}
		if e = delivery.Validate(); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO mcp_service_deliveries(id,task_id,receiver_connection,receiver_address,control) VALUES(?,?,?,?,?)", delivery.DeliveryID, item.Task, item.Connection, mcpJSON(binding.Receiver), mcpJSON(delivery)); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_remote_approvals SET state='decided',decision=? WHERE connection_id=? AND approval_id=? AND state='pending'", mcpJSON(decision), item.Connection, decision.ApprovalID)
		return e
	})
}
