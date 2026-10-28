package agenthost

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type receiverFixtureRemote struct {
	mu         sync.Mutex
	queue      []receiverDelivery
	claims     map[string]receiverDelivery
	acks       map[string]agentlink.MCPDeliveryAck
	admissions map[string]agentlink.MCPRunAdmission
	receipts   map[string]map[uint64]agentlink.MCPEventReceipt
	events     []agentlink.MCPExecutionEvent
	fail       string
}

func (r *receiverFixtureRemote) Register(context.Context, agentlink.MCPReceiverRegistration) error {
	return nil
}
func (r *receiverFixtureRemote) lost(where string) error {
	if r.fail == where {
		r.fail = ""
		return errors.New("fixture_response_lost")
	}
	return nil
}
func (r *receiverFixtureRemote) Claim(_ context.Context, id string, _ int) (receiverDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.claims[id]; ok {
		return d, r.lost("claim")
	}
	d := receiverDelivery{}
	if len(r.queue) > 0 {
		d = r.queue[0]
		r.queue = r.queue[1:]
	}
	r.claims[id] = d
	return d, r.lost("claim")
}
func (r *receiverFixtureRemote) Ack(_ context.Context, a agentlink.MCPDeliveryAck) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.acks[a.Claim.ClaimID]; ok && encoded(old) != encoded(a) {
		return agentlink.ErrConflict
	}
	r.acks[a.Claim.ClaimID] = a
	return r.lost("ack")
}
func (r *receiverFixtureRemote) Admit(_ context.Context, a agentlink.MCPRunAdmission) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.admissions[a.Claim.ClaimID]; ok && old != a {
		return agentlink.ErrConflict
	}
	r.admissions[a.Claim.ClaimID] = a
	return r.lost("admit")
}
func (r *receiverFixtureRemote) InputReceipt(_ context.Context, input agentlink.MCPInputReceipt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if input.Validate() != nil {
		return agentlink.ErrInvalid
	}
	return r.lost("input")
}
func (r *receiverFixtureRemote) Event(_ context.Context, e agentlink.MCPExecutionEvent) (agentlink.MCPEventReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	bySequence := r.receipts[e.Claim.ClaimID]
	if bySequence == nil {
		bySequence = map[uint64]agentlink.MCPEventReceipt{}
		r.receipts[e.Claim.ClaimID] = bySequence
	}
	if old, ok := bySequence[e.Sequence]; ok {
		return old.Retry(e)
	}
	digest, err := e.Digest()
	if err != nil {
		return agentlink.MCPEventReceipt{}, err
	}
	receipt := agentlink.MCPEventReceipt{Claim: e.Claim, Sequence: e.Sequence, Digest: digest}
	bySequence[e.Sequence] = receipt
	r.events = append(r.events, e)
	return receipt, r.lost("event")
}
func receiverFixture(t *testing.T) (*receiverPump, *receiverFixtureRemote, receiverDelivery) {
	t.Helper()
	b := testBackend(t)
	db, err := storage.OpenDatabase(filepath.Join(t.TempDir(), "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	identity, err := agentlink.NewMCPReceiver("fixture-connection", b.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES(?,?,?,?,'active')", identity.ConnectionID(), "http://fixture.invalid/mcp", b.nodeID, "fixture-ref"); err != nil {
		t.Fatal(err)
	}
	from := protocol.AgentRef{NodeID: "origin", Kind: "assistant", AgentID: "main"}
	binding := agentlink.MCPDispatchBinding{Task: protocol.AgentRef{NodeID: "origin", Kind: "task", AgentID: "origin-task"}, Receiver: agentlink.NodeTarget(b.nodeID)}
	claim := agentlink.MCPClaim{ConnectionID: identity.ConnectionID(), DeliveryID: "first-delivery", ClaimID: "first-claim", Binding: binding}
	work := agentlink.MCPWorkDelivery{Version: 1, DeliveryID: claim.DeliveryID, Binding: binding, Request: agentlink.TaskRequest{RequestID: "original-request", From: from, Target: binding.Receiver, Text: "receiver fixture"}}
	delivery := receiverDelivery{Claim: &claim, Work: &work}
	remote := &receiverFixtureRemote{queue: []receiverDelivery{delivery}, claims: map[string]receiverDelivery{}, acks: map[string]agentlink.MCPDeliveryAck{}, admissions: map[string]agentlink.MCPRunAdmission{}, receipts: map[string]map[uint64]agentlink.MCPEventReceipt{}}
	p := &receiverPump{backend: b, control: db, identity: identity, remote: remote}
	return p, remote, delivery
}
func receiverMustStep(t *testing.T, p *receiverPump) {
	t.Helper()
	if err := p.step(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func receiverRecordFixture(t *testing.T, p *receiverPump, d receiverDelivery) receiverRecord {
	t.Helper()
	r, err := readReceiverRecord(context.Background(), p.backend.db, p.identity.ConnectionID(), d.Claim.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReceiverResponseLossNeverDuplicatesAdmissionOrExecution(t *testing.T) {
	for _, point := range []string{"claim", "ack", "admit", "event"} {
		t.Run(point, func(t *testing.T) {
			p, remote, d := receiverFixture(t)
			ctx := context.Background()
			remote.fail = point
			// Claim response loss leaves exactly the same saved claim request pending.
			err := p.step(ctx)
			if point == "claim" {
				if err == nil {
					t.Fatal("claim loss hidden")
				}
				receiverMustStep(t, p)
			} else if err != nil {
				t.Fatal(err)
			}
			if point != "claim" {
				if err = p.step(ctx); err == nil {
					t.Fatal("response loss hidden")
				}
			}
			// Replace the pump after the failed exchange: only durable state survives.
			restarted := *p
			p = &restarted
			receiverMustStep(t, p)
			r := receiverRecordFixture(t, p, d)
			if r.Admission == nil {
				t.Fatal("missing actual admission")
			}
			if r.Admission.Binding.Task == d.Claim.Binding.Task || r.Admission.Binding.RunID == "" {
				t.Fatal("lost origin/execution mapping", r)
			}
			j, err := p.backend.read(ctx, d.Work.Request.From, r.Admission.Binding.Task.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			p.backend.runTask(ctx, j)
			receiverMustStep(t, p)
			r = receiverRecordFixture(t, p, d)
			if r.LastPhase != protocol.TaskComplete || r.Outbox != nil || r.Receipt == nil {
				t.Fatal("terminal outbox not reconciled", r)
			}
			if err = p.accept(ctx, d); err != nil {
				t.Fatal(err)
			}
			receiverMustStep(t, p)
			var count int
			if err = p.backend.db.QueryRow("SELECT count(*) FROM agent_adapter_jobs").Scan(&count); err != nil || count != 1 {
				t.Fatal("duplicate jobs", count, err)
			}
			// Re-entering the native scheduler for the old selection is fenced.
			p.backend.runTask(ctx, j)
			current, err := p.backend.read(ctx, d.Work.Request.From, j.Task.ID)
			if err != nil || current.Task.State != "completed" {
				t.Fatal(current, err)
			}
			if len(remote.admissions) != 1 {
				t.Fatal("duplicate admissions", remote.admissions)
			}
		})
	}
}

func TestReceiverTerminalContinuationUsesNewRunAndRejectsStaleControl(t *testing.T) {
	for _, retiredCount := range []int{0, 32, 256} {
		t.Run(strconv.Itoa(retiredCount), func(t *testing.T) { receiverTerminalContinuationFixture(t, retiredCount) })
	}
}

func receiverTerminalContinuationFixture(t *testing.T, retiredCount int) {
	t.Helper()
	p, remote, d := receiverFixture(t)
	ctx := context.Background()
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	old := receiverRecordFixture(t, p, d)
	j, err := p.backend.read(ctx, d.Work.Request.From, old.Admission.Binding.Task.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	p.backend.runTask(ctx, j)
	receiverMustStep(t, p)
	assertRetiredUnchanged := receiverRetiredJobsFixture(t, p, retiredCount)
	var previousRun string
	if err = p.backend.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", j.Task.ID).Scan(&previousRun); err != nil {
		t.Fatal(err)
	}
	control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "input-delivery", Dispatch: d.Claim.Binding, Binding: old.Admission.Binding, Caller: d.Work.Request.From, InputPhase: protocol.TaskComplete, Input: &agentlink.MCPTaskInput{RequestID: "follow-up", TaskID: d.Claim.Binding.Task.AgentID, RunID: old.Admission.Binding.RunID, Text: "continue fixture"}}
	claim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: control.DeliveryID, ClaimID: "input-claim", Binding: d.Claim.Binding}
	next := receiverDelivery{Claim: &claim, Control: &control}
	remote.queue = append(remote.queue, next)
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	current := receiverRecordFixture(t, p, next)
	if current.Admission == nil || current.Admission.Binding.Task != old.Admission.Binding.Task || current.Admission.PreviousRunID != old.Admission.Binding.RunID || current.Admission.Binding.RunID == old.Admission.Binding.RunID {
		t.Fatal("continuation mapping", current)
	}
	// A stale selection from the previous run cannot launch the new queued run.
	p.backend.runTask(ctx, j)
	pending, err := p.backend.read(ctx, d.Work.Request.From, j.Task.ID)
	if err != nil || pending.Task.State != "queued" {
		t.Fatal("old scheduler selection crossed runs", pending, err)
	}
	p.backend.runTask(ctx, pending)
	receiverMustStep(t, p)
	current = receiverRecordFixture(t, p, next)
	if current.LastPhase != protocol.TaskComplete || current.Outbox != nil || current.Receipt == nil || !current.AdmissionAcknowledged {
		t.Fatal("continued terminal receipt not reconciled", current)
	}
	var savedPreviousRun string
	if err = p.backend.db.QueryRow("SELECT previous_run FROM agent_adapter_inputs WHERE task_id=?", j.Task.ID).Scan(&savedPreviousRun); err != nil || savedPreviousRun != previousRun {
		t.Fatal("previous run bytes changed", err)
	}
	assertRetiredUnchanged()
	var history int
	p.backend.db.QueryRow("SELECT count(*) FROM agent_adapter_inputs").Scan(&history)
	if history != 1 {
		t.Fatal(history)
	}
	// A control accepted by the trusted queue may arrive after prior mailbox
	// messages advanced the run. The gateway owns the original expected-run check;
	// the receiver must preserve that acceptance and use the current run chain.

}

func TestReceiverCancelRecordsIntentAndUnknownCleanupCannotContinue(t *testing.T) {
	p, _, d := receiverFixture(t)
	ctx := context.Background()
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	old := receiverRecordFixture(t, p, d)
	if err := p.backend.update(ctx, old.Admission.Binding.Task.AgentID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "cancel-delivery", Dispatch: d.Claim.Binding, Binding: old.Admission.Binding, Caller: d.Work.Request.From, Cancel: &agentlink.MCPCancelInput{RequestID: "cancel-request", TaskID: d.Claim.Binding.Task.AgentID, RunID: old.Admission.Binding.RunID}}
	claim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: control.DeliveryID, ClaimID: "cancel-claim", Binding: d.Claim.Binding}
	cancel := receiverDelivery{Claim: &claim, Control: &control}
	if err := p.accept(ctx, cancel); err != nil {
		t.Fatal(err)
	}
	r := receiverRecordFixture(t, p, cancel)
	if err := p.advance(ctx, &r); err != nil {
		t.Fatal(err)
	}
	j, err := p.backend.read(ctx, d.Work.Request.From, old.Admission.Binding.Task.AgentID)
	if err != nil || j.Task.State != "cancelling" || !r.ControlApplied || r.Admission != nil {
		t.Fatal(j, r, err)
	}
	if err = p.snapshot(ctx, &old); err != nil || old.Outbox != nil {
		t.Fatal("intent became cancellation evidence", old, err)
	}
	if err = p.backend.recover(ctx); err != nil {
		t.Fatal(err)
	}
	receiverMustStep(t, p)
	old = receiverRecordFixture(t, p, d)
	if old.LastPhase != protocol.TaskFailed {
		t.Fatal("unknown outcome became cancelled", old)
	}
	j, err = p.backend.read(ctx, d.Work.Request.From, j.Task.ID)
	if err != nil || !j.CleanupUncertain {
		t.Fatal(j, err)
	}
	_, err = p.backend.Input(ctx, agentlink.TaskGuidance{From: d.Work.Request.From, ID: j.Task.ID, RequestID: "unsafe-resume", Text: "resume"})
	if !errors.Is(err, agentlink.ErrFenced) {
		t.Fatal("uncertain cleanup resumed", err)
	}
}

func TestReceiverRejectsForeignDeliveryAndRevocationBeforeAdmission(t *testing.T) {
	p, _, d := receiverFixture(t)
	ctx := context.Background()
	foreign := *d.Claim
	foreign.ConnectionID = "other-connection"
	if err := p.accept(ctx, receiverDelivery{Claim: &foreign, Work: d.Work}); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal(err)
	}
	receiverMustStep(t, p)
	if _, err := p.control.Exec("UPDATE mcp_receiver_connections SET state='revoked'"); err != nil {
		t.Fatal(err)
	}
	if err := p.step(ctx); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal(err)
	}
	var jobs int
	p.backend.db.QueryRow("SELECT count(*) FROM agent_adapter_jobs").Scan(&jobs)
	if jobs != 0 {
		t.Fatal("revoked receiver admitted work", jobs)
	}
}

func TestReceiverJournalCASFencesConcurrentPumpAndConflictingDelivery(t *testing.T) {
	p, _, d := receiverFixture(t)
	ctx := context.Background()
	receiverMustStep(t, p)
	first := receiverRecordFixture(t, p, d)
	second := first
	first.Acknowledged = true
	if err := p.save(ctx, &first); err != nil {
		t.Fatal(err)
	}
	if err := p.save(ctx, &second); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("stale writer accepted", err)
	}
	changedClaim := *d.Claim
	changedClaim.ClaimID = "superseded-claim"
	if err := p.accept(ctx, receiverDelivery{Claim: &changedClaim, Work: d.Work}); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("changed claim accepted", err)
	}
	changed := *d.Work
	changed.Request.Text = "different"
	if err := p.accept(ctx, receiverDelivery{Claim: d.Claim, Work: &changed}); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("conflicting delivery accepted", err)
	}
}

