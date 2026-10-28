package control

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

type mcpOriginalHumanKey struct{}

// This runs in the submission intent transaction. An exact retry retains its
// original human; a prior submission without provenance cannot acquire it later.
func bindMCPOriginalHumanTx(ctx context.Context, tx *sql.Tx, caller agentlink.MCPCaller, request string, existing bool) error {
	human, supplied := ctx.Value(mcpOriginalHumanKey{}).(protocol.AgentRef)
	if !supplied {
		return nil
	}
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT human FROM mcp_task_humans WHERE connection_id=? AND actor=? AND request_id=?", caller.ConnectionID(), mcpJSON(caller.Actor()), request).Scan(&raw)
	if err == nil {
		if raw != mcpJSON(human) {
			return agentlink.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existing {
		return agentlink.ErrUnauthorized
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO mcp_task_humans VALUES(?,?,?,?)", caller.ConnectionID(), mcpJSON(caller.Actor()), request, mcpJSON(human))
	return err
}

func (s *Service) originalMCPHuman(ctx context.Context, receiver agentlink.MCPReceiver, caller agentlink.MCPCaller, request string) (protocol.AgentRef, error) {
	if caller.Actor().Kind != "user" {
		return protocol.AgentRef{NodeID: receiver.NodeID(), Kind: "user", AgentID: linkChatID(receiver.ConnectionID(), "operator")}, nil
	}
	var raw string
	var human protocol.AgentRef
	if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT human FROM mcp_task_humans WHERE connection_id=? AND actor=? AND request_id=?", receiver.ConnectionID(), mcpJSON(caller.Actor()), request).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return human, agentlink.ErrUnauthorized
		}
		return human, err
	}
	if mcpRead(raw, &human) != nil || !human.Valid() || human.Kind != "user" || human.NodeID != receiver.NodeID() {
		return protocol.AgentRef{}, agentlink.ErrUnauthorized
	}
	return human, nil
}
