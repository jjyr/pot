package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"time"
)

// Correspondence uses the receiver delivery journal and the existing message
// backend. The Mailbox envelope remains the causal authority; delivery alone
// creates no business Task in the origin catalogue.
func (q *MCPAccessQueue) enqueueMessageTx(ctx context.Context, tx *sql.Tx, c agentlink.MCPCaller, input MCPAccessMessageInput, message protocol.Envelope, digest string) error {
	var connection, raw string
	if e := tx.QueryRowContext(ctx, "SELECT connection_id,registration FROM mcp_receiver_endpoints WHERE address=?", mcpJSON(message.To)).Scan(&connection, &raw); e != nil {
		return mcpMissing(e)
	}
	if !q.ReceiverOnline(connection) {
		return agentlink.ErrUnavailable
	}
	var registration agentlink.MCPReceiverRegistration
	if mcpRead(raw, &registration) != nil || !registration.Capabilities.Supports(agentlink.ChatSend) {
		return agentlink.ErrUnsupported
	}
	now := time.Now().UTC()
	id := rand.Text()
	request := agentlink.TaskRequest{From: c.Actor(), Target: message.To, RequestID: input.RequestID, Text: message.Text}
	task := agentlink.Task{ID: id, RequestID: input.RequestID, Receiver: registration.Receiver, State: "queued", CreatedAt: now, UpdatedAt: now}
	work := agentlink.MCPWorkDelivery{Version: agentlink.MCPContractVersion, DeliveryID: rand.Text(), Binding: agentlink.MCPDispatchBinding{Task: protocol.AgentRef{NodeID: q.NodeID, Kind: "task", AgentID: id}, Receiver: message.To}, Request: request, Message: &message}
	if e := work.Validate(); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO mcp_service_tasks(id,connection_id,actor,request_id,digest,submission,task) VALUES(?,?,?,?,?,?,?)`, id, c.ConnectionID(), mcpJSON(c.Actor()), input.RequestID, digest, mcpJSON(input), mcpJSON(task)); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO mcp_service_deliveries(id,task_id,receiver_connection,receiver_address,work) VALUES(?,?,?,?,?)`, work.DeliveryID, id, connection, mcpJSON(message.To), mcpJSON(work))
	return e
}

// enqueueLocalMCPMail forwards only an immutable envelope already committed by
// this owning Control. Callers cannot replace its sender or processing parent.
func (a *MCPAccess) enqueueLocalMCPMail(ctx context.Context, connection string, message protocol.Envelope) error {
	if a.options.Local == nil || message.From.NodeID != a.options.Queue.NodeID || message.From.Kind != "assistant" || message.Validate() != nil {
		return agentlink.ErrUnauthorized
	}
	c, e := agentlink.NewMCPNodeCaller(connection, a.options.Queue.NodeID, message.From, nil)
	if e != nil {
		return e
	}
	digest, e := core.HashJSON(message)
	if e != nil {
		return e
	}
	return storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		var saved, actualConnection string
		if e := tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", message.ID).Scan(&saved); e != nil || saved != mcpJSON(message) {
			return agentlink.ErrUnauthorized
		}
		if e := tx.QueryRowContext(ctx, "SELECT connection_id FROM mcp_receiver_endpoints WHERE address=?", mcpJSON(message.To)).Scan(&actualConnection); e != nil || actualConnection != connection {
			return agentlink.ErrUnauthorized
		}
		var old string
		e := tx.QueryRowContext(ctx, "SELECT digest FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?", connection, mcpJSON(message.From), agentlink.ChatSend, message.ID).Scan(&old)
		if e == nil {
			if old != digest {
				return agentlink.ErrConflict
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		input := MCPAccessMessageInput{RequestID: message.ID, Message: protocol.SendMail{To: message.To, Text: message.Text}}
		if e = a.options.Queue.enqueueMessageTx(ctx, tx, c, input, message, digest); e != nil {
			return e
		}
		receipt := MCPAccessMessageAcceptance{Receipt: agentlink.MCPMutationReceipt{RequestID: message.ID, Digest: digest}, Message: protocol.Receipt{Message: message}}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_local_mutations VALUES(?,?,?,?,?,?,'accepted',?)", connection, mcpJSON(message.From), agentlink.ChatSend, message.ID, digest, mcpJSON(input), mcpJSON(receipt))
		return e
	})
}

func (a *MCPAccess) messageResultTx(ctx context.Context, tx *sql.Tx, work agentlink.MCPWorkDelivery, event agentlink.MCPExecutionEvent) error {
	if a.options.Local == nil || work.Message == nil || event.Result == nil {
		return nil
	}
	parent := work.Message
	text := event.Result.Text
	if text == "" {
		text = event.Result.Error
	}
	if text == "" {
		text = string(event.Phase)
	}
	_, e := a.options.Local.Mailbox.ReceiveExecutionTx(ctx, tx, "mcp-result-"+work.DeliveryID, parent.To, protocol.SendMail{To: parent.From, Text: text}, parent)
	return e
}
