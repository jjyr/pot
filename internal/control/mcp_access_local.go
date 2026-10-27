package control

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// NewMCPAccess composes this Pot's collaboration backend with the receiver queue.
// The caller supplies an existing owner session verifier and deployment origin;
// this method neither starts a listener nor exposes the local owner Port.
func (s *Service) NewMCPAccess(options MCPOAuthOptions, capabilities func(context.Context, agentlink.MCPReceiver, protocol.AgentRef) (agentlink.Description, error)) (*MCPAccess, error) {
	options.DB = s.integrationDB
	options.NodeID = s.options.RuntimeID
	oauth, e := NewMCPOAuthServer(options)
	if e != nil {
		return nil, e
	}
	return NewMCPAccess(MCPAccessOptions{OAuth: oauth, Queue: &MCPAccessQueue{DB: s.Tasks.DB(), Tasks: s.Tasks, NodeID: s.options.RuntimeID}, Local: s, ReceiverCapabilities: capabilities})
}
func (a *MCPAccess) directory(ctx context.Context) (agentlink.Directory, error) {
	directory, e := a.options.Queue.Directory(ctx)
	if e != nil {
		return directory, e
	}
	if a.options.Local == nil {
		return directory, nil
	}
	definitions, e := a.options.Local.Registry.Definitions(ctx)
	if e != nil {
		return directory, e
	}
	for _, definition := range definitions {
		identity, e := a.options.Local.agentLinkIdentity(ctx, definition.ID)
		if e != nil {
			return directory, e
		}
		identity.Avatar = definition.Avatar
		// Explicit MCP targets never read or initialize the legacy default setting.
		operations := []agentlink.Operation{agentlink.ChatRead, agentlink.ChatReceipt, agentlink.ChatSend, agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel}
		if a.options.HumanReview != nil {
			operations = append(operations, agentlink.Approvals)
		}
		directory.Targets = append(directory.Targets, agentlink.Endpoint{Identity: identity, Capabilities: agentlink.Description{Version: 1, Scope: "peer", Operations: operations}})
	}

	return directory, nil
}
func (a *MCPAccess) submit(ctx context.Context, c agentlink.MCPCaller, input agentlink.MCPSubmitInput) (MCPTaskAcceptance, error) {
	if input.Target.NodeID != a.options.Queue.NodeID || a.options.Local == nil {
		return a.options.Queue.Submit(ctx, c, input)
	}
	digest, e := input.Digest(c)
	if e != nil {
		return MCPTaskAcceptance{}, e
	}
	request, e := input.Bind(c)
	if e != nil {
		return MCPTaskAcceptance{}, e
	}
	// Resolve a saved receipt before consulting the current target directory.
	return a.localMutation(ctx, c, agentlink.TaskSubmit, input.RequestID, digest, input,
		func() (agentlink.Task, error) {
			d, e := a.directory(ctx)
			if e != nil {
				return agentlink.Task{}, e
			}
			if !d.Capabilities(input.Target).Supports(agentlink.TaskSubmit) {
				return agentlink.Task{}, agentlink.ErrUnsupported
			}
			return (potLinkBackend{a.options.Local}).Submit(ctx, request)
		},
		func() (agentlink.Task, error) {
			id, _ := core.HashJSON([]string{callerKey(c.Actor()), input.RequestID})
			return (potLinkBackend{a.options.Local}).Status(ctx, c.Actor(), id)
		})
}
func (a *MCPAccess) task(ctx context.Context, c agentlink.MCPCaller, id string) (agentlink.Task, error) {
	task, e := a.options.Queue.Task(ctx, c, id)
	if !errors.Is(e, agentlink.ErrNotFound) || a.options.Local == nil {
		return task, e
	}
	return (potLinkBackend{a.options.Local}).Status(ctx, c.Actor(), id)
}
func (a *MCPAccess) cancel(ctx context.Context, c agentlink.MCPCaller, input agentlink.MCPCancelInput) (MCPTaskAcceptance, error) {
	var owned int
	e := a.options.Queue.DB.QueryRowContext(ctx, "SELECT count(*) FROM mcp_service_tasks WHERE id=? AND connection_id=? AND actor=?", input.TaskID, c.ConnectionID(), mcpJSON(c.Actor())).Scan(&owned)
	if e != nil {
		return MCPTaskAcceptance{}, e
	}
	if owned > 0 || a.options.Local == nil {
		return a.options.Queue.Control(ctx, c, nil, &input)
	}
	digest, e := input.Digest(c)
	if e != nil {
		return MCPTaskAcceptance{}, e
	}
	s := a.options.Local
	lookup := func() (string, string, error) {
		var owner, task string
		e := s.integrationDB.QueryRowContext(ctx, "SELECT assistant_id,task_id FROM agent_link_received_tasks WHERE id=? AND caller=?", input.TaskID, callerKey(c.Actor())).Scan(&owner, &task)
		if e != nil {
			return "", "", mcpMissing(e)
		}
		return owner, task, nil
	}
	return a.localMutation(ctx, c, agentlink.TaskCancel, input.RequestID, digest, input,
		func() (agentlink.Task, error) {
			owner, task, e := lookup()
			if e != nil {
				return agentlink.Task{}, e
			}
			if e = s.Tasks.StopExpectedRun(ctx, owner, task, input.RunID); e != nil {
				return agentlink.Task{}, e
			}
			s.wakeTasks()
			return (potLinkBackend{s}).Status(ctx, c.Actor(), input.TaskID)
		},
		func() (agentlink.Task, error) {
			owner, task, e := lookup()
			if e != nil {
				return agentlink.Task{}, e
			}
			record, e := s.Tasks.Get(ctx, owner, task)
			if e != nil || record == nil {
				return agentlink.Task{}, agentlink.ErrUnavailable
			}
			if record.RunID != input.RunID || record.StopMode != "cancel" && record.Info.Phase.Scope() == "active" {
				return agentlink.Task{}, agentlink.MCPError{Code: agentlink.MCPUnknown}
			}
			return (potLinkBackend{s}).Status(ctx, c.Actor(), input.TaskID)
		})
}

