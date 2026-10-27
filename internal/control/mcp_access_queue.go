package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// MCPAccessQueue owns receiver transport receipts. It never launches execution.
// DB is the caller-owned, migrated tasks database; Close remains with its owner.
type MCPAccessQueue struct {
	DB              *sql.DB
	Tasks           *tasks.Service
	NodeID          string
	CheckConnection func(context.Context, string) error
}

func mcpJSON(v any) string            { b, _ := json.Marshal(v); return string(b) }
func mcpRead(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }
func mcpMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return agentlink.ErrNotFound
	}
	return err
}

// Register checks the authenticated node against an effective driver description.
// The caller must obtain actual from a trusted receiver configuration, not tool metadata.
func (q *MCPAccessQueue) Register(ctx context.Context, receiver agentlink.MCPReceiver, registration agentlink.MCPReceiverRegistration, actual agentlink.Description) error {
	if err := registration.ValidateAgainst(receiver, actual); err != nil {
		return err
	}
	return storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		address := mcpJSON(registration.Receiver.Address)
		var connection string
		err := tx.QueryRowContext(ctx, "SELECT connection_id FROM mcp_receiver_endpoints WHERE address=?", address).Scan(&connection)
		if err == nil && connection != receiver.ConnectionID() {
			return agentlink.ErrUnauthorized
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO mcp_receiver_endpoints(connection_id,address,registration) VALUES(?,?,?) ON CONFLICT(connection_id,address) DO UPDATE SET registration=excluded.registration`, receiver.ConnectionID(), address, mcpJSON(registration))
		return err
	})
}

func (q *MCPAccessQueue) Directory(ctx context.Context) (agentlink.Directory, error) {
	d := agentlink.Directory{NodeID: q.NodeID, Name: "Pot", Adapter: "pot", Targets: []agentlink.Endpoint{}}
	rows, err := q.DB.QueryContext(ctx, "SELECT registration FROM mcp_receiver_endpoints ORDER BY address")
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var r agentlink.MCPReceiverRegistration
		if err = rows.Scan(&raw); err != nil {
			return d, err
		}
		if err = mcpRead(raw, &r); err != nil {
			return d, err
		}
		d.Targets = append(d.Targets, agentlink.Endpoint{Identity: r.Receiver, Capabilities: r.Capabilities})
	}
	return d, rows.Err()
}

type MCPTaskAcceptance struct {
	Receipt agentlink.MCPMutationReceipt `json:"receipt"`
	Task    agentlink.Task               `json:"task"`
}

func mutationRetry(ctx context.Context, tx *sql.Tx, c agentlink.MCPCaller, op agentlink.Operation, request, digest string) (MCPTaskAcceptance, bool, error) {
	var raw, saved string
	err := tx.QueryRowContext(ctx, `SELECT digest,receipt FROM mcp_service_mutations WHERE connection_id=? AND actor=? AND operation=? AND request_id=?`, c.ConnectionID(), mcpJSON(c.Actor()), op, request).Scan(&saved, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPTaskAcceptance{}, false, nil
	}
	if err != nil {
		return MCPTaskAcceptance{}, false, err
	}
	if saved != digest {
		return MCPTaskAcceptance{}, true, agentlink.ErrConflict
	}
	var a MCPTaskAcceptance
	err = mcpRead(raw, &a)
	a.Receipt.Duplicate = true
	return a, true, err
}
func saveMutation(ctx context.Context, tx *sql.Tx, c agentlink.MCPCaller, op agentlink.Operation, a MCPTaskAcceptance) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO mcp_service_mutations VALUES(?,?,?,?,?,?)`, c.ConnectionID(), mcpJSON(c.Actor()), op, a.Receipt.RequestID, a.Receipt.Digest, mcpJSON(a))
	return err
}
func (q *MCPAccessQueue) Submit(ctx context.Context, c agentlink.MCPCaller, in agentlink.MCPSubmitInput) (a MCPTaskAcceptance, err error) {
	defer func() {
		if err == nil {
			q.Tasks.Notify()
		}
	}()
	digest, err := in.Digest(c)
	if err != nil {
		return a, err
	}
	request, err := in.Bind(c)
	if err != nil {
		return a, err
	}
	err = storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		prior, found, e := mutationRetry(ctx, tx, c, agentlink.TaskSubmit, in.RequestID, digest)
		if e != nil {
			return e
		}
		if found {
			a = prior
			return nil
		}
		var raw, connection string
		e = tx.QueryRowContext(ctx, "SELECT connection_id,registration FROM mcp_receiver_endpoints WHERE address=?", mcpJSON(in.Target)).Scan(&connection, &raw)
		if e != nil {
			return mcpMissing(e)
		}
		var r agentlink.MCPReceiverRegistration
		if e = mcpRead(raw, &r); e != nil {
			return e
		}
		if !r.Capabilities.Supports(agentlink.TaskSubmit) {
			return agentlink.ErrUnsupported
		}
		id := rand.Text()
		delivery := agentlink.MCPWorkDelivery{Version: agentlink.MCPContractVersion, DeliveryID: rand.Text(), Binding: agentlink.MCPDispatchBinding{Task: protocol.AgentRef{NodeID: q.NodeID, Kind: "task", AgentID: id}, Receiver: in.Target}, Request: request}
		if e = delivery.Validate(); e != nil {
			return e
		}
		now := time.Now().UTC()
		task := agentlink.Task{ID: id, RequestID: in.RequestID, Receiver: r.Receiver, State: string(protocol.TaskQueued), CreatedAt: now, UpdatedAt: now}
		a = MCPTaskAcceptance{agentlink.MCPMutationReceipt{RequestID: in.RequestID, Digest: digest}, task}
		if e = q.acceptCatalogue(ctx, tx, c, request, task); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO mcp_service_tasks(id,connection_id,actor,request_id,digest,submission,task) VALUES(?,?,?,?,?,?,?)`, id, c.ConnectionID(), mcpJSON(c.Actor()), in.RequestID, digest, mcpJSON(in), mcpJSON(task))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO mcp_service_deliveries(id,task_id,receiver_connection,receiver_address,work) VALUES(?,?,?,?,?)`, delivery.DeliveryID, id, connection, mcpJSON(in.Target), mcpJSON(delivery))
		if e != nil {
			return e
		}
		return saveMutation(ctx, tx, c, agentlink.TaskSubmit, a)
	})
	return a, err
}
func ownedMCPTask(ctx context.Context, tx *sql.Tx, c agentlink.MCPCaller, id string) (t agentlink.Task, execution *agentlink.MCPRunBinding, err error) {
	var raw, binding string
	err = tx.QueryRowContext(ctx, `SELECT task,execution FROM mcp_service_tasks WHERE id=? AND connection_id=? AND actor=?`, id, c.ConnectionID(), mcpJSON(c.Actor())).Scan(&raw, &binding)
	if err != nil {
		return t, nil, mcpMissing(err)
	}
	if err = mcpRead(raw, &t); err != nil {
		return
	}
	if binding != "" {
		execution = &agentlink.MCPRunBinding{}
		err = mcpRead(binding, execution)
	}
	return
}
func (q *MCPAccessQueue) Task(ctx context.Context, c agentlink.MCPCaller, id string) (t agentlink.Task, err error) {
	err = storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error { var e error; t, _, e = ownedMCPTask(ctx, tx, c, id); return e })
	return
}

