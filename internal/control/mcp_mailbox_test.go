package control

import (
	"errors"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMCPMailboxAcceptsActiveTaskMessagesInOrder(t *testing.T) {
	a, _ := potMCPControl(t, "mailbox-a")
	b, driver := potMCPControl(t, "mailbox-b")
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	info, err := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "mailbox-task", Text: "original turn", Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, err := receiver.Records(t.Context())
	if err != nil || len(records) != 1 || records[0].Admission == nil {
		t.Fatal(err)
	}
	first := records[0].Admission.Binding
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	owner := agentlink.AssistantTarget(a.options.RuntimeID, "main")
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	for _, request := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(request string) {
			defer wg.Done()
			result, e := a.sendAgentMessage(t.Context(), request, "main", owner, protocol.SendMail{To: *info.Address, Text: "guidance " + request})
			if e == nil && result.(map[string]any)["accepted"] != true {
				e = errors.New("message did not return acceptance")
			}
			failures <- e
		}(request)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal("running Task rejected concurrent durable mailbox input", e)
		}
	}
	for range 4 {
		potMCPStep(t, client, receiver)
	}

	records, err = receiver.Records(t.Context())
	if err != nil || len(records) != 4 || driver.launches != 1 {
		t.Fatal("mailbox interrupted or duplicated the original turn", len(records), driver.launches, err)
	}
	for _, r := range records[1:] {
		if !r.Ack.Accepted || !r.Acknowledged || r.Admission != nil || r.Control.Caller != owner || r.Control.Binding != first {
			t.Fatal("active mailbox input was executed or changed provenance", r)
		}
	}
}

