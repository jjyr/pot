package agenthost

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
)

func (h *agentHost) executionCall(ctx context.Context, in executionToolRequest) (any, error) {
	binding, err := h.executionContext(ctx, executionContextInput{JobID: in.JobID, RunID: in.RunID, FromAssistantID: in.AssistantID})
	if err != nil {
		return nil, err
	}
	if binding.Actor.Kind == "task" && (in.Tool == "pot_task_submit" || in.Tool == "pot_message") {
		return nil, agentlink.ErrUnauthorized
	}
	var connection, delivery string
	if err = h.tasks.QueryRowContext(ctx, "SELECT connection_id,delivery_id FROM mcp_receiver_journal WHERE execution_task=? AND execution_run=?", in.JobID, in.RunID).Scan(&connection, &delivery); err != nil {
		return nil, agentlink.ErrFenced
	}
	r, err := readReceiverRecord(ctx, h.tasks, connection, delivery)
	if err != nil {
		return nil, err
	}
	if r.Admission == nil || !r.AdmissionAcknowledged || !r.Ack.Accepted || r.Admission.Binding.Task != binding.Task || r.Admission.Binding.RunID != binding.RunID {
		return nil, agentlink.ErrFenced
	}
	trusted := agentlink.MCPExecutionContext{Claim: r.Claim, Binding: binding}
	// Decode before opening transport. Caller, execution, owner and credentials
	// are absent from every model DTO and strict decoding rejects such fields.
	var name string
	var input any
	switch in.Tool {
	case "pot_directory":
		var v struct{}
		if agentlink.DecodeMCPContract(in.Input, &v) != nil {
			return nil, agentlink.ErrInvalid
		}
		name = "pot_receiver_directory"
		input = trusted
	case "pot_message":
		var v agentlink.MCPMessageInput
		if agentlink.DecodeMCPContract(in.Input, &v) != nil {
			return nil, agentlink.ErrInvalid
		}
		name = "pot_receiver_message"
		input = agentlink.MCPExecutionMessage{Context: trusted, Input: v}
	case "pot_task_submit":
		var v agentlink.MCPSubmitInput
		if agentlink.DecodeMCPContract(in.Input, &v) != nil {
			return nil, agentlink.ErrInvalid
		}
		name = "pot_receiver_task_submit"
		input = agentlink.MCPExecutionSubmit{Context: trusted, Input: v}
	case "pot_task_read":
		var v struct {
			TaskID string `json:"task_id" required:"true"`
		}
		if agentlink.DecodeMCPContract(in.Input, &v) != nil {
			return nil, agentlink.ErrInvalid
		}
		name = "pot_receiver_task_read"
		input = agentlink.MCPExecutionRead{Context: trusted, TaskID: v.TaskID}
	case "pot_task_input":
		var v agentlink.MCPTaskInput
		if agentlink.DecodeMCPContract(in.Input, &v) != nil {
			return nil, agentlink.ErrInvalid
		}
		name = "pot_receiver_task_input"
		input = agentlink.MCPExecutionInput{Context: trusted, Input: v}
	case "pot_task_cancel":
		var v agentlink.MCPCancelInput
		if agentlink.DecodeMCPContract(in.Input, &v) != nil {
			return nil, agentlink.ErrInvalid
		}
		name = "pot_receiver_task_cancel"
		input = agentlink.MCPExecutionCancel{Context: trusted, Input: v}
	default:
		return nil, agentlink.ErrUnsupported
	}
	client, err := h.reverseClient(ctx, connection)
	if err != nil {
		return nil, err
	}
	defer client.close()
	// Check the active native run again after transport establishment.
	if _, err = h.executionContext(ctx, executionContextInput{JobID: in.JobID, RunID: in.RunID, FromAssistantID: in.AssistantID}); err != nil {
		return nil, err
	}
	var output any
	if err = client.call(ctx, name, input, &output); err != nil {
		return nil, err
	}
	return output, nil
}
func (h *agentHost) reverseClient(ctx context.Context, connection string) (*receiverClient, error) {
	var endpoint, node, ref, state string
	if err := h.db.QueryRowContext(ctx, "SELECT url,node_id,secret_ref,state FROM mcp_receiver_connections WHERE id=?", connection).Scan(&endpoint, &node, &ref, &state); err != nil {
		return nil, err
	}
	if node != h.backend.nodeID || state != "active" {
		return nil, agentlink.ErrUnauthorized
	}
	raw, err := h.secrets.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var token receiverToken
	if json.Unmarshal(raw, &token) != nil || token.SchemaVersion != 1 {
		return nil, agentlink.ErrUnauthorized
	}
	identity, err := agentlink.NewMCPReceiver(connection, node)
	if err != nil {
		return nil, err
	}
	return connectReceiver(ctx, endpoint, token.AccessToken, identity)
}
