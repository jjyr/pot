package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

type gatedLifecycleLiveServices struct {
	recoveryLiveServices
	entered, release chan struct{}
	once             *sync.Once
}

func (s gatedLifecycleLiveServices) Model(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.recoveryLiveServices.Model(ctx, request)
}

// Uses production prompts without test-specific overrides; model archive
// choices and original-workspace continuation are both qualified.
func TestConfiguredModelTaskLifecycleLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	profile, provider, credential := notificationLiveModel(t, ctx)
	t.Logf("configured model=%s protocol=%s", profile.Model, provider.Protocol)
	manager, err := execution.New("lifecycle-live", "boot", execution.Limits{MaxActive: 4, MaxQueued: 16})
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "lifecycle-live", BootID: "boot", WorkspaceHome: t.TempDir(), Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.Close()
		manager.Stop()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := manager.Drain(drain); err != nil {
			t.Error(err)
		}
	}()
	if _, err = s.Registry.Create(ctx, "create", "Lifecycle qualification"); err != nil {
		t.Fatal(err)
	}
	settings := core.ModelSettings{}
	if profile.ModelSettings != nil {
		settings = *profile.ModelSettings
	}
	output := uint64(2048)
	settings.MaxOutputTokens = &output
	saved, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "configured", Name: "Lifecycle qualification", Model: profile.Model, Endpoint: provider.Endpoint, Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: output, ReasoningEffort: profile.Params.ReasoningEffort, ModelSettings: &settings})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"exec_command", "apply_patch", "write_stdin", "send_message", "list_tasks", "get_task", "set_task_dispositions"}
	spec.Params.MaxOutputTokens = output
	spec.Params.Agent.AllowedTools = &allowed
	spec.Params.Agent.BuiltinTools = []string{"exec_command", "apply_patch", "write_stdin"}
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.RequestTimeoutMS = 90000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	if err = s.Permissions.Configure("lifecycle-live", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	originalPrepare := s.Runner.Prepare
	s.Runner.Prepare = func(ctx context.Context, run runner.Run) (runner.Prepared, error) {
		prepared, err := originalPrepare(ctx, run)
		if err == nil {
			prepared.Agent.Services = recoveryLiveServices{prepared.Agent.Services.(core.ExecutionServices), &requests, false}
		}
		return prepared, err
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var c *assistant.Conversation
	c, err = assistant.OpenConversation(filepath.Join(root, "live.sqlite"), assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "lifecycle-live", BootID: "boot", Manager: manager, InitialSpec: &spec, MaxRequests: 24, MaxTurnRequests: 8, OnFinished: func(ctx context.Context, r protocol.SendMessage, result execution.Result) error {
		return s.finishAgentDelivery(ctx, "main", r, result)
	}, Prepare: func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
		options, err := s.prepare(ctx, c, thread, request)
		if err == nil {
			limited := recoveryLiveServices{options.Services, &requests, false}
			options.Services = limited
			if request.RequestID == "gate" {
				options.Services = gatedLifecycleLiveServices{limited, entered, release, &once}
			}
		}
		return options, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.conversations["main"] = c
	s.mu.Unlock()
	s.watchTaskOwner("main", c)
	defer func() {
		if output := os.Getenv("POT_LIVE_OUTPUT"); output != "" {
			finalCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			snapshot, _ := c.Snapshot(finalCtx)
			page, _ := s.Tasks.Page(finalCtx, "main", protocol.TaskQuery{Scope: "all", Limit: 50})
			raw, _ := json.MarshalIndent(map[string]any{"model": profile.Model, "requests": requests.Load(), "snapshot": snapshot, "tasks": page}, "", "  ")
			if err := os.MkdirAll(output, 0700); err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(filepath.Join(output, "task-lifecycle.json"), raw, 0600); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "gate", ConversationID: "main", Text: "请只回复 READY。"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	fixtureGit(t, t.TempDir(), "init", "--bare", "--initial-branch=trunk", remote)
	project := filepath.Join(t.TempDir(), "project")
	fixtureGit(t, t.TempDir(), "clone", remote, project)
	writeTaskFixture(t, filepath.Join(project, "base.txt"), "base")
	fixtureGit(t, project, "add", ".")
	fixtureGit(t, project, "commit", "-m", "base")
	fixtureGit(t, project, "push", "origin", "trunk")
	a, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "a", Text: "本地文本交付\n在当前目录创建 result.txt，内容为 INITIAL。只需本地文件，不要提交或推送；文件写好就完成了这次要求。另外记住标记 CONTEXT_42，暂时不要把这个标记写入文件。", Workspace: protocol.TaskWorkspace{Kind: "worktree", ProjectDir: project}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: "b", Text: "待用户补充\n请让我选择红色或蓝色，在得到我的选择之前不要替我决定。"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.Info.ID, b.Info.ID} {
		awaitLifecycleTask(t, ctx, s, id, func(r *tasks.Record) bool { return r.Info.Phase.Scope() != "active" })
	}
	close(release)
	awaitLifecycleResults(t, ctx, s, c)
	first, _ := s.Tasks.Get(ctx, "main", a.Info.ID)
	second, _ := s.Tasks.Get(ctx, "main", b.Info.ID)
	t.Logf("production prompts: delivered archived=%v reason=%q; needs-user archived=%v reason=%q", first.Info.Archived, first.Info.ArchiveReason, second.Info.Archived, second.Info.ArchiveReason)
	if first.Info.Phase != protocol.TaskComplete || second.Info.Phase != protocol.TaskComplete {
		t.Fatal("worker failed", first.Error, second.Error)
	}
	if !first.Info.Archived {
		t.Error("configured assistant did not archive the delivered task")
	}
	if second.Info.Archived {
		t.Error("configured assistant archived a task needing user input")
	}
	if second.Info.ArchiveReason == "" {
		t.Error("configured assistant did not record a keep-open decision")
	}
	// Continue even when the model makes no archive choice, so its policy result
	// cannot hide a broken original-workspace/context continuation mechanism.
	if !first.Info.Archived {
		if _, err = s.Tasks.SetDispositions(ctx, "main", []protocol.TaskDisposition{{TaskID: a.Info.ID, RunID: first.RunID, Archived: true, Reason: "qualification of archived continuation"}}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.RunnerJournal.TaskWorkspace(ctx, a.Info.ID)
	if err != nil || before == nil {
		t.Fatal(before, err)
	}
	followup := fmt.Sprintf("继续“本地文本交付”这个已有任务（ID %s），将最初要求它记住的标记追加到原 worktree 的 result.txt，保留原来的内容。不要提交或推送，在同一个任务里完成，不要新建。", a.Info.ID)
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "followup-main", ConversationID: "main", Text: followup}); err != nil {
		t.Fatal(err)
	}
	awaitConversationContext(t, ctx, c, func(snapshot protocol.Snapshot) bool {
		if snapshot.Turn != nil {
			return false
		}
		for _, message := range snapshot.Messages {
			if message.Role == protocol.User && message.Text == followup && message.Status == protocol.Complete {
				return true
			}
		}
		return false
	})
	dispatched, _ := s.Tasks.Get(ctx, "main", a.Info.ID)
	if dispatched.RunID == a.RunID && dispatched.Info.Pending == 0 {
		t.Fatal("Main did not send follow-up to the existing Task")
	}
	next := awaitLifecycleTask(t, ctx, s, a.Info.ID, func(r *tasks.Record) bool { return r.RunID != a.RunID && r.Info.Phase.Scope() != "active" })
	if next.Info.Phase != protocol.TaskComplete {
		t.Fatal("continuation failed", next.Error)
	}
	after, err := s.RunnerJournal.TaskWorkspace(ctx, a.Info.ID)
	if err != nil || after.Path != before.Path || after.Branch != before.Branch {
		t.Fatal("continuation changed worktree", after, err)
	}
	contents, err := os.ReadFile(filepath.Join(after.Path, "result.txt"))
	if err != nil || !strings.Contains(string(contents), "INITIAL") || !strings.Contains(string(contents), "CONTEXT_42") {
		t.Fatal("continuation lost files or original context", string(contents), err)
	}
	awaitLifecycleResults(t, ctx, s, c)
	var taskCount int
	if err = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks").Scan(&taskCount); err != nil || taskCount != 2 {
		t.Fatal("Main created a replacement Task", taskCount, err)
	}
	t.Logf("continuation kept Task/worktree/context; real requests=%d", requests.Load())
}
