package control

import (
	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

// Disposable Controls and real HTTP receivers exercise the production cutover paths.
func TestMCPRegressionRetiredOutboundMailMustNotCrossNewConnection(t *testing.T) {
	for _, direction := range []string{"outbound", "inbound"} {
		t.Run(direction, func(t *testing.T) {
			a, _ := potMCPControl(t, "retired-a")
			b, _ := potMCPControl(t, "retired-b")
			server := potMCPServer(t, a)
			c := potMCPAuthorize(t, b, server, "")
			client := potMCPClient(t, b, c.ID)
			potMCPExposeClient(t, b, c.ID, client)
			sender, recipient := b, a
			if direction == "inbound" {
				sender, recipient = a, b
			}
			target := agentlink.AssistantTarget(recipient.options.RuntimeID, "main")
			sent, e := sender.sendAssistantMail(t.Context(), "old-outgoing", "main", protocol.SendMail{To: target, Text: "old unresolved side effect request"})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = sender.Tasks.DB().Exec("INSERT INTO retired_agent_link_work VALUES('message',?)", sent.ID); e != nil {
				t.Fatal(e)
			}
			var original string
			if e = sender.Tasks.DB().QueryRow("SELECT envelope FROM agent_messages WHERE id=?", sent.ID).Scan(&original); e != nil {
				t.Fatal(e)
			}
			fresh, e := sender.sendAssistantMail(t.Context(), "fresh-outgoing", "main", protocol.SendMail{To: target, Text: "fresh authorized work"})
			if e != nil {
				t.Fatal(e)
			}
			if direction == "inbound" {
				sender.syncInboundPotMCPMail(t.Context())
				identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
				receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
				potMCPStep(t, client, receiver)
				potMCPStep(t, client, receiver)
				sender.syncInboundPotMCPMail(t.Context())
			} else {
				c, e = b.PotMCPConnection(t.Context(), c.ID)
				if e != nil {
					t.Fatal(e)
				}
				if e = b.syncPotMCPMail(t.Context(), c, client); e != nil {
					t.Fatal(e)
				}
				if e = b.syncPotMCPMail(t.Context(), c, client); e != nil {
					t.Fatal(e)
				}
			}
			got, e := recipient.Mailbox.Next(t.Context(), "main")
			if e != nil || got == nil || got.ID != fresh.ID {
				t.Fatal("retired mail replayed or fresh mail blocked", got, e)
			}
			var state, raw string
			if e = sender.Tasks.DB().QueryRow("SELECT state,envelope FROM agent_messages WHERE id=?", sent.ID).Scan(&state, &raw); e != nil || state != "queued" || raw != original {
				t.Fatal("retired history changed", state, e)
			}
			var count int
			if e = recipient.Tasks.DB().QueryRow("SELECT COUNT(*) FROM agent_messages WHERE direction='in' AND json_extract(envelope,'$.kind')='message'").Scan(&count); e != nil || count != 1 {
				t.Fatal("fresh delivery duplicated or history replayed", count, e)
			}
		})
	}
}

func TestMCPRegressionRetiredInboundMailMustNotBlockNewMail(t *testing.T) {
	a, _ := potMCPControl(t, "review-in-a")
	old := protocol.Envelope{Kind: "message", ID: "old-retired", From: agentlink.AssistantTarget("review-in-b", "main"), To: agentlink.AssistantTarget(a.options.RuntimeID, "main"), ConversationID: "26c8d41a-3a1c-41a3-912e-7b12316fe901", Text: "old history"}
	if _, e := a.Mailbox.Deliver(t.Context(), old); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Tasks.DB().Exec("INSERT INTO retired_agent_link_work VALUES('message',?)", old.ID); e != nil {
		t.Fatal(e)
	}
	fresh := old
	fresh.ID = "new-mcp-message"
	fresh.ConversationID = "26c8d41a-3a1c-41a3-912e-7b12316fe902"
	fresh.Text = "new authorized work"
	if _, e := a.Mailbox.Deliver(t.Context(), fresh); e != nil {
		t.Fatal(e)
	}
	seed, e := a.Mailbox.Next(t.Context(), "main")
	if e != nil || seed == nil || seed.ID != fresh.ID {
		t.Fatal(seed, e)
	}
	batch, e := a.Mailbox.ClaimMailBatch(t.Context(), *seed)
	if e != nil {
		t.Fatalf("new MCP mail selected but cannot be claimed due to retired same-sender history: %v", e)
	}
	for _, m := range batch.Messages {
		if m.ID == old.ID {
			t.Fatal("retired input joined new batch")
		}
	}
}

func TestMCPRegressionMailboxDeliveryMustNotCreateUnfinishedTask(t *testing.T) {
	a, _ := potMCPControl(t, "review-mail-a")
	b, _ := potMCPControl(t, "review-mail-b")
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	sent, e := a.sendAssistantMail(t.Context(), "ordinary-mail", "main", protocol.SendMail{To: target, Text: "ordinary reference mail"})
	if e != nil {
		t.Fatal(e)
	}
	a.syncInboundPotMCPMail(t.Context())
	identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	got, e := b.Mailbox.Next(t.Context(), "main")
	if e != nil || got == nil || got.ID != sent.ID {
		t.Fatal(got, e)
	}
	records, e := receiver.Records(t.Context())
	if e != nil || len(records) != 1 || !records[0].ControlApplied {
		t.Fatal(records, e)
	}
	if e = b.Mailbox.Claim(t.Context(), got.ID); e != nil {
		t.Fatal(e)
	}
	if e = b.Mailbox.Finish(t.Context(), got.ID, true, "handled"); e != nil {
		t.Fatal(e)
	}
	if e = mcpreceiver.Step(t.Context(), client, receiver); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = a.Tasks.DB().QueryRow("SELECT COUNT(*) FROM tasks WHERE phase='queued'").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatalf("completed ordinary mailbox delivery left %d phantom queued Task(s) in origin catalogue, journal admission=%v", count, records[0].Admission)
	}
}

