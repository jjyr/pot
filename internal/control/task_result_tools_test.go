package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTaskResultExecutesCommandAndImportsTaskAvatar(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rounds atomic.Int32
	var s *Service
	s, _ = batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []struct{ Content string }
			Tools    []struct{ Function struct{ Name string } }
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		if !strings.Contains(body.Messages[0].Content, "Process background updates") {
			batchReply(w, "Created avatar.png")
			return
		}
		names := map[string]bool{}
		for _, tool := range body.Tools {
			names[tool.Function.Name] = true
		}
		for _, name := range []string{"exec_command", "exec", "wait", "create_task", "send_message", "manage_schedule"} {
			if !names[name] {
				t.Error("notification lost configured assistant tool", name)
			}
		}
		toolReply := func(name string, args any) {
			input, _ := json.Marshal(args)
			event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("%s-%d", name, rounds.Load()), "type": "function", "function": map[string]any{"name": name, "arguments": string(input)}}}}, "finish_reason": "tool_calls"}}})
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: " + string(event) + "\n\ndata: [DONE]\n\n"))
		}
		switch rounds.Add(1) {
		case 1:
			toolReply("exec_command", map[string]any{"cmd": "printf RESULT_FOLLOWUP > followup.txt", "login": false})
		case 2:
			// The authenticated completion payload supplies the artifact's task ID.
			var taskID string
			for _, message := range body.Messages {
				start := strings.LastIndex(message.Content, "\n[")
				if start < 0 {
					continue
				}
				var payload []struct {
					TaskID string `json:"artifact_task_id"`
				}
				if json.Unmarshal([]byte(message.Content[start+1:]), &payload) == nil && len(payload) == 1 {
					taskID = payload[0].TaskID
				}
			}
			if taskID == "" {
				t.Error("missing task artifact identity")
				batchReply(w, "Missing artifact")
				return
			}
			workspace, err := s.taskWorkspacePath(ctx, taskID)
			if err != nil {
				t.Error(err)
				return
			}
			profile := filepath.Join(s.Registry.Directory("main"), "profile.json")
			avatar := filepath.Join(s.Registry.Directory("main"), "avatar.png")
			toolReply("exec_command", map[string]any{"cmd": fmt.Sprintf("cp %s %s && printf '%s\\n' > %s", quoteFixturePath(filepath.Join(workspace, "avatar.png")), quoteFixturePath(avatar), `{"schema_version":1,"name":"Keyboard monkey"}`, quoteFixturePath(profile)), "login": false})
		default:
			batchReply(w, "Avatar saved")
		}
	}, 2)
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	encoded, _ := assistantavatar.Render("cup", "fixture")
	source, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "data:image/png;base64,"))
	prepare := s.TaskRuns.Prepare
	s.TaskRuns.Prepare = func(ctx context.Context, run tasks.Run) (tasks.Prepared, error) {
		prepared, err := prepare(ctx, run)
		if err == nil {
			err = os.WriteFile(filepath.Join(prepared.Context.Workspace, "avatar.png"), source, 0600)
		}
		return prepared, err
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "avatar", Text: "Generate avatar\nCreate avatar.png for my profile."}); err != nil {
		t.Fatal(err)
	}
	final := awaitConversationContext(t, ctx, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) > 0 && snapshot.Messages[len(snapshot.Messages)-1].Text == "Avatar saved"
	})
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want, err := assistant.NormalizeAvatar(source)
	if err != nil || definitions[0].Avatar != want || definitions[0].Name != "Keyboard monkey" {
		t.Fatal("profile did not import the task artifact", err)
	}
	workspace, _ := s.ExecutionWorkspace("main")
	marker, err := os.ReadFile(filepath.Join(workspace, "followup.txt"))
	if err != nil || string(marker) != "RESULT_FOLLOWUP" {
		t.Fatal("notification command did not run", err)
	}
	for _, message := range final.Messages {
		if message.Tool != nil && message.Tool.Phase != "completed" {
			t.Fatal("follow-up tool failed", message.Tool.Name, message.Tool.Phase)
		}
	}
}

func quoteFixturePath(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
