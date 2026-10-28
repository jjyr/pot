package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (a *MCPAccess) nativeUserTools() {
	a.tool("pot_receiver_user_poll", mcpSchema(map[string]any{}), func(ctx context.Context, r *mcp.CallToolRequest, _ json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		return a.nativeUserPoll(ctx, receiver)
	})
	a.tool("pot_receiver_user_result", mcpContractSchema(agentlink.MCPNativeUserResult{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		var result agentlink.MCPNativeUserResult
		if e = agentlink.DecodeMCPContract(raw, &result); e != nil {
			return nil, e
		}
		e = a.nativeUserResult(ctx, receiver, result)
		return map[string]bool{"accepted": e == nil}, e
	})
}
func (a *MCPAccess) nativeUserPoll(ctx context.Context, receiver agentlink.MCPReceiver) ([]agentlink.MCPNativeUserRequest, error) {
	result := []agentlink.MCPNativeUserRequest{}
	if e := a.options.OAuth.CheckConnection(ctx, receiver.ConnectionID()); e != nil {
		return nil, e
	}
	e := storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		// Profile avatars can fill most of one payload. Keep each operation atomic
		// without combining several individually valid requests above the wire limit.
		rows, e := tx.QueryContext(ctx, `SELECT o.id,o.request FROM mcp_native_user_operations o JOIN mcp_receiver_endpoints r ON r.connection_id=o.connection_id AND r.address=o.address WHERE o.connection_id=? AND o.state IN ('pending','claimed') AND json_extract(r.registration,'$.epoch')=o.epoch ORDER BY o.created_at LIMIT 1`, receiver.ConnectionID())
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id, raw string
			var request agentlink.MCPNativeUserRequest
			if e = rows.Scan(&id, &raw); e != nil {
				rows.Close()
				return e
			}
			if e = mcpRead(raw, &request); e != nil {
				rows.Close()
				return e
			}
			if request.Target.NodeID != receiver.NodeID() {
				rows.Close()
				return agentlink.ErrUnauthorized
			}
			ids = append(ids, id)
			result = append(result, request)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			if _, e = tx.ExecContext(ctx, "UPDATE mcp_native_user_operations SET state='claimed' WHERE id=? AND state='pending'", id); e != nil {
				return e
			}
		}
		return nil
	})
	return result, e
}
func (a *MCPAccess) nativeUserResult(ctx context.Context, receiver agentlink.MCPReceiver, result agentlink.MCPNativeUserResult) error {
	if e := a.options.OAuth.CheckConnection(ctx, receiver.ConnectionID()); e != nil {
		return e
	}
	return storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		var raw, state, prior, address, epoch string
		e := tx.QueryRowContext(ctx, "SELECT request,state,result,address,epoch FROM mcp_native_user_operations WHERE id=? AND connection_id=?", result.ID, receiver.ConnectionID()).Scan(&raw, &state, &prior, &address, &epoch)
		if e != nil {
			return agentlink.ErrUnauthorized
		}
		var request agentlink.MCPNativeUserRequest
		if e = mcpRead(raw, &request); e != nil {
			return e
		}
		if request.Target.NodeID != receiver.NodeID() || epoch != result.Epoch {
			return agentlink.ErrFenced
		}
		if state == "finished" {
			if prior == mcpJSON(result) {
				return nil
			}
			return agentlink.ErrConflict
		}
		if state != "claimed" {
			return agentlink.ErrFenced
		}
		var current string
		if e = tx.QueryRowContext(ctx, "SELECT json_extract(registration,'$.epoch') FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), address).Scan(&current); e != nil || current != epoch {
			return agentlink.ErrFenced
		}
		if result.Error == "" {
			if request.Management != nil {
				if result.Management == nil || result.Profile != nil || result.Deleted != nil || request.Management.Action == "catalog" && result.Management.SchemaVersion != 1 {
					return agentlink.ErrConflict
				}
				if request.Management.Action != "catalog" {
					assistant := result.Management.Assistant
					if assistant == nil || assistant.ID != request.Management.ID || assistant.Address != agentlink.AssistantTarget(receiver.NodeID(), request.Management.ID) {
						return agentlink.ErrConflict
					}
				}
				for _, assistant := range result.Management.Assistants {
					if assistant.Address.NodeID != receiver.NodeID() || assistant.Address.Kind != "assistant" || assistant.Address.AgentID != assistant.ID {
						return agentlink.ErrConflict
					}
				}
			} else if request.Profile != nil {
				if result.Profile == nil || result.Deleted != nil || result.Profile.ID != request.Target.AgentID {
					return agentlink.ErrConflict
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO mcp_native_user_profiles(connection_id,address,profile) VALUES(?,?,?) ON CONFLICT(connection_id,address) DO UPDATE SET profile=excluded.profile`, receiver.ConnectionID(), address, mcpJSON(result.Profile)); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `UPDATE mcp_receiver_endpoints SET registration=json_set(registration,'$.receiver.name',?,'$.receiver.avatar',?) WHERE connection_id=? AND address=?`, result.Profile.Name, result.Profile.Avatar, receiver.ConnectionID(), address); e != nil {
					return e
				}
			} else {
				if result.Deleted == nil || result.Profile != nil || !result.Deleted.Deleted || result.Deleted.ID != request.Target.AgentID {
					return agentlink.ErrConflict
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO mcp_native_user_profiles(connection_id,address,deleted) VALUES(?,?,1) ON CONFLICT(connection_id,address) DO UPDATE SET deleted=1`, receiver.ConnectionID(), address); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, "DELETE FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), address); e != nil {
					return e
				}
			}
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_native_user_operations SET state='finished',result=? WHERE id=? AND state='claimed'", mcpJSON(result), result.ID)
		return e
	})
}

