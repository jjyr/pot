package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The operation port and frame contract are identical for both execution adapters.
func TestChatEndpointContract(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"native", "peer"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 15*time.Second))
			defer cancel()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { batchReply(w, "hello") }))
			defer upstream.Close()
			a := &agentControlFixture{service: taskControl(t, "contract-a", upstream.URL)}
			a.endpoint = controlPeerEndpoint(a.service)
			owner := "main"
			if mode == "peer" {
				b := agentControl(t, "contract-b")
				c := connectAgentControls(t, a, b)
				owner = "agent-link:" + c.ID + ":main"
			}
			capabilities, err := a.service.AgentCapabilities(ctx, owner)
			if err != nil || !capabilities.Supports(agentlink.ChatSubscribe) {
				t.Fatal(capabilities, err)
			}
			sub, err := a.service.SubscribeAgent(ctx, owner, "")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			select {
			case <-sub.Updates:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			before, err := sub.Frame(ctx, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(protocol.SendMessage{RequestID: "contract-input", ConversationID: before.ConversationID, Text: "hello"})
			first, err := a.service.CallAgent(ctx, owner, agentlink.ChatSend, agentlink.Call{Body: payload})
			if err != nil {
				t.Fatal(err)
			}
			second, err := a.service.CallAgent(ctx, owner, agentlink.ChatSend, agentlink.Call{Body: payload})
			if err != nil {
				t.Fatal(err)
			}
			firstJSON, _ := json.Marshal(first)
			secondJSON, _ := json.Marshal(second)
			var firstReceipt, secondReceipt struct {
				MessageID string `json:"message_id"`
				Duplicate bool   `json:"duplicate"`
			}
			json.Unmarshal(firstJSON, &firstReceipt)
			json.Unmarshal(secondJSON, &secondReceipt)
			if firstReceipt.MessageID == "" || secondReceipt.MessageID != firstReceipt.MessageID || !secondReceipt.Duplicate {
				t.Fatal(first, second)
			}
			value, err := a.service.CallAgent(ctx, owner, agentlink.ChatRead, agentlink.Call{})
			if err != nil {
				t.Fatal(err)
			}
			frame := value.(protocol.UIFrame)
			if err := frame.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(frame.Replies) != 1 || frame.Replies[0].InputID != firstReceipt.MessageID || !frame.Replies[0].Valid() {
				t.Fatal(frame)
			}
			value, err = a.service.CallAgent(ctx, owner, agentlink.ChatReceipt, agentlink.Call{Query: map[string]string{"request_id": "contract-input", "conversation_id": before.ConversationID}})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(value)
			var receipt struct {
				State string `json:"state"`
			}
			json.Unmarshal(raw, &receipt)
			if receipt.State != "accepted" {
				t.Fatal(value)
			}
			for _, query := range []map[string]string{
				{"request_id": "", "conversation_id": before.ConversationID},
				{"request_id": "contract-input", "conversation_id": ""},
				{"request_id": "contract-input", "conversation_id": before.ConversationID, "user_message_id": "invalid id"},
			} {
				if _, err := a.service.CallAgent(ctx, owner, agentlink.ChatReceipt, agentlink.Call{Query: query}); err == nil {
					t.Fatal("adapter bypassed common receipt constraints", query)
				}
			}
		})
	}
}

func TestChatStatusPreservesBindingAndTerminalFailureWithoutText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, b := agentControl(t, "status-a"), agentControl(t, "status-b")
	c := connectAgentControls(t, a, b)
	result, err := a.service.SendLinkChat(ctx, c.ID, "main", "status-input", "hello")
	if err != nil {
		t.Fatal(err)
	}
	id := result.MessageID
	parent := awaitAgentLinkMail(t, b.service, id).Message
	query := agentlink.ChatLookup{From: parent.From, AssistantID: "main", MessageID: id}
	status, err := a.service.agentLinks.service.ChatStatus(ctx, c.ID, query)
	if err != nil || status.State != "queued" {
		t.Fatal(status, err)
	}
	for _, change := range []func(*agentlink.ChatLookup){func(q *agentlink.ChatLookup) { q.From.AgentID = "other-user" }, func(q *agentlink.ChatLookup) { q.AssistantID = "other-assistant" }, func(q *agentlink.ChatLookup) { q.From.NodeID = "other-node" }} {
		wrong := query
		change(&wrong)
		if _, err := a.service.agentLinks.service.ChatStatus(ctx, c.ID, wrong); !errors.Is(err, agentlink.ErrUnauthorized) {
			t.Fatal("unbound status read admitted", wrong, err)
		}
	}
	if err := b.service.Mailbox.Claim(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := b.service.Mailbox.Finish(ctx, id, false, "preparation failed"); err != nil {
		t.Fatal(err)
	}
	a.service.syncLinkChats(ctx)
	frameValue, err := a.service.LinkChat(ctx, c.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	frame := frameValue
	if len(frame.Replies) != 1 || frame.Replies[0].State != "failed" || frame.Replies[0].Error != "preparation failed" || len(frame.Messages) != 1 {
		t.Fatal(frame)
	}
	before := frame.Cursor
	a.service.syncLinkChats(ctx)
	value, _ := a.service.LinkChat(ctx, c.ID, "main")
	if value.Cursor != before {
		t.Fatal("terminal reads changed projection")
	}
}

func TestChatRepliesKeepExactParentsAcrossOutOfOrderDeliveryAndRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, b := agentControl(t, "parents-a"), agentControl(t, "parents-b")
	c := connectAgentControls(t, a, b)
	inputs := []protocol.Envelope{}
	for _, request := range []string{"first", "second"} {
		accepted, err := a.service.SendLinkChat(ctx, c.ID, "main", request, request)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, awaitAgentLinkMail(t, b.service, accepted.MessageID).Message)
	}
	if err := b.service.Mailbox.Claim(ctx, inputs[0].ID); err != nil {
		t.Fatal(err)
	}
	for i := len(inputs) - 1; i >= 0; i-- {
		parent := inputs[i]
		replyCtx := context.WithValue(ctx, mailParentKey{}, parent)
		if _, err := b.service.sendAssistantMail(replyCtx, "answer-"+parent.ID, "main", protocol.SendMail{To: parent.From, Text: "answer " + parent.Text}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		frame, err := a.service.LinkChat(ctx, c.ID, "main")
		if err != nil {
			t.Fatal(err)
		}
		if len(frame.Messages) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reply delivery timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Simulate an old projection; the mailbox remains the authority for binding.
	if _, err := a.service.integrationDB.Exec("UPDATE agent_link_chat SET reply_to='' WHERE role='assistant'"); err != nil {
		t.Fatal(err)
	}
	options := a.service.options
	if err := a.service.Close(); err != nil {
		t.Fatal(err)
	}
	options.BootID = "parents-restarted"
	var err error
	a.service, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := a.service.LinkChat(ctx, c.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Replies) != 2 {
		t.Fatal(frame)
	}
	for _, m := range frame.Messages {
		if m.Role != protocol.Assistant {
			continue
		}
		matched := false
		for _, parent := range inputs {
			if m.ReplyTo == parent.ID && m.Text == "answer "+parent.Text {
				matched = true
			}
		}
		if !matched {
			t.Fatal("reply inherited the nearest displayed input", m)
		}
	}
	if err := a.service.agentLinks.service.Disconnect(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	a.service.syncLinkChats(ctx)
	frame, err = a.service.LinkChat(ctx, c.ID, "main")
	if err != nil || !frame.Replies[0].Uncertain || frame.Replies[0].Terminal() {
		t.Fatal("disconnect fabricated a terminal outcome", frame, err)
	}
}
