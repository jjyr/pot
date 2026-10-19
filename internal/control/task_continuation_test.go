package control

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func lifecycleToolReply(w http.ResponseWriter, name string, args any) {
	raw, _ := json.Marshal(args)
	event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "lifecycle-call", "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "finish_reason": "tool_calls"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
}

func awaitLifecycleTask(t *testing.T, ctx context.Context, s *Service, id string, predicate func(*tasks.Record) bool) *tasks.Record {
	t.Helper()
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	for {
		r, err := s.Tasks.Get(ctx, "main", id)
		if err != nil {
			t.Fatal(err)
		}
		if r != nil && predicate(r) {
			return r
		}
		if _, err = sub.Next(ctx); err != nil {
			t.Fatal("waiting for task", id, r, err)
		}
	}
}

func awaitLifecycleResults(t *testing.T, ctx context.Context, s *Service, catalogue *assistant.Conversation) protocol.Snapshot {
	t.Helper()
	snapshot := awaitConversationContext(t, ctx, catalogue, func(snapshot protocol.Snapshot) bool {
		if snapshot.Turn != nil {
			return false
		}
		var pending int
		err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE recipient='main' AND json_extract(envelope,'$.kind')='task_result' AND state IN ('queued','processing')").Scan(&pending)
		return err == nil && pending == 0
	})
	if snapshot.Agent.Detail != nil {
		t.Fatal("result handling failed", *snapshot.Agent.Detail)
	}
	for _, m := range snapshot.Messages {
		if m.Tool != nil && m.Tool.Name == "set_task_dispositions" && m.Status != protocol.Complete {
			t.Fatal("archive tool did not finish", m.Tool)
		}
	}
	var incomplete int
	if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE recipient='main' AND json_extract(envelope,'$.kind')='task_result' AND state!='processed'").Scan(&incomplete); err != nil || incomplete != 0 {
		t.Fatal("result batch was not acknowledged", incomplete, err)
	}
	return snapshot
}

