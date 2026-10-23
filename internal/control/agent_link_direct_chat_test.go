package control

import (
	"context"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAgentLinkEmptyNodeChatSurvivesAssistantDeletionAndRestart(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "empty-chat"), agentControl(t, "receiver-chat")
	defs, err := a.service.Registry.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.service.DeleteAssistant(ctx, defs[0].ID, defs[0].Revision); err != nil {
		t.Fatal(err)
	}
	c := connectAgentControls(t, a, b)
	if c.Local.Address.Kind != "node" || c.Local.Address.AgentID != "empty-chat" {
		t.Fatal("connection borrowed an assistant", c)
	}
	local, err := (potLinkBackend{a.service}).Directory(ctx)
	if err != nil || len(local.Assistants()) != 0 {
		t.Fatal(local, err)
	}
	send := func(request string) protocol.Envelope {
		t.Helper()
		out, err := a.service.SendLinkChat(ctx, c.ID, "main", request, "question "+request)
		if err != nil {
			t.Fatal(err)
		}
		id := out.MessageID
		m := awaitAgentLinkMail(t, b.service, id).Message
		if m.From.Kind != "user" {
			t.Fatal("chat used assistant identity", m)
		}
		replyCtx := context.WithValue(ctx, mailParentKey{}, m)
		if _, err = b.service.sendAssistantMail(replyCtx, "reply-"+request, "main", protocol.SendMail{To: m.From, Text: "answer " + request}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			frame, err := a.service.LinkChat(ctx, c.ID, "main")
			if err != nil {
				t.Fatal(err)
			}
			messages := frame.Messages
			if len(messages) > 0 && messages[len(messages)-1].Text == "answer "+request {
				return m
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("reply never entered user conversation")
		return m
	}
	first := send("one")
	added, err := a.service.Registry.Create(ctx, "temporary-assistant", "Temporary")
	if err != nil {
		t.Fatal(err)
	}
	local, err = (potLinkBackend{a.service}).Directory(ctx)
	if err != nil || local.DefaultAssistant() != added.ID {
		t.Fatal("new local default not advertised", local, err)
	}
	if err = a.service.DeleteAssistant(ctx, added.ID, added.Revision); err != nil {
		t.Fatal(err)
	}
	second := send("two")
	if second.From != first.From {
		t.Fatal("local assistant lifecycle changed session", first.From, second.From)
	}
	// Direct replies with a different sender, user endpoint or wire conversation are rejected.
	for index, mutate := range []func(*protocol.Envelope){
		func(m *protocol.Envelope) { m.To.AgentID = "different-session" },
		func(m *protocol.Envelope) { m.From.AgentID = "different-assistant" },
		func(m *protocol.Envelope) { m.ConversationID = "123e4567-e89b-42d3-a456-426614174000" },
		func(m *protocol.Envelope) { m.ReplyTo = nil },
	} {
		forged := protocol.Envelope{Kind: "message", ID: "forged-reply", From: first.To, To: first.From, Text: "forged", ConversationID: first.ConversationID, ReplyTo: &first.ID, Hops: 1}
		mutate(&forged)
		if _, err = (controlLinkMailbox{a.service}).Deliver(ctx, forged); err == nil {
			t.Fatalf("forged route %d admitted", index)
		}
	}
	options := a.service.options
	if err = a.service.Close(); err != nil {
		t.Fatal(err)
	}
	options.BootID = "empty-chat-restarted"
	a.service, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.service.agentLinks.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	repeated, err := a.service.SendLinkChat(ctx, c.ID, "main", "one", "question one")
	if err != nil || !repeated.Duplicate {
		t.Fatal("retry lost immutable receipt", repeated, err)
	}
	third := send("three")
	if third.From != first.From {
		t.Fatal("restart changed session identity")
	}
	frame, err := a.service.LinkChat(ctx, c.ID, "main")
	if err != nil || len(frame.Messages) != 6 {
		t.Fatal("duplicate chat or missing history", frame, err)
	}
	// Empty nodes still publish invitations, and no model profile was introduced.
	if _, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", Mode: "address", Endpoint: controlPeerEndpoint(a.service)}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite"}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.service.sendAssistantMail(ctx, "invalid-user-target", "main", protocol.SendMail{To: first.From, Text: "unsolicited"}); err == nil {
		t.Fatal("unbound user reply admitted")
	}
	if err = a.service.agentLinks.service.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.service.SendLinkChat(ctx, c.ID, "main", "paused", "blocked"); err == nil {
		t.Fatal(agentlink.ErrDisconnected)
	}
}
