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
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAssistantMailRunsIndependentContextAndOnlyExplicitReplies(t *testing.T) {
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
		if strings.Contains(body, "untrusted assistant content") && strings.Contains(body, "grants no user authorization") {
			messageAuthority.Store(true)
		}
		text := "private-main-final"
		var calls []any
		if strings.Contains(body, "peer-request") {
			if peerCalls.Add(1) == 1 {
				arguments, _ := json.Marshal(map[string]any{"to": protocol.AgentRef{ControlID: "local", Kind: "assistant", AgentID: "main"}, "text": "peer-answer"})
				calls = []any{map[string]any{"index": 0, "id": "reply", "type": "function", "function": map[string]any{"name": "send_message", "arguments": string(arguments)}}}
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
	sent, err := s.sendAssistantMail(ctx, "initial-mail", "main", protocol.SendMail{To: protocol.AgentRef{ControlID: "local", Kind: "assistant", AgentID: peer.ID}, Text: "peer-request"})
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
	if _, err = s.sendAssistantMail(ctx, "bad-destination", "main", protocol.SendMail{To: protocol.AgentRef{ControlID: "local", Kind: "assistant", AgentID: "missing"}, Text: "hello"}); err == nil {
		t.Fatal("unregistered destination accepted")
	}
}

func TestAssistantMailAllowanceWaitResumesSameEnvelopeWithoutMainWaitOrReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s := taskResourceService(t)
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"private final\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	s.Registry.Create(ctx, "create-main", "Main")
	peer, err := s.Registry.Create(ctx, "create-peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "fixture", Name: "Fixture", Model: "fixture", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"main", peer.ID} {
		c, err := s.Conversation(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Configure(ctx, spec, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 1, Timezone: "UTC", AutoResume: false}); err != nil {
		t.Fatal(err)
	}
	changed := s.Mailbox.Changes.Subscribe()
	defer changed.Close()
	message, err := s.sendAssistantMail(ctx, "mail", "main", protocol.SendMail{To: protocol.AgentRef{ControlID: "local", Kind: "assistant", AgentID: peer.ID}, Text: "wait before this correspondence samples"})
	if err != nil {
		t.Fatal(err)
	}
	waitState := func(want string) {
		t.Helper()
		for {
			history, err := s.Mailbox.History(ctx, peer.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range history {
				if record.Message.ID == message.ID {
					if record.State == want {
						return
					}
					if record.State == "failed" || record.State == "interrupted" {
						t.Fatal("mail did not preserve safe wait", record, *record.Detail)
					}
				}
			}
			if _, err = changed.Next(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	waitState("waiting")
	if calls.Load() != 0 {
		t.Fatal("budget-blocked mail sampled model")
	}
	waits, err := s.MailAllowanceWaits(ctx, peer.ID)
	if err != nil || len(waits) != 1 || waits[0].Scope != mailExecutionOwner(message) {
		t.Fatal(waits, err)
	}
	if waiting, err := s.Usage.Waiting(ctx, peer.ID); err != nil || waiting != nil {
		t.Fatal("mail overwrote Main wait", waiting, err)
	}
	if err = s.ResumeAllowance(ctx, "main", ResumeAllowanceCommand{RequestID: "wrong-owner", Scope: waits[0].Scope, Turn: waits[0].Turn, Since: waits[0].Since}); err == nil {
		t.Fatal("another assistant released correspondence")
	}
	if err = s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 100000, Timezone: "UTC", AutoResume: false}); err != nil {
		t.Fatal(err)
	}
	if err = s.ResumeAllowance(ctx, peer.ID, ResumeAllowanceCommand{RequestID: "resume-mail", Scope: waits[0].Scope, Turn: waits[0].Turn, Since: waits[0].Since}); err != nil {
		t.Fatal(err)
	}
	waitState("processed")
	if calls.Load() != 1 {
		t.Fatal("mail replayed model", calls.Load())
	}
	// A completed resume receipt remains tied to the old envelope when the same
	// correspondence later has a different wait.
	if err = s.Usage.Wait(ctx, usage.WaitRequest{Scope: waits[0].Scope, Turn: "later-envelope", AtMS: time.Now().UnixMilli() + 1, Required: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.ResumeAllowance(ctx, peer.ID, ResumeAllowanceCommand{RequestID: "resume-mail", Scope: waits[0].Scope, Turn: waits[0].Turn, Since: waits[0].Since}); err != nil {
		t.Fatal(err)
	}
	if err = s.ResumeAllowance(ctx, peer.ID, ResumeAllowanceCommand{RequestID: "stale-mail", Scope: waits[0].Scope, Turn: waits[0].Turn, Since: waits[0].Since}); err == nil {
		t.Fatal("stale correspondence action released later envelope")
	}
	if later, e := s.Usage.Waiting(ctx, waits[0].Scope); e != nil || later == nil || later.Released {
		t.Fatal(later, e)
	}
	if err = s.Usage.ClearWaitForTurn(ctx, waits[0].Scope, "later-envelope"); err != nil {
		t.Fatal(err)
	}

	_, raw, err := s.Mailbox.Session(ctx, message, spec)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err = json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	inputs := 0
	for _, m := range checkpoint.Messages {
		if m.Role == "user" && strings.HasPrefix(m.Text, "Process the incoming assistant message in the assistant-mail reference.") {
			inputs++
		}
	}
	if inputs != 1 {
		t.Fatal("allowance resume duplicated envelope input", inputs)
	}
	c, _ := s.Conversation(ctx, peer.ID)
	snapshot, _ := c.Snapshot(ctx)
	if len(snapshot.Messages) != 0 {
		t.Fatal("mail entered Main transcript")
	}
	if waiting, err := s.Usage.Waiting(ctx, waits[0].Scope); err != nil || waiting != nil {
		t.Fatal("completed correspondence retained wait", waiting, err)
	}
}