// Control serializes original caller ownership, expected-run CAS and enqueue.
func (q *MCPAccessQueue) Control(ctx context.Context, c agentlink.MCPCaller, input *agentlink.MCPTaskInput, cancel *agentlink.MCPCancelInput) (a MCPTaskAcceptance, err error) {
	defer func() {
		if err == nil {
			q.Tasks.Notify()
		}
	}()
	if (input == nil) == (cancel == nil) {
		return a, agentlink.ErrInvalid
	}
	var id, request, digest, run string
	op := agentlink.TaskCancel
	if input != nil {
		id, request, run = input.TaskID, input.RequestID, input.RunID
		op = agentlink.TaskInput
		digest, err = input.Digest(c)
	} else {
		id, request, run = cancel.TaskID, cancel.RequestID, cancel.RunID
		digest, err = cancel.Digest(c)
	}
	if err != nil {
		return a, err
	}
	err = storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		prior, found, e := mutationRetry(ctx, tx, c, op, request, digest)
		if e != nil {
			return e
		}
		if found {
			a = prior
			return nil
		}
		t, binding, e := ownedMCPTask(ctx, tx, c, id)
		if e != nil {
			return e
		}
		if binding == nil || binding.RunID != run {
			return agentlink.ErrFenced
		}
		if input != nil {
			if _, e = input.Guidance(c, t); e != nil {
				return e
			}
		}
		var connection, raw string
		e = tx.QueryRowContext(ctx, "SELECT connection_id,registration FROM mcp_receiver_endpoints WHERE address=?", mcpJSON(t.Receiver.Address)).Scan(&connection, &raw)
		if e != nil {
			return mcpMissing(e)
		}
		var registration agentlink.MCPReceiverRegistration
		if e = mcpRead(raw, &registration); e != nil {
			return e
		}
		if !registration.Capabilities.Supports(op) {
			return agentlink.ErrUnsupported
		}
		// A pending continuation fences another new control until real admission.
		var pending int
		e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_service_deliveries WHERE task_id=? AND control!='' AND admission='' AND (ack='' OR json_extract(ack,'$.accepted')=1) AND json_type(control,'$.input') IS NOT NULL`, id).Scan(&pending)
		if e != nil {
			return e
		}
		if pending != 0 {
			return agentlink.ErrConflict
		}
		d := agentlink.MCPControlDelivery{Version: agentlink.MCPContractVersion, DeliveryID: rand.Text(), Dispatch: agentlink.MCPDispatchBinding{Task: protocol.AgentRef{NodeID: q.NodeID, Kind: "task", AgentID: id}, Receiver: t.Receiver.Address}, Binding: *binding, Caller: c.Actor(), Input: input, Cancel: cancel}
		if e = d.Validate(); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO mcp_service_deliveries(id,task_id,receiver_connection,receiver_address,control) VALUES(?,?,?,?,?)`, d.DeliveryID, id, connection, mcpJSON(t.Receiver.Address), mcpJSON(d))
		if e != nil {
			return e
		}
		if cancel != nil {
			_, e = tx.ExecContext(ctx, "UPDATE mcp_service_tasks SET cancel_requested=1 WHERE id=?", id)
			if e != nil {
				return e
			}
		}
		a = MCPTaskAcceptance{agentlink.MCPMutationReceipt{RequestID: request, Digest: digest}, t}
		return saveMutation(ctx, tx, c, op, a)
	})
	return a, err
}