func TestReceiverConcurrentAdmissionHasOneBackendReceipt(t *testing.T) {
	p, _, d := receiverFixture(t)
	ctx := context.Background()
	receiverMustStep(t, p)
	a := receiverRecordFixture(t, p, d)
	if err := p.remote.Ack(ctx, a.Ack); err != nil {
		t.Fatal(err)
	}
	a.Acknowledged = true
	if err := p.save(ctx, &a); err != nil {
		t.Fatal(err)
	}
	b := a
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, record := range []*receiverRecord{&a, &b} {
		wg.Add(1)
		go func(r *receiverRecord) { defer wg.Done(); results <- p.admit(ctx, r) }(record)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if a.Admission == nil || b.Admission == nil || *a.Admission != *b.Admission {
		t.Fatal("different concurrent admissions", a, b)
	}
	var jobs int
	p.backend.db.QueryRow("SELECT count(*) FROM agent_adapter_jobs").Scan(&jobs)
	if jobs != 1 {
		t.Fatal("double pump duplicated backend", jobs)
	}
}

func TestReceiverControlRejectsForeignCallerTaskAndRun(t *testing.T) {
	for _, kind := range []string{"caller", "task", "run"} {
		t.Run(kind, func(t *testing.T) {
			p, _, d := receiverFixture(t)
			ctx := context.Background()
			receiverMustStep(t, p)
			receiverMustStep(t, p)
			old := receiverRecordFixture(t, p, d)
			control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "bad-control", Dispatch: d.Claim.Binding, Binding: old.Admission.Binding, Caller: d.Work.Request.From, InputPhase: protocol.TaskComplete, Input: &agentlink.MCPTaskInput{RequestID: "bad-input", TaskID: d.Claim.Binding.Task.AgentID, RunID: old.Admission.Binding.RunID, Text: "continue"}}
			switch kind {
			case "caller":
				control.Caller.AgentID = "foreign"
			case "task":
				control.Binding.Task.AgentID = "foreign-task"
			case "run":
				control.Binding.RunID = "foreign-run"
				control.Input.RunID = "foreign-run"
			}
			claim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: control.DeliveryID, ClaimID: "bad-control-claim", Binding: d.Claim.Binding}
			delivery := receiverDelivery{Claim: &claim, Control: &control}
			if err := p.accept(ctx, delivery); err != nil {
				t.Fatal(err)
			}
			r := receiverRecordFixture(t, p, delivery)
			if r.Ack.Accepted || r.Ack.Error == nil {
				t.Fatal("invalid control not durably rejected", kind)
			}
			if err := p.advance(ctx, &r); err != nil {
				t.Fatal(err)
			}
			if r.Admission != nil {
				t.Fatal("rejected control admitted", kind)
			}
			current, err := p.backend.read(ctx, d.Work.Request.From, old.Admission.Binding.Task.AgentID)
			if err != nil || current.Task.RunID != old.Admission.Binding.RunID {
				t.Fatal("rejected control changed execution", current, err)
			}
		})
	}
}

