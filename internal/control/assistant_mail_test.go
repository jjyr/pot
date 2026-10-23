package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAssistantMailUsesOwningConversationAndExplicitPeerReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := taskResourceService(t)
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	var peerCalls, mainCalls atomic.Int32
	var messageAuthority atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer mail-key" {
			t.Error("Control model credential missing")
		}
		body := string(raw)
		if strings.Contains(body, "not new user requests or additional authorization") {
			messageAuthority.Store(true)
		}
		text := "main-user-report"
		var calls []any
		if strings.Contains(body, "peer-request") {
			if peerCalls.Add(1) == 1 {
				arguments, _ := json.Marshal(map[string]any{"to": protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, "text": "peer-answer"})
				calls = []any{map[string]any{"index": 0, "id": "reply", "type": "function", "function": map[string]any{"name": "send_message", "arguments": string(arguments)}}}
				text = ""
			} else {
				text = "peer-user-report"
			}
		} else {
			mainCalls.Add(1)
		}
		delta := map[string]any{"content": text}
		finish := "stop"
		if len(calls) > 0 {
			delta["tool_calls"] = calls
			finish = "tool_calls"
		}
		event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5}})
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + string(event) + "\n\n"))
	}))
	defer server.Close()
	if _, err := legacyMainAssistant(ctx, s, "create-main", "Main"); err != nil {
		t.Fatal(err)
	}
	peer, err := s.Registry.Create(ctx, "create-peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "mail-profile", ProfileID: "mail", Name: "Mail", Model: "fixture", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "mail-key", MaxOutputTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"main", peer.ID} {
		conversation, err := s.Conversation(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if err = conversation.Configure(ctx, spec, 0); err != nil {
			t.Fatal(err)
		}
	}
	sub := s.Mailbox.Changes.Subscribe()
	defer sub.Close()
	sent, err := s.sendAssistantMail(ctx, "initial-mail", "main", protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: peer.ID}, Text: "peer-request"})
	if err != nil {
		t.Fatal(err)
	}
	var history []protocol.MailRecord
	for {
		history, err = s.Mailbox.History(ctx, "main", 0)
		if err != nil {
			t.Fatal(err)
		}
		processed := 0
		for _, record := range history {
			if record.State == "failed" || record.State == "interrupted" {
				t.Fatalf("mail failed: %+v; detail=%v", history, *record.Detail)
			}
			if record.State == "processed" {
				processed++
			}
		}
		if len(history) == 2 && processed == 2 {
			break
		}
		if _, err = sub.Next(ctx); err != nil {
			t.Fatalf("mail did not complete: %+v %v", history, err)
		}
	}
	reply := history[1].Message
	if reply.Text != "peer-answer" || reply.ReplyTo == nil || *reply.ReplyTo != sent.ID || reply.ConversationID != sent.ConversationID || reply.Hops != 1 {
		t.Fatalf("explicit reply lost parent binding: %+v", reply)
	}
	if peerCalls.Load() != 2 || mainCalls.Load() != 1 || !messageAuthority.Load() {
		t.Fatalf("mail calls peer=%d main=%d peer_authority=%v", peerCalls.Load(), mainCalls.Load(), messageAuthority.Load())
	}
	for _, owner := range []string{"main", peer.ID} {
		conversation, _ := s.Conversation(ctx, owner)
		snapshot, _ := conversation.Snapshot(ctx)
		if len(snapshot.Messages) == 0 || snapshot.Turn != nil {
			t.Fatalf("correspondence did not enter the owning conversation: %+v", snapshot)
		}
	}
	if _, err = s.sendAssistantMail(ctx, "bad-destination", "main", protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "missing"}, Text: "hello"}); err == nil {
		t.Fatal("unregistered destination accepted")
	}
}

func TestAssistantMailAllowanceUsesOwningConversation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var calls atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		batchReply(w, "user report")
	}, 2)
	if err := s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 1, Timezone: "UTC", AutoResume: false}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Conversation(ctx, "main")
	message, err := s.Mailbox.Send(ctx, "mail", protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "tony"}, protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: "assessment ready"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(snapshot protocol.Snapshot) bool { return snapshot.Turn != nil && snapshot.Turn.State == "waiting" })
	if calls.Load() != 0 {
		t.Fatal("budget-blocked correspondence sampled model")
	}
	wait, err := s.Usage.Waiting(ctx, "main")
	if err != nil || wait == nil || wait.Turn != "pot-result-"+message.ID {
		t.Fatal(wait, err)
	}
	if err := s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 100000, Timezone: "UTC", AutoResume: false}); err != nil {
		t.Fatal(err)
	}
	q := ResumeAllowanceCommand{RequestID: "resume", Turn: wait.Turn, Since: wait.Since}
	if err := s.ResumeAllowance(ctx, "main", q); err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) > 1
	})
	if err := s.ResumeAllowance(ctx, "main", q); err != nil {
		t.Fatal("exact resume receipt lost", err)
	}
	if calls.Load() != 1 {
		t.Fatal("correspondence replayed", calls.Load())
	}
	frame, err := c.UIFrame(ctx, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	inputs := 0
	for _, m := range frame.Messages {
		if m.RequestID != nil && *m.RequestID == wait.Turn {
			inputs++
			if len(m.Correspondence) != 1 || m.Correspondence[0].ID != message.ID {
				t.Fatal("missing incoming receipt", m)
			}
		}
	}
	if inputs != 1 {
		t.Fatal("resume duplicated correspondence input", inputs)
	}
}