// localMutation uses a durable intent before crossing databases. Pending retries
// only query existing business state; they never invoke dispatch again.
func (a *MCPAccess) localMutation(ctx context.Context, c agentlink.MCPCaller, op agentlink.Operation, request, digest string, input any, dispatch, reconcile func() (agentlink.Task, error)) (acceptance MCPTaskAcceptance, err error) {
	pending := false
	err = storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		var old, state, receipt string
		e := tx.QueryRowContext(ctx, "SELECT digest,state,receipt FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?", c.ConnectionID(), mcpJSON(c.Actor()), op, request).Scan(&old, &state, &receipt)
		if op == agentlink.TaskSubmit && (e == nil || errors.Is(e, sql.ErrNoRows)) {
			if err := bindMCPOriginalHumanTx(ctx, tx, c, request, e == nil); err != nil {
				return err
			}
		}
		if e == nil {
			if old != digest {
				return agentlink.ErrConflict
			}
			if state == "accepted" {
				if e = mcpRead(receipt, &acceptance); e != nil {
					return e
				}
				acceptance.Receipt.Duplicate = true
				return nil
			}
			if state == "rejected" {
				var failure agentlink.MCPError
				if e = mcpRead(receipt, &failure); e != nil {
					return e
				}
				return failure
			}
			pending = true
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_local_mutations(connection_id,actor,operation,request_id,digest,input,state) VALUES(?,?,?,?,?,?,'pending')", c.ConnectionID(), mcpJSON(c.Actor()), op, request, digest, mcpJSON(input))
		return e
	})
	if err != nil || acceptance.Receipt.RequestID != "" {
		return
	}
	var task agentlink.Task
	if pending {
		task, err = reconcile()
		if err != nil {
			return acceptance, agentlink.MCPError{Code: agentlink.MCPUnknown}
		}
	} else {
		task, err = dispatch()
	}
	if err != nil {
		failure := mcpFailure(err)
		if failure.Code == agentlink.MCPUnavailable {
			failure.Code = agentlink.MCPUnknown
			err = failure
		}
		if failure.Code != agentlink.MCPUnknown {
			_, _ = a.options.Queue.DB.ExecContext(ctx, "UPDATE mcp_local_mutations SET state='rejected',receipt=? WHERE connection_id=? AND actor=? AND operation=? AND request_id=? AND state='pending'", mcpJSON(failure), c.ConnectionID(), mcpJSON(c.Actor()), op, request)
		}
		return acceptance, err
	}
	acceptance = MCPTaskAcceptance{Receipt: agentlink.MCPMutationReceipt{RequestID: request, Digest: digest, Duplicate: pending}, Task: task}
	_, err = a.options.Queue.DB.ExecContext(ctx, "UPDATE mcp_local_mutations SET state='accepted',receipt=? WHERE connection_id=? AND actor=? AND operation=? AND request_id=? AND digest=? AND state='pending'", mcpJSON(acceptance), c.ConnectionID(), mcpJSON(c.Actor()), op, request, digest)
	if err != nil {
		return MCPTaskAcceptance{}, agentlink.MCPError{Code: agentlink.MCPUnknown}
	}
	return
}

