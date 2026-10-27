package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (a *MCPAccess) nodeMailTool() {
	a.tool("pot_receiver_node_mail", mcpContractSchema(agentlink.MCPNodeMail{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var in agentlink.MCPNodeMail
		if agentlink.DecodeMCPContract(raw, &in) != nil {
			return nil, agentlink.ErrInvalid
		}
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		return a.nodeMail(ctx, receiver, in)
	})
}
func (a *MCPAccess) nodeMail(ctx context.Context, receiver agentlink.MCPReceiver, in agentlink.MCPNodeMail) (acceptance MCPAccessMessageAcceptance, err error) {
	message := in.Message
	if a.options.Local == nil {
		return acceptance, agentlink.ErrUnsupported
	}
	if message.Validate() != nil || message.Kind != "message" || message.From.Kind != "assistant" || message.From.NodeID != receiver.NodeID() || message.To.NodeID != a.options.Queue.NodeID {
		return acceptance, agentlink.ErrUnauthorized
	}
	err = storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		var registrationRaw string
		if e := tx.QueryRowContext(ctx, "SELECT registration FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", receiver.ConnectionID(), mcpJSON(message.From)).Scan(&registrationRaw); e != nil {
			return agentlink.ErrUnauthorized
		}
		var registration agentlink.MCPReceiverRegistration
		if mcpRead(registrationRaw, &registration) != nil || !registration.Capabilities.Supports(agentlink.ChatSend) {
			return agentlink.ErrUnsupported
		}
		digest, e := core.HashJSON(in)
		if e != nil {
			return e
		}
		var old, raw string
		e = tx.QueryRowContext(ctx, "SELECT digest,receipt FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?", receiver.ConnectionID(), mcpJSON(message.From), agentlink.ChatSend, message.ID).Scan(&old, &raw)
		if e == nil {
			if old != digest {
				return agentlink.ErrConflict
			}
			if e = mcpRead(raw, &acceptance); e != nil {
				return e
			}
			acceptance.Receipt.Duplicate = true
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		if in.ProcessingParentID != "" {
			var parentRaw string
			var parent protocol.Envelope
			if e = tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", in.ProcessingParentID).Scan(&parentRaw); e != nil {
				return agentlink.ErrUnauthorized
			}
			if mcpRead(parentRaw, &parent) != nil || parent.To != message.From {
				return agentlink.ErrUnauthorized
			}
			hops, e := protocol.NextMailHop(parent.Hops)
			if e != nil || message.Hops != hops {
				return agentlink.ErrFenced
			}
			if message.To == parent.From {
				if message.ReplyTo == nil || *message.ReplyTo != parent.ID || message.ConversationID != parent.ConversationID {
					return agentlink.ErrUnauthorized
				}
			} else if message.ReplyTo != nil {
				return agentlink.ErrUnauthorized
			}
		} else if message.Hops != 0 || message.ReplyTo != nil {
			return agentlink.ErrUnauthorized
		}
		if e = a.options.Local.Mailbox.DeliverTx(ctx, tx, message); e != nil {
			return e
		}
		acceptance = MCPAccessMessageAcceptance{Receipt: agentlink.MCPMutationReceipt{RequestID: message.ID, Digest: digest}, Message: protocol.Receipt{Message: message}}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_local_mutations VALUES(?,?,?,?,?,?,'accepted',?)", receiver.ConnectionID(), mcpJSON(message.From), agentlink.ChatSend, message.ID, digest, mcpJSON(in), mcpJSON(acceptance))
		return e
	})
	if err == nil {
		a.options.Local.Mailbox.Changes.Notify("assistant-mail")
		a.options.Local.wakeTasks()
	}
	return
}
