package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (a *MCPAccess) nodeExecutionTools() {
	a.tool("pot_receiver_execution_admit", mcpContractSchema(agentlink.MCPNodeExecutionAdmission{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeExecutionAdmission
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		return in, a.admitNodeExecution(ctx, receiver, in)
	})
	a.tool("pot_receiver_execution_end", mcpContractSchema(agentlink.MCPNodeExecutionEnd{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeExecutionEnd
		if agentlink.DecodeMCPContract(raw, &in) != nil || in.Binding.Validate() != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		result, e := a.options.Queue.DB.ExecContext(ctx, "UPDATE mcp_node_executions SET active=0 WHERE connection_id=? AND execution_id=? AND binding=?", receiver.ConnectionID(), in.ExecutionID, mcpJSON(in.Binding))
		if e != nil {
			return nil, e
		}
		n, e := result.RowsAffected()
		if e == nil && n != 1 {
			e = agentlink.ErrFenced
		}
		return in, e
	})
}
func (a *MCPAccess) admitNodeExecution(ctx context.Context, receiver agentlink.MCPReceiver, in agentlink.MCPNodeExecutionAdmission) error {
	if !core.Identifier(in.ExecutionID) || in.Binding.Validate() != nil || in.Binding.Task.NodeID != receiver.NodeID() {
		return agentlink.ErrUnauthorized
	}
	return storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		endpoint := agentlink.NodeTarget(receiver.NodeID())
		if in.Binding.Actor.Kind == "assistant" {
			endpoint = in.Binding.Actor
		}
		var registration string
		if e := tx.QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), mcpJSON(endpoint)).Scan(&registration); e != nil {
			return agentlink.ErrUnauthorized
		}
		if in.ParentID != "" {
			var raw string
			var parent protocol.Envelope
			if tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", in.ParentID).Scan(&raw) != nil || mcpRead(raw, &parent) != nil || parent.To != in.Binding.Actor {
				return agentlink.ErrUnauthorized
			}
		}
		var oldID, raw, parent string
		var active bool
		e := tx.QueryRowContext(ctx, "SELECT execution_id,binding,parent_id,active FROM mcp_node_executions WHERE connection_id=? AND task=?", receiver.ConnectionID(), mcpJSON(in.Binding.Task)).Scan(&oldID, &raw, &parent, &active)
		if e == nil {
			if oldID == in.ExecutionID {
				if raw != mcpJSON(in.Binding) || parent != in.ParentID {
					return agentlink.ErrConflict
				}
				if !active {
					return agentlink.ErrFenced
				}
				return nil
			}
			var old agentlink.MCPExecutionBinding
			if mcpRead(raw, &old) != nil || active || old.RunID != in.PreviousRunID || in.Binding.RunID == old.RunID || in.Binding.Actor != old.Actor {
				return agentlink.ErrFenced
			}
			_, e = tx.ExecContext(ctx, "UPDATE mcp_node_executions SET execution_id=?,binding=?,parent_id=?,active=1 WHERE connection_id=? AND task=?", in.ExecutionID, mcpJSON(in.Binding), in.ParentID, receiver.ConnectionID(), mcpJSON(in.Binding.Task))
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if in.PreviousRunID != "" {
			return agentlink.ErrFenced
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_node_executions VALUES(?,?,?,?,?,1)", receiver.ConnectionID(), mcpJSON(in.Binding.Task), in.ExecutionID, mcpJSON(in.Binding), in.ParentID)
		return e
	})
}
func (a *MCPAccess) nodeExecutionCaller(ctx context.Context, receiver agentlink.MCPReceiver, in agentlink.MCPExecutionContext) (agentlink.MCPCaller, *protocol.Envelope, error) {
	var binding, parentID string
	var active bool
	e := a.options.Queue.DB.QueryRowContext(ctx, "SELECT binding,parent_id,active FROM mcp_node_executions WHERE connection_id=? AND execution_id=? AND task=?", receiver.ConnectionID(), in.NodeExecutionID, mcpJSON(in.Binding.Task)).Scan(&binding, &parentID, &active)
	if e != nil || !active || binding != mcpJSON(in.Binding) || in.Binding.Task.NodeID != receiver.NodeID() {
		return agentlink.MCPCaller{}, nil, agentlink.ErrFenced
	}
	var parent *protocol.Envelope
	if parentID != "" {
		var raw string
		parent = &protocol.Envelope{}
		if a.options.Queue.DB.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", parentID).Scan(&raw) != nil || mcpRead(raw, parent) != nil || parent.To != in.Binding.Actor {
			return agentlink.MCPCaller{}, nil, agentlink.ErrFenced
		}
	}
	caller, e := agentlink.NewMCPNodeCaller(receiver.ConnectionID(), receiver.NodeID(), in.Binding.Actor, &in.Binding)
	return caller, parent, e
}

func (a *MCPAccess) nodeSubmitTool() {
	a.tool("pot_receiver_node_submit", mcpContractSchema(agentlink.MCPNodeSubmit{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeSubmit
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		if !in.Actor.Valid() || in.Actor.Kind != "assistant" || in.Actor.NodeID != receiver.NodeID() {
			return nil, agentlink.ErrUnauthorized
		}
		var registration string
		if a.options.Queue.DB.QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), mcpJSON(in.Actor)).Scan(&registration) != nil {
			return nil, agentlink.ErrUnauthorized
		}
		c, e := agentlink.NewMCPNodeCaller(receiver.ConnectionID(), receiver.NodeID(), in.Actor, nil)
		if e != nil {
			return nil, e
		}
		return a.submit(ctx, c, in.Input)
	})
}

func (a *MCPAccess) registeredAssistant(ctx context.Context, r *mcp.CallToolRequest, actor protocol.AgentRef) (agentlink.MCPCaller, error) {
	receiver, e := mcpReceiver(r)
	if e != nil {
		return agentlink.MCPCaller{}, e
	}
	if !actor.Valid() || actor.Kind != "assistant" || actor.NodeID != receiver.NodeID() {
		return agentlink.MCPCaller{}, agentlink.ErrUnauthorized
	}
	var registration string
	if a.options.Queue.DB.QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), mcpJSON(actor)).Scan(&registration) != nil {
		return agentlink.MCPCaller{}, agentlink.ErrUnauthorized
	}
	return agentlink.NewMCPNodeCaller(receiver.ConnectionID(), receiver.NodeID(), actor, nil)
}
func (a *MCPAccess) nodeTaskTools() {
	a.tool("pot_receiver_node_task_get", mcpContractSchema(agentlink.MCPNodeRead{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeRead
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := a.registeredAssistant(ctx, r, in.Actor)
		if e != nil {
			return nil, e
		}
		return a.task(ctx, c, in.TaskID)
	})
	a.tool("pot_receiver_node_task_input", mcpContractSchema(agentlink.MCPNodeInput{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeInput
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := a.registeredAssistant(ctx, r, in.Actor)
		if e != nil {
			return nil, e
		}
		return a.options.Queue.Control(ctx, c, &in.Input, nil)
	})
	a.tool("pot_receiver_node_task_cancel", mcpContractSchema(agentlink.MCPNodeCancel{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeCancel
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := a.registeredAssistant(ctx, r, in.Actor)
		if e != nil {
			return nil, e
		}
		return a.cancel(ctx, c, in.Input)
	})
}
