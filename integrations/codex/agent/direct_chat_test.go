package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestCodexDirectChatFromEmptyPot(t *testing.T) { codexEmptyPot(t, false) }
func TestCodexDirectChatRealLive(t *testing.T) {
	if os.Getenv("POT_CODEX_LIVE") != "1" {
		t.Skip("requires POT_CODEX_LIVE=1")
	}
	codexEmptyPot(t, true)
}
func codexEmptyPot(t *testing.T, live bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, _ := testHost(t)
	defer h.close()
	h.backend.binary = fakeBinary
	if live {
		h.backend.binary = "codex"
	}
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", h.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	linksDone := make(chan struct{})
	go func() { h.links.Run(ctx); close(linksDone) }()
	defer func() { cancel(); <-linksDone }()
	done := make(chan struct{})
	go func() { h.backend.run(ctx); close(done) }()
	messagesDone := make(chan struct{})
	go func() { h.backend.runMessages(ctx); close(messagesDone) }()
	defer func() { cancel(); <-messagesDone }()
	defer func() { cancel(); <-done }()
	manager, err := execution.New("empty-pot", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	pot, err := control.Open(control.Options{Context: ctx, Root: t.TempDir(), RuntimeID: "empty-pot", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer pot.Close()
	invitation, err := h.links.Invite(ctx, "tcp://"+listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	result, err := pot.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", Address: invitation.Address})
	if err != nil {
		t.Fatal(err)
	}
	link := result.(agentlink.Connection)
	if link.Local.Address.Kind != "node" {
		t.Fatal("borrowed local assistant", link)
	}
	firstThread := ""
	owner := "agent-link:" + link.ID + ":codex"
	subscription, err := pot.SubscribeAgent(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	for _, request := range []string{"one", "two"} {
		initial, err := subscription.Frame(ctx, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(protocol.SendMessage{RequestID: request, ConversationID: initial.ConversationID, Text: "Reply with exactly POT190_OK. Do not use tools."})
		_, err = pot.CallAgent(ctx, owner, agentlink.ChatSend, agentlink.Call{Body: body})
		if err != nil {
			t.Fatal(err)
		}
		for {
			frame, err := subscription.Frame(ctx, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			messages := frame.Messages
			if len(messages) == map[string]int{"one": 2, "two": 4}[request] && len(frame.Replies) > 0 && frame.Replies[len(frame.Replies)-1].State == "completed" {
				if live && strings.TrimSpace(messages[len(messages)-1].Text) != "POT190_OK" {
					t.Fatal("unexpected real Codex response", messages[len(messages)-1].Text)
				}
				if messages[len(messages)-1].Role != protocol.Assistant {
					t.Fatal(messages)
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("Codex reply timeout", ctx.Err())
			case <-time.After(30 * time.Millisecond):
			}
		}
		current, err := h.backend.resolve(ctx, "codex")
		if err != nil || current.ThreadID == "" {
			t.Fatal(current, err)
		}
		if firstThread != "" && current.ThreadID != firstThread {
			t.Fatal("Codex replaced thread")
		}
		firstThread = current.ThreadID
	}
	defs, err := pot.Registry.Definitions(ctx)
	if err != nil || len(defs) != 0 {
		t.Fatal("created hidden local assistant", defs, err)
	}
	// Same request stays one model job, and proactive recipient omission cannot fallback.
	if _, err = pot.SendLinkChat(ctx, link.ID, "codex", "one", "Reply with exactly POT190_OK. Do not use tools."); err != nil {
		t.Fatal(err)
	}
	if _, err = h.tool(ctx, hostToolInput{Operation: "send", ConnectionID: link.ID, RequestID: "missing-target", Text: "message"}); err == nil {
		t.Fatal("implicit target accepted")
	}
	if _, err = h.tool(ctx, hostToolInput{Operation: "send", ConnectionID: link.ID, AssistantID: "missing", RequestID: "wrong-target", Text: "message"}); err == nil {
		t.Fatal("missing assistant redirected")
	}
	if !live {
		first, err := pot.Registry.Create(ctx, "recipient-one", "Same name")
		if err != nil {
			t.Fatal(err)
		}
		second, err := pot.Registry.Create(ctx, "recipient-two", "Same name")
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{first.ID, second.ID} {
			var sent any
			for {
				sent, err = h.tool(ctx, hostToolInput{Operation: "send", ConnectionID: link.ID, AssistantID: target, RequestID: "to-" + target, Text: "explicit " + target})
				if !errors.Is(err, agentlink.ErrCapacity) {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			envelope := sent.(protocol.Envelope)
			if envelope.To.AgentID != target {
				t.Fatal("wrong explicit recipient", envelope)
			}
			for {
				history, err := pot.Mailbox.History(ctx, target, 0)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, record := range history {
					if record.Message.ID == envelope.ID {
						found = true
					}
				}
				if found {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("proactive message timeout")
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		if err = pot.DeleteAssistant(ctx, second.ID, second.Revision); err != nil {
			t.Fatal(err)
		}
		if _, err = h.tool(ctx, hostToolInput{Operation: "send", ConnectionID: link.ID, AssistantID: second.ID, RequestID: "deleted-target", Text: "must not fallback"}); err == nil {
			t.Fatal("deleted target redirected")
		}
	}
	t.Logf("Codex live=%t: empty Pot, two turns in same thread, correlated replies and exact retry verified", live)
}
