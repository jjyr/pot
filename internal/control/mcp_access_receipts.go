package control

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jjyr/pot/pkg/agentlink"
)

type MCPAccessReceiptInput struct {
	Operation agentlink.Operation `json:"operation" required:"true"`
	RequestID string              `json:"request_id" required:"true"`
}
type MCPAccessReceipt struct {
	InputState string                     `json:"input_state,omitempty"`
	State      string                     `json:"state"`
	Acceptance *MCPTaskAcceptance         `json:"acceptance,omitempty"`
	Ack        *agentlink.MCPDeliveryAck  `json:"ack,omitempty"`
	Input      *agentlink.MCPInputReceipt `json:"input,omitempty"`
	Error      *agentlink.MCPError        `json:"error,omitempty"`
}

// mutationReceipt reads a single owned business mutation, never a generic
// operation dispatcher. Pending means reconcile, not authorization to dispatch.
func (a *MCPAccess) mutationReceipt(ctx context.Context, c agentlink.MCPCaller, input MCPAccessReceiptInput) (result MCPAccessReceipt, err error) {
	switch input.Operation {
	case agentlink.TaskSubmit, agentlink.TaskInput, agentlink.TaskCancel:
	default:
		return result, agentlink.ErrInvalid
	}
	var raw string
	err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT receipt FROM mcp_service_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?`, c.ConnectionID(), mcpJSON(c.Actor()), input.Operation, input.RequestID).Scan(&raw)
	if err == nil {
		result.State = "accepted"
		result.Acceptance = &MCPTaskAcceptance{}
		if err = mcpRead(raw, result.Acceptance); err != nil {
			return result, err
		}
		var ack string
		switch input.Operation {
		case agentlink.TaskSubmit:
			err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT ack FROM mcp_service_deliveries WHERE task_id=? AND work!=''`, result.Acceptance.Task.ID).Scan(&ack)
		case agentlink.TaskInput:
			err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT ack FROM mcp_service_deliveries WHERE task_id=? AND control!='' AND json_extract(control,'$.input.request_id')=?`, result.Acceptance.Task.ID, input.RequestID).Scan(&ack)
		case agentlink.TaskCancel:
			err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT ack FROM mcp_service_deliveries WHERE task_id=? AND control!='' AND json_extract(control,'$.cancel.request_id')=?`, result.Acceptance.Task.ID, input.RequestID).Scan(&ack)
		}
		if err != nil {
			return result, mcpMissing(err)
		}
		if ack != "" {
			result.Ack = &agentlink.MCPDeliveryAck{}
			err = mcpRead(ack, result.Ack)
		}
		if err == nil && input.Operation == agentlink.TaskInput {
			var disposition string
			err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT input_receipt FROM mcp_service_deliveries WHERE task_id=? AND control!='' AND json_extract(control,'$.input.request_id')=?`, result.Acceptance.Task.ID, input.RequestID).Scan(&disposition)
			if err == nil && disposition != "" {
				result.Input = &agentlink.MCPInputReceipt{}
				err = mcpRead(disposition, result.Input)
			}
		}
		if err == nil && input.Operation == agentlink.TaskInput {
			result.InputState = "pending"
			if result.Ack != nil && !result.Ack.Accepted {
				result.InputState = "rejected"
			}
			if result.Input != nil {
				result.InputState = result.Input.State
			}
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT state,receipt FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?`, c.ConnectionID(), mcpJSON(c.Actor()), input.Operation, input.RequestID).Scan(&result.State, &raw)
	if err != nil {
		return result, mcpMissing(err)
	}
	if result.State == "accepted" {
		result.Acceptance = &MCPTaskAcceptance{}
		err = mcpRead(raw, result.Acceptance)
	} else if result.State == "rejected" {
		result.Error = &agentlink.MCPError{}
		err = mcpRead(raw, result.Error)
	} else {
		result.Error = &agentlink.MCPError{Code: agentlink.MCPUnknown}
	}
	return
}
