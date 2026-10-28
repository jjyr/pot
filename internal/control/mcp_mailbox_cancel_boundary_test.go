package control

import (
	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"testing"
)

func TestMCPMailboxEndedCancelDoesNotBlockNewRunOrOtherTask(t *testing.T) {
	a, _ := potMCPControl(t, "cancel-boundary-origin")
	b, driver := potMCPControl(t, "cancel-boundary-executor")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	identity, _ := agentlink.NewMCPReceiver(connection.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	session := server.session(t, server.grant(t, false))
	var accepted MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "original", Target: agentlink.AssistantTarget(b.options.RuntimeID, "main"), Text: "synthetic original"}, &accepted)
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if err := b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	records, err := receiver.Records(t.Context())
	if err != nil || len(records) != 1 || records[0].Admission == nil {
		t.Fatalf("admission: %v", err)
	}
	original := records[0].Admission.Binding
	fixtureCall(t, session, "pot_task_input", agentlink.MCPTaskInput{RequestID: "input-before-cancel", TaskID: accepted.Task.ID, RunID: original.RunID, Text: "next turn"}, &accepted)
	fixtureCall(t, session, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: "cancel-original", TaskID: accepted.Task.ID, RunID: original.RunID}, &accepted)
	// Input is accepted/ACKed while running; cancellation is durably claimed but
	// has not yet been applied. The native run then completes naturally.
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, err = receiver.Records(t.Context())
	if err != nil || len(records) != 3 || records[1].Admission != nil || records[2].ControlApplied {
		t.Fatalf("unexpected boundary records: %v", err)
	}
	driver.complete(original.RunID)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	err1 := mcpreceiver.Step(t.Context(), client, receiver)
	err2 := mcpreceiver.Step(t.Context(), client, receiver)
	var other MCPTaskAcceptance
	fixtureCall(t, session, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "unrelated-after-cancel", Target: agentlink.AssistantTarget(b.options.RuntimeID, "main"), Text: "unrelated synthetic work"}, &other)
	err3 := mcpreceiver.Step(t.Context(), client, receiver)
	err4 := mcpreceiver.Step(t.Context(), client, receiver)
	var actual agentlink.Task
	fixtureCall(t, session, "pot_task_read", map[string]string{"task_id": other.Task.ID}, &actual)
	records, _ = receiver.Records(t.Context())
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || len(records) != 4 {
		t.Fatalf("CONNECTION_BLOCKED: first=%v retry=%v after_other_submit=%v after_other_retry=%v unrelated_state=%s journal_records=%d input_new_run=%s cancel_applied=%t", err1, err2, err3, err4, actual.State, len(records), records[1].Admission.Binding.RunID, records[2].ControlApplied)
	}
	if !records[2].ControlApplied || records[2].Admission != nil || records[2].Control.Binding != original || records[0].LastPhase != "complete" {
		t.Fatal("cancel reconciliation changed original evidence", records)
	}
	next := records[1].Admission.Binding
	unrelated := records[3].Admission.Binding
	fixtureCallError(t, session, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: "new-stale-cancel", TaskID: accepted.Task.ID, RunID: original.RunID}, agentlink.MCPFenced)
	fixtureCall(t, session, "pot_task_cancel", agentlink.MCPCancelInput{RequestID: "cancel-original", TaskID: accepted.Task.ID, RunID: original.RunID}, &accepted)
	if !accepted.Receipt.Duplicate {
		t.Fatal("original accepted cancel did not reconcile")
	}
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if driver.launches != 3 {
		t.Fatal("later accepted work was not executed", driver.launches)
	}
	for _, binding := range []agentlink.MCPRunBinding{next, unrelated} {
		driver.mu.Lock()
		running, ok := driver.runs[binding.RunID]
		driver.mu.Unlock()
		if !ok || string(running.Phase) != "running" {
			t.Fatal("old cancel stopped newer or unrelated execution", binding, running)
		}
		driver.complete(binding.RunID)
	}
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	records, err = receiver.Records(t.Context())
	if err != nil || len(records) != 4 || records[1].LastPhase != "complete" || records[3].LastPhase != "complete" {
		t.Fatal("later results did not settle", records, err)
	}

}