func TestReceiverDatabaseReopenReconcilesLaunchUncertaintyWithoutReplay(t *testing.T) {
	for _, state := range []string{"queued", "running"} {
		t.Run(state, func(t *testing.T) {
			p, _, d := receiverFixture(t)
			ctx := context.Background()
			receiverMustStep(t, p)
			receiverMustStep(t, p)
			r := receiverRecordFixture(t, p, d)
			original := r.Admission.Binding
			if state == "running" {
				if err := p.backend.update(ctx, original.Task.AgentID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
					t.Fatal(err)
				}
				if err := p.snapshot(ctx, &r); err != nil || r.Outbox == nil {
					t.Fatal("launch intent outbox missing", err)
				}
			}
			root, node := p.backend.root, p.backend.nodeID
			if err := p.backend.db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reopened.Close() })
			b, err := newBackend(reopened, node, root)
			if err != nil {
				t.Fatal(err)
			}
			b.binary = fakeBinary
			p.backend = b
			if err = b.recover(ctx); err != nil {
				t.Fatal(err)
			}
			receiverMustStep(t, p)
			receiverMustStep(t, p)
			result := receiverRecordFixture(t, p, d)
			if result.Admission.Binding != original || result.LastPhase != protocol.TaskFailed {
				t.Fatal("recovery changed execution identity or replayed", result)
			}
			j, err := b.read(ctx, d.Work.Request.From, original.Task.AgentID)
			if err != nil || j.CleanupUncertain != (state == "running") {
				t.Fatal("cleanup fence lost", j, err)
			}
			b.runTask(ctx, j)
			current, err := b.read(ctx, d.Work.Request.From, j.Task.ID)
			if err != nil || current.Task.State != "failed" {
				t.Fatal("restart launched work", current, err)
			}
		})
	}
}

