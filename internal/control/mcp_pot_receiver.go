package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type potMCPReceiver struct {
	s        *Service
	identity agentlink.MCPReceiver
	remote   *mcpreceiver.Client
}

func (p *potMCPReceiver) CheckActive(ctx context.Context) error {
	var state, node string
	e := p.s.integrationDB.QueryRowContext(ctx, "SELECT state,node_id FROM mcp_receiver_connections WHERE id=?", p.identity.ConnectionID()).Scan(&state, &node)
	if e != nil {
		return e
	}
	if state != "active" || node != p.identity.NodeID() {
		return agentlink.ErrUnauthorized
	}
	return nil
}
func (p *potMCPReceiver) Records(ctx context.Context) ([]agentlink.MCPReceiverRecord, error) {
	rows, e := p.s.Tasks.DB().QueryContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? ORDER BY rowid", p.identity.ConnectionID())
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	result := []agentlink.MCPReceiverRecord{}
	for _, id := range ids {
		r, e := mcpreceiver.Read(ctx, p.s.Tasks.DB(), p.identity.ConnectionID(), id)
		if e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, nil
}
func (p *potMCPReceiver) ClaimRequest(ctx context.Context) (string, error) {
	var request string
	e := storage.Transaction(ctx, p.s.integrationDB, func(tx *sql.Tx) error {
		var state, node string
		if e := tx.QueryRowContext(ctx, "SELECT state,node_id,claim_request_id FROM mcp_receiver_connections WHERE id=?", p.identity.ConnectionID()).Scan(&state, &node, &request); e != nil {
			return e
		}
		if state != "active" || node != p.identity.NodeID() {
			return agentlink.ErrUnauthorized
		}
		if request != "" {
			return nil
		}
		request = rand.Text()
		_, e := tx.ExecContext(ctx, "UPDATE mcp_receiver_connections SET claim_request_id=?,revision=revision+1 WHERE id=?", request, p.identity.ConnectionID())
		return e
	})
	return request, e
}
func (p *potMCPReceiver) ClearClaimRequest(ctx context.Context, request string) error {
	_, e := p.s.integrationDB.ExecContext(ctx, "UPDATE mcp_receiver_connections SET claim_request_id='',revision=revision+1 WHERE id=? AND claim_request_id=?", p.identity.ConnectionID(), request)
	return e
}
func (p *potMCPReceiver) Save(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	return storage.Transaction(ctx, p.s.Tasks.DB(), func(tx *sql.Tx) error { return mcpreceiver.Save(ctx, tx, r) })
}
func (p *potMCPReceiver) Accept(ctx context.Context, d agentlink.MCPReceiverDelivery) error {
	r, e := mcpreceiver.Record(p.identity, d)
	if e != nil {
		return e
	}
	// Resolve assistant availability before the Tasks writer transaction; its
	// OwnerAvailable fence checks again in the existing admission transaction.
	if r.Claim.Binding.Receiver.Kind != "assistant" {
		return agentlink.ErrUnsupported
	}
	_, identityError := p.s.agentLinkIdentity(ctx, r.Claim.Binding.Receiver.AgentID)
	return storage.Transaction(ctx, p.s.Tasks.DB(), func(tx *sql.Tx) error {
		old, e := mcpreceiver.Read(ctx, tx, r.Claim.ConnectionID, r.Claim.DeliveryID)
		if e == nil {
			if old.Claim != r.Claim || mcpJSON(old.Work) != mcpJSON(r.Work) || mcpJSON(old.Control) != mcpJSON(r.Control) {
				return agentlink.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		rejection := identityError
		if r.Control != nil && rejection == nil {
			_, rejection = p.prior(ctx, tx, r.Control)
		}
		if rejection != nil {
			r.Ack.Accepted = false
			failure := mcpFailure(rejection)
			r.Ack.Error = &failure
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_receiver_journal(connection_id,delivery_id,claim_id,revision,body) VALUES(?,?,?,1,?)", r.Claim.ConnectionID, r.Claim.DeliveryID, r.Claim.ClaimID, mcpJSON(r))
		return e
	})
}
func (p *potMCPReceiver) prior(ctx context.Context, tx *sql.Tx, c *agentlink.MCPControlDelivery) (agentlink.MCPReceiverRecord, error) {
	var r agentlink.MCPReceiverRecord
	var delivery string
	if e := tx.QueryRowContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? AND execution_task=? AND execution_run=?", p.identity.ConnectionID(), c.Binding.Task.AgentID, c.Binding.RunID).Scan(&delivery); e != nil {
		return r, agentlink.ErrFenced
	}
	r, e := mcpreceiver.Read(ctx, tx, p.identity.ConnectionID(), delivery)
	if e != nil {
		return r, e
	}
	if r.Admission == nil || r.Admission.Binding != c.Binding || r.Claim.Binding != c.Dispatch {
		return r, agentlink.ErrFenced
	}
	caller := potMCPRecordCaller(r)
	if caller != c.Caller {
		return r, agentlink.ErrUnauthorized
	}
	return r, nil
}
func potMCPRequest(r *agentlink.MCPReceiverRecord) protocol.CreateTask {
	id, _ := core.HashJSON([]string{r.Claim.ConnectionID, r.Claim.DeliveryID})
	return protocol.CreateTask{RequestID: id, Text: r.Work.Request.Text, Target: &r.Work.Request.Target}
}
func (p *potMCPReceiver) Admit(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	var selection tasks.Selection
	var proof *tasks.Run
	var e error
	if r.Work != nil && r.Work.Message == nil {
		selection, e = p.s.Tasks.Prepare(ctx, r.Claim.Binding.Receiver.AgentID, potMCPRequest(r))
		if e != nil {
			return e
		}
	}
	if r.Control != nil && r.Control.Input != nil {
		proof, e = p.s.Tasks.ReceiverContinuationProof(ctx, r.Claim.Binding.Receiver.AgentID, r.Control.Binding.Task.AgentID, r.Control.Binding.RunID)
		if e != nil {
			return e
		}
	}
	// Cancellation is an existing idempotent, expected-run business mutation.
	// Journal intent was committed before this call; a response loss can only
	// repeat cancellation intent against this same run, never start a new run.
	if r.Control != nil && r.Control.Cancel != nil {
		if e = p.s.Tasks.StopExpectedRun(ctx, r.Claim.Binding.Receiver.AgentID, r.Control.Binding.Task.AgentID, r.Control.Binding.RunID); e != nil {
			return e
		}
		r.ControlApplied = true
		return p.Save(ctx, r)
	}
	e = storage.Transaction(ctx, p.s.Tasks.DB(), func(tx *sql.Tx) error {
		current, e := mcpreceiver.Read(ctx, tx, r.Claim.ConnectionID, r.Claim.DeliveryID)
		if e != nil {
			return e
		}
		*r = current
		if r.Admission != nil || r.ControlApplied || !r.Ack.Accepted {
			return nil
		}
		if !r.Acknowledged {
			return agentlink.ErrFenced
		}
		var record tasks.Record
		var previous *agentlink.MCPRunBinding
		owner := r.Claim.Binding.Receiver.AgentID
		if r.Work != nil {
			if r.Work.Message != nil {
				if e = p.s.Mailbox.DeliverTx(ctx, tx, *r.Work.Message); e != nil {
					return e
				}
				r.ControlApplied = true
				return mcpreceiver.Save(ctx, tx, r)
			}
			record, e = p.s.Tasks.EnqueueTx(ctx, tx, owner, potMCPRequest(r), selection, protocol.TaskOrigin{Kind: "manual"}, time.Now().UnixMilli())
			if e != nil {
				return e
			}
		} else {
			prior, e := p.prior(ctx, tx, r.Control)
			if e != nil {
				return e
			}
			previous = &prior.Admission.Binding
			if r.Control.Input == nil {
				return agentlink.ErrUnsupported
			}
			if prior.Outbox != nil || prior.Receipt == nil || !potMCPTerminal(prior.LastPhase) {
				return agentlink.ErrFenced
			}
			request, _ := core.HashJSON([]string{r.Claim.ConnectionID, r.Claim.DeliveryID})
			record, e = p.s.Tasks.ContinueReceiverTx(ctx, tx, owner, r.Control.Binding.Task.AgentID, r.Control.Binding.RunID, request, r.Control.Input.Text, r.Control.Caller, proof)
			if e != nil {
				return e
			}
		}
		admission := agentlink.MCPRunAdmission{Claim: r.Claim, Binding: agentlink.MCPRunBinding{Task: protocol.AgentRef{NodeID: p.identity.NodeID(), Kind: "task", AgentID: record.Info.ID}, RunID: record.RunID, Receiver: r.Claim.Binding.Receiver}}
		if previous != nil {
			admission.PreviousRunID = previous.RunID
		}
		if e = admission.ValidateAgainst(p.identity, r.Claim, previous); e != nil {
			return e
		}
		r.Admission = &admission
		return mcpreceiver.Save(ctx, tx, r)
	})
	if e == nil {
		p.s.Tasks.Notify()
		p.s.Mailbox.Changes.Notify("assistant-mail")
		p.s.wakeTasks()
	}
	return e
}
func (p *potMCPReceiver) Snapshot(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	if r.Admission == nil || r.Outbox != nil || potMCPTerminal(r.LastPhase) {
		return nil
	}
	record, e := p.s.Tasks.Run(ctx, r.Claim.Binding.Receiver.AgentID, r.Admission.Binding.Task.AgentID, r.Admission.Binding.RunID)
	if e != nil {
		return e
	}
	if record == nil || record.RunID != r.Admission.Binding.RunID || record.Execution != nil {
		return agentlink.ErrFenced
	}
	phase := record.Info.Phase
	if phase == r.LastPhase {
		return nil
	}
	event := agentlink.MCPExecutionEvent{Claim: r.Claim, Binding: r.Admission.Binding, Sequence: r.Sequence + 1, Phase: phase}
	if potMCPTerminal(phase) {
		if record.Outcome == nil {
			return agentlink.ErrFenced
		}
		result := *record.Outcome
		event.Result = &result
	}
	if e = event.ValidateAfter(p.identity, r.Claim, r.Admission.Binding, r.Sequence); e != nil {
		return e
	}
	r.Outbox = &event
	return p.Save(ctx, r)
}
func potMCPTerminal(p protocol.TaskPhase) bool {
	return p == protocol.TaskComplete || p == protocol.TaskFailed || p == protocol.TaskCancelled || p == protocol.TaskInterrupted
}

func potMCPRecordCaller(r agentlink.MCPReceiverRecord) protocol.AgentRef {
	if r.Work != nil {
		return r.Work.Request.From
	}
	if r.Control != nil {
		return r.Control.Caller
	}
	return protocol.AgentRef{}
}
