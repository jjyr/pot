package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestPotLinkDirectChatUsesExistingMailboxAndSeparateUserProjection(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "chat-a"), agentControl(t, "chat-b")
	c := connectAgentControls(t, a, b)
	result, e := a.service.SendLinkChat(ctx, c.ID, "main", "user-request", "hello")
	if e != nil {
		t.Fatal(e)
	}
	id := result.MessageID
	repeat, e := a.service.SendLinkChat(ctx, c.ID, "main", "user-request", "hello")
	if e != nil || !repeat.Duplicate {
		t.Fatal(repeat, e)
	}
	if _, e = a.service.SendLinkChat(ctx, c.ID, "main", "user-request", "different"); e == nil {
		t.Fatal("conflicting retry admitted")
	}
	message := awaitAgentLinkMail(t, b.service, id).Message
	replyCtx := context.WithValue(ctx, mailParentKey{}, message)
	_, e = b.service.sendAssistantMail(replyCtx, "peer-reply", "main", protocol.SendMail{To: message.From, Text: "answer"})
	if e != nil {
		t.Fatal(e)
	}
	var frame protocol.UIFrame
	var view protocol.UIFrame
	deadline := time.Now().Add(5 * time.Second)
	for {
		frame, e = a.service.LinkChat(ctx, c.ID, "main")
		if e != nil {
			t.Fatal(e)
		}
		view = frame
		if len(view.Messages) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(view)
		}
		time.Sleep(time.Millisecond)
	}
	if e = view.Validate(); e != nil {
		t.Fatal(e)
	}
	if view.Messages[0].Role != protocol.User || view.Messages[1].Text != "answer" {
		t.Fatal(view)
	}
	notices, e := a.service.Mailbox.Candidates(ctx, "main")
	if e != nil || len(notices) != 0 {
		t.Fatal("direct reply entered local agent consumer", notices, e)
	}
	if e = a.service.agentLinks.service.Disconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.service.SendLinkChat(ctx, c.ID, "main", "paused", "blocked"); e == nil {
		t.Fatal("paused chat admitted")
	}
	if e = a.service.agentLinks.service.Reconnect(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if e = a.service.agentLinks.service.Delete(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	frame, e = a.service.LinkChat(ctx, c.ID, "main")
	if e != nil || len(frame.Messages) != 2 {
		t.Fatal("delete removed conversation", frame, e)
	}
}
func TestPotLinkDelegationOwnsTaskOnReceiverAndNotifiesOriginalAssistant(t *testing.T) {
	// Allow the complete multi-step workflow to finish under the race detector.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { batchReply(w, "receiver-result") }))
	defer upstream.Close()
	a := agentControl(t, "sender")
	receiver := taskControl(t, "receiver", upstream.URL)
	b := &agentControlFixture{service: receiver, endpoint: controlPeerEndpoint(receiver)}
	c := connectAgentControls(t, a, b)
	request := protocol.CreateTask{RequestID: "delegation", Text: "Read report\nReturn the result.", Target: &protocol.AgentRef{NodeID: c.PeerHost.NodeID, Kind: "assistant", AgentID: "main"}}
	info, e := a.service.CreateTask(ctx, "main", request)
	if e != nil {
		t.Fatal(e)
	}
	repeated, e := a.service.CreateTask(ctx, "main", request)
	if e != nil || repeated.ID != info.ID {
		t.Fatal(repeated, e)
	}
	request.Text = "changed"
	if _, e = a.service.CreateTask(ctx, "main", request); !errors.Is(e, agentlink.ErrConflict) {
		t.Fatal(e)
	}
	for {
		d, e := a.service.Tasks.Execution(ctx, "main", info.ID)
		if e != nil {
			t.Fatal(e)
		}
		if terminalLinkTask(d.Execution.Receipt.State) {
			if d.Execution.Receipt.Result != "receiver-result" || d.Execution.Receipt.Receiver.Address.NodeID != "receiver" {
				t.Fatal(d)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	var count int
	if e = a.service.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM task_runs WHERE task_id=?", info.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("sender launched remote task locally", count, e)
	}
	for {
		notices, e := a.service.Mailbox.Notifications(ctx, "main")
		if e != nil {
			t.Fatal(e)
		}
		if len(notices) == 1 {
			if notices[0].Result.TaskID != info.ID || notices[0].To.AgentID != "main" {
				t.Fatal(notices)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	page, e := a.service.TaskPage(ctx, "main", protocol.TaskQuery{Scope: "all"})
	if e != nil || len(page.Items) != 1 || page.Items[0].AgentLink == nil || page.Items[0].Phase != protocol.TaskComplete {
		t.Fatal(page, e)
	}
	raw, e := json.Marshal(page)
	if e != nil || len(raw) == 0 {
		t.Fatal(e)
	}
	archived, e := a.service.Tasks.SetDispositions(ctx, "main", []protocol.TaskDisposition{{TaskID: info.ID, RunID: page.Items[0].RunID, Archived: true, Reason: "reviewed"}})
	if e != nil || len(archived) != 1 || !archived[0].Archived {
		t.Fatal(archived, e)
	}
	backend := potLinkBackend{receiver}
	from := protocol.AgentRef{NodeID: "sender", Kind: "assistant", AgentID: "main"}
	r := agentlink.TaskRequest{RequestID: "default-bound", From: from, Text: "Read\nReturn result", Target: agentlink.AssistantTarget(receiver.options.RuntimeID, "main")}
	first, e := backend.Submit(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	other, e := receiver.Registry.Create(ctx, "other", "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = receiver.integrationDB.ExecContext(ctx, "UPDATE agent_link_node_settings SET default_assistant_id=?", other.ID); e != nil {
		t.Fatal(e)
	}
	same, e := backend.Submit(ctx, r)
	if e != nil || same.ID != first.ID || same.Receiver.Address.AgentID != "main" {
		t.Fatal("default change retargeted accepted work", same, e)
	}
	if _, e = backend.Status(ctx, protocol.AgentRef{NodeID: "another", Kind: "assistant", AgentID: "main"}, same.ID); !errors.Is(e, agentlink.ErrNotFound) {
		t.Fatal("another caller read task", e)
	}
}