type MCPAccessMessageInput = agentlink.MCPMessageInput
type MCPAccessMessageAcceptance struct {
	Receipt agentlink.MCPMutationReceipt `json:"receipt"`
	Message protocol.Receipt             `json:"message"`
}

func (a *MCPAccess) message(ctx context.Context, c agentlink.MCPCaller, input MCPAccessMessageInput) (acceptance MCPAccessMessageAcceptance, err error) {
	if a.options.Local == nil {
		return acceptance, agentlink.ErrUnsupported
	}
	if !core.Identifier(input.RequestID) || !input.Message.To.Valid() || (input.Message.To.Kind != "assistant" && input.Message.To.Kind != "user") {
		return acceptance, agentlink.ErrInvalid
	}
	// Processing parents are trusted execution context, never model input. Ordinary
	// OAuth callers have no processing parent and cannot fabricate reply authority.
	var parent *protocol.Envelope
	authority, supervised := ctx.Value(mcpExecutionKey{}).(mcpExecutionAuthority)
	if supervised {
		parent = authority.parent
	}
	digest, e := core.HashJSON(struct {
		Version    int                            `json:"version"`
		Connection string                         `json:"connection_id"`
		Actor      protocol.AgentRef              `json:"actor"`
		Execution  *agentlink.MCPExecutionBinding `json:"execution"`
		Operation  agentlink.Operation            `json:"operation"`
		Input      MCPAccessMessageInput          `json:"input"`
		Parent     *protocol.Envelope             `json:"parent"`
	}{agentlink.MCPContractVersion, c.ConnectionID(), c.Actor(), c.Execution(), agentlink.ChatSend, input, parent})
	if e != nil {
		return acceptance, agentlink.ErrInvalid
	}
	// Recover accepted messages without a live target/directory lookup.
	var oldDigest, oldReceipt string
	e = a.options.Queue.DB.QueryRowContext(ctx, "SELECT digest,receipt FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?", c.ConnectionID(), mcpJSON(c.Actor()), agentlink.ChatSend, input.RequestID).Scan(&oldDigest, &oldReceipt)
	if e == nil {
		if oldDigest != digest {
			return acceptance, agentlink.ErrConflict
		}
		if e = mcpRead(oldReceipt, &acceptance); e != nil {
			return acceptance, e
		}
		acceptance.Receipt.Duplicate = true
		return acceptance, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return acceptance, e
	}
	directory, e := a.directory(ctx)
	if e != nil {
		return acceptance, e
	}
	err = storage.Transaction(ctx, a.options.Queue.DB, func(tx *sql.Tx) error {
		var old, raw string
		e := tx.QueryRowContext(ctx, "SELECT digest,receipt FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?", c.ConnectionID(), mcpJSON(c.Actor()), agentlink.ChatSend, input.RequestID).Scan(&old, &raw)
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
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if input.Message.To.Kind != "user" && !directory.Capabilities(input.Message.To).Supports(agentlink.ChatSend) {
			return agentlink.ErrUnsupported
		}
		id, _ := core.HashJSON([]string{c.ConnectionID(), mcpJSON(c.Actor()), input.RequestID})
		var envelope protocol.Envelope
		if c.Actor().NodeID != a.options.Queue.NodeID {
			if !supervised {
				return agentlink.ErrUnauthorized
			}
			envelope, e = a.options.Local.Mailbox.ReceiveExecutionTx(ctx, tx, id, c.Actor(), input.Message, parent)
		} else {
			envelope, e = a.options.Local.Mailbox.SendTx(ctx, tx, id, c.Actor(), input.Message, parent)
		}
		if e != nil {
			return agentlink.ErrInvalid
		}
		if envelope.To.NodeID != a.options.Queue.NodeID {
			if e = a.options.Queue.enqueueMessageTx(ctx, tx, c, input, envelope, digest); e != nil {
				return e
			}
		}
		acceptance = MCPAccessMessageAcceptance{Receipt: agentlink.MCPMutationReceipt{RequestID: input.RequestID, Digest: digest}, Message: protocol.Receipt{Message: envelope}}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_local_mutations VALUES(?,?,?,?,?,?,'accepted',?)", c.ConnectionID(), mcpJSON(c.Actor()), agentlink.ChatSend, input.RequestID, digest, mcpJSON(input), mcpJSON(acceptance))
		return e
	})
	if err == nil {
		a.options.Local.Mailbox.Changes.Notify("assistant-mail")
		a.options.Local.wakeTasks()
	}
	return
}

