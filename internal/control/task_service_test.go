package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/secrets"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalTaskUsesRunnerSharedCapacityControlModelAndSummary(t *testing.T) {
	var calls atomic.Int32
	var summaryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer task-key" {
			t.Error("provider credential did not stay at Control")
		}
		raw, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		isSummary := strings.Contains(string(fields["messages"]), "Summarize background task results")
		var event any
		if isSummary {
			summaryCalls.Add(1)
			if strings.Contains(string(fields["tools"]), "exec_command") {
				t.Error("summary exposed effect tools")
			}
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Task result summarized"}, "finish_reason": "stop"}}}
		} else if calls.Add(1) == 1 {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "task-command", "type": "function", "function": map[string]any{"name": "exec_command", "arguments": `{"cmd":"printf proof > artifact.txt; printf worker-ok","login":false}`}}}}, "finish_reason": "tool_calls"}}}
		} else {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Completed task artifact"}, "finish_reason": "stop"}}}
		}
		event.(map[string]any)["usage"] = map[string]any{"prompt_tokens": 20, "completion_tokens": 5}
		data, _ := json.Marshal(event)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + string(data) + "\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: root, RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Registry.Create(ctx, "create", "Pot")
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{"exec_command"}
	settings.AutoCompact = false
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "task-key", Agent: &settings, MaxOutputTokens: 100})
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
	if err = s.Permissions.Configure("local", MachinePolicy{"host", "allow"}); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "task-create", Text: "Write one artifact", MachineID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	for {
		r, err := s.Tasks.Get(ctx, "main", accepted.Info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Info.Phase == protocol.TaskComplete {
			if r.Result == nil || !strings.Contains(r.Result.Text, "Completed") {
				t.Fatalf("result: %+v", r)
			}
			break
		}
		if _, err = sub.Next(ctx); err != nil {
			t.Fatalf("Task did not finish: %+v: %v", r, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "agent-workspace", "tasks", accepted.Info.ID, "artifact.txt"))
	if err != nil || string(data) != "proof" {
		t.Fatalf("fixed worker artifact: %s %v", data, err)
	}
	snapshot := awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && strings.Contains(func() string {
			var out string
			for _, m := range snapshot.Messages {
				out += m.Text
			}
			return out
		}(), "Task result summarized")
	})
	if calls.Load() != 2 || summaryCalls.Load() != 1 {
		t.Fatalf("model count worker=%d summary=%d, snapshot=%+v", calls.Load(), summaryCalls.Load(), snapshot)
	}
	for _, m := range snapshot.Messages {
		if m.RequestID != nil && strings.HasPrefix(*m.RequestID, "pot-result-") && m.Role != protocol.System {
			t.Fatal("result became user authority")
		}
	}
	notices, _ := s.Tasks.Notifications(ctx, "main")
	if len(notices) != 0 {
		t.Fatal("acknowledged result still pending")
	}
	again, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "task-create", Text: "Write one artifact", MachineID: "local"})
	if err != nil || again.Info.ID != accepted.Info.ID {
		t.Fatal("duplicate task admission changed identity")
	}
}
