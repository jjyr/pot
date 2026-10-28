package control

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// admitPotMCPExecution is an owning-Control transport boundary, not a model
// operation. Attestation proves the real actor; it adds no Worker capabilities.
func (s *Service) admitPotMCPExecution(ctx context.Context, id, owner string, actor protocol.AgentRef) (agentlink.MCPExecutionContext, error) {
	var result agentlink.MCPExecutionContext
	proof, e := s.localPotMCPExecution(ctx, owner, actor)
	if e != nil {
		return result, e
	}
	// Received work already has an actual claim. Never replace it with a synthetic
	// one or make its Task actor become the target assistant.
	var delivery string
	e = s.Tasks.DB().QueryRowContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? AND execution_task=? AND execution_run=?", id, actor.AgentID, proof.Binding.RunID).Scan(&delivery)
	if e == nil {
		r, e := mcpreceiver.Read(ctx, s.Tasks.DB(), id, delivery)
		if e != nil {
			return result, e
		}
		if r.Admission == nil || !r.AdmissionAcknowledged {
			return result, agentlink.ErrFenced
		}
		return agentlink.MCPExecutionContext{Claim: r.Claim, Binding: proof.Binding}, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return result, e
	}
	if e = s.endPotMCPExecutions(ctx, id); e != nil {
		return result, e
	}
	e = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		var oldID, raw, parent string
		var active bool
		e := tx.QueryRowContext(ctx, "SELECT execution_id,binding,parent_id,active FROM mcp_node_executions WHERE connection_id=? AND task=?", id, mcpJSON(proof.Binding.Task)).Scan(&oldID, &raw, &parent, &active)
		if e == nil {
			var old agentlink.MCPExecutionBinding
			if mcpRead(raw, &old) != nil {
				return agentlink.ErrFenced
			}
			if oldID == proof.ExecutionID {
				if old != proof.Binding || !active {
					return agentlink.ErrFenced
				}
				proof.PreviousRunID = ""
				return nil
			}
			if active {
				return agentlink.ErrFenced
			}
			proof.PreviousRunID = old.RunID
			_, e = tx.ExecContext(ctx, "UPDATE mcp_node_executions SET execution_id=?,binding=?,parent_id='',active=1 WHERE connection_id=? AND task=?", proof.ExecutionID, mcpJSON(proof.Binding), id, mcpJSON(proof.Binding.Task))
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		proof.PreviousRunID = ""
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_node_executions VALUES(?,?,?,?,'',1)", id, mcpJSON(proof.Binding.Task), proof.ExecutionID, mcpJSON(proof.Binding))
		return e
	})
	if e != nil {
		return result, e
	}
	var accepted agentlink.MCPNodeExecutionAdmission
	if e = s.potMCPCall(ctx, id, "pot_receiver_execution_admit", proof, &accepted); e != nil {
		return result, e
	}
	if accepted != proof {
		return result, agentlink.ErrConflict
	}
	// Recheck after network dispatch, immediately before any authorized callback.
	if _, e = s.localPotMCPExecution(ctx, owner, actor); e != nil {
		return result, e
	}
	return agentlink.MCPExecutionContext{NodeExecutionID: proof.ExecutionID, Binding: proof.Binding}, nil
}
func (s *Service) endPotMCPExecutions(ctx context.Context, id string) error {
	rows, e := s.Tasks.DB().QueryContext(ctx, "SELECT execution_id,binding FROM mcp_node_executions WHERE connection_id=? AND active=1 AND json_extract(task,'$.node_id')=?", id, s.options.RuntimeID)
	if e != nil {
		return e
	}
	var ends []agentlink.MCPNodeExecutionEnd
	for rows.Next() {
		var end agentlink.MCPNodeExecutionEnd
		var raw string
		if e = rows.Scan(&end.ExecutionID, &raw); e != nil {
			rows.Close()
			return e
		}
		if e = mcpRead(raw, &end.Binding); e != nil {
			rows.Close()
			return e
		}
		ends = append(ends, end)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, end := range ends {
		var owner string
		e = s.Tasks.DB().QueryRowContext(ctx, "SELECT assistant_id FROM tasks WHERE id=?", end.Binding.Task.AgentID).Scan(&owner)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			record, e := s.Tasks.Get(ctx, owner, end.Binding.Task.AgentID)
			if e != nil {
				return e
			}
			if record != nil && record.Execution == nil && record.RunID == end.Binding.RunID && record.Info.Phase.Scope() == "active" && record.StopMode == "" {
				continue
			}
		}
		var accepted agentlink.MCPNodeExecutionEnd
		if e = s.potMCPCall(ctx, id, "pot_receiver_execution_end", end, &accepted); e != nil {
			return e
		}
		if accepted != end {
			return agentlink.ErrConflict
		}
		if _, e = s.Tasks.DB().ExecContext(ctx, "UPDATE mcp_node_executions SET active=0 WHERE connection_id=? AND execution_id=? AND binding=?", id, end.ExecutionID, mcpJSON(end.Binding)); e != nil {
			return e
		}
	}
	return nil
}