type MCPAccessMessageRead struct {
	MessageID string `json:"message_id" required:"true"`
	After     int64  `json:"after" required:"true"`
}
type MCPAccessMessagePage struct {
	Messages []protocol.MailRecord `json:"messages"`
}

// messages reads only a conversation originally accepted under this connection
// and actor. A model cannot select another caller's conversation by ID.
func (a *MCPAccess) messages(ctx context.Context, c agentlink.MCPCaller, input MCPAccessMessageRead) (page MCPAccessMessagePage, err error) {
	page.Messages = []protocol.MailRecord{}
	if a.options.Local == nil {
		return page, agentlink.ErrUnsupported
	}
	if !core.Identifier(input.MessageID) || input.After < 0 || uint64(input.After) > core.MaxSafeInteger {
		return page, agentlink.ErrInvalid
	}
	var raw string
	err = a.options.Queue.DB.QueryRowContext(ctx, `SELECT receipt FROM mcp_local_mutations WHERE connection_id=? AND actor=? AND operation=? AND state='accepted' AND json_extract(receipt,'$.message.message.id')=? LIMIT 1`, c.ConnectionID(), mcpJSON(c.Actor()), agentlink.ChatSend, input.MessageID).Scan(&raw)
	if err != nil {
		return page, mcpMissing(err)
	}
	var original MCPAccessMessageAcceptance
	if err = mcpRead(raw, &original); err != nil {
		return page, err
	}
	if original.Message.Message.From != c.Actor() {
		return page, agentlink.ErrUnauthorized
	}
	rows, err := a.options.Queue.DB.QueryContext(ctx, `SELECT sequence,envelope,state,detail FROM agent_messages WHERE conversation=? AND sequence>? AND (id=? OR (json_extract(envelope,'$.to.node_id')=? AND json_extract(envelope,'$.to.kind')=? AND json_extract(envelope,'$.to.agent_id')=?)) ORDER BY sequence LIMIT ?`, original.Message.Message.ConversationID, input.After, input.MessageID, c.Actor().NodeID, c.Actor().Kind, c.Actor().AgentID, protocol.MailPageSize)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var record protocol.MailRecord
		var envelope string
		if err = rows.Scan(&record.Sequence, &envelope, &record.State, &record.Detail); err != nil {
			return page, err
		}
		if err = mcpRead(envelope, &record.Message); err != nil {
			return page, err
		}
		page.Messages = append(page.Messages, record)
	}
	return page, rows.Err()
}
