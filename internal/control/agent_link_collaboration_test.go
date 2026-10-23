package control

import (
	"context"
	"testing"
	"time"

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
	if err := b.service.Mailbox.Claim(ctx, message.ID); err != nil {
		t.Fatal(err)
	}
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
