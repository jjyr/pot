package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
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
	messageResult   func(context.Context, *sql.Tx, agentlink.MCPWorkDelivery, agentlink.MCPExecutionEvent) error
	liveMu          sync.Mutex
	live            map[string]time.Time
	dispatchMu      sync.Mutex
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
// actual is the server policy ceiling, supplied independently of tool metadata.
// The authenticated, consented node owns driver capability truth; the policy
// permits only its supported subset and never owner operations.
func (q *MCPAccessQueue) Register(ctx context.Context, receiver agentlink.MCPReceiver, registration agentlink.MCPReceiverRegistration, actual agentlink.Description) error {
	if receiver.NodeID() == q.NodeID {
		return agentlink.ErrUnauthorized
	}
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, receiver.ConnectionID()); e != nil {
			return e
		}
	}
	for _, op := range registration.Capabilities.Operations {
		if op != agentlink.AssistantManagement && op != agentlink.AssistantProfile && op != agentlink.AssistantDelete && op != agentlink.TaskSubmit && op != agentlink.TaskRead && op != agentlink.TaskCancel && op != agentlink.TaskInput && op != agentlink.TaskEvents && op != agentlink.ChatSend && op != agentlink.ChatRead && op != agentlink.ChatReceipt && op != agentlink.Approvals {
			return agentlink.ErrUnsupported
		}
	}
	if err := registration.ValidateAgainst(receiver, actual); err != nil {
		return err
	}
	err := storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE mcp_remote_approvals SET state='invalidated' WHERE connection_id=? AND epoch!=? AND state='pending'", receiver.ConnectionID(), registration.Epoch); e != nil {
			return e
		}
		address := mcpJSON(registration.Receiver.Address)
		if _, e := tx.ExecContext(ctx, "UPDATE mcp_native_user_operations SET state='unknown' WHERE connection_id=? AND address=? AND epoch!=? AND state IN ('pending','claimed')", receiver.ConnectionID(), address, registration.Epoch); e != nil {
			return e
		}
		var profile string
		var deleted bool
		e := tx.QueryRowContext(ctx, "SELECT profile,deleted FROM mcp_native_user_profiles WHERE connection_id=? AND address=?", receiver.ConnectionID(), address).Scan(&profile, &deleted)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if deleted {
			return agentlink.ErrNotFound
		}
		if profile != "" {
			var saved agentlink.Profile
			if e = mcpRead(profile, &saved); e != nil {
				return e
			}
			registration.Receiver.Name = saved.Name
			registration.Receiver.Avatar = saved.Avatar
		}

		var connection string
		err := tx.QueryRowContext(ctx, "SELECT connection_id FROM mcp_receiver_endpoints WHERE address=?", address).Scan(&connection)
		if err == nil && connection != receiver.ConnectionID() {
			// Explicit new owner consent may replace a revoked endpoint for
			// future work. Existing deliveries keep their original connection;
			// accepted claims are never migrated or replayed.
			if q.CheckConnection == nil || !errors.Is(q.CheckConnection(ctx, connection), agentlink.ErrUnauthorized) {
				return agentlink.ErrUnauthorized
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM mcp_receiver_endpoints WHERE connection_id=? AND address=?", connection, address); err != nil {
				return err
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO mcp_receiver_endpoints(connection_id,address,registration) VALUES(?,?,?) ON CONFLICT(connection_id,address) DO UPDATE SET registration=excluded.registration`, receiver.ConnectionID(), address, mcpJSON(registration))
		return err
	})
	if err == nil {
		q.receiverSeen(receiver.ConnectionID())
	}
	return err
}

func (q *MCPAccessQueue) Directory(ctx context.Context) (agentlink.Directory, error) {
	d := agentlink.Directory{NodeID: q.NodeID, Name: "Pot", Adapter: "pot", Targets: []agentlink.Endpoint{}}
	rows, err := q.DB.QueryContext(ctx, "SELECT connection_id,registration FROM mcp_receiver_endpoints ORDER BY address")
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw, connection string
		var r agentlink.MCPReceiverRegistration
		if err = rows.Scan(&connection, &raw); err != nil {
			return d, err
		}
		if err = mcpRead(raw, &r); err != nil {
			return d, err
		}
		if q.CheckConnection != nil {
			if e := q.CheckConnection(ctx, connection); e != nil {
				continue
			}
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
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, c.ConnectionID()); e != nil {
			return a, e
		}
	}
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
		if e = bindMCPOriginalHumanTx(ctx, tx, c, in.RequestID, found); e != nil {
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
		if q.CheckConnection != nil {
			if e = q.CheckConnection(ctx, connection); e != nil {
				return agentlink.ErrUnavailable
			}
		}
		if !q.ReceiverOnline(connection) {
			return agentlink.ErrUnavailable
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
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, c.ConnectionID()); e != nil {
			return t, e
		}
	}
	err = storage.Transaction(ctx, q.DB, func(tx *sql.Tx) error { var e error; t, _, e = ownedMCPTask(ctx, tx, c, id); return e })
	return
}

// Control serializes original caller ownership, expected-run CAS and enqueue.
func (q *MCPAccessQueue) Control(ctx context.Context, c agentlink.MCPCaller, input *agentlink.MCPTaskInput, cancel *agentlink.MCPCancelInput) (a MCPTaskAcceptance, err error) {
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, c.ConnectionID()); e != nil {
			return a, e
		}
	}
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
		if q.CheckConnection != nil {
			if e = q.CheckConnection(ctx, connection); e != nil {
				return agentlink.ErrUnavailable
			}
		}
		var registration agentlink.MCPReceiverRegistration
		if e = mcpRead(raw, &registration); e != nil {
			return e
		}
		if !registration.Capabilities.Supports(op) {
			return agentlink.ErrUnsupported
		}
		d := agentlink.MCPControlDelivery{Version: agentlink.MCPContractVersion, DeliveryID: rand.Text(), Dispatch: agentlink.MCPDispatchBinding{Task: protocol.AgentRef{NodeID: q.NodeID, Kind: "task", AgentID: id}, Receiver: t.Receiver.Address}, Binding: *binding, Caller: c.Actor(), Input: input, Cancel: cancel}
		if input != nil {
			d.InputPhase = protocol.TaskPhase(t.State)
		}
		if e = d.Validate(); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO mcp_service_deliveries(id,task_id,receiver_connection,receiver_address,control) VALUES(?,?,?,?,?)`, d.DeliveryID, id, connection, mcpJSON(t.Receiver.Address), mcpJSON(d))
		if e != nil {
			return e
		}
		if cancel != nil {
			if e = q.Tasks.StopExecutionTx(ctx, tx, c.Actor().AgentID, id); e != nil {
				return e
			}
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
	if _, e := agentlink.NewMCPReceiver(r.ConnectionID(), r.NodeID()); e != nil {
		return nil, e
	}
	if !core.Identifier(requestID) || wait < 0 || wait > 30*time.Second {
		return nil, agentlink.ErrInvalid
	}
	q.receiverSeen(r.ConnectionID())
	deadline := time.Now().Add(wait)
	for {
		if check, ok := ctx.Value(mcpAdmissionCheckKey{}).(func() error); ok {
			if e := check(); e != nil {
				return nil, e
			}
		}
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
				if e = mcpRead(raw, result); e != nil {
					return e
				}
				return result.Claim.ValidateAgainst(r, result.Claim)
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
func (q *MCPAccessQueue) Ack(ctx context.Context, r agentlink.MCPReceiver, ack agentlink.MCPDeliveryAck) (err error) {
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, r.ConnectionID()); e != nil {
			return e
		}
	}
	defer func() {
		if err == nil {
			q.Tasks.Notify()
		}
	}()
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
		if !ack.Accepted && d.work != "" {
			var actorRaw, taskRaw string
			e = tx.QueryRowContext(ctx, "SELECT actor,task FROM mcp_service_tasks WHERE id=?", ack.Claim.Binding.Task.AgentID).Scan(&actorRaw, &taskRaw)
			if e != nil {
				return e
			}
			var actor protocol.AgentRef
			var task agentlink.Task
			if e = mcpRead(actorRaw, &actor); e != nil {
				return e
			}
			if e = mcpRead(taskRaw, &task); e != nil {
				return e
			}
			var work agentlink.MCPWorkDelivery
			if e = mcpRead(d.work, &work); e != nil {
				return e
			}
			if work.Message == nil {
				if e = q.Tasks.RejectExecutionTx(ctx, tx, actor.AgentID, task.ID, string(ack.Error.Code)); e != nil {
					return e
				}
			}
			task.State = string(protocol.TaskFailed)
			task.Error = string(ack.Error.Code)
			task.UpdatedAt = time.Now().UTC()
			if _, e = tx.ExecContext(ctx, "UPDATE mcp_service_tasks SET task=? WHERE id=?", mcpJSON(task), task.ID); e != nil {
				return e
			}
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
	q.dispatchMu.Lock()
	defer q.dispatchMu.Unlock()
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, r.ConnectionID()); e != nil {
			return e
		}
	}
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
		var disposition string
		if e = tx.QueryRowContext(ctx, "SELECT input_receipt FROM mcp_service_deliveries WHERE id=?", a.Claim.DeliveryID).Scan(&disposition); e != nil {
			return e
		}
		if disposition != "" {
			return agentlink.ErrFenced
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
			// The input's old run was checked when it entered the mailbox. Earlier
			// accepted messages may since have advanced the same execution Task.
			if previous.Task != control.Binding.Task || previous.Receiver != control.Binding.Receiver {
				return agentlink.ErrFenced
			}
			var earlier int
			if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_service_deliveries WHERE task_id=? AND rowid<(SELECT rowid FROM mcp_service_deliveries WHERE id=?) AND control!='' AND json_type(control,'$.input')='object' AND admission='' AND (ack='' OR json_extract(ack,'$.accepted')=1) AND COALESCE(json_extract(NULLIF(input_receipt,''),'$.state'),'')!='cancelled'`, a.Claim.Binding.Task.AgentID, a.Claim.DeliveryID).Scan(&earlier); e != nil {
				return e
			}
			if earlier != 0 {
				return agentlink.ErrFenced
			}
			var current agentlink.Task
			if e = mcpRead(taskRaw, &current); e != nil {
				return e
			}
			if !terminalLinkTask(current.State) || current.State == "unknown" || current.State == "cancelled" && (control.Binding.RunID != previous.RunID || control.InputPhase.Scope() == "active") {
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
		if e = q.projectCatalogue(ctx, tx, t, a.Binding, a.PreviousRunID, nil); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_tasks SET execution=?,current_claim=?,task=?,cancel_requested=0 WHERE id=?", mcpJSON(a.Binding), a.Claim.ClaimID, mcpJSON(t), t.ID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE mcp_service_deliveries SET admission=? WHERE id=?", raw, a.Claim.DeliveryID)
		return e
	})
}
func (q *MCPAccessQueue) Event(ctx context.Context, r agentlink.MCPReceiver, event agentlink.MCPExecutionEvent) (receipt agentlink.MCPEventReceipt, err error) {
	q.dispatchMu.Lock()
	defer q.dispatchMu.Unlock()
	if q.CheckConnection != nil {
		if e := q.CheckConnection(ctx, r.ConnectionID()); e != nil {
			return receipt, e
		}
	}
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
		if terminal && q.messageResult != nil {
			var workRaw string
			var work agentlink.MCPWorkDelivery
			if e = tx.QueryRowContext(ctx, "SELECT work FROM mcp_service_deliveries WHERE task_id=? AND work!='' ORDER BY rowid LIMIT 1", task.ID).Scan(&workRaw); e != nil {
				return e
			}
			if e = mcpRead(workRaw, &work); e != nil {
				return e
			}
			if e = q.messageResult(ctx, tx, work, event); e != nil {
				return e
			}
		}
		if terminal {
			if _, e = tx.ExecContext(ctx, "UPDATE mcp_remote_approvals SET state='invalidated' WHERE task_id=? AND run_id=? AND state='pending'", task.ID, binding.RunID); e != nil {
				return e
			}
			task.Result = event.Result.Text
			task.Error = event.Result.Error
		}
		if e = q.projectCatalogue(ctx, tx, task, binding, binding.RunID, event.Result); e != nil {
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
