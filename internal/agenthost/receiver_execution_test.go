package agenthost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestReceiverExecutionContextUsesActualActiveTaskActorRun(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
	task, err := h.backend.Submit(ctx, agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(h.backend.nodeID), RequestID: "bound-context", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.backend.update(ctx, task.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	h.backend.active[task.ID] = func() {}
	input := hostToolInput{Operation: "execution_context", JobID: task.ID, RunID: task.RunID}
	binding, err := h.executionContext(ctx, input)
	if err != nil || binding.Actor.Kind != "task" || binding.Actor != binding.Task || binding.Task.AgentID != task.ID || binding.RunID != task.RunID {
		t.Fatal("wrong execution actor", binding, err)
	}
	for _, mutate := range []func(*hostToolInput){func(in *hostToolInput) { in.RunID = "old-run" }, func(in *hostToolInput) { in.JobID = "foreign-task" }, func(in *hostToolInput) { in.FromAssistantID = "codex" }} {
		invalid := input
		mutate(&invalid)
		if _, err = h.executionContext(ctx, invalid); err == nil {
			t.Fatal("foreign context accepted")
		}
	}
	delete(h.backend.active, task.ID)
	if _, err = h.executionContext(ctx, input); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatal("inactive execution accepted", err)
	}
}

func TestReceiverDisconnectPreservesNativeExecutionAndHistory(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
	task, err := h.backend.Submit(ctx, agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(h.backend.nodeID), RequestID: "disconnect-context", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.backend.update(ctx, task.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	var cancelled atomic.Bool
	h.backend.active[task.ID] = func() { cancelled.Store(true) }
	if _, err = h.db.Exec("INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES('connection','http://fixture.invalid/mcp',?,'fixture-secret','active')", h.backend.nodeID); err != nil {
		t.Fatal(err)
	}
	client := agentHostClient(h.token)
	defer client.client.CloseIdleConnections()
	client.client.Transport = hostRoundTripper(func(r *http.Request) (*http.Response, error) {
		r.RemoteAddr = "127.0.0.1:23456"
		r.Host = hostListen
		w := httptest.NewRecorder()
		h.handler().ServeHTTP(w, r)
		return w.Result(), nil
	})
	var result any
	if err = client.call(ctx, "/api/v1/agent-host/receiver/disconnect", struct {
		ConnectionID string `json:"connection_id"`
	}{"connection"}, &result); err != nil {
		t.Fatal(err)
	}
	var state string
	h.db.QueryRow("SELECT state FROM mcp_receiver_connections WHERE id='connection'").Scan(&state)
	current, err := h.backend.read(ctx, from, task.ID)
	if err != nil || cancelled.Load() || current.Task.State != "running" || state != "disconnected" {
		t.Fatal("disconnect altered execution", current, state, err)
	}
}

func TestReceiverNativeReviewCannotApproveAnotherRun(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	caller := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
	task, err := b.Submit(ctx, agentlink.TaskRequest{From: caller, Target: agentlink.NodeTarget(b.nodeID), RequestID: "review-run", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := b.read(ctx, caller, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.reviews["request-id"] = &pendingReview{value: agentlink.ExecutionApproval{ID: "request-id", JobID: task.ID}, job: original, ctx: ctx, done: make(chan string, 1)}
	if err = b.update(ctx, task.ID, func(j *job) error { j.Task.RunID = "new-run"; j.Task.State = "waiting_approval"; return nil }); err != nil {
		t.Fatal(err)
	}
	_, err = b.Review(ctx, agentlink.ReviewRequest{From: protocol.AgentRef{NodeID: "pot", Kind: "user", AgentID: "operator"}, Caller: caller, ID: "request-id", Action: "allow_once"})
	if !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("old review crossed into new run", err)
	}
	if b.reviews["request-id"].resolved {
		t.Fatal("stale request consumed a decision")
	}
}

func TestReceiverApprovalDecisionIsBoundAndNeverReplayed(t *testing.T) {
	p, _, delivery := receiverFixture(t)
	p.epoch = "epoch"
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	record := receiverRecordFixture(t, p, delivery)
	ctx := context.Background()
	caller := record.Work.Request.From
	taskID := record.Admission.Binding.Task.AgentID
	if e := p.backend.update(ctx, taskID, func(j *job) error { j.Task.State = "waiting_approval"; return nil }); e != nil {
		t.Fatal(e)
	}
	j, e := p.backend.read(ctx, caller, taskID)
	if e != nil {
		t.Fatal(e)
	}
	p.backend.active[taskID] = func() {}
	pending := &pendingReview{value: agentlink.ExecutionApproval{ID: "approval", JobID: taskID}, job: j, done: make(chan string, 1), ctx: ctx}
	p.backend.reviews["approval"] = pending
	decision := agentlink.MCPApprovalDecision{ApprovalID: "approval", Epoch: p.epoch, RequestID: record.Work.Request.RequestID, User: protocol.AgentRef{NodeID: caller.NodeID, Kind: "user", AgentID: "operator"}, Action: "allow_once"}
	control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "approval-delivery", Dispatch: record.Claim.Binding, Binding: record.Admission.Binding, Caller: caller, Approval: &decision}
	claim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: control.DeliveryID, ClaimID: "approval-claim", Binding: control.Dispatch}
	d := receiverDelivery{Claim: &claim, Control: &control}
	if e = p.accept(ctx, d); e != nil {
		t.Fatal(e)
	}
	saved := receiverRecordFixture(t, p, d)
	saved.Acknowledged = true
	if e = p.save(ctx, &saved); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*agentlink.MCPControlDelivery){func(c *agentlink.MCPControlDelivery) { c.Caller.AgentID = "foreign" }, func(c *agentlink.MCPControlDelivery) { c.Binding.RunID = "old-run" }, func(c *agentlink.MCPControlDelivery) { c.Approval.Epoch = "old-epoch" }, func(c *agentlink.MCPControlDelivery) { c.Approval.RequestID = "wrong-request" }} {
		bad := saved
		copyControl := *saved.Control
		copyDecision := *saved.Control.Approval
		copyControl.Approval = &copyDecision
		bad.Control = &copyControl
		mutate(bad.Control)
		if e = p.applyApproval(ctx, &bad); e == nil {
			t.Fatal("unbound native decision accepted")
		}
		if pending.resolved {
			t.Fatal("bad decision consumed native channel")
		}
	}
	if e = p.backend.update(ctx, taskID, func(j *job) error { j.Task.State = "cancelled"; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = p.applyApproval(ctx, &saved); !errors.Is(e, agentlink.ErrFenced) || pending.resolved {
		t.Fatal("terminal native request consumed a decision", e)
	}
	if e = p.backend.update(ctx, taskID, func(j *job) error { j.Task.State = "waiting_approval"; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = p.applyApproval(ctx, &saved); e != nil {
		t.Fatal(e)
	}
	if !saved.ControlApplied || <-pending.done != "allow_once" {
		t.Fatal("native one-shot missing")
	}
	if e = p.applyApproval(ctx, &saved); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("native decision replayed", e)
	}
}

type invalidatedApprovalRemote struct{ receiverRemote }

func (invalidatedApprovalRemote) ApprovalPending(context.Context, agentlink.MCPApprovalPending) error {
	return agentlink.MCPError{Code: agentlink.MCPFenced}
}
func TestReceiverInvalidatedHumanRequestFailsClosedWithoutApproving(t *testing.T) {
	p, remote, delivery := receiverFixture(t)
	p.epoch = "epoch"
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	record := receiverRecordFixture(t, p, delivery)
	j, e := p.backend.read(context.Background(), record.Work.Request.From, record.Admission.Binding.Task.AgentID)
	if e != nil {
		t.Fatal(e)
	}
	pending := &pendingReview{value: agentlink.ExecutionApproval{ID: "invalidated", JobID: j.Task.ID}, job: j, done: make(chan string, 1), ctx: context.Background()}
	p.backend.reviews[pending.value.ID] = pending
	var stopped bool
	p.backend.active[j.Task.ID] = func() { stopped = true }
	p.remote = invalidatedApprovalRemote{remote}
	if e = p.reportApprovals(context.Background(), &record); e != nil {
		t.Fatal(e)
	}
	if !stopped || pending.resolved || len(pending.done) != 0 {
		t.Fatal("invalidated request approved or restored")
	}
}
