package control

import (
	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

// Uses the existing production tools and unmodified Pot receiver capabilities.
// Only the executor is synthetic. No direct inserts, forged sender or route changes.
func TestMCPRemoteCompletionRetainsOriginNotificationScope(t *testing.T) {
	a, _ := potMCPControl(t, "independent-hub")
	b, _ := potMCPControl(t, "independent-origin")
	c, driver := potMCPControl(t, "independent-executor")
	server := potMCPServer(t, a)
	bconn := potMCPAuthorize(t, b, server, "")
	bc := potMCPClient(t, b, bconn.ID)
	cconn := potMCPAuthorize(t, c, server, "")
	cc := potMCPClient(t, c, cconn.ID)
	actor := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	target := agentlink.AssistantTarget(c.options.RuntimeID, "main")
	var accepted MCPTaskAcceptance
	err := bc.Call(t.Context(), "pot_receiver_node_submit", agentlink.MCPNodeSubmit{Actor: actor, Input: agentlink.MCPSubmitInput{RequestID: "independent-origin-submit", Target: target, Text: "synthetic remote-owned result"}}, &accepted)
	if err != nil {
		t.Fatalf("real tool route did not accept; cannot claim bug: %v", err)
	}
	identity, _ := agentlink.NewMCPReceiver(cconn.ID, c.options.RuntimeID)
	receiver := &potMCPReceiver{s: c, identity: identity, remote: cc}
	potMCPStep(t, cc, receiver)
	potMCPStep(t, cc, receiver)
	records, err := receiver.Records(t.Context())
	if err != nil || len(records) != 1 || records[0].Admission == nil {
		t.Fatalf("admission: %v", err)
	}
	run := records[0].Admission.Binding.RunID
	if err = c.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, cc, receiver)
	driver.complete(run)
	if err = c.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, cc, receiver)
	var actual agentlink.Task
	if err = bc.Call(t.Context(), "pot_receiver_node_task_get", agentlink.MCPNodeRead{Actor: actor, TaskID: accepted.Task.ID}, &actual); err != nil || actual.State != "complete" {
		t.Fatalf("owned result read failed: %v state=%s", err, actual.State)
	}
	// The producer must not write or mark remote-owned results as notified;
	// filtering an erroneous inbox row alone would hide the ownership bug.
	for range 2 {
		record, e := a.Tasks.Get(t.Context(), actor.AgentID, accepted.Task.ID)
		if e != nil || record == nil || record.Execution.Notified {
			t.Fatal("foreign owner received local notification receipt", record, e)
		}
		var count int
		if e = a.Tasks.DB().QueryRow("SELECT count(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND json_extract(envelope,'$.result.task_id')=?", accepted.Task.ID).Scan(&count); e != nil || count != 0 {
			t.Fatal("foreign result inserted into local inbox", count, e)
		}
		a.syncTaskExecutions(t.Context())
	}
	notices, err := a.Mailbox.Notifications(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notices {
		if n.Result != nil && n.Result.TaskID == accepted.Task.ID {
			t.Fatalf("WRONG_LOCAL_OWNER: hub=%s local_owner=main envelope_to=%+v dispatch=%s actual_run=%s from=%+v phase=%s", a.options.RuntimeID, n.To, n.Result.TaskID, n.Result.RunID, n.From, n.Result.Phase)
		}
	}
}

func TestMCPCompletionNotificationRollsBackWithTerminalReceipt(t *testing.T) {
	a, _ := potMCPControl(t, "tx-origin")
	b, driver := potMCPControl(t, "tx-executor")
	server := potMCPServer(t, a)
	conn := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, conn.ID)
	identity, _ := agentlink.NewMCPReceiver(conn.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	info, err := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "tx-proof", Text: "synthetic transaction", Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	rs, err := receiver.Records(t.Context())
	if err != nil || len(rs) != 1 || rs[0].Admission == nil {
		t.Fatalf("admission: %v", err)
	}
	run := rs[0].Admission.Binding.RunID
	driver.complete(run)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Event insertion is after notification/projection code. Rejecting it must
	// roll back envelope, Notified, outcome and projection as one Tasks transaction.
	if _, err = a.Tasks.DB().Exec(`CREATE TEMP TRIGGER reject_independent_receipt BEFORE INSERT ON mcp_service_events BEGIN SELECT RAISE(ABORT,'independent_receipt_failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = mcpreceiver.Step(t.Context(), client, receiver); err == nil {
		t.Fatal("fault injection did not reject event commit")
	}
	record, err := a.Tasks.Get(t.Context(), "main", info.ID)
	if err != nil || record.Info.Phase != protocol.TaskRunning || record.Execution.Notified {
		t.Fatalf("partial terminal projection: %v %+v", err, record)
	}
	notices, err := a.Mailbox.Notifications(t.Context(), "main")
	if err != nil || len(notices) != 0 {
		t.Fatalf("orphan completion notification: %v count=%d", err, len(notices))
	}
	if _, err = a.Tasks.DB().Exec(`DROP TRIGGER reject_independent_receipt`); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	record, err = a.Tasks.Get(t.Context(), "main", info.ID)
	if err != nil || record.Info.Phase != protocol.TaskComplete || !record.Execution.Notified {
		t.Fatalf("terminal receipt not committed: %v", err)
	}
	notices, err = a.Mailbox.Notifications(t.Context(), "main")
	if err != nil || len(notices) != 1 || notices[0].To != agentlink.AssistantTarget(a.options.RuntimeID, "main") || notices[0].Result.TaskID != info.ID || notices[0].Result.RunID != run {
		t.Fatalf("wrong/duplicate completion: %v %+v", err, notices)
	}
}
