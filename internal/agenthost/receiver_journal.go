package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// receiverRecord is the version-2 body owned by the receiver-host migration.
// The single pending event is an outbox: its bytes survive until the exact
// claim/sequence/digest receipt commits. Network retries never launch execution.
type receiverRecord = agentlink.MCPReceiverRecord
type receiverDelivery = agentlink.MCPReceiverDelivery

func readReceiverRecord(ctx context.Context, q mcpreceiver.Query, connection, delivery string) (receiverRecord, error) {
	return mcpreceiver.Read(ctx, q, connection, delivery)
}
func saveReceiverRecord(ctx context.Context, tx *sql.Tx, r *receiverRecord) error {
	return mcpreceiver.Save(ctx, tx, r)
}

func (p *receiverPump) save(ctx context.Context, r *receiverRecord) error {
	return storage.Transaction(ctx, p.backend.db, func(tx *sql.Tx) error { return saveReceiverRecord(ctx, tx, r) })
}

func (p *receiverPump) accept(ctx context.Context, d receiverDelivery) error {
	r, err := mcpreceiver.Record(p.identity, d)
	if err != nil {
		return err
	}
	// Human-channel validation uses the same lock order as native Review and
	// decision application: pending-map lock, then the Tasks transaction.
	if r.Control != nil && r.Control.Approval != nil {
		p.backend.mu.Lock()
		defer p.backend.mu.Unlock()
	}
	return storage.Transaction(ctx, p.backend.db, func(tx *sql.Tx) error {
		old, err := readReceiverRecord(ctx, tx, r.Claim.ConnectionID, r.Claim.DeliveryID)
		if err == nil {
			if old.Claim != r.Claim || encoded(old.Work) != encoded(r.Work) || encoded(old.Control) != encoded(r.Control) {
				return agentlink.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Decide an immutable rejection before the first ack. The original
		// caller comes only from the authenticated service's claimed delivery;
		// the server verifies this exact claim again when committing our ack.
		var rejection error
		if r.Work != nil {
			if (r.Work.Message == nil && r.Work.Request.Target != agentlink.NodeTarget(p.identity.NodeID())) || len(r.Work.Request.Text) > 24000 {
				rejection = agentlink.ErrUnsupported
			}
		} else {
			rejection = p.validateControl(ctx, tx, r.Control)
		}
		if rejection != nil {
			if !receiverPermanent(rejection) {
				return rejection
			}
			code := agentlink.MCPFenced
			if errors.Is(rejection, agentlink.ErrUnauthorized) {
				code = agentlink.MCPUnauthorized
			}
			if errors.Is(rejection, agentlink.ErrUnsupported) {
				code = agentlink.MCPUnsupported
			}
			r.Ack.Accepted = false
			r.Ack.Error = &agentlink.MCPError{Code: code}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO mcp_receiver_journal(connection_id,delivery_id,claim_id,revision,body) VALUES(?,?,?,1,?)", r.Claim.ConnectionID, r.Claim.DeliveryID, r.Claim.ClaimID, encoded(r))
		return err
	})
}

func (p *receiverPump) validateControl(ctx context.Context, tx *sql.Tx, c *agentlink.MCPControlDelivery) error {
	var delivery string
	if err := tx.QueryRowContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? AND execution_task=? AND execution_run=?", p.identity.ConnectionID(), c.Binding.Task.AgentID, c.Binding.RunID).Scan(&delivery); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agentlink.ErrFenced
		}
		return err
	}
	prior, err := readReceiverRecord(ctx, tx, p.identity.ConnectionID(), delivery)
	if err != nil {
		return err
	}
	if prior.Admission == nil || prior.Admission.Binding != c.Binding || prior.Claim.Binding != c.Dispatch {
		return agentlink.ErrFenced
	}
	caller := protocol.AgentRef{}
	if prior.Work != nil {
		caller = prior.Work.Request.From
	} else if prior.Control != nil {
		caller = prior.Control.Caller
	}
	if caller != c.Caller {
		return agentlink.ErrUnauthorized
	}
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE id=? AND caller=?", c.Binding.Task.AgentID, callerKey(caller)).Scan(&raw); err != nil {
		return err
	}
	var current job
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if current.Task.RunID != c.Binding.RunID {
		return agentlink.ErrFenced
	}
	if c.Approval != nil {
		if c.Approval.Epoch != p.epoch || current.Task.State != "waiting_approval" {
			return agentlink.ErrFenced
		}
		pending := p.backend.reviews[c.Approval.ApprovalID]
		valid := pending != nil && !pending.resolved && pending.ctx.Err() == nil && pending.job.Task.RunID == c.Binding.RunID && pending.job.Task.ID == c.Binding.Task.AgentID && pending.job.Request.From == c.Caller && p.backend.active[c.Binding.Task.AgentID] != nil
		if !valid {
			return agentlink.ErrFenced
		}
	}
	if c.Input != nil && (!terminal(current.Task.State) || current.CleanupUncertain || prior.Outbox != nil || prior.Receipt == nil || !receiverTerminal(prior.LastPhase)) {
		return agentlink.ErrFenced
	}
	return nil
}

