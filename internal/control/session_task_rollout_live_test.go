package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/storage"
)

// A no-tools fixture adapter. Context is exclusively loaded/saved through the
// production Task Journal; no shadow history can hide a failed persistence step.
type taskRolloutLiveFeedback struct {
	journal *tasks.Journal
	launch  tasks.Launch
}

func (f taskRolloutLiveFeedback) current(ctx context.Context) (*tasks.Run, error) {
	run, err := f.journal.Inspect(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.Phase != tasks.Running || run.BootID != f.journal.BootID || run.Launch.Generation != f.launch.Generation {
		return nil, fmt.Errorf("live task run is fenced")
	}
	return run, nil
}
func (f taskRolloutLiveFeedback) LoadContext(ctx context.Context) (json.RawMessage, error) {
	run, err := f.current(ctx)
	if err != nil {
		return nil, err
	}
	return run.Context, nil
}
func (f taskRolloutLiveFeedback) SaveContext(ctx context.Context, raw json.RawMessage) error {
	return f.journal.SaveContext(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID, raw)
}
func (f taskRolloutLiveFeedback) RecordTurn(ctx context.Context, id string) error {
	if id != f.launch.RunID {
		return fmt.Errorf("live task turn mismatch")
	}
	_, err := f.current(ctx)
	return err
}
func (f taskRolloutLiveFeedback) InputStreaming(ctx context.Context, _ string) error {
	_, err := f.current(ctx)
	return err
}
func (f taskRolloutLiveFeedback) Ready(ctx context.Context) error {
	_, err := f.current(ctx)
	return err
}
func (f taskRolloutLiveFeedback) Steering(ctx context.Context, _ bool) ([]agent.SteeringInput, error) {
	return nil, f.Ready(ctx)
}
func (f taskRolloutLiveFeedback) AcknowledgeSteering(ctx context.Context, ids []string) error {
	if len(ids) > 0 {
		return fmt.Errorf("unexpected steering")
	}
	return f.Ready(ctx)
}
func (f taskRolloutLiveFeedback) Observe(ctx context.Context, _ agent.Observation) error {
	return f.Ready(ctx)
}
func (f taskRolloutLiveFeedback) Settle(ctx context.Context) error { return f.Ready(ctx) }

// Paid opt-in qualification: actual Agent + Control ModelSession + Task Journal.
// This intentionally excludes task admission/workspace/tool dispatch; those are
// covered elsewhere. Prior history is synthetic; the compact summary and both
// measured recall replies come from the configured real model.
func TestConfiguredModelTaskSessionRolloutCompactResumeLive(t *testing.T) {
	if os.Getenv("POT_SESSION_ROLLOUT_LIVE") != "1" {
		t.Skip("requires POT_SESSION_ROLLOUT_LIVE=1 and configured POT_LIVE model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	profile, provider, credential := notificationLiveModel(t, ctx)
	manager, err := execution.New("task-rollout-live", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "task-rollout-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), WorkspaceHome: t.TempDir(), Manager: manager, Keys: keys})
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
	none := "none"
	saved, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "task-rollout-live", Name: "Task rollout qualification", Model: profile.Model, Endpoint: provider.Endpoint, Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: 768, ReasoningEffort: &none})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{}
	threshold := uint64(4096)
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.AllowedTools = &allowed
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = true
	spec.Params.Agent.CompactAt = &threshold
	spec.Params.Agent.UseProviderUsage = false
	spec.Params.Agent.RequestTimeoutMS = 60000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(t.TempDir(), "journal")
	if err = os.MkdirAll(taskRoot, 0700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(taskRoot, "tasks.sqlite")
	db, err := storage.OpenDatabase(dbPath, storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	journal, err := tasks.OpenJournal(db, filepath.Join(taskRoot, "resources"), "task-rollout-live", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := tasks.Launch{ControlID: "task-rollout-live", AssistantID: "owner", RuntimeID: "task-rollout-live", TaskID: "qualification", RunID: "first-run", Generation: 1, ExecutionTarget: core.RuntimeTarget("task-rollout-live"), Config: spec, Plugins: []string{}, Text: "Recall the durable token"}
	if _, err = journal.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if claimed, err := journal.Claim(ctx, launch.ControlID, launch.TaskID, launch.RunID); err != nil || !claimed {
		t.Fatal("claim", claimed, err)
	}
	marker := "TASK-ROLL-" + storage.ID()
	background := "The exact durable recovery token is " + marker + ". This is the only unique fact and must be preserved literally in every summary.\n" + strings.Repeat("Expendable background: ordinary redundant reference material with no action or unique facts.\n", 700)
	seed := agent.Checkpoint{Type: "context", Version: 2, Messages: []m.Message{m.TextMessage("user", background), m.TextMessage("assistant", "Acknowledged. I will preserve the durable recovery token."), m.TextMessage("user", "Keep that durable token for the next question. All background may be summarized."), m.TextMessage("assistant", "Ready for your next question.")}, Pending: []m.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.SaveContext(ctx, launch.ControlID, launch.TaskID, launch.RunID, raw); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	compactions := 0
	execute := func(l tasks.Launch) string {
		t.Helper()
		feedback := taskRolloutLiveFeedback{journal: journal, launch: l}
		model := &ModelSession{Directory: s.Profiles.directory, Registry: s.Providers, Usage: s.Usage, Spec: spec, Scope: l.TaskID, Turn: l.RunID, Generation: l.Generation, Role: "task", Claim: func(ctx context.Context, _ string) error { return feedback.Ready(ctx) }}
		ports := &executionPorts{model: model, compaction: func(ctx context.Context, r json.RawMessage) error {
			return s.recordCompaction(ctx, l.TaskID, l.RunID, r)
		}}
		engine := agent.Agent{Services: rolloutLiveServices{ports, &requests}, Feedback: feedback, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
		prompt := "What is the exact durable recovery token from our earlier conversation? Reply only the token."
		if strings.Contains(prompt, marker) {
			t.Fatal("marker leaked into recall prompt")
		}
		var reply strings.Builder
		err := engine.Execute(ctx, core.ExecutionContext{Owner: l.TaskID, Role: core.Worker, RolePrompt: assistant.PromptForRole(core.Worker), Workspace: filepath.Join(root, "workspace")}, core.Thread{Owner: l.TaskID, Generation: l.Generation, Config: spec}, core.TurnRequest{RequestID: l.RunID, Text: prompt}, false, func(event core.AgentEvent) error {
			if event.Update.Type == "tool_started" {
				return fmt.Errorf("unexpected tool in no-tools live fixture")
			}
			if event.Update.Type == "text_delta" {
				reply.WriteString(event.Update.Text)
			}
			if event.Update.Type == "compaction" {
				var report core.CompactionReport
				if err := json.Unmarshal(event.Update.Result, &report); err != nil {
					return err
				}
				if report.Action == "complete" {
					compactions++
				}
			}
			return journal.AppendEvent(ctx, l.ControlID, l.TaskID, l.RunID, event)
		})
		if err != nil {
			t.Fatal("real task Agent failed", err)
		}
		if !strings.Contains(reply.String(), marker) {
			t.Fatal("real task Agent lost recovery token")
		}
		if err = journal.Finish(ctx, l.ControlID, l.TaskID, l.RunID, tasks.Terminal{Outcome: core.Success, Text: reply.String()}); err != nil {
			t.Fatal(err)
		}
		return reply.String()
	}
	execute(launch)
	if compactions == 0 || requests.Load() < 2 {
		t.Fatal("no real task compaction observed", compactions, requests.Load())
	}
	before, err := journal.Inspect(ctx, launch.ControlID, launch.TaskID, launch.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err = json.Unmarshal(before.Context, &checkpoint); err != nil {
		t.Fatal(err)
	}
	summary := false
	for _, message := range checkpoint.Messages {
		if message.Kind.Type == "summary" && strings.Contains(message.Text, marker) {
			summary = true
		}
	}
	if !summary {
		t.Fatal("committed task rollout lacks marker-bearing compact summary")
	}
	files, err := filepath.Glob(filepath.Join(taskRoot, "sessions", "tasks", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if before.RolloutRef == nil || int64(len(lines)) != before.RolloutRef.Sequence {
		t.Fatal("task committed reference differs from JSONL")
	}
	deltas := 0
	for _, line := range lines {
		var entry map[string]json.RawMessage
		if err = json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry["delta"] != nil {
			deltas++
		}
	}
	if deltas == 0 {
		t.Fatal("task rollout has no incremental records")
	}
	if err = journal.CompressColdRollouts(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.Stat(files[0] + ".zst")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatal("compression left plain journal", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.OpenDatabase(dbPath, storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	journal, err = tasks.OpenJournal(db, filepath.Join(taskRoot, "resources"), "task-rollout-live", "nextboot")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := journal.Inspect(ctx, launch.ControlID, launch.TaskID, launch.RunID)
	if err != nil || !bytes.Equal(before.Context, restored.Context) {
		t.Fatal("zstd/reopen changed task checkpoint", err)
	}
	next := launch
	next.RunID = "continued-run"
	next.Text = "Recall the earlier recovery token without repeating it in this instruction"
	inherited, err := journal.Accept(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Context, inherited.Context) {
		t.Fatal("continuation did not inherit committed task checkpoint")
	}
	if claimed, err := journal.Claim(ctx, next.ControlID, next.TaskID, next.RunID); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	execute(next)
	original, err := journal.Inspect(ctx, launch.ControlID, launch.TaskID, launch.RunID)
	if err != nil || !bytes.Equal(before.Context, original.Context) {
		t.Fatal("continuation modified the earlier run checkpoint", err)
	}
	continued, err := journal.Inspect(ctx, next.ControlID, next.TaskID, next.RunID)
	if err != nil || continued.RolloutRef == nil || continued.RolloutRef.Sequence <= before.RolloutRef.Sequence {
		t.Fatal("continuation did not advance the shared task session", err)
	}
	filesAfter, err := filepath.Glob(filepath.Join(taskRoot, "sessions", "tasks", "*.jsonl"))
	if err != nil || len(filesAfter) != 1 || filesAfter[0] != files[0] {
		t.Fatal("continuation created a different session", filesAfter, err)
	}
	appended, err := os.ReadFile(files[0])
	if err != nil || !bytes.HasPrefix(appended, data) {
		t.Fatal("continuation rewrote earlier committed session records", err)
	}
	if err = journal.CompressColdRollouts(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []*tasks.Run{before, continued} {
		got, err := journal.Inspect(ctx, expected.Launch.ControlID, expected.Launch.TaskID, expected.Launch.RunID)
		if err != nil || got == nil || !bytes.Equal(got.Context, expected.Context) {
			t.Fatal("shared archive lost a run checkpoint", err)
		}
	}
	t.Logf("model=%s requests=%d successful_compactions=%d records_before_resume=%d records_after_resume=%d deltas=%d jsonl_bytes=%d zstd_bytes=%d task_recall_after_compact=true task_recall_after_reopen=true shared_task_session=true", profile.Model, requests.Load(), compactions, before.RolloutRef.Sequence, continued.RolloutRef.Sequence, deltas, len(data), compressed.Size())
}
