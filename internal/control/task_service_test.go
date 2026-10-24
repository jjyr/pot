package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
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

func TestLocalTaskUsesSharedCapacityOwnerModelAndSummary(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var summaryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer task-key" {
			t.Error("provider credential did not stay at Control")
		}
		raw, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		isSummary := strings.Contains(string(fields["messages"]), "Process background updates")
		var event any
		if isSummary {
			summaryCalls.Add(1)
			if !strings.Contains(string(fields["tools"]), "exec_command") || strings.Contains(string(fields["tools"]), "update_agent_profile") {
				t.Error("result handling lost assistant tools")
			}
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Task result summarized"}, "finish_reason": "stop"}}}
		} else if calls.Add(1) == 1 {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "task-command", "type": "function", "function": map[string]any{"name": "exec_command", "arguments": `{"cmd":"printf proof > artifact.txt; printf worker-ok","login":false}`}}}}, "finish_reason": "tool_calls"}}}
		} else {
			event = map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Completed task [artifact](artifact.txt)"}, "finish_reason": "stop"}}}
		}
		event.(map[string]any)["usage"] = map[string]any{"prompt_tokens": 20, "completion_tokens": 5}
		data, _ := json.Marshal(event)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: " + string(data) + "\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	ctx := t.Context()
	root := t.TempDir()
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: root, RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	legacyMainAssistant(ctx, s, "create", "Pot")
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: server.URL, Protocol: "openai_chat", APIKey: "task-key", MaxOutputTokens: 100})
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
	if err = s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "task-create", Text: "Write one artifact"})
	if err != nil {
		t.Fatal(err)
	}
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	// Bound execution waiting independently of setup and later attribution reads.
	// Summary completion has its own wait and must not expire usage queries.
	taskCtx, cancelTask := context.WithTimeout(ctx, testutil.Timeout(t, 10*time.Second))
	defer cancelTask()
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
		if _, err = sub.Next(taskCtx); err != nil {
			t.Fatalf("Task did not finish: %+v: %v", r, err)
		}
	}
	taskRoot, err := s.taskWorkspacePath(ctx, accepted.Info.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(taskRoot, "artifact.txt"))
	if err != nil || string(data) != "proof" {
		t.Fatalf("fixed worker artifact: %s %v", data, err)
	}
	// Completed worker messages capture their own original run and workspace.
	events, err := s.TaskEvents(ctx, "main", accepted.Info.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	message := ""
	for _, event := range events.Events {
		if event.Event.Update.Type == "message_started" {
			message = "message-" + event.Event.Update.ID
		}
	}
	var ref resources.MessageFile
	for {
		rows, e := s.MessageFiles(ctx, "main", accepted.Info.ID, message)
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) > 0 && rows[0].State != "pending" && rows[0].State != "resolving" {
			ref = rows[0]
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker file timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if ref.State != "ready" || ref.Source.Task != accepted.Info.ID || ref.Source.Root != taskRoot {
		t.Fatalf("worker file source %+v", ref)
	}
	// Main may reference the owned Task; a worker cannot select another Task.
	mainRoot, _ := s.ExecutionWorkspace("main")
	s.bindMessageFiles("main", "", "", "owned-file", "[file](pot-file://"+accepted.Info.ID+"/artifact.txt)", mainRoot)
	owned := awaitFiles(t, s, "main", "owned-file")
	if owned[0].State != "ready" || owned[0].Source.Run != ref.Source.Run {
		t.Fatal(owned)
	}
	s.bindMessageFiles("main", accepted.Info.ID, ref.Source.Run, "foreign-file", "[file](pot-file://another-task/artifact.txt)", taskRoot)
	for {
		rows, e := s.MessageFiles(ctx, "main", accepted.Info.ID, "foreign-file")
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) > 0 && rows[0].State == "forbidden" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("foreign worker reference not rejected")
		case <-time.After(10 * time.Millisecond):
		}
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
	workerUsage, err := s.Usage.Summary(ctx, usage.Query{Scope: accepted.Info.ID, Days: 14, Timezone: "UTC"}, time.Now().UnixMilli())
	if err != nil || workerUsage.Total.Requests != 2 || workerUsage.Total.Input != 40 || workerUsage.Total.Output != 10 {
		t.Fatal("Task usage attribution", workerUsage, err)
	}
	workerTools, err := s.Usage.Page(ctx, "tools", usage.Query{Scope: accepted.Info.ID}, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	tools := workerTools.Items.([]usage.ToolTotal)
	if len(tools) != 1 || tools[0].Name != "exec_command" || tools[0].Calls != 1 {
		t.Fatal("Task tool attribution", workerTools)
	}
	mainUsage, err := s.Usage.Summary(ctx, usage.Query{Scope: "main", Days: 14, Timezone: "UTC"}, time.Now().UnixMilli())
	if err != nil || mainUsage.Total.Requests != 1 {
		t.Fatal("Task usage contaminated Main", mainUsage, err)
	}
	mainTools, err := s.Usage.Page(ctx, "tools", usage.Query{Scope: "main"}, time.Now().UnixMilli())
	if err != nil || len(mainTools.Items.([]usage.ToolTotal)) != 0 {
		t.Fatal("Task tools contaminated Main", mainTools, err)
	}
	notices, _ := s.Tasks.Notifications(ctx, "main")
	if len(notices) != 0 {
		t.Fatal("acknowledged result still pending")
	}
	again, err := s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "task-create", Text: "Write one artifact"})
	if err != nil || again.Info.ID != accepted.Info.ID {
		t.Fatal("duplicate task admission changed identity")
	}
}
