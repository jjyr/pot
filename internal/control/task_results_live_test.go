package control

import (
	"context"
	"encoding/base64"
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
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type taskResultsLiveServices struct {
	core.ExecutionServices
	requests *atomic.Int32
}

func (s taskResultsLiveServices) Model(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	var limits struct {
		MaxOutput     uint64 `json:"max_output_tokens"`
		MaxTokens     uint64 `json:"max_tokens"`
		MaxCompletion uint64 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(request.Body, &limits); err != nil {
		return nil, err
	}
	if limits.MaxOutput > 2048 || limits.MaxTokens > 2048 || limits.MaxCompletion > 2048 {
		return nil, fmt.Errorf("live task result request exceeds 2048 output tokens")
	}
	if s.requests.Add(1) > 48 {
		return nil, fmt.Errorf("live task result qualification reached its 48-request cap")
	}
	return s.ExecutionServices.Model(ctx, request)
}

// The configured real model writes every deliverable itself. Production prompts,
// execution, collection, inbox delivery and owner resource tools remain in use.
func TestConfiguredModelTaskResultsLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	profile, provider, credential := notificationLiveModel(t, ctx)
	t.Logf("configured model=%s protocol=%s", profile.Model, provider.Protocol)
	manager, err := execution.New("results-live", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "results-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), AgentHome: filepath.Join(root, "agent"), WorkspaceHome: t.TempDir(), Manager: manager, Keys: keys})
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
	if _, err := legacyMainAssistant(ctx, s, "create", "Task result qualification"); err != nil {
		t.Fatal(err)
	}
	settings := core.ModelSettings{}
	if profile.ModelSettings != nil {
		settings = *profile.ModelSettings
	}
	output := uint64(2048)
	settings.MaxOutputTokens = &output
	saved, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "configured", Name: "Task result qualification", Model: profile.Model, Endpoint: provider.Endpoint, Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: output, ReasoningEffort: profile.Params.ReasoningEffort, ModelSettings: &settings})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"exec_command", "apply_patch", "write_stdin", "send_message", "get_task", "list_tasks", "set_task_dispositions"}
	spec.Params.MaxOutputTokens = output
	spec.Params.Agent.AllowedTools = &allowed
	spec.Params.Agent.BuiltinTools = []string{"exec_command", "apply_patch", "write_stdin"}
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.RequestTimeoutMS = 90000
	if err := spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	if err := s.Permissions.Configure("results-live", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	prepare := s.TaskRuns.Prepare
	s.TaskRuns.Prepare = func(ctx context.Context, run tasks.Run) (tasks.Prepared, error) {
		prepared, err := prepare(ctx, run)
		if err == nil {
			prepared.Agent.Services = taskResultsLiveServices{prepared.Agent.Services.(core.ExecutionServices), &requests}
		}
		return prepared, err
	}
	var c *assistant.Conversation
	c, err = assistant.OpenConversation(filepath.Join(root, "live.sqlite"), assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "results-live", BootID: "boot", Manager: manager, InitialSpec: &spec, MaxRequests: 48, MaxTurnRequests: 12, OnFinished: func(ctx context.Context, request protocol.SendMessage, result execution.Result) error {
		return s.finishAgentDelivery(ctx, "main", request, result)
	}, Prepare: func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
		options, err := s.prepare(ctx, c, thread, request)
		if err == nil {
			options.Services = taskResultsLiveServices{options.Services, &requests}
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
	observations := []map[string]any{}
	defer func() {
		if output := os.Getenv("POT_LIVE_OUTPUT"); output != "" {
			data, _ := json.MarshalIndent(map[string]any{"model": profile.Model, "protocol": provider.Protocol, "requests": requests.Load(), "observations": observations}, "", "  ")
			if err := os.MkdirAll(output, 0700); err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(filepath.Join(output, "task-results.json"), data, 0600); err != nil {
				t.Error(err)
			}
		}
	}()
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "setup", ConversationID: "main", Text: "这是隔离的任务结果验收。后续任务完成时，请向我报告最终回复里的 RESULT_ 标记；有附件时使用资源工具预览实际附件，有附件收集异常时如实说明但保留任务结论。任务途中发来的 PROGRESS_ 消息只是进度参考，无需回复任务或用户。不要创建或重试任务。现在只回复 READY。"}); err != nil {
		t.Fatal(err)
	}
	awaitConversationContext(t, ctx, c, func(snapshot protocol.Snapshot) bool { return snapshot.Turn == nil && len(snapshot.Messages) > 1 })
	cases := []struct {
		name, goal, marker                             string
		scheduled, attachment, artifactError, progress bool
	}{
		{"plain", "只回复 RESULT_PLAIN_219。这是纯文本任务，不需要文件或工具。", "RESULT_PLAIN_219", false, false, false, false},
		{"report", "创建一个作为交付附件的 report.md，正文包含 RESULT_REPORT_219 和 17+25=42。另在工作目录创建 .pot-schedule-result.json，内容为 {\"tool_failures_observed\":3,\"status\":\"success_with_observability_limits\"}，作为本次业务的原始记录，不必作为附件交付。最终回复包含 RESULT_REPORT_219 和实际文件情况。", "RESULT_REPORT_219", true, true, false, false},
		{"attachment-error", "创建交付附件 report.md，正文包含 RESULT_ATTACHMENT_219。为验证附件异常，在交付附件目录再创建一个名为 escape 的软链接，指向该目录里的 report.md（ln -s report.md escape）。保留软链接。最终如实回复 RESULT_ATTACHMENT_219 和实际操作结果。", "RESULT_ATTACHMENT_219", true, true, true, false},
		{"communication", "先用 send_message 向 owner_assistant 发送 PROGRESS_COORDINATION_219，说明正在生成报告，无需对方回复。然后创建交付附件 report.md，正文包含 RESULT_COORDINATION_219；完成后在最终回复报告 RESULT_COORDINATION_219。", "RESULT_COORDINATION_219", true, true, false, true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			started := time.Now()
			var id string
			if scenario.scheduled {
				definition := protocol.ScheduleSpec{Title: "Live " + scenario.name, Goal: scenario.goal, Timezone: "UTC", Kind: "interval", EveryMinutes: 60, Misfire: "coalesce", Notify: "all", Enabled: false}
				raw, err := s.Schedules.Manage(ctx, "main", schedule.Request{RequestID: "create-" + scenario.name, Action: "create", Spec: &definition}, storage.Now())
				if err != nil {
					t.Fatal(err)
				}
				var info protocol.ScheduleInfo
				if err := json.Unmarshal(raw, &info); err != nil {
					t.Fatal(err)
				}
				raw, err = s.Schedules.Manage(ctx, "main", schedule.Request{RequestID: "run-" + scenario.name, Action: "run", ScheduleID: info.ID}, storage.Now())
				if err != nil {
					t.Fatal(err)
				}
				var accepted struct {
					TaskID string `json:"task_id"`
				}
				if err := json.Unmarshal(raw, &accepted); err != nil {
					t.Fatal(err)
				}
				id = accepted.TaskID
			} else {
				record, err := s.CreateTask(ctx, "main", protocol.CreateTask{RequestID: scenario.name, Text: "Live " + scenario.name + "\n" + scenario.goal})
				if err != nil {
					t.Fatal(err)
				}
				id = record.ID
			}
			s.wakeTasks()
			record := awaitLifecycleTask(t, ctx, s, id, func(r *tasks.Record) bool { return r.Info.Phase.Scope() != "active" })
			observation := map[string]any{"scenario": scenario.name, "task_id": id, "outcome": record.Outcome, "execution_error": record.Error}
			observations = append(observations, observation)
			if record.Info.Phase != protocol.TaskComplete || record.Outcome == nil || record.Result == nil {
				t.Fatal("execution failed", record.Info.Phase, record.Error)
			}
			if record.Outcome.Text != record.Result.Text || !strings.Contains(record.Outcome.Text, scenario.marker) {
				t.Fatal("final reply changed or lost marker", record.Outcome)
			}
			var cursor uint64
			var finalReply string
			var assistantMessages int
			for {
				page, err := s.TaskEvents(ctx, "main", id, cursor, 100)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range page.Events {
					if event.Event.Update.Type == "message_finished" {
						finalReply = event.Event.Update.Text
						assistantMessages++
					}
				}
				if !page.HasMore {
					break
				}
				cursor = page.Cursor
			}
			if assistantMessages == 0 || record.Outcome.Text != finalReply {
				t.Fatal("summary includes earlier assistant replies", record.Outcome.Text, finalReply)
			}
			observation["assistant_messages"], observation["final_reply_matches_transcript"] = assistantMessages, true
			if (len(record.Outcome.Artifacts) != 0) != scenario.attachment || (record.Outcome.ArtifactsError != "") != scenario.artifactError {
				t.Fatal("wrong delivery metadata", record.Outcome)
			}
			if scenario.attachment {
				if len(record.Outcome.Artifacts) != 1 || record.Outcome.Artifacts[0] != ".pot-artifacts/report.md" {
					t.Fatal("model did not use the production artifact convention", record.Outcome.Artifacts)
				}
				contents, err := s.ReadTaskFile(ctx, "main", id, record.Outcome.Artifacts[0], 65536)
				if err != nil || !strings.Contains(string(contents), scenario.marker) {
					t.Fatal("deliverable missing", string(contents), err)
				}
				observation["artifact_contents"] = string(contents)
			}
			snapshot := awaitLifecycleResults(t, ctx, s, c)
			reported, presented := false, false
			var ownerReplies []string
			for _, message := range snapshot.Messages {
				if message.Role == protocol.Assistant && strings.Contains(message.Text, scenario.marker) {
					reported = true
					ownerReplies = append(ownerReplies, message.Text)
				}
				if message.Presentation != nil {
					chunk, err := s.Resources.Chunk(ctx, "main", message.Presentation.Resource.ID, "", 0)
					if err != nil {
						t.Fatal(err)
					}
					data, err := base64.StdEncoding.DecodeString(chunk.Data)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(data), scenario.marker) {
						raw, _ := json.Marshal(message.Presentation)
						observation["presentation"] = json.RawMessage(raw)
						presented = true
					}
				}
			}
			observation["owner_replies"] = ownerReplies
			if !reported || scenario.attachment && !presented {
				t.Error("owner did not report final result or present attachment", reported, presented)
			}
			var terminal, messages int
			if err := s.Tasks.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.result.task_id')=? AND json_extract(envelope,'$.kind')='task_result'`, id).Scan(&terminal); err != nil {
				t.Fatal(err)
			}
			if terminal != 1 {
				t.Fatal("terminal result duplicated", terminal)
			}
			if err := s.Tasks.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.from.agent_id')=? AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.text') LIKE '%PROGRESS_COORDINATION_219%'`, id).Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if (messages != 0) != scenario.progress {
				t.Error("unexpected progress communication", messages)
			}
			observation["reported"], observation["presented"], observation["progress_messages"], observation["terminal_events"] = reported, presented, messages, terminal
			observation["elapsed_seconds"], observation["cumulative_requests"] = time.Since(started).Seconds(), requests.Load()
			t.Logf("%s: phase=%s artifacts=%v artifact_error=%v owner_reported=%v progress=%d terminal_events=%d real_requests=%d elapsed=%.1fs", scenario.name, record.Info.Phase, record.Outcome.Artifacts, record.Outcome.ArtifactsError != "", reported, messages, terminal, requests.Load(), time.Since(started).Seconds())
		})
		if ctx.Err() != nil {
			t.Fatal(fmt.Errorf("live task result qualification stopped: %w", ctx.Err()))
		}
	}
}
