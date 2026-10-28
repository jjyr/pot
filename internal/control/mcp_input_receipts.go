package control

import (
	"context"
	"database/sql"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
)

// InputReceipt preserves a mailbox disposition separately from receiver ACK,
// execution admission and terminal results. Reconciliation never executes input.
func (q *MCPAccessQueue) InputReceipt(ctx context.Context, receiver agentlink.MCPReceiver, receipt agentlink.MCPInputReceipt) error {
	q.dispatchMu.Lock()
	defer q.dispatchMu.Unlock()
	if receipt.Validate() != nil {
		return agentlink.ErrInvalid
	}
	if q.CheckConnection != nil {
		if err := q.CheckConnection(ctx, receiver.ConnectionID()); err != nil {
			return err
		}
	}
	return storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		delivery, err := loadMCPDelivery(ctx, tx, receiver, receipt.Claim)
		if err != nil {
			return err
		}
		if !acceptedMCPDelivery(delivery) || delivery.control == "" {
			return agentlink.ErrFenced
		}
		var saved string
		if err = tx.QueryRowContext(ctx, "SELECT input_receipt FROM mcp_service_deliveries WHERE id=?", receipt.Claim.DeliveryID).Scan(&saved); err != nil {
			return err
		}
		if saved != "" {
			var previous agentlink.MCPInputReceipt
			if mcpRead(saved, &previous) != nil || previous != receipt {
				return agentlink.ErrConflict
			}
			return nil
		}
		var control agentlink.MCPControlDelivery
		if mcpRead(delivery.control, &control) != nil || control.Input == nil || control.Binding.Task != receipt.Binding.Task || control.Binding.Receiver != receipt.Binding.Receiver {
			return agentlink.ErrFenced
		}
		if receipt.State == "consumed" {
			var admission agentlink.MCPRunAdmission
			if delivery.admission == "" || mcpRead(delivery.admission, &admission) != nil || admission.Binding != receipt.Binding {
				return agentlink.ErrFenced
			}
		} else {
			var execution, taskRaw string
			if err = tx.QueryRowContext(ctx, "SELECT execution,task FROM mcp_service_tasks WHERE id=?", receipt.Claim.Binding.Task.AgentID).Scan(&execution, &taskRaw); err != nil {
				return err
			}
			var current agentlink.MCPRunBinding
			var task agentlink.Task
			if delivery.admission != "" || mcpRead(execution, &current) != nil || current != receipt.Binding || mcpRead(taskRaw, &task) != nil || task.State != "cancelled" || control.Binding.RunID == current.RunID && control.InputPhase.Scope() != "active" {
				return agentlink.ErrFenced
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE mcp_service_deliveries SET input_receipt=? WHERE id=?", mcpJSON(receipt), receipt.Claim.DeliveryID)
		return err
	})
}
