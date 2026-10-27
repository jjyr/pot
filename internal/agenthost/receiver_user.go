package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
)

func (h *agentHost) receiverUserStep(ctx context.Context, client *receiverClient, identity agentlink.MCPReceiver) error {
	var requests []agentlink.MCPNativeUserRequest
	if e := client.call(ctx, "pot_receiver_user_poll", struct{}{}, &requests); e != nil {
		return e
	}
	for _, request := range requests {
		result, e := h.executeNativeUser(ctx, identity, request)
		if e != nil {
			return e
		}
		if result.Error == "" && request.Management != nil {
			if e = h.registerReceiverAssistants(ctx, client); e != nil {
				return e
			}
		}
		if e = client.call(ctx, "pot_receiver_user_result", result, new(any)); e != nil {
			return e
		}
	}
	return nil
}
func (h *agentHost) executeNativeUser(ctx context.Context, identity agentlink.MCPReceiver, request agentlink.MCPNativeUserRequest) (agentlink.MCPNativeUserResult, error) {
	result := agentlink.MCPNativeUserResult{ID: request.ID, Epoch: request.Epoch}
	count := 0
	if request.Profile != nil {
		count++
	}
	if request.Delete != nil {
		count++
	}
	if request.Management != nil {
		count++
	}
	if request.Version != 1 || request.Connection != identity.ConnectionID() || request.Epoch != h.receiverEpoch || request.Target.NodeID != h.backend.nodeID || count != 1 || request.Management == nil && request.Target.Kind != "assistant" || request.Management != nil && request.Target != agentlink.NodeTarget(h.backend.nodeID) {
		return result, agentlink.ErrUnauthorized
	}
	if request.Profile != nil {
		r := request.Profile
		if r.From.Kind != "user" || !r.From.Valid() || r.From.NodeID == h.backend.nodeID || r.AssistantID != request.Target.AgentID {
			return result, agentlink.ErrUnauthorized
		}
	} else if request.Delete != nil {
		r := request.Delete
		if r.From.Kind != "user" || !r.From.Valid() || r.From.NodeID == h.backend.nodeID || r.AssistantID != request.Target.AgentID {
			return result, agentlink.ErrUnauthorized
		}
	}
	if r := request.Management; r != nil && (r.From.Kind != "user" || !r.From.Valid() || r.From.NodeID == h.backend.nodeID) {
		return result, agentlink.ErrUnauthorized
	}
	raw, _ := json.Marshal(request)
	execute := false
	e := storage.Transaction(ctx, h.tasks, func(tx *sql.Tx) error {
		var prior, state, saved string
		e := tx.QueryRowContext(ctx, "SELECT request,state,result FROM mcp_native_user_journal WHERE id=? AND connection_id=?", request.ID, identity.ConnectionID()).Scan(&prior, &state, &saved)
		if e == nil {
			if prior != string(raw) {
				return agentlink.ErrConflict
			}
			if state == "finished" {
				return json.Unmarshal([]byte(saved), &result)
			}
			result.Error = agentlink.ErrFenced.Error()
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_native_user_journal(id,connection_id,request,state) VALUES(?,?,?,'executing')", request.ID, identity.ConnectionID(), string(raw))
		execute = e == nil
		return e
	})
	if e != nil || !execute {
		return result, e
	}
	// Marking executing precedes the existing backend transaction. A crash in the
	// gap is unknown and never replayed; saved finished responses reconcile loss.
	if request.Profile != nil {
		profile, err := h.backend.Profile(ctx, *request.Profile)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Profile = &profile
		}
	} else if request.Delete != nil {
		if err := h.backend.DeleteAssistant(ctx, *request.Delete); err != nil {
			result.Error = err.Error()
		} else {
			result.Deleted = &agentlink.AssistantDeletion{ID: request.Target.AgentID, Deleted: true}
		}
	}
	if request.Management != nil {
		value, err := h.backend.ManageAssistant(ctx, *request.Management)
		if err != nil {
			result.Error = err.Error()
		} else {
			value.SchemaVersion = 1
			if value.Actions == nil {
				value.Actions = []string{}
			}
			if value.Fields == nil {
				value.Fields = []agentlink.ManagementField{}
			}
			if value.Models == nil {
				value.Models = []agentlink.ExecutionModel{}
			}
			if value.Workspaces == nil {
				value.Workspaces = []agentlink.ExecutionWorkspace{}
			}
			if value.Assistants == nil {
				value.Assistants = []agentlink.ManagedAssistant{}
			}
			result.Management = &value
		}
	}
	encoded, _ := json.Marshal(result)
	_, e = h.tasks.ExecContext(ctx, "UPDATE mcp_native_user_journal SET state='finished',result=? WHERE id=? AND connection_id=? AND state='executing'", string(encoded), request.ID, identity.ConnectionID())
	return result, e
}