// MCPClaimedDelivery is transport acceptance data, not execution completion.
type MCPClaimedDelivery struct {
	Claim   agentlink.MCPClaim            `json:"claim"`
	Work    *agentlink.MCPWorkDelivery    `json:"work,omitempty"`
	Control *agentlink.MCPControlDelivery `json:"control,omitempty"`
}

// Claim durably allocates one delivery. requestID reconciles response loss;
// claims never expire into permission to replay or reassign execution.
func (q *MCPAccessQueue) Claim(ctx context.Context, r agentlink.MCPReceiver, requestID string, wait time.Duration) (result *MCPClaimedDelivery, err error) {
	if !core.Identifier(requestID) || wait < 0 || wait > 30*time.Second {
		return nil, agentlink.ErrInvalid
	}
	deadline := time.Now().Add(wait)
	for {
		if q.CheckConnection != nil {
			if e := q.CheckConnection(ctx, r.ConnectionID()); e != nil {
				return nil, e
			}
		}
		err = storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
			var raw string
			e := tx.QueryRowContext(ctx, `SELECT receipt FROM mcp_service_claim_requests WHERE connection_id=? AND request_id=?`, r.ConnectionID(), requestID).Scan(&raw)
			if e == nil {
				result = &MCPClaimedDelivery{}
				return mcpRead(raw, result)
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			var id, work, control string
			e = tx.QueryRowContext(ctx, `SELECT id,work,control FROM mcp_service_deliveries WHERE receiver_connection=? AND claim='' ORDER BY rowid LIMIT 1`, r.ConnectionID()).Scan(&id, &work, &control)
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			result = &MCPClaimedDelivery{}
			var dispatch agentlink.MCPDispatchBinding
			if work != "" {
				result.Work = &agentlink.MCPWorkDelivery{}
				if e = mcpRead(work, result.Work); e != nil {
					return e
				}
				dispatch = result.Work.Binding
			} else {
				result.Control = &agentlink.MCPControlDelivery{}
				if e = mcpRead(control, result.Control); e != nil {
					return e
				}
				dispatch = result.Control.Dispatch
			}
			if dispatch.Receiver.NodeID != r.NodeID() {
				return agentlink.ErrUnauthorized
			}
			result.Claim = agentlink.MCPClaim{ConnectionID: r.ConnectionID(), DeliveryID: id, ClaimID: rand.Text(), Binding: dispatch}
			_, e = tx.ExecContext(ctx, `UPDATE mcp_service_deliveries SET claim=? WHERE id=?`, mcpJSON(result.Claim), id)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO mcp_service_claim_requests VALUES(?,?,?)`, r.ConnectionID(), requestID, mcpJSON(result))
			return e
		})
		if err != nil || result != nil || !time.Now().Before(deadline) {
			return
		}
		timer := time.NewTimer(min(100*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

type mcpSavedDelivery struct {
	claim                         agentlink.MCPClaim
	work, control, ack, admission string
	last                          uint64
	terminal                      bool
}

func loadMCPDelivery(ctx context.Context, tx *sql.Tx, r agentlink.MCPReceiver, claim agentlink.MCPClaim) (d mcpSavedDelivery, err error) {
	var raw, connection, address string
	err = tx.QueryRowContext(ctx, `SELECT receiver_connection,receiver_address,claim,work,control,ack,admission,last_sequence,terminal FROM mcp_service_deliveries WHERE id=?`, claim.DeliveryID).Scan(&connection, &address, &raw, &d.work, &d.control, &d.ack, &d.admission, &d.last, &d.terminal)
	if err != nil {
		return d, mcpMissing(err)
	}
	// Authenticate ownership before decoding/reconciling an idempotent response.
	var target protocol.AgentRef
	if mcpRead(address, &target) != nil || connection != r.ConnectionID() || target.NodeID != r.NodeID() {
		return d, agentlink.ErrUnauthorized
	}
	if raw == "" {
		return d, agentlink.ErrFenced
	}
	if err = mcpRead(raw, &d.claim); err != nil {
		return
	}
	err = claim.ValidateAgainst(r, d.claim)
	return
}
func (q *MCPAccessQueue) Ack(ctx context.Context, r agentlink.MCPReceiver, ack agentlink.MCPDeliveryAck) error {
	if err := ack.Validate(); err != nil {
		return err
	}
	return storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		d, e := loadMCPDelivery(ctx, tx, r, ack.Claim)
		if e != nil {
			return e
		}
		raw := mcpJSON(ack)
		if d.ack != "" {
			if d.ack != raw {
				return agentlink.ErrConflict
			}
			return nil
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_deliveries SET ack=? WHERE id=?", raw, ack.Claim.DeliveryID)
		return e
	})
}
func acceptedMCPDelivery(d mcpSavedDelivery) bool {
	var ack agentlink.MCPDeliveryAck
	return d.ack != "" && mcpRead(d.ack, &ack) == nil && ack.Accepted
}
func (q *MCPAccessQueue) Admit(ctx context.Context, r agentlink.MCPReceiver, a agentlink.MCPRunAdmission) (err error) {
	defer func() {
		if err == nil {
			q.Tasks.Notify()
		}
	}()
	if err := a.Validate(); err != nil {
		return err
	}
	return storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		d, e := loadMCPDelivery(ctx, tx, r, a.Claim)
		if e != nil {
			return e
		}
		if !acceptedMCPDelivery(d) {
			return agentlink.ErrFenced
		}
		raw := mcpJSON(a)
		if d.admission != "" {
			if d.admission != raw {
				return agentlink.ErrConflict
			}
			return nil
		}
		var execution, taskRaw string
		e = tx.QueryRowContext(ctx, "SELECT execution,task FROM mcp_service_tasks WHERE id=?", a.Claim.Binding.Task.AgentID).Scan(&execution, &taskRaw)
		if e != nil {
			return e
		}
		var previous *agentlink.MCPRunBinding
		if d.control != "" {
			var control agentlink.MCPControlDelivery
			if e = mcpRead(d.control, &control); e != nil {
				return e
			}
			if control.Input == nil {
				return agentlink.ErrUnsupported
			}
			previous = &agentlink.MCPRunBinding{}
			if e = mcpRead(execution, previous); e != nil {
				return e
			}
			if *previous != control.Binding {
				return agentlink.ErrFenced
			}
		}
		if previous == nil && execution != "" {
			return agentlink.ErrFenced
		}
		if e = a.ValidateAgainst(r, d.claim, previous); e != nil {
			return e
		}
		var t agentlink.Task
		if e = mcpRead(taskRaw, &t); e != nil {
			return e
		}
		t.RunID = a.Binding.RunID
		t.State = string(protocol.TaskQueued)
		t.Result = ""
		t.Error = ""
		t.UpdatedAt = time.Now().UTC()
		if e = q.projectCatalogue(ctx, tx, t, a.Binding, a.PreviousRunID); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_tasks SET execution=?,current_claim=?,task=? WHERE id=?", mcpJSON(a.Binding), a.Claim.ClaimID, mcpJSON(t), t.ID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_deliveries SET admission=? WHERE id=?", raw, a.Claim.DeliveryID)
		return e
	})
}
func (q *MCPAccessQueue) Event(ctx context.Context, r agentlink.MCPReceiver, event agentlink.MCPExecutionEvent) (receipt agentlink.MCPEventReceipt, err error) {
	defer func() {
		if err == nil {
			q.Tasks.Notify()
		}
	}()
	digest, err := event.Digest()
	if err != nil {
		return receipt, err
	}
	err = storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		d, e := loadMCPDelivery(ctx, tx, r, event.Claim)
		if e != nil {
			return e
		}
		if !acceptedMCPDelivery(d) || d.admission == "" {
			return agentlink.ErrFenced
		}
		var execution, current, taskRaw string
		e = tx.QueryRowContext(ctx, "SELECT execution,current_claim,task FROM mcp_service_tasks WHERE id=?", event.Claim.Binding.Task.AgentID).Scan(&execution, &current, &taskRaw)
		if e != nil {
			return e
		}
		var binding agentlink.MCPRunBinding
		if e = mcpRead(execution, &binding); e != nil {
			return e
		}
		if current != event.Claim.ClaimID || binding != event.Binding {
			return agentlink.ErrFenced
		}
		var saved string
		e = tx.QueryRowContext(ctx, "SELECT receipt FROM mcp_service_events WHERE claim_id=? AND sequence=?", event.Claim.ClaimID, event.Sequence).Scan(&saved)
		if e == nil {
			if e = mcpRead(saved, &receipt); e != nil {
				return e
			}
			receipt, e = receipt.Retry(event)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e = event.ValidateAfter(r, d.claim, binding, d.last); e != nil {
			return e
		}
		if d.terminal {
			return agentlink.ErrFenced
		}
		var task agentlink.Task
		if e = mcpRead(taskRaw, &task); e != nil {
			return e
		}
		// Preserve business transition order; native waiting/approval may return to running.
		if event.Phase == protocol.TaskQueued && task.State != string(protocol.TaskQueued) {
			return agentlink.ErrFenced
		}
		task.State = string(event.Phase)
		task.UpdatedAt = time.Now().UTC()
		terminal := event.Result != nil
		if terminal {
			task.Result = event.Result.Text
			task.Error = event.Result.Error
		}
		if e = q.projectCatalogue(ctx, tx, task, binding, binding.RunID); e != nil {
			return e
		}
		receipt = agentlink.MCPEventReceipt{Claim: event.Claim, Sequence: event.Sequence, Digest: digest}
		_, e = tx.ExecContext(ctx, "INSERT INTO mcp_service_events VALUES(?,?,?,?,?)", event.Claim.ClaimID, event.Sequence, digest, mcpJSON(event), mcpJSON(receipt))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_deliveries SET last_sequence=?,terminal=? WHERE id=?", event.Sequence, terminal, event.Claim.DeliveryID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_tasks SET task=? WHERE id=?", mcpJSON(task), task.ID)
		return e
	})
	return
}