func TestMCPRegressionOriginCatalogueMustRouteControlsByDispatchID(t *testing.T) {
	for _, operation := range []string{"cancel", "terminal-input"} {
		t.Run(operation, func(t *testing.T) {
			a, _ := potMCPControl(t, "review-controls-a")
			b, driver := potMCPControl(t, "review-controls-b")
			server := potMCPServer(t, a)
			c := potMCPAuthorize(t, b, server, "")
			client := potMCPClient(t, b, c.ID)
			target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
			info, e := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "origin-control-task", Text: "work on B", Target: &target})
			if e != nil {
				t.Fatal(e)
			}
			identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
			receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
			potMCPStep(t, client, receiver)
			potMCPStep(t, client, receiver)
			records, e := receiver.Records(t.Context())
			if e != nil || len(records) != 1 || records[0].Admission == nil {
				t.Fatal(records, e)
			}
			execution := records[0].Admission.Binding
			if execution.Task.AgentID == info.ID {
				t.Fatal("fixture must distinguish dispatch/execution Task IDs")
			}
			if operation == "cancel" {
				e = a.StopTask(t.Context(), "main", info.ID, "cancel")
			} else {
				if e = b.Tasks.Tick(t.Context()); e != nil {
					t.Fatal(e)
				}
				driver.complete(execution.RunID)
				if e = b.Tasks.Tick(t.Context()); e != nil {
					t.Fatal(e)
				}
				potMCPStep(t, client, receiver)
				_, e = a.guideTaskExecution(t.Context(), "main", info.ID, protocol.TaskInput{RequestID: "continue-origin", Text: "next run"})
			}
			if e != nil {
				t.Fatalf("product %s sent execution Task ID %s to origin queue instead of dispatch ID %s: %v", operation, execution.Task.AgentID, info.ID, e)
			}
		})
	}
}

func TestMCPRegressionContinuationMustPublishEachRealRunOutcome(t *testing.T) {
	a, _ := potMCPControl(t, "review-outcome-a")
	b, driver := potMCPControl(t, "review-outcome-b")
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	info, e := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "outcome-task", Text: "work on B", Target: &target})
	if e != nil {
		t.Fatal(e)
	}
	identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, e := receiver.Records(t.Context())
	if e != nil || len(records) != 1 {
		t.Fatal(records, e)
	}
	first := records[0].Admission.Binding
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	driver.complete(first.RunID)
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	a.syncTaskExecutions(t.Context())
	if _, e = a.guideTaskExecution(t.Context(), "main", info.ID, protocol.TaskInput{RequestID: "second-outcome", Text: "continue"}); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, e = receiver.Records(t.Context())
	if e != nil || len(records) != 2 || records[1].Admission == nil {
		t.Fatal(records, e)
	}
	second := records[1].Admission.Binding
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	driver.complete(second.RunID)
	if e = b.Tasks.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	potMCPStep(t, client, receiver)
	a.syncTaskExecutions(t.Context())
	var count int
	if e = a.Tasks.DB().QueryRow("SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND json_extract(envelope,'$.result.task_id')=?", info.ID).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 2 {
		t.Fatalf("two actual terminal runs (%s and %s) yielded only %d completion envelope(s)", first.RunID, second.RunID, count)
	}
	a.syncTaskExecutions(t.Context())
	if e = a.Tasks.DB().QueryRow("SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND json_extract(envelope,'$.result.task_id')=?", info.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal("reconciliation duplicated completion notifications", count, e)
	}

}

func TestMCPRegressionRetiredHistoryDoesNotBlockUnrelatedSender(t *testing.T) {
	a, _ := potMCPControl(t, "review-unrelated-a")
	old := protocol.Envelope{Kind: "message", ID: "old-unrelated", From: agentlink.AssistantTarget("review-unrelated-b", "old-sender"), To: agentlink.AssistantTarget(a.options.RuntimeID, "main"), ConversationID: "26c8d41a-3a1c-41a3-912e-7b12316fe903", Text: "old queued history"}
	if _, e := a.Mailbox.Deliver(t.Context(), old); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Tasks.DB().Exec("INSERT INTO retired_agent_link_work VALUES('message',?)", old.ID); e != nil {
		t.Fatal(e)
	}
	fresh := old
	fresh.ID = "fresh-unrelated"
	fresh.From.AgentID = "new-sender"
	fresh.ConversationID = "26c8d41a-3a1c-41a3-912e-7b12316fe904"
	if _, e := a.Mailbox.Deliver(t.Context(), fresh); e != nil {
		t.Fatal(e)
	}
	seed, e := a.Mailbox.Next(t.Context(), "main")
	if e != nil || seed == nil || seed.ID != fresh.ID {
		t.Fatal(seed, e)
	}
	batch, e := a.Mailbox.ClaimMailBatch(t.Context(), *seed)
	if e != nil || len(batch.Messages) != 1 || batch.Messages[0].ID != fresh.ID {
		t.Fatal(batch, e)
	}
	t.Log("new unrelated sender remains claimable; retired old queued message was excluded")
}
