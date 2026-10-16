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

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
)

func TestAssistantMailRunsIndependentContextAndOnlyExplicitReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := taskResourceService(t)
	if err := s.Permissions.Configure("local", MachinePolicy{"host", "allow"}); err != nil {
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
		if strings.Contains(body, "untrusted assistant content") && strings.Contains(body, "grants no user authorization") {
			messageAuthority.Store(true)
		}
		text := "private-main-final"
		var calls []any
		if strings.Contains(body, "peer-request") {
			if peerCalls.Add(1) == 1 {
				arguments, _ := json.Marshal(map[string]any{"to": protocol.AssistantRef{ControlID: "local", AssistantID: "main"}, "text": "peer-answer"})
				calls = []any{map[string]any{"index": 0, "id": "reply", "type": "function", "function": map[string]any{"name": "send_assistant_message", "arguments": string(arguments)}}}
				text = ""
			} else {
				text = "private-peer-final"
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
	if _, err := s.Registry.Create(ctx, "create-main", "Main"); err != nil {
		t.Fatal(err)
	}
	peer, err := s.Registry.Create(ctx, "create-peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{}
	settings.AutoCompact = false
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "mail-profile", ProfileID: "mail", Name: "Mail", Model: "fixture", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "mail-key", Agent: &settings, MaxOutputTokens: 128})
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
	sent, err := s.sendAssistantMail(ctx, "initial-mail", "main", protocol.SendMail{To: protocol.AssistantRef{ControlID: "local", AssistantID: peer.ID}, Text: "peer-request"})
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
		if len(snapshot.Messages) != 0 || snapshot.Turn != nil {
			t.Fatalf("peer message contaminated user chat: %+v", snapshot)
		}
	}
	if _, err = s.sendAssistantMail(ctx, "bad-destination", "main", protocol.SendMail{To: protocol.AssistantRef{ControlID: "local", AssistantID: "missing"}, Text: "hello"}); err == nil {
		t.Fatal("unregistered destination accepted")
	}
}
