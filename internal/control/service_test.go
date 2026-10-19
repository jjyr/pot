package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func awaitConversation(t *testing.T, c *assistant.Conversation, predicate func(protocol.Snapshot) bool) protocol.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return awaitConversationContext(t, ctx, c, predicate)
}

func awaitConversationContext(t *testing.T, ctx context.Context, c *assistant.Conversation, predicate func(protocol.Snapshot) bool) protocol.Snapshot {
	t.Helper()
	updates, remove, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	for {
		select {
		case <-ctx.Done():
			snapshot, _ := c.Snapshot(context.Background())
			t.Fatalf("waiting for conversation: %+v", snapshot)
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatal("conversation closed")
			}
			if predicate(snapshot) {
				return snapshot
			}
		}
	}
}
func TestLocalControlAgentApprovalCommandPersistenceAndRestart(t *testing.T) {
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing Control credential")
		}
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		if json.Unmarshal(body, &request) != nil {
			t.Error("invalid model body")
		}
		call := modelCalls.Add(1)
		var event any
		if call == 1 {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "command-1", "type": "function", "function": map[string]any{"name": "exec_command", "arguments": `{"cmd":"printf x >> proof; printf ok","login":false,"request_unsandboxed":true,"justification":"Access required by the execution contract test"}`}}}}, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 5}}
		} else {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Finished 世界"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 4}}
		}
		data, _ := json.Marshal(event)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + string(data) + "\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	ctx := context.Background()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	os.Mkdir(workspace, 0700)
	manager, _ := execution.New("local", "boot-1", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	options := Options{Root: root, RuntimeID: "local", BootID: "boot-1", Workspace: workspace, Manager: manager, Keys: keys}
	service, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err = service.Registry.Create(ctx, "create-assistant", "Pot"); err != nil {
		t.Fatal(err)
	}
	profile, err := service.Profiles.Save(ctx, SaveProfile{RequestID: "configure", ProfileID: "test", Name: "Test", Model: "test", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "test-key", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
		t.Fatal(err)
	}
	conversation, err := service.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	specification, err := service.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = conversation.Configure(ctx, specification, 0); err != nil {
		t.Fatal(err)
	}
	send := protocol.SendMessage{RequestID: "request-1", ConversationID: "main", Text: "Run the approved command 用户原文"}
	receipt, err := conversation.Send(ctx, send)
	if err != nil {
		t.Fatal(err)
	}
	sub := service.Permissions.Changes.Subscribe()
	defer sub.Close()
	approvalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var pending []Approval
	for {
		pending = service.Permissions.Pending("main")
		if len(pending) > 0 {
			break
		}
		if _, err = sub.Next(approvalCtx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(workspace, "assistants", "main", "proof")); !os.IsNotExist(err) {
		t.Fatal("command ran before approval")
	}
	if err = service.Permissions.Decide("main", pending[0].ID, "allow_task"); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitConversation(t, conversation, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) > 1 })
	if snapshot.Agent.Phase == protocol.AgentFailed {
		t.Fatal(snapshot.Agent)
	}
	if snapshot.Messages[len(snapshot.Messages)-1].Text != "Finished 世界" || modelCalls.Load() != 2 {
		t.Fatal(snapshot, modelCalls.Load())
	}
	body, err := os.ReadFile(filepath.Join(workspace, "assistants", "main", "proof"))
	if err != nil || string(body) != "x" {
		details, _ := json.Marshal(snapshot.Messages)
		t.Fatal(string(body), err, string(details))
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	manager.Stop()
	if err = manager.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	options.Manager, _ = execution.New("local", "boot-2", execution.DefaultLimits())
	options.BootID = "boot-2"
	service, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err = service.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := conversation.Send(ctx, send)
	if err != nil || !duplicate.Duplicate || duplicate.MessageID != receipt.MessageID || modelCalls.Load() != 2 {
		t.Fatal(duplicate, err, modelCalls.Load())
	}
	spec, err := service.Profiles.Resolve(profile.ID)
	if err != nil || spec.Model != "test" {
		t.Fatal(err)
	}
}
