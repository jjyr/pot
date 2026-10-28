package mcpreceiver

import (
	"context"
	"database/sql"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

// InputPredecessor resolves the current execution only after mailbox admission.
// The captured run remains immutable provenance; consumption follows the same
// Task's actual run chain. A nil predecessor means wait without rejecting input.
func InputPredecessor(ctx context.Context, tx *sql.Tx, input *agentlink.MCPReceiverRecord, current agentlink.MCPRunBinding) (*agentlink.MCPReceiverRecord, error) {
	control := input.Control
	if control == nil || control.Input == nil || current.Task != control.Binding.Task || current.Receiver != control.Binding.Receiver {
		return nil, agentlink.ErrFenced
	}
	var earlier int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_receiver_journal WHERE connection_id=? AND rowid<(SELECT rowid FROM mcp_receiver_journal WHERE connection_id=? AND delivery_id=?) AND json_extract(body,'$.claim.binding.task.agent_id')=? AND json_type(body,'$.control.input')='object' AND json_extract(body,'$.ack.accepted')=1 AND json_type(body,'$.admission') IS NULL AND COALESCE(json_extract(body,'$.control_applied'),0)=0`, input.Claim.ConnectionID, input.Claim.ConnectionID, input.Claim.DeliveryID, input.Claim.Binding.Task.AgentID).Scan(&earlier); err != nil {
		return nil, err
	}
	if earlier != 0 {
		return nil, nil
	}
	var delivery string
	if err := tx.QueryRowContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? AND execution_task=? AND execution_run=?", input.Claim.ConnectionID, current.Task.AgentID, current.RunID).Scan(&delivery); err != nil {
		return nil, err
	}
	prior, err := Read(ctx, tx, input.Claim.ConnectionID, delivery)
	if err != nil {
		return nil, err
	}
	if prior.Admission == nil || prior.Admission.Binding != current || prior.Claim.Binding != input.Claim.Binding {
		return nil, agentlink.ErrFenced
	}
	caller := prior.Control
	if prior.Work != nil {
		if prior.Work.Request.From != control.Caller {
			return nil, agentlink.ErrUnauthorized
		}
	} else if caller == nil || caller.Caller != control.Caller {
		return nil, agentlink.ErrUnauthorized
	}
	if prior.Outbox != nil || prior.Receipt == nil || !terminal(prior.LastPhase) {
		return nil, nil
	}
	return &prior, nil
}

func terminal(phase protocol.TaskPhase) bool {
	return phase == protocol.TaskComplete || phase == protocol.TaskFailed || phase == protocol.TaskCancelled || phase == protocol.TaskInterrupted
}
