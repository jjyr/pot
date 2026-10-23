package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

type recoveryLiveServices struct {
	core.ExecutionServices
	requests *atomic.Int32
	fail     bool
}

func (s recoveryLiveServices) Model(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	var limits struct {
		MaxOutput     uint64 `json:"max_output_tokens"`
		MaxTokens     uint64 `json:"max_tokens"`
		MaxCompletion uint64 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(request.Body, &limits); err != nil {
		return nil, err
	}
	if limits.MaxOutput > 2048 || limits.MaxTokens > 2048 || limits.MaxCompletion > 2048 {
		return nil, fmt.Errorf("live request exceeds the 2048-token output cap")
	}
	if s.fail {
		return nil, fmt.Errorf("temporary model transport failure before execution; no commands or external effects started; retry is safe")
	}
	if s.requests.Add(1) > 32 {
		return nil, fmt.Errorf("configured-model recovery qualification reached its 32-request cap")
	}
	return s.ExecutionServices.Model(ctx, request)
}

// Main, successful Workers and result handling use the configured real model.
// Failure injection happens before a Worker model request, with no tool effects.
func TestConfiguredModelTaskRecoveryLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	profile, provider, credential := notificationLiveModel(t, ctx)
	reasoning := "default"
	if profile.Params.ReasoningEffort != nil {
		reasoning = *profile.Params.ReasoningEffort
	}
	t.Logf("configured model=%s protocol=%s reasoning=%s", profile.Model, provider.Protocol, reasoning)
	var requests atomic.Int32
	cases := []struct {
		name, input     string
		count, failures int
		baseline        bool
	}{
		{"single", "请创建一个后台任务：只返回 READY_SINGLE，不需要查资料或执行命令。", 1, 0, false},
		{"multiple", "请分别创建两个独立后台任务：任务 A 只返回 READY_ALPHA；任务 B 只返回 READY_BETA。它们都不需要查资料或执行命令。", 2, 0, false},
		{"recover", "请创建一个后台任务：只返回 READY_RECOVER，不需要查资料或执行命令。", 1, 1, false},
		{"repeated-recovery", "请创建一个后台任务：只返回 READY_RECOVER，不需要查资料或执行命令。", 1, 2, false},
	}
	if os.Getenv("POT_BASELINE_MAIN_PROMPT_FILE") != "" {
		cases = append(cases, struct {
			name, input     string
			count, failures int
			baseline        bool
		}{"baseline", cases[0].input, 1, 0, true})
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			manager, err := execution.New("recovery-live", "boot", execution.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			keys, err := secrets.NewMemoryKey(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "recovery-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), AgentHome: filepath.Join(root, "agent"), Manager: manager, Keys: keys})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				s.Close()
				manager.Stop()
				cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				if err := manager.Drain(cleanup); err != nil {
					t.Error(err)
				}
			}()
			if _, err = legacyMainAssistant(ctx, s, "create", "Live recovery"); err != nil {
				t.Fatal(err)
			}
			modelSettings := core.ModelSettings{}
			if profile.ModelSettings != nil {
				modelSettings = *profile.ModelSettings
			}
			outputLimit := uint64(2048)
			modelSettings.MaxOutputTokens = &outputLimit
			saved, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "configured", Name: "Configured live qualification", Model: profile.Model, Endpoint: provider.Endpoint, Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: 2048, ReasoningEffort: profile.Params.ReasoningEffort, ModelSettings: &modelSettings})
			if err != nil {
				t.Fatal(err)
			}
			spec, err := s.Profiles.Resolve(saved.ID)
			if err != nil {
				t.Fatal(err)
			}
			// These harmless tasks require only coordination and text; no execution,
			// external messaging or production resource access is exposed.
			allowed := []string{"create_task", "list_tasks", "get_task", "get_model_catalog", "retry_task"}
			spec.Params.MaxOutputTokens = 2048
			spec.Params.Agent.AllowedTools = &allowed
			spec.Params.Agent.BuiltinTools = []string{}
			spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
			spec.Params.Agent.AutoCompact = false
			spec.Params.Agent.RequestTimeoutMS = 90000
			if err = spec.Rehash(); err != nil {
				t.Fatal(err)
			}
			gate := make(chan struct{})
			var attempts atomic.Int32
			prepare := s.TaskRuns.Prepare
			s.TaskRuns.Prepare = func(workerCtx context.Context, run tasks.Run) (tasks.Prepared, error) {
				select {
				case <-gate:
				case <-workerCtx.Done():
					return tasks.Prepared{}, workerCtx.Err()
				}
				prepared, err := prepare(workerCtx, run)
				if err == nil {
					prepared.Agent.Services = recoveryLiveServices{prepared.Agent.Services.(core.ExecutionServices), &requests, int(attempts.Add(1)) <= scenario.failures}
				}
				return prepared, err
			}
			var c *assistant.Conversation
			c, err = assistant.OpenConversation(filepath.Join(root, "live.sqlite"), assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "recovery-live", BootID: "boot", Manager: manager, InitialSpec: &spec, MaxRequests: 16, MaxTurnRequests: 8, OnFinished: func(ctx context.Context, r protocol.SendMessage, result execution.Result) error {
				return s.finishAgentDelivery(ctx, "main", r, result)
			}, Prepare: func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
				options, err := s.prepare(ctx, c, thread, request)
				if err == nil {
					options.Services = recoveryLiveServices{options.Services, &requests, false}
					if scenario.baseline {
						data, err := os.ReadFile(os.Getenv("POT_BASELINE_MAIN_PROMPT_FILE"))
						if err != nil {
							return options, err
						}
						options.Execution.MainPrompt = string(data)
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
			var dispatched protocol.Snapshot
			defer func() {
				if output := os.Getenv("POT_LIVE_OUTPUT"); output != "" {
					finalCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
					defer done()
					final, err := c.Snapshot(finalCtx)
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Error(err)
						return
					}
					data, _ := json.MarshalIndent(map[string]any{"model": profile.Model, "scenario": scenario.name, "dispatch": dispatched, "final": final, "attempts": attempts.Load(), "cumulative_real_requests": requests.Load()}, "", "  ")
					if err := os.WriteFile(filepath.Join(output, scenario.name+".json"), data, 0600); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "arrange", ConversationID: "main", Text: scenario.input}); err != nil {
				t.Fatal(err)
			}
			wait := func(predicate func(protocol.Snapshot) bool) protocol.Snapshot {
				t.Helper()
				updates, stop, err := c.Subscribe(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer stop()
				for {
					select {
					case <-ctx.Done():
						t.Fatal("live qualification timed out")
					case snapshot := <-updates:
						if snapshot.Agent.Detail != nil {
							t.Fatal("live model failed:", *snapshot.Agent.Detail)
						}
						if predicate(snapshot) {
							return snapshot
						}
					}
				}
			}
			dispatched = wait(func(snapshot protocol.Snapshot) bool { return snapshot.Turn == nil })
			// This snapshot precedes all Worker results, so any prose is dispatch
			// chatter rather than a useful result notification.
			var prose []string
			for _, message := range dispatched.Messages {
				if message.Role == protocol.Assistant && strings.TrimSpace(message.Text) != "" {
					prose = append(prose, message.Text)
				}
			}
			var taskCount int
			if err = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks").Scan(&taskCount); err != nil {
				t.Fatal(err)
			}
			t.Logf("dispatch tasks=%d prose=%q", taskCount, prose)
			close(gate)
			if taskCount != scenario.count {
				t.Fatalf("created %d tasks, expected %d", taskCount, scenario.count)
			}
			if scenario.baseline {
				if len(prose) == 0 {
					t.Fatal("baseline unexpectedly omitted dispatch confirmation")
				}
			} else if len(prose) > 0 {
				t.Error("unnecessary dispatch reply:", prose)
			}
			final := wait(func(snapshot protocol.Snapshot) bool {
				if snapshot.Turn != nil {
					return false
				}
				var pending int
				if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE recipient='main' AND json_extract(envelope,'$.kind')='task_result' AND state IN ('queued','processing')").Scan(&pending); err != nil {
					t.Fatal(err)
				}
				if pending != 0 {
					return false
				}
				var active int
				if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE phase IN ('queued','running','waiting','waiting_approval')").Scan(&active); err != nil {
					t.Fatal(err)
				}
				return active == 0
			})
			var retries int
			if err = s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM task_retries").Scan(&retries); err != nil {
				t.Fatal(err)
			}
			if scenario.failures > 0 && (retries != scenario.failures || int(attempts.Load()) != scenario.failures+1) {
				t.Fatalf("recovery attempts=%d replacements=%d", attempts.Load(), retries)
			}
			if scenario.failures == 0 && (retries != 0 || int(attempts.Load()) != scenario.count) {
				t.Fatalf("successful dispatch unexpectedly needed recovery: attempts=%d replacements=%d", attempts.Load(), retries)
			}
			var completed, failed int
			if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE phase='complete'").Scan(&completed); err != nil {
				t.Fatal(err)
			}
			if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE phase='failed'").Scan(&failed); err != nil {
				t.Fatal(err)
			}
			wantComplete, wantFailed := scenario.count, scenario.failures
			if completed != wantComplete || failed != wantFailed {
				t.Fatalf("unexpected terminal outcomes: complete=%d failed=%d", completed, failed)
			}
			var finalProse []string
			for _, message := range final.Messages[len(dispatched.Messages):] {
				if message.Role == protocol.Assistant && strings.TrimSpace(message.Text) != "" {
					finalProse = append(finalProse, message.Text)
				}
			}

			for _, reply := range finalProse {
				if strings.Contains(reply, "<pot_message_metadata>") {
					t.Error("outcome leaked internal message metadata", reply)
				}
			}
			if scenario.failures > 0 && len(finalProse) != 1 {
				t.Fatalf("recovery should produce one substantive report, got %q", finalProse)
			}
			if len(finalProse) == 0 {
				t.Fatal("missing final outcome report")
			}
			t.Logf("worker attempts=%d replacements=%d final reports=%q cumulative real requests=%d", attempts.Load(), retries, finalProse, requests.Load())
		})
	}
}
