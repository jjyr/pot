package control

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type mcpExecutionKey struct{}
type mcpExecutionAuthority struct {
	receiver agentlink.MCPReceiver
	context  agentlink.MCPExecutionContext
	parent   *protocol.Envelope
}

// The stored claim and admission are the authority. No caller constructor or SDK
// session can substitute for this lookup. The receiver has independently fenced
// its real active native job immediately before forwarding these typed calls.
func (a *MCPAccess) executionCaller(ctx context.Context, receiver agentlink.MCPReceiver, in agentlink.MCPExecutionContext) (caller agentlink.MCPCaller, parent *protocol.Envelope, err error) {
	if in.Validate() != nil {
		return caller, nil, agentlink.ErrUnauthorized
	}
	if in.NodeExecutionID != "" {
		return a.nodeExecutionCaller(ctx, receiver, in)
	}
	err = storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		if in.Validate() != nil {
			return agentlink.ErrUnauthorized
		}
		d, e := loadMCPDelivery(ctx, tx, receiver, in.Claim)
		if e != nil {
			return e
		}
		if !acceptedMCPDelivery(d) || d.admission == "" || d.terminal {
			return agentlink.ErrFenced
		}
		var admission agentlink.MCPRunAdmission
		if mcpRead(d.admission, &admission) != nil || admission.Binding.Task != in.Binding.Task || admission.Binding.RunID != in.Binding.RunID {
			return agentlink.ErrFenced
		}
		var execution, current, raw string
		if e = tx.QueryRowContext(ctx, "SELECT execution,current_claim,task FROM mcp_service_tasks WHERE id=?", in.Claim.Binding.Task.AgentID).Scan(&execution, &current, &raw); e != nil {
			return e
		}
		var binding agentlink.MCPRunBinding
		var task agentlink.Task
		if mcpRead(execution, &binding) != nil || mcpRead(raw, &task) != nil || binding != admission.Binding || current != in.Claim.ClaimID {
			return agentlink.ErrFenced
		}
		switch task.State {
		case "queued", "running", "waiting_approval":
		default:
			return agentlink.ErrFenced
		}
		// A continuation retains its first immutable message as processing parent.
		var workRaw string
		if e = tx.QueryRowContext(ctx, "SELECT work FROM mcp_service_deliveries WHERE task_id=? AND work!='' ORDER BY rowid LIMIT 1", in.Claim.Binding.Task.AgentID).Scan(&workRaw); e != nil {
			return e
		}
		var work agentlink.MCPWorkDelivery
		if e = mcpRead(workRaw, &work); e != nil {
			return e
		}
		parent = work.Message
		actor := binding.Task
		if work.Message != nil {
			actor = binding.Receiver
		}
		if in.Binding.Actor != actor {
			return agentlink.ErrUnauthorized
		}
		caller, e = agentlink.NewMCPNodeCaller(receiver.ConnectionID(), receiver.NodeID(), actor, &in.Binding)
		return e
	})
	return
}

func (a *MCPAccess) executionTools() {
	a.tool("pot_receiver_directory", mcpContractSchema(agentlink.MCPExecutionContext{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPExecutionContext
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		if _, _, e = a.executionCaller(ctx, receiver, in); e != nil {
			return nil, e
		}
		return a.directory(ctx)
	})
	a.tool("pot_receiver_task_submit", mcpContractSchema(agentlink.MCPExecutionSubmit{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPExecutionSubmit
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		c, _, e := a.executionCaller(ctx, receiver, in.Context)
		if e != nil {
			return nil, e
		}
		if c.Actor().Kind == "task" {
			return nil, agentlink.ErrUnauthorized
		}
		return a.submit(ctx, c, in.Input)
	})
	a.tool("pot_receiver_task_read", mcpContractSchema(agentlink.MCPExecutionRead{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPExecutionRead
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		c, _, e := a.executionCaller(ctx, receiver, in.Context)
		if e != nil {
			return nil, e
		}
		return a.task(ctx, c, in.TaskID)
	})
	a.tool("pot_receiver_task_input", mcpContractSchema(agentlink.MCPExecutionInput{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPExecutionInput
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		c, _, e := a.executionCaller(ctx, receiver, in.Context)
		if e != nil {
			return nil, e
		}
		return a.options.Queue.Control(ctx, c, &in.Input, nil)
	})
	a.tool("pot_receiver_task_cancel", mcpContractSchema(agentlink.MCPExecutionCancel{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPExecutionCancel
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		c, _, e := a.executionCaller(ctx, receiver, in.Context)
		if e != nil {
			return nil, e
		}
		return a.cancel(ctx, c, in.Input)
	})
	a.tool("pot_receiver_message", mcpContractSchema(agentlink.MCPExecutionMessage{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPExecutionMessage
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		c, parent, e := a.executionCaller(ctx, receiver, in.Context)
		if e != nil {
			return nil, e
		}
		if c.Actor().Kind == "task" {
			return nil, agentlink.ErrUnauthorized
		}
		return a.message(context.WithValue(ctx, mcpExecutionKey{}, mcpExecutionAuthority{receiver, in.Context, parent}), c, in.Input)
	})
}