// admit commits the native backend's job/session or continuation and the real
// execution binding together. A restart cannot discover an admitted job without
// its receiver mapping, or a mapping without the backend admission receipt.
func (p *receiverPump) admit(ctx context.Context, r *receiverRecord) error {
	cancelID := ""
	err := storage.Transaction(ctx, p.backend.db, func(tx *sql.Tx) error {
		current, err := readReceiverRecord(ctx, tx, r.Claim.ConnectionID, r.Claim.DeliveryID)
		if err != nil {
			return err
		}
		*r = current
		if r.Admission != nil || r.ControlApplied || !r.Ack.Accepted {
			return nil
		}
		if !r.Acknowledged {
			return agentlink.ErrFenced
		}
		var accepted job
		var previous *agentlink.MCPRunBinding
		if r.Work != nil {
			request := r.Work.Request
			if (r.Work.Message == nil && request.Target != agentlink.NodeTarget(p.identity.NodeID())) || len(request.Text) > 24000 {
				return agentlink.ErrUnsupported
			}
			request.RequestID = jobID(protocol.AgentRef{NodeID: p.identity.NodeID(), Kind: "task", AgentID: r.Claim.ConnectionID}, r.Claim.DeliveryID)
			if r.Work.Message != nil {
				err = p.backend.submitMessageTx(ctx, tx, request, *r.Work.Message, &accepted)
			} else {
				err = p.backend.submitTaskTx(ctx, tx, request, &accepted)
			}
			if err != nil {
				return err
			}
		} else {
			c := r.Control
			var delivery string
			if err = tx.QueryRowContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? AND execution_task=? AND execution_run=?", r.Claim.ConnectionID, c.Binding.Task.AgentID, c.Binding.RunID).Scan(&delivery); err != nil {
				return agentlink.ErrFenced
			}
			prior, err := readReceiverRecord(ctx, tx, r.Claim.ConnectionID, delivery)
			if err != nil {
				return err
			}
			if prior.Admission == nil || prior.Admission.Binding != c.Binding || prior.Claim.Binding != c.Dispatch {
				return agentlink.ErrFenced
			}
			caller := protocol.AgentRef{}
			if prior.Work != nil {
				caller = prior.Work.Request.From
			} else if prior.Control != nil {
				caller = prior.Control.Caller
			}
			if caller != c.Caller {
				return agentlink.ErrUnauthorized
			}
			previous = &prior.Admission.Binding
			if c.Input != nil {
				// Keep the old terminal outbox until its receipt is durable. Input cannot
				// erase terminal evidence still awaiting report reconciliation.
				if prior.Outbox != nil || prior.Receipt == nil || !receiverTerminal(prior.LastPhase) {
					return agentlink.ErrFenced
				}
				guidance := agentlink.TaskGuidance{From: caller, ID: c.Binding.Task.AgentID, RequestID: jobID(caller, r.Claim.ConnectionID+":"+r.Claim.DeliveryID), Text: c.Input.Text}
				if err = p.backend.inputTx(ctx, tx, guidance, c.Binding.RunID, &accepted); err != nil {
					return err
				}
			} else {
				var raw string
				if err = tx.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE id=? AND caller=?", c.Binding.Task.AgentID, callerKey(caller)).Scan(&raw); err != nil {
					return err
				}
				if err = json.Unmarshal([]byte(raw), &accepted); err != nil {
					return err
				}
				if accepted.Task.RunID != c.Binding.RunID {
					return agentlink.ErrFenced
				}
				if !terminal(accepted.Task.State) {
					if accepted.Task.State == "queued" {
						accepted.Task.State = "cancelled"
					} else {
						accepted.Task.State = "cancelling"
					}
					accepted.Task.UpdatedAt = time.Now().UTC()
					if _, err = tx.ExecContext(ctx, "UPDATE agent_adapter_jobs SET data=? WHERE id=?", encoded(accepted), accepted.Task.ID); err != nil {
						return err
					}
					cancelID = accepted.Task.ID
				}
				r.ControlApplied = true
				return saveReceiverRecord(ctx, tx, r)
			}
		}
		binding := agentlink.MCPRunBinding{Task: protocol.AgentRef{NodeID: p.identity.NodeID(), Kind: "task", AgentID: accepted.Task.ID}, RunID: accepted.Task.RunID, Receiver: r.Claim.Binding.Receiver}
		admission := agentlink.MCPRunAdmission{Claim: r.Claim, Binding: binding}
		if previous != nil {
			admission.PreviousRunID = previous.RunID
		}
		if err = admission.ValidateAgainst(p.identity, r.Claim, previous); err != nil {
			return err
		}
		r.Admission = &admission
		return saveReceiverRecord(ctx, tx, r)
	})
	if err == nil && cancelID != "" {
		p.backend.mu.Lock()
		cancel := p.backend.active[cancelID]
		p.backend.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return err
}