func TestTaskArchiveBatchAndContinuedWorkspace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var batches, workers atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			return
		}
		last := body.Messages[len(body.Messages)-1]
		if strings.Contains(body.Messages[0].Content, "Process background updates") {
			if last.Role == "tool" {
				batchReply(w, "")
				return
			}
			var payload []struct {
				TaskID string `json:"task_id"`
				RunID  string `json:"run_id"`
			}
			start := strings.LastIndex(last.Content, "\n[")
			if start < 0 || json.Unmarshal([]byte(last.Content[start+1:]), &payload) != nil {
				t.Error("missing batch payload")
				return
			}
			batch := batches.Add(1)
			if batch == 1 && len(payload) != 2 {
				t.Error("outcomes were not batched", len(payload))
			}
			var decisions []protocol.TaskDisposition
			for _, item := range payload {
				decisions = append(decisions, protocol.TaskDisposition{TaskID: item.TaskID, RunID: item.RunID, Archived: true, Reason: "delivered"})
			}
			lifecycleToolReply(w, "set_task_dispositions", protocol.TaskDispositions{Decisions: decisions})
			return
		}
		if last.Content == "foreground gate" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			batchReply(w, "ready")
			return
		}
		workers.Add(1)
		if last.Role == "tool" {
			batchReply(w, "file delivered")
			return
		}
		if strings.Contains(last.Content, "FOLLOWUP") {
			if !strings.Contains(string(raw), "original nonce KEEP_42") || !strings.Contains(string(raw), "file delivered") {
				t.Error("continuation lost original model context")
			}
			lifecycleToolReply(w, "exec_command", map[string]any{"cmd": "printf -- '-followup' >> result.txt", "login": false})
			return
		}
		if strings.Contains(last.Content, "Task A") {
			lifecycleToolReply(w, "exec_command", map[string]any{"cmd": "printf initial > result.txt", "login": false})
			return
		}
		batchReply(w, "READY_B")
	}, 4)
	s.options.WorkspaceHome = t.TempDir()
	if err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Conversation(ctx, "main")
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "gate", ConversationID: "main", Text: "foreground gate"}); err != nil {
		t.Fatal(err)
	}
	<-entered
	remote := filepath.Join(t.TempDir(), "remote.git")
	fixtureGit(t, t.TempDir(), "init", "--bare", "--initial-branch=trunk", remote)
	project := filepath.Join(t.TempDir(), "project")
	fixtureGit(t, t.TempDir(), "clone", remote, project)
	writeTaskFixture(t, filepath.Join(project, "base.txt"), "base")
	fixtureGit(t, project, "add", ".")
	fixtureGit(t, project, "commit", "-m", "base")
	fixtureGit(t, project, "push", "origin", "trunk")
	a, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "a", Text: "Task A\nWrite result.txt; original nonce KEEP_42.", Workspace: protocol.TaskWorkspace{Kind: "worktree", ProjectDir: project}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "b", Text: "Task B\nReturn READY_B."})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.Info.ID, b.Info.ID} {
		awaitLifecycleTask(t, ctx, s, id, func(r *tasks.Record) bool { return r.Info.Phase == protocol.TaskComplete })
	}
	before, err := s.RunnerJournal.TaskWorkspace(ctx, a.Info.ID)
	if err != nil || before == nil {
		t.Fatal(before, err)
	}
	notices, err := s.Tasks.Notifications(ctx, "main")
	if err != nil || len(notices) != 2 {
		t.Fatal(notices, err)
	}
	if err = s.Mailbox.ClaimEventBatch(ctx, "main", []string{notices[0].ID}); err != nil {
		t.Fatal(err)
	}
	turn := "pot-result-" + notices[0].ID
	dispatch := s.productDispatch("main", turn, 1, false)["set_task_dispositions"]
	args, _ := json.Marshal(protocol.TaskDispositions{Decisions: []protocol.TaskDisposition{{TaskID: a.Info.ID, RunID: a.RunID, Archived: true, Reason: "delivered"}, {TaskID: b.Info.ID, RunID: b.RunID, Archived: true, Reason: "delivered"}}})
	if _, err = dispatch(ctx, core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "outside-batch", Name: "set_task_dispositions", Arguments: args}, func(core.Update) error { return nil }); err == nil {
		t.Fatal("archive tool accepted an undelivered task")
	}
	for _, id := range []string{a.Info.ID, b.Info.ID} {
		r, _ := s.Tasks.Get(ctx, "main", id)
		if r.Info.Archived {
			t.Fatal("rejected batch partially archived", id)
		}
	}
	if err = s.Mailbox.ReleaseEvent(ctx, notices[0].ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitLifecycleTask(t, ctx, s, a.Info.ID, func(r *tasks.Record) bool { return r.Info.Archived })
	awaitLifecycleTask(t, ctx, s, b.Info.ID, func(r *tasks.Record) bool { return r.Info.Archived })
	awaitLifecycleResults(t, ctx, s, c)
	// A dirty worktree is permitted: only the assistant decides whether to archive.
	if !strings.Contains(fixtureGit(t, before.Path, "status", "--porcelain"), "result.txt") {
		t.Fatal("fixture has no uncommitted changes")
	}
	if err = s.Tasks.SendMessage(ctx, "main", a.Info.ID, "followup", protocol.AgentRef{NodeID: s.Tasks.ControlID(), Kind: "user", AgentID: "main"}, "FOLLOWUP: append to the existing result; retain KEEP_42."); err != nil {
		t.Fatal(err)
	}
	next := awaitLifecycleTask(t, ctx, s, a.Info.ID, func(r *tasks.Record) bool { return r.RunID != a.RunID && r.Info.Phase == protocol.TaskComplete })
	after, err := s.RunnerJournal.TaskWorkspace(ctx, a.Info.ID)
	if err != nil || after.Path != before.Path || after.Branch != before.Branch || after.Commit != before.Commit {
		t.Fatal("workspace changed", before, after, err)
	}
	contents, err := os.ReadFile(filepath.Join(after.Path, "result.txt"))
	if err != nil || string(contents) != "initial-followup" {
		t.Fatal(string(contents), err)
	}
	events, err := s.TaskEvents(ctx, "main", a.Info.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	applied := false
	for i, event := range events.Events {
		if event.Sequence != uint64(i+1) {
			t.Fatal("transcript gap", event)
		}
		seen[event.Event.TurnID] = true
		if event.Event.Update.Type == "input_applied" && event.Event.Update.ID == "followup" {
			applied = true
		}
	}
	if !seen[a.RunID] || !seen[next.RunID] || !applied {
		t.Fatal("transcript lost a run or input", seen, applied)
	}
	old, err := s.Tasks.Run(ctx, "main", a.Info.ID, a.RunID)
	if err != nil || old.Info.Phase != protocol.TaskComplete || old.Outcome.RunID != a.RunID {
		t.Fatal(old, err)
	}
	var count int
	if err = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks").Scan(&count); err != nil || count != 2 {
		t.Fatal("continuation created another Task", count, err)
	}
	if batches.Load() < 1 || workers.Load() < 5 {
		t.Fatal("execution was incomplete", batches.Load(), workers.Load())
	}
	awaitLifecycleResults(t, ctx, s, c)
}
