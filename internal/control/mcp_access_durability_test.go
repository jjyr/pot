package control

import (
	"context"
	"errors"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func mcpReadyExecution(t *testing.T) (*mcpAccessFixture, agentlink.MCPCaller, agentlink.MCPReceiver, *MCPClaimedDelivery, agentlink.MCPRunBinding) {
	t.Helper()
	f := mcpAccessTestFixture(t)
	ctx := context.Background()
	grant := f.grant(t, false)
	receiverGrant := f.grant(t, true)
	info, e := f.oauth.Verify(ctx, grant.Token, nil)
	if e != nil {
		t.Fatal(e)
	}
	caller := info.Extra["caller"].(agentlink.MCPCaller)
	receiver, e := agentlink.NewMCPReceiver(receiverGrant.Connection, receiverGrant.Node)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.queue.Register(ctx, receiver, fixtureRegistration(), fixtureReceiverDescription()); e != nil {
		t.Fatal(e)
	}
	_, e = f.queue.Submit(ctx, caller, agentlink.MCPSubmitInput{RequestID: "durable", Target: agentlink.NodeTarget(receiver.NodeID()), Text: "Durable work"})
	if e != nil {
		t.Fatal(e)
	}
	delivery, e := f.queue.Claim(ctx, receiver, "claim", 0)
	if e != nil || delivery == nil {
		t.Fatal(delivery, e)
	}
	if e = f.queue.Ack(ctx, receiver, agentlink.MCPDeliveryAck{Claim: delivery.Claim, Accepted: true}); e != nil {
		t.Fatal(e)
	}
	binding := agentlink.MCPRunBinding{Task: protocol.AgentRef{NodeID: receiver.NodeID(), Kind: "task", AgentID: "execution-task"}, RunID: "execution-run", Receiver: agentlink.NodeTarget(receiver.NodeID())}
	if e = f.queue.Admit(ctx, receiver, agentlink.MCPRunAdmission{Claim: delivery.Claim, Binding: binding}); e != nil {
		t.Fatal(e)
	}
	return f, caller, receiver, delivery, binding
}
func TestMCPAccessEventProjectionAtomicAndRestarted(t *testing.T) {
	f, caller, receiver, delivery, binding := mcpReadyExecution(t)
	ctx := context.Background()
	event := agentlink.MCPExecutionEvent{Claim: delivery.Claim, Binding: binding, Sequence: 1, Phase: protocol.TaskComplete, Result: &protocol.TaskResultEvent{TaskID: binding.Task.AgentID, RunID: binding.RunID, Title: "Actual result", Phase: protocol.TaskComplete, Text: "Evidence", Evidence: []string{"observed"}, Artifacts: []string{"execution-node/original-run/report.txt"}, Origin: protocol.TaskOrigin{Kind: "manual"}}}
	// Simulate a failed receipt commit after the Task projection was prepared.
	if _, e := f.queue.DB.Exec(`CREATE TEMP TRIGGER reject_fixture_event BEFORE INSERT ON mcp_service_events BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.queue.Event(ctx, receiver, event); e == nil {
		t.Fatal("synthetic commit failure ignored")
	}
	task, e := f.queue.Task(ctx, caller, delivery.Claim.Binding.Task.AgentID)
	if e != nil || task.State != "queued" {
		t.Fatal("uncommitted projection visible", task, e)
	}
	record, e := f.queue.Tasks.Get(ctx, caller.Actor().AgentID, task.ID)
	if e != nil || record.Info.Phase != protocol.TaskQueued {
		t.Fatal(record, e)
	}
	var count int
	if e = f.queue.DB.QueryRow("SELECT count(*) FROM task_outcomes").Scan(&count); e != nil || count != 0 {
		t.Fatal("orphan outcome", count, e)
	}
	if _, e = f.queue.DB.Exec("DROP TRIGGER reject_fixture_event"); e != nil {
		t.Fatal(e)
	}
	receipt, e := f.queue.Event(ctx, receiver, event)
	if e != nil {
		t.Fatal(e)
	}
	projected, e := f.queue.Tasks.Get(ctx, caller.Actor().AgentID, task.ID)
	if e != nil || projected.Outcome == nil || mcpJSON(projected.Outcome.Artifacts) != mcpJSON(event.Result.Artifacts) || mcpJSON(projected.Outcome.Evidence) != mcpJSON(event.Result.Evidence) {
		t.Fatal("result metadata was lost in catalogue", projected, e)
	}
	var path, name string
	var seq int
	if e = f.queue.DB.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	f.access.Close()
	f.http.Close()
	if e = f.queue.DB.Close(); e != nil {
		t.Fatal(e)
	}
	db, e := storage.OpenDatabase(path, storage.TasksDatabase)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	catalogue, e := tasks.Open(db, tasks.Options{NodeID: f.queue.NodeID})
	if e != nil {
		t.Fatal(e)
	}
	reopened := &MCPAccessQueue{DB: db, Tasks: catalogue, NodeID: f.queue.NodeID, CheckConnection: f.oauth.CheckConnection}
	retry, e := reopened.Event(ctx, receiver, event)
	if e != nil || !retry.Duplicate || retry.Digest != receipt.Digest {
		t.Fatal("event lost on database reopen", retry, e)
	}
	claim, e := reopened.Claim(ctx, receiver, "claim", 0)
	if e != nil || claim.Claim != delivery.Claim {
		t.Fatal(claim, e)
	}
	none, e := reopened.Claim(ctx, receiver, "next", 0)
	if e != nil || none != nil {
		t.Fatal("execution replay after restart", none, e)
	}
	task, e = reopened.Task(ctx, caller, task.ID)
	if e != nil || task.Result != "Evidence" || task.RunID != binding.RunID {
		t.Fatal(task, e)
	}
	var saved agentlink.MCPExecutionEvent
	var raw string
	if e = db.QueryRow("SELECT event FROM mcp_service_events WHERE claim_id=?", event.Claim.ClaimID).Scan(&raw); e != nil || mcpRead(raw, &saved) != nil || mcpJSON(saved.Result.Artifacts) != mcpJSON(event.Result.Artifacts) {
		t.Fatal("artifact ownership lost", e)
	}
}
func TestMCPAccessRejectsCrossClaimAndExecutionBindings(t *testing.T) {
	f, caller, receiver, delivery, binding := mcpReadyExecution(t)
	ctx := context.Background()
	forgedNode, _ := agentlink.NewMCPReceiver(receiver.ConnectionID(), "wrong-node")
	if _, e := f.queue.Claim(ctx, forgedNode, "claim", 0); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("claim retry leaked across node", e)
	}
	foreign, _ := agentlink.NewMCPReceiver("foreign-connection", receiver.NodeID())
	event := agentlink.MCPExecutionEvent{Claim: delivery.Claim, Binding: binding, Sequence: 1, Phase: protocol.TaskRunning}
	if _, e := f.queue.Event(ctx, foreign, event); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("event crossed connection", e)
	}
	changed := event
	changed.Claim.ClaimID = "forged-claim"
	if _, e := f.queue.Event(ctx, receiver, changed); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("forged claim accepted", e)
	}
	changed = event
	changed.Binding.Task.AgentID = "another-execution-task"
	if _, e := f.queue.Event(ctx, receiver, changed); !errors.Is(e, agentlink.ErrFenced) {
		t.Fatal("foreign execution accepted", e)
	}
	actor := caller.Actor()
	actor.AgentID = "forged-caller"
	forgedCaller, _ := agentlink.NewMCPCaller(caller.ConnectionID(), actor)
	if _, e := f.queue.Task(ctx, forgedCaller, delivery.Claim.Binding.Task.AgentID); !errors.Is(e, agentlink.ErrNotFound) {
		t.Fatal("task crossed effective actor", e)
	}
	if _, e := f.queue.Control(ctx, forgedCaller, nil, &agentlink.MCPCancelInput{RequestID: "cancel", TaskID: delivery.Claim.Binding.Task.AgentID, RunID: binding.RunID}); !errors.Is(e, agentlink.ErrNotFound) {
		t.Fatal("control crossed effective actor", e)
	}
	if e := f.oauth.Revoke(ctx, receiver.ConnectionID()); e != nil {
		t.Fatal(e)
	}
	if _, e := f.queue.Claim(ctx, receiver, "claim", 0); !errors.Is(e, agentlink.ErrUnauthorized) {
		t.Fatal("revoked receiver recovered claim", e)
	}
	// Revocation retains the existing Task and receipt while denying new dispatch.
	if _, e := f.queue.Submit(ctx, caller, agentlink.MCPSubmitInput{RequestID: "new-after-revocation", Target: binding.Receiver, Text: "Rejected new work"}); !errors.Is(e, agentlink.ErrUnavailable) {
		t.Fatal("revoked receiver admitted work", e)
	}
	task, e := f.queue.Task(ctx, caller, delivery.Claim.Binding.Task.AgentID)
	if e != nil || task.RunID != binding.RunID {
		t.Fatal(task, e)
	}
}
func TestMCPAccessRejectedDeliveryIsNotAnExecutionResult(t *testing.T) {
	f := mcpAccessTestFixture(t)
	ctx := context.Background()
	client := f.grant(t, false)
	receiverGrant := f.grant(t, true)
	info, e := f.oauth.Verify(ctx, client.Token, nil)
	if e != nil {
		t.Fatal(e)
	}
	caller := info.Extra["caller"].(agentlink.MCPCaller)
	receiver, e := agentlink.NewMCPReceiver(receiverGrant.Connection, receiverGrant.Node)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.queue.Register(ctx, receiver, fixtureRegistration(), fixtureReceiverDescription()); e != nil {
		t.Fatal(e)
	}
	accepted, e := f.queue.Submit(ctx, caller, agentlink.MCPSubmitInput{RequestID: "rejected", Target: agentlink.NodeTarget(receiver.NodeID()), Text: "Receiver refuses admission"})
	if e != nil {
		t.Fatal(e)
	}
	delivery, e := f.queue.Claim(ctx, receiver, "rejection-claim", 0)
	if e != nil {
		t.Fatal(e)
	}
	ack := agentlink.MCPDeliveryAck{Claim: delivery.Claim, Error: &agentlink.MCPError{Code: agentlink.MCPUnsupported}}
	if e = f.queue.Ack(ctx, receiver, ack); e != nil {
		t.Fatal(e)
	}
	if e = f.queue.Ack(ctx, receiver, ack); e != nil {
		t.Fatal(e)
	}
	task, e := f.queue.Task(ctx, caller, accepted.Task.ID)
	if e != nil || task.State != "failed" || task.RunID != "" || task.Error != string(agentlink.MCPUnsupported) {
		t.Fatal("rejection hidden/fabricated run", task, e)
	}
	record, e := f.queue.Tasks.Get(ctx, caller.Actor().AgentID, task.ID)
	if e != nil || record.Info.Phase != protocol.TaskFailed || record.Outcome != nil || record.RunID != "" {
		t.Fatal("invented execution outcome", record, e)
	}
	var count int
	if e = f.queue.DB.QueryRow("SELECT count(*) FROM mcp_service_events").Scan(&count); e != nil || count != 0 {
		t.Fatal("invented terminal event", count, e)
	}
	status, e := f.access.mutationReceipt(ctx, caller, MCPAccessReceiptInput{Operation: agentlink.TaskSubmit, RequestID: "rejected"})
	if e != nil || status.Ack == nil || status.Ack.Accepted || status.Ack.Error.Code != agentlink.MCPUnsupported {
		t.Fatal(status, e)
	}
	// A saved rejection cannot be changed into acceptance later.
	ack.Accepted = true
	ack.Error = nil
	if e = f.queue.Ack(ctx, receiver, ack); !errors.Is(e, agentlink.ErrConflict) {
		t.Fatal("ack conflict accepted", e)
	}
}