func receiverTerminal(phase protocol.TaskPhase) bool {
	return phase == protocol.TaskComplete || phase == protocol.TaskFailed || phase == protocol.TaskCancelled || phase == protocol.TaskInterrupted
}

func (p *receiverPump) snapshot(ctx context.Context, r *receiverRecord) error {
	if r.Admission == nil || r.Outbox != nil || receiverTerminal(r.LastPhase) {
		return nil
	}
	caller := protocol.AgentRef{}
	if r.Work != nil {
		caller = r.Work.Request.From
	} else {
		caller = r.Control.Caller
	}
	j, err := p.backend.read(ctx, caller, r.Admission.Binding.Task.AgentID)
	if err != nil {
		return err
	}
	if j.Task.RunID != r.Admission.Binding.RunID {
		return agentlink.ErrFenced
	}
	phase := protocol.TaskPhase(j.Task.State)
	switch j.Task.State {
	case "completed":
		phase = protocol.TaskComplete
	case "queued", "running", "waiting_approval":
	case "failed", "cancelled":
	case "cancelling":
		return nil // intent is not terminal evidence
	default:
		return agentlink.ErrUnsupported
	}
	if phase == r.LastPhase {
		return nil
	}
	e := agentlink.MCPExecutionEvent{Claim: r.Claim, Binding: r.Admission.Binding, Sequence: r.Sequence + 1, Phase: phase, ThreadID: j.ThreadID}
	if receiverTerminal(phase) {
		// Uncertain cleanup is failure evidence, never successful cancellation.
		if j.CleanupUncertain && phase == protocol.TaskCancelled {
			e.Phase = protocol.TaskFailed
		}
		e.Result = &protocol.TaskResultEvent{TaskID: j.Task.ID, RunID: j.Task.RunID, Title: trimBytes(j.Request.Text, 160), Phase: e.Phase, Text: j.Task.Result, Error: j.Task.Error, Origin: protocol.TaskOrigin{Kind: "manual"}, Evidence: j.Files}
	}
	if err = e.ValidateAfter(p.identity, r.Claim, r.Admission.Binding, r.Sequence); err != nil {
		return err
	}
	r.Outbox = &e
	return p.save(ctx, r)
}
