package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/controlplane"
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

func taskControl(t *testing.T, id, url string) *Service {
	t.Helper()
	manager, _ := execution.New(id, "boot-"+id, execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: id, BootID: "boot-" + id, Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if url != "" {
		if err = s.Permissions.Configure(id, MachinePolicy{"host", "allow"}); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		s.Registry.Create(ctx, "create", "Pot")
		settings := core.DefaultAgentSettings()
		settings.Plugins = map[string]core.PluginConfig{}
		settings.BuiltinTools = []string{"exec_command"}
		settings.AutoCompact = false
		profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: url, Protocol: "openai_chat", APIKey: "owner-key", Agent: &settings, MaxOutputTokens: 100})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.Profiles.Resolve(profile.ID)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := s.Conversation(ctx, "main")
		if err = c.Configure(ctx, spec, 0); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func TestRemoteTaskUsesOutwardRPCControlModelApprovalAndFixedWorker(t *testing.T) {
	var workerCalls atomic.Int32
	var summaries atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owner-key" {
			t.Error("provider key missing at Control")
		}
		raw, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		var event any
		if strings.Contains(string(fields["messages"]), "Summarize background task results") {
			summaries.Add(1)
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Remote result summarized"}, "finish_reason": "stop"}}}
		} else if workerCalls.Add(1) == 1 {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "remote-command", "type": "function", "function": map[string]any{"name": "exec_command", "arguments": `{"cmd":"printf remote-proof > artifact.txt; printf remote-ok","login":false}`}}}}, "finish_reason": "tool_calls"}}}
		} else {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Remote artifact complete"}, "finish_reason": "stop"}}}
		}
		event.(map[string]any)["usage"] = map[string]any{"prompt_tokens": 20, "completion_tokens": 5}
		data, _ := json.Marshal(event)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + string(data) + "\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	control := taskControl(t, "control", model.URL)
	worker := taskControl(t, "worker", "")
	if err := control.Machines.EnableControl(); err != nil {
		t.Fatal(err)
	}
	listener := httptest.NewServer(control.ControlServer(nil))
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	inv, err := control.Machines.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	connector := controlplane.Connector{Directory: worker.Machines}
	if _, err = connector.Enroll(ctx, listener.URL, control.Machines.Identity(), inv.Code, "enroll-worker"); err != nil {
		t.Fatal(err)
	}
	peer, err := worker.RunnerConnect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err = control.Permissions.Configure("worker", MachinePolicy{"host", "ask"}); err != nil {
		t.Fatal(err)
	}
	record, err := control.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "remote-create", MachineID: "worker", Text: "Write a remote artifact"})
	if err != nil {
		t.Fatal(err)
	}
	approvalSub := control.Permissions.Changes.Subscribe()
	defer approvalSub.Close()
	for {
		pending := control.Permissions.Pending("main")
		if len(pending) > 0 {
			if pending[0].Request.Task != record.Info.ID || pending[0].Request.MachineID != "worker" {
				t.Fatalf("approval scope: %+v", pending)
			}
			if err = control.Permissions.Decide("main", pending[0].ID, "allow_once"); err != nil {
				t.Fatal(err)
			}
			break
		}
		if _, err = approvalSub.Next(ctx); err != nil {
			current, _ := control.Tasks.Get(context.Background(), "main", record.Info.ID)
			t.Fatalf("remote approval missing: task=%+v error=%v", current, err)
		}
	}
	sub := control.Tasks.Subscribe()
	defer sub.Close()
	for {
		current, err := control.Tasks.Get(ctx, "main", record.Info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Info.Phase == protocol.TaskComplete {
			break
		}
		if _, err = sub.Next(ctx); err != nil {
			t.Fatalf("remote task did not complete: %+v %v", current, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(worker.options.Workspace, "tasks", record.Info.ID, "artifact.txt"))
	if err != nil || string(data) != "remote-proof" {
		t.Fatalf("remote artifact: %s %v", data, err)
	}
	if _, err = os.Stat(filepath.Join(control.options.Workspace, "tasks", record.Info.ID, "artifact.txt")); !os.IsNotExist(err) {
		t.Fatal("remote execution fell back to Control")
	}
	if len(worker.Profiles.Snapshot().Profiles) != 0 {
		t.Fatal("Runner received provider configuration")
	}
	main, _ := control.Conversation(ctx, "main")
	awaitConversation(t, main, func(snapshot protocol.Snapshot) bool {
		for _, message := range snapshot.Messages {
			if strings.Contains(message.Text, "Remote result summarized") {
				return snapshot.Turn == nil
			}
		}
		return false
	})
	if workerCalls.Load() != 2 || summaries.Load() != 1 {
		t.Fatalf("model count worker=%d summary=%d", workerCalls.Load(), summaries.Load())
	}
	for _, execution := range control.options.Manager.Snapshot() {
		if execution.Role == "task" {
			t.Fatal("remote wait acquired Control task slot")
		}
	}
}