func TestMCPMailboxConsumesFIFOAcrossRunChanges(t *testing.T) {
	a, _ := potMCPControl(t, "fifo-a")
	b, driver := potMCPControl(t, "fifo-b")
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	info, err := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "fifo-task", Text: "original", Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, _ := receiver.Records(t.Context())
	first := records[0].Admission.Binding
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	owner := agentlink.AssistantTarget(a.options.RuntimeID, "main")
	caller, _ := agentlink.NewMCPNodeCaller(c.ID, a.options.RuntimeID, owner, nil)
	for _, id := range []string{"one", "two", "three"} {
		if _, err = a.sendAgentMessage(t.Context(), id, "main", owner, protocol.SendMail{To: *info.Address, Text: id}); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		potMCPStep(t, client, receiver)
	}
	// An unconsumed second claim cannot bypass FIFO at the service boundary.
	records, _ = receiver.Records(t.Context())
	forged := agentlink.MCPRunAdmission{Claim: records[2].Claim, Binding: first, PreviousRunID: first.RunID}
	forged.Binding.RunID = "forged-run"
	if err = client.Admit(t.Context(), forged); err == nil {
		t.Fatal("out-of-order admission accepted")
	}
	previous := first
	for i, id := range []string{"one", "two", "three"} {
		driver.complete(previous.RunID)
		if err = b.Tasks.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		potMCPStep(t, client, receiver)
		records, err = receiver.Records(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		r := records[i+1]
		if r.Admission == nil || r.Admission.PreviousRunID != previous.RunID || r.InputReceipt == nil || r.InputReceipt.State != "consumed" {
			t.Fatal("missing FIFO admission", i, r)
		}
		if r.Control.Binding != first {
			t.Fatal("original accepted run changed")
		}
		// Recreate the pump from its durable journal between every message.
		receiver = &potMCPReceiver{s: b, identity: identity, remote: client}
		if err = b.Tasks.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		driver.mu.Lock()
		launch := driver.runs[r.Admission.Binding.RunID].Launch
		driver.mu.Unlock()
		if launch.Text != id {
			t.Fatal("mailbox reordered or overwrote input", launch.Text, id)
		}
		previous = r.Admission.Binding
		if _, err = a.sendAgentMessage(t.Context(), id, "main", owner, protocol.SendMail{To: *info.Address, Text: id}); err != nil {
			t.Fatal("cross-run retry", err)
		}
		wire := agentlink.MCPTaskInput{RequestID: id, TaskID: info.ID, RunID: first.RunID, Text: id}
		if got, e := server.queue.Control(t.Context(), caller, &wire, nil); e != nil || !got.Receipt.Duplicate {
			t.Fatal("lost acceptance", got, e)
		}
		wire.RequestID = "stale-" + id
		if _, e := server.queue.Control(t.Context(), caller, &wire, nil); !errors.Is(e, agentlink.ErrFenced) {
			t.Fatal("new stale-run input accepted", e)
		}
	}
	driver.complete(previous.RunID)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	records, err = receiver.Records(t.Context())
	if err != nil || driver.launches != 4 {
		t.Fatal("duplicate execution", driver.launches, err)
	}
	for _, r := range records {
		if r.LastPhase != protocol.TaskComplete || r.Receipt == nil {
			t.Fatal("missing per-run result", r)
		}
	}
	var notifications int
	if err = a.Tasks.DB().QueryRow("SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result'").Scan(&notifications); err != nil || notifications != 4 {
		t.Fatal("missing per-run notification", notifications, err)
	}
}

func TestMCPMailboxCancellationWaitsForConfirmationAndAllowsLaterGuidance(t *testing.T) {
	a, _ := potMCPControl(t, "cancel-mail-a")
	b, driver := potMCPControl(t, "cancel-mail-b")
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	info, err := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "cancel-mail", Text: "original", Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	owner := agentlink.AssistantTarget(a.options.RuntimeID, "main")
	caller, _ := agentlink.NewMCPNodeCaller(c.ID, a.options.RuntimeID, owner, nil)
	records, _ := receiver.Records(t.Context())
	first := records[0].Admission.Binding
	input := agentlink.MCPTaskInput{RequestID: "before-cancel", TaskID: info.ID, RunID: first.RunID, Text: "pending before cancellation"}
	if _, err = server.queue.Control(t.Context(), caller, &input, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = server.queue.Control(t.Context(), caller, nil, &agentlink.MCPCancelInput{RequestID: "cancel", TaskID: info.ID, RunID: first.RunID}); err != nil {
		t.Fatal(err)
	}
	input.RequestID = "during-intent"
	if _, err = server.queue.Control(t.Context(), caller, &input, nil); err != nil {
		t.Fatal("intent rejected mailbox admission", err)
	}
	// The original run has not confirmed cancellation yet; no input may disappear.
	for range 4 {
		potMCPStep(t, client, receiver)
	}
	records, _ = receiver.Records(t.Context())
	for _, r := range records {
		if r.Control != nil && r.Control.Input != nil && (r.InputReceipt != nil || r.Admission != nil) {
			t.Fatal("intent discarded or consumed guidance", r)
		}
	}
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		potMCPStep(t, client, receiver)
	}
	records, _ = receiver.Records(t.Context())
	for _, r := range records {
		if r.Control != nil && r.Control.Input != nil {
			if r.Admission != nil || r.InputReceipt == nil || r.InputReceipt.State != "cancelled" || !r.InputAcknowledged {
				t.Fatal("confirmed cancellation did not retire pending guidance", r)
			}
		}
	}
	var task agentlink.Task
	task, err = server.queue.Task(t.Context(), caller, info.ID)
	if err != nil || task.State != "cancelled" {
		t.Fatal(task, err)
	}
	input.RequestID = "after-confirmed"
	input.Text = "new owner guidance"
	if _, err = server.queue.Control(t.Context(), caller, &input, nil); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, _ = receiver.Records(t.Context())
	last := records[len(records)-1]
	if last.Admission == nil || last.Admission.PreviousRunID != first.RunID || last.InputReceipt.State != "consumed" {
		t.Fatal("new terminal guidance blocked", last)
	}
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if driver.launches != 2 {
		t.Fatal("cancelled guidance launched", driver.launches)
	}
	driver.complete(last.Admission.Binding.RunID)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	// Even a receiver cannot resurrect a delivery already reported cancelled.
	retired := records[1]
	forged := agentlink.MCPRunAdmission{Claim: retired.Claim, PreviousRunID: last.Admission.Binding.RunID, Binding: last.Admission.Binding}
	forged.Binding.RunID = "resurrected-run"
	if err = client.Admit(t.Context(), forged); err == nil {
		t.Fatal("cancelled mailbox delivery resurrected")
	}
}