// nativeUserCall is only called by the existing authenticated human UI dispatcher.
// It supplies From itself; no model/OAuth tool can enqueue these operations.
type nativeHumanKey struct{}

// CallHumanAgent is the original authenticated human API entry. MCP/model tools
// call the collaboration port and cannot manufacture its private context key.
func (s *Service) CallHumanAgent(ctx context.Context, owner string, op agentlink.Operation, call agentlink.Call) (any, error) {
	return s.CallAgent(context.WithValue(ctx, nativeHumanKey{}, true), owner, op, call)
}
func (s *Service) nativeUserCall(ctx context.Context, link string, target protocol.AgentRef, profile *agentlink.ProfileRequest, deletion *agentlink.DeleteAssistantRequest, management *agentlink.AssistantManagementRequest) (any, error) {
	if human, _ := ctx.Value(nativeHumanKey{}).(bool); !human {
		return nil, agentlink.ErrUnauthorized
	}
	a := s.localPotMCPAccess()
	count := 0
	if profile != nil {
		count++
	}
	if deletion != nil {
		count++
	}
	if management != nil {
		count++
	}
	if a == nil || count != 1 {
		return nil, agentlink.ErrUnsupported
	}
	c, e := s.inboundPotMCPConnection(ctx, link)
	if e != nil || c.State != "active" || c.Directory == nil {
		return nil, agentlink.ErrUnavailable
	}
	op := agentlink.AssistantProfile
	if deletion != nil {
		op = agentlink.AssistantDelete
	}
	if management != nil {
		op = agentlink.AssistantManagement
	}
	if !c.Directory.Capabilities(target).Supports(op) {
		return nil, agentlink.ErrUnsupported
	}
	var registration agentlink.MCPReceiverRegistration
	var raw string
	if e = s.Tasks.DB().QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", link, mcpJSON(target)).Scan(&raw); e != nil {
		return nil, agentlink.ErrNotFound
	}
	if e = mcpRead(raw, &registration); e != nil {
		return nil, e
	}
	if registration.Epoch == "" {
		return nil, agentlink.ErrUnsupported
	}
	human := protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: linkChatID(link, target.AgentID)}
	if profile != nil {
		profile.From = human
		profile.AssistantID = target.AgentID
	} else if deletion != nil {
		deletion.From = human
		deletion.AssistantID = target.AgentID
	}
	if management != nil {
		management.From = human
	}
	id := rand.Text()
	request := agentlink.MCPNativeUserRequest{Version: 1, ID: id, Connection: link, Epoch: registration.Epoch, Target: target, Profile: profile, Delete: deletion, Management: management}
	raw = mcpJSON(request)
	e = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		var prior string
		e := tx.QueryRowContext(ctx, "SELECT request FROM mcp_native_user_operations WHERE id=?", id).Scan(&prior)
		if e == nil {
			if prior != raw {
				return agentlink.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_native_user_operations(id,connection_id,address,epoch,request,state,created_at) VALUES(?,?,?,?,?,'pending',?)", id, link, mcpJSON(target), registration.Epoch, raw, time.Now().UnixMilli())
		return e
	})
	if e != nil {
		return nil, e
	}
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	for {
		var state, result string
		if e = s.Tasks.DB().QueryRowContext(ctx, "SELECT state,result FROM mcp_native_user_operations WHERE id=?", id).Scan(&state, &result); e != nil {
			return nil, e
		}
		if state == "unknown" {
			return nil, agentlink.ErrFenced
		}
		if state == "finished" {
			var response agentlink.MCPNativeUserResult
			if e = mcpRead(result, &response); e != nil {
				return nil, e
			}
			if response.Error != "" {
				return nil, nativeUserError(response.Error)
			}
			s.notifyMCPConnections()
			if profile != nil {
				return *response.Profile, nil
			}
			if management != nil {
				return *response.Management, nil
			}
			return *response.Deleted, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, agentlink.ErrUnavailable
		case <-timer.C:
		}
	}
}
func nativeUserError(code string) error {
	for _, e := range []error{agentlink.ErrModelUnavailable, agentlink.ErrWorkspaceUnavailable, agentlink.ErrCapacity, agentlink.ErrAssistantBusy, agentlink.ErrAssistantChanged, agentlink.ErrConflict, agentlink.ErrInvalid, agentlink.ErrNotFound, agentlink.ErrUnauthorized, agentlink.ErrFenced, agentlink.ErrUnsupported} {
		if e.Error() == code {
			return e
		}
	}
	return agentlink.ErrUnavailable
}
