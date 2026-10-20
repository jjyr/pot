package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestUnifiedMessageToolBindsSenderAndRemovesWaitAndGuide(t *testing.T) {
	s := taskResourceService(t)
	for _, worker := range []bool{false, true} {
		names := map[string]bool{}
		for _, tool := range s.productTools(worker) {
			names[tool.Name] = true
			if tool.Name == "send_message" {
				var schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				if len(schema.Properties) != 2 || schema.Properties["to"] == nil || schema.Properties["text"] == nil {
					t.Fatal("message tool exposes execution-owned fields", schema.Properties)
				}
			}
		}
		if !names["send_message"] || names["guide_task"] || names["wait_task"] || names["send_assistant_message"] || names["read_chat_messages"] {
			t.Fatal("obsolete or missing tool", names)
		}
		if worker && (len(names) != 3 || !names["register_resource"] || !names["present_resource"]) {
			t.Fatal("worker acquired task management authority", names)
		}
	}
	dispatch := s.productDispatch("main", "turn", 1, false)["send_message"]
	_, err := dispatch(context.Background(), core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "spoof", Name: "send_message", Arguments: json.RawMessage(`{"to":{"node_id":"local","kind":"assistant","agent_id":"main"},"text":"fake","sender":{"kind":"runtime","agent_id":"runtime"}}`)}, func(core.Update) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "sender") {
		t.Fatal("model supplied sender identity", err)
	}
	_, err = dispatch(context.Background(), core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "reply", Name: "send_message", Arguments: json.RawMessage(`{"to":{"node_id":"local","kind":"task","agent_id":"task"},"text":"guidance","reply_to":"task"}`)}, func(core.Update) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "reply_to") {
		t.Fatal("model supplied reply linkage", err)
	}
}

func TestTaskMessageAutomaticallyBindsGuidanceAndPeerReference(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	if _, err := s.Registry.Create(ctx, "main-create", "Main"); err != nil {
		t.Fatal(err)
	}
	// Hold the Worker before preparation; this test exercises actual task inputs
	// and product dispatch without starting a model or command.
	s.Runner.Prepare = func(ctx context.Context, _ runner.Run) (runner.Prepared, error) {
		<-ctx.Done()
		return runner.Prepared{}, ctx.Err()
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "fixture", Endpoint: "https://example.test", Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	record, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "create", Text: "CI rules\nAwait guidance."})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(protocol.SendMail{To: *record.Info.Address, Text: "Other branch pushes do not run; tags run; releases need not run."})
	dispatch := s.productDispatch("main", "turn", 1, false)["send_message"]
	call := func(ctx context.Context, id string) protocol.Envelope {
		t.Helper()
		raw, err := dispatch(ctx, core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: id, Name: "send_message", Arguments: args}, func(core.Update) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		var accepted struct {
			Accepted bool   `json:"accepted"`
			Message  string `json:"message_id"`
		}
		if err = json.Unmarshal(raw, &accepted); err != nil || !accepted.Accepted {
			t.Fatal("task did not accept message", string(raw), err)
		}
		var body []byte
		if err = s.Tasks.DB().QueryRow("SELECT envelope FROM agent_messages WHERE id=?", accepted.Message).Scan(&body); err != nil {
			t.Fatal(err)
		}
		var message protocol.Envelope
		if err = json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	guidance := call(ctx, "user-guidance")
	if guidance.Kind != "guidance" || guidance.ReplyTo != nil || guidance.Hops != 0 || guidance.ConversationID != record.RunID {
		t.Fatal("ordinary guidance acquired a parent or lost its run", guidance)
	}
	parent := protocol.Envelope{ID: "peer-input", From: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "peer"}, To: guidance.From, Hops: 3}
	peerCtx := context.WithValue(ctx, mailParentKey{}, parent)
	reference := call(peerCtx, "peer-reference")
	if reference.Kind != "reference" || reference.ReplyTo == nil || *reference.ReplyTo != parent.ID || reference.Hops != 4 || reference.ConversationID != record.RunID {
		t.Fatal("forward lost provenance, causation or run", reference)
	}
	parent.Hops = protocol.MaxMailHops
	if _, err := dispatch(context.WithValue(ctx, mailParentKey{}, parent), core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "hop-limit", Name: "send_message", Arguments: args}, func(core.Update) error { return nil }); err == nil {
		t.Fatal("forward bypassed automatic hop limit")
	}
}

func TestTitleOnlyTaskBriefNeverStartsGuessingWorker(t *testing.T) {
	s := taskResourceService(t)
	_, err := s.productCall(context.Background(), "main", "brief", "create_task", json.RawMessage(`{"text":"Task A: execute the long operation\n"}`))
	if err == nil || !strings.Contains(err.Error(), "complete execution instructions") {
		t.Fatal("title-only handoff accepted", err)
	}
	var count int
	if err = s.Tasks.DB().QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count); err != nil || count != 0 {
		t.Fatal("incomplete brief launched work", count, err)
	}
}