func TestReceiverCapacityDoesNotBlockCancellationControls(t *testing.T) {
	p, remote, d := receiverFixture(t)
	ctx := context.Background()
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	original := receiverRecordFixture(t, p, d)
	for i := 0; i < 31; i++ {
		if _, err := p.backend.Submit(ctx, agentlink.TaskRequest{From: d.Work.Request.From, Target: d.Work.Request.Target, RequestID: "capacity-" + strconv.Itoa(i), Text: "queued fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	extraWork := *d.Work
	extraWork.DeliveryID = "capacity-delivery"
	extraWork.Request.RequestID = "capacity-work"
	extraWork.Binding.Task.AgentID = "capacity-origin"
	extraClaim := *d.Claim
	extraClaim.DeliveryID = extraWork.DeliveryID
	extraClaim.ClaimID = "capacity-claim"
	extraClaim.Binding = extraWork.Binding
	remote.queue = append(remote.queue, receiverDelivery{Claim: &extraClaim, Work: &extraWork})
	control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "capacity-cancel", Dispatch: d.Claim.Binding, Binding: original.Admission.Binding, Caller: d.Work.Request.From, Cancel: &agentlink.MCPCancelInput{RequestID: "capacity-cancel-request", TaskID: d.Claim.Binding.Task.AgentID, RunID: original.Admission.Binding.RunID}}
	cancelClaim := *d.Claim
	cancelClaim.DeliveryID = control.DeliveryID
	cancelClaim.ClaimID = "capacity-cancel-claim"
	remote.queue = append(remote.queue, receiverDelivery{Claim: &cancelClaim, Control: &control})
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	current, err := p.backend.read(ctx, d.Work.Request.From, original.Admission.Binding.Task.AgentID)
	if err != nil || current.Task.State != "cancelled" {
		t.Fatal("capacity blocked control", current, err)
	}
	r := receiverRecordFixture(t, p, receiverDelivery{Claim: &extraClaim})
	if r.Admission == nil {
		t.Fatal("known non-admission did not recover after capacity freed")
	}
}
