package control

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

type parallelObservationServices struct {
	core.ExecutionServices
	requests *atomic.Int32
}

func (s parallelObservationServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	if s.requests.Add(1) > 32 {
		return nil, fmt.Errorf("live observation reached its 32-request safety cap")
	}
	return s.ExecutionServices.Model(ctx, r)
}

// Opt-in qualification uses real DeepSeek for the assistant, both workers and
// result summaries. Fixture commands are harmless and bound to a private Runtime.
// Production prompts, tools, execution capacity and notification delivery are retained.
func TestDeepSeekParallelDispatchObservationLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" {
		t.Skip("explicit POT_LIVE=1 required for paid requests")
	}
	envPath := os.Getenv("POT_LIVE_ENV")
	if envPath == "" {
		var err error
		envPath, err = liveEnvironmentPath()
		if err != nil {
			t.Fatal(err)
		}
	}
	config, err := readLiveModelConfig(envPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	root := t.TempDir()
	manager, err := execution.New("live", "parallel-observation", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.NewMemoryKey(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "live", BootID: "parallel-observation", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		manager.Stop()
		cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		if err := manager.Drain(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if _, err = s.Registry.Create(ctx, "live-create", "Parallel observation"); err != nil {
		t.Fatal(err)
	}
	none := "none"
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "live-provider", ProfileID: "live-profile", Name: "DeepSeek observation", Model: config.Model, Endpoint: strings.TrimRight(config.BaseURL, "/") + "/chat/completions", Protocol: "openai_chat", APIKey: config.APIKey, MaxOutputTokens: 768, ReasoningEffort: &none})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.RequestTimeoutMS = 60000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	// Permit harmless fixture commands on this isolated Runtime, so approval
	// waiting does not confound the dispatch behavior under observation.
	if err = s.Permissions.Configure("live", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	originalPrepare := s.TaskRuns.Prepare
	s.TaskRuns.Prepare = func(ctx context.Context, r tasks.Run) (tasks.Prepared, error) {
		prepared, err := originalPrepare(ctx, r)
		if err == nil {
			prepared.Agent.Services = parallelObservationServices{prepared.Agent.Services.(core.ExecutionServices), &requests}
		}
		return prepared, err
	}
	var c *assistant.Conversation
	c, err = assistant.OpenConversation(filepath.Join(root, "observation.sqlite"), assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "live", BootID: "parallel-observation", Manager: manager, InitialSpec: &spec, MaxTurnRequests: 128, OnFinished: func(ctx context.Context, r protocol.SendMessage, result execution.Result) error {
		return s.finishAgentDelivery(ctx, "main", r, result)
	}, Prepare: func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
		options, err := s.prepare(ctx, c, thread, request)
		if err == nil {
			options.Services = parallelObservationServices{options.Services, &requests}
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
	out := os.Getenv("POT_LIVE_OUTPUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "observation")
	}
	if err = os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	records := []map[string]any{}
	log := func(kind string, value any) {
		elapsed := time.Since(started).Seconds()
		records = append(records, map[string]any{"elapsed_seconds": elapsed, "kind": kind, "value": value})
		t.Logf("+%.2fs %s %s", elapsed, kind, mustObservationJSON(value))
	}
	var snapshot protocol.Snapshot
	defer func() {
		finalCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		snapshot, _ = c.Snapshot(finalCtx)
		page, _ := s.Tasks.Page(finalCtx, "main", protocol.TaskQuery{Scope: "all", Limit: 50})
		modelUsage, _ := s.Usage.Summary(finalCtx, usage.Query{Days: 14, Timezone: "UTC"}, time.Now().UnixMilli())
		report := map[string]any{"model": config.Model, "model_requests_attempted": requests.Load(), "timeline": records, "snapshot": snapshot, "tasks": page, "usage": modelUsage, "runtime_max_active": execution.DefaultLimits().MaxActive, "workload_seconds": map[string]int{"A": 70, "B": 5}}
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "observation.json"), data, 0600); err != nil {
			t.Error(err)
		}
		t.Logf("observation retained at %s; model requests=%d", filepath.Join(out, "observation.json"), requests.Load())
	}()
	first := "请执行任务 A：在任务工作区执行这条命令 `printf 'A_STARTED\\n'; sleep 70; printf 'A_DONE\\n'`。这是一个本地耗时操作，结束后通知我实际结果。"
	second := "再帮我执行一个独立的任务 B：在任务工作区执行这条命令 `printf 'B_STARTED\\n'; sleep 5; printf 'B_DONE\\n'`。结束后通知我实际结果。"
	log("user_A", first)
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "observe-a", ConversationID: "main", Text: first}); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	seen := map[string]string{}
	cursors := map[string]uint64{}
	var aStart time.Time
	bSent := false
	var bAcceptedAt int64
	creations := map[string]protocol.TaskInfo{}
	for {
		if ctx.Err() != nil {
			log("deadline", ctx.Err().Error())
			break
		}
		snapshot, err = c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range snapshot.Messages {
			if m.Tool != nil {
				tool := m.Tool
				if tool.Name == "guide_task" || tool.Name == "wait_task" || tool.Name == "send_assistant_message" {
					t.Fatal("removed tool invoked", tool.Name)
				}
				if tool.Name == "get_task" {
					t.Fatal("assistant polled instead of yielding to the user")
				}
				if tool.Name == "create_task" && tool.Phase == "completed" {
					var created protocol.TaskInfo
					if tool.Phase == "completed" && len(tool.Result) == 0 {
						t.Fatal("missing task receipt")
					}
					if err := json.Unmarshal(tool.Result, &created); err != nil {
						t.Fatal(err)
					}
					var args struct {
						Text string `json:"text"`
					}
					_ = json.Unmarshal(tool.Args, &args)
					if strings.Contains(args.Text, "A_STARTED") {
						creations["A"] = created
					}
					if strings.Contains(args.Text, "B_STARTED") {
						creations["B"] = created
					}
					if !strings.Contains(args.Text, "A_STARTED") && !strings.Contains(args.Text, "B_STARTED") {
						t.Fatal("task brief omitted the actual workload command")
					}
				}
				key := "tool:" + m.ID
				if seen[key] != tool.Phase {
					seen[key] = tool.Phase
					log("main_tool", tool)
				}
			} else if m.Status == protocol.Complete && seen[m.ID] == "" {
				seen[m.ID] = "complete"
				log("chat_message", m)
			}
		}
		page, err := s.Tasks.Page(ctx, "main", protocol.TaskQuery{Scope: "all", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Items {
			if seen["task:"+task.ID] != string(task.Phase) {
				seen["task:"+task.ID] = string(task.Phase)
				log("task_state", task)
			}
			events, err := s.TaskEvents(ctx, "main", task.ID, cursors[task.ID], 100)
			if err != nil {
				t.Fatal(err)
			}
			cursors[task.ID] = events.Cursor
			for _, event := range events.Events {
				u := event.Event.Update
				if strings.HasPrefix(u.Type, "tool_") || u.Type == "turn_finished" {
					log("worker_event", map[string]any{"task_id": task.ID, "event": event})
				}
				if aStart.IsZero() && u.Type == "tool_output" && strings.Contains(u.Text, "A_STARTED") {
					aStart = time.Now()
					log("A_command_started", task.ID)
				}
			}
		}
		if !bSent && !aStart.IsZero() && time.Since(aStart) >= time.Second {
			log("user_B", second)
			var receipt protocol.SendMessageAccepted
			if snapshot.Turn != nil {
				receipt, err = c.Steer(ctx, assistant.SteerRequest{RequestID: "observe-b", UserMessageID: snapshot.Turn.UserMessageID, Text: second})
				log("B_entrypoint", "active turn: normal UI steering")
			} else {
				receipt, err = c.Send(ctx, protocol.SendMessage{RequestID: "observe-b", ConversationID: "main", Text: second})
				log("B_entrypoint", "idle turn: normal chat send")
			}
			if err != nil {
				log("B_accept_error", err.Error())
				break
			}
			bAcceptedAt = time.Now().UnixMilli()
			log("B_accepted", receipt)
			bSent = true
		}
		systems := map[string]bool{}
		for _, m := range snapshot.Messages {
			if m.Role == protocol.System && m.RequestID != nil && strings.HasPrefix(*m.RequestID, "pot-result-") {
				systems[m.ID] = true
			}
		}
		summaries := 0
		for _, m := range snapshot.Messages {
			if m.Role == protocol.Assistant && m.Status == protocol.Complete && systems[m.ReplyTo] {
				summaries++
			}
		}
		ended := len(page.Items) >= 2
		for _, task := range page.Items {
			ended = ended && (task.Phase == protocol.TaskComplete || task.Phase == protocol.TaskFailed || task.Phase == protocol.TaskCancelled || task.Phase == protocol.TaskInterrupted)
		}
		if bSent && ended && summaries >= 2 && snapshot.Turn == nil {
			log("observation_finished", map[string]any{"tasks": len(page.Items), "separate_result_replies": summaries})
			break
		}
		if snapshot.Agent.Phase == protocol.AgentFailed && snapshot.Turn == nil {
			log("main_failed", snapshot.Agent)
			break
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	if !bSent || len(creations) != 2 {
		t.Fatal("independent user requests did not dispatch both Tasks")
	}
	a, err := s.Tasks.Get(context.Background(), "main", creations["A"].ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Tasks.Get(context.Background(), "main", creations["B"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil || b == nil || a.Info.Phase != protocol.TaskComplete || b.Info.Phase != protocol.TaskComplete {
		t.Fatal("Tasks did not complete successfully")
	}
	if b.Info.CreatedAtMS >= a.Info.UpdatedAtMS || b.Info.CreatedAtMS-bAcceptedAt > 15000 {
		t.Fatal("B dispatch was blocked by A or an excessive wait")
	}
	if b.Info.UpdatedAtMS >= a.Info.UpdatedAtMS {
		t.Fatal("short B did not finish independently before A")
	}
	for _, record := range []*tasks.Record{a, b} {
		eventID, _ := core.HashJSON([]string{record.Info.ID, record.RunID, "terminal"})
		systemID := ""
		for _, m := range snapshot.Messages {
			if m.RequestID != nil && *m.RequestID == "pot-result-"+eventID {
				systemID = m.ID
			}
		}
		replies := 0
		for _, m := range snapshot.Messages {
			if m.Role == protocol.Assistant && m.ReplyTo == systemID && m.Status == protocol.Complete {
				replies++
				for _, link := range regexp.MustCompile(`pot-file://[^)\s]+`).FindAllString(m.Text, -1) {
					parsed, err := url.Parse(link)
					if err != nil || parsed.Host != record.Info.ID || parsed.Path == "" || parsed.Path == "/" {
						t.Fatal("artifact link omitted or changed the actual relative path", link)
					}
				}
				prose := regexp.MustCompile(`\]\([^)]*\)`).ReplaceAllString(m.Text, "]")
				if strings.Contains(prose, record.Info.ID) || strings.Contains(prose, record.Info.ID[:8]) || strings.Contains(prose, "任务 ID") {
					t.Fatal("notification exposed an internal task ID")
				}
				other := a
				if record.Info.ID == a.Info.ID {
					other = b
				}
				if strings.Contains(m.Text, other.Info.Title) || strings.Contains(m.Text, "仍在运行") {
					t.Fatal("notification inferred an unrelated task state")
				}
			}
		}
		if systemID == "" || replies != 1 {
			t.Fatal("result was missing or reported more than once", record.Info.Title, replies)
		}
	}
	notices, err := s.Tasks.Notifications(context.Background(), "main")
	if err != nil || len(notices) != 0 {
		t.Fatal("reported events remain pending", notices, err)
	}
	log("acceptance_passed", map[string]any{"B_dispatch_delay_ms": b.Info.CreatedAtMS - bAcceptedAt, "B_finished_before_A": true, "one_notification_per_task": true})
}

func mustObservationJSON(value any) string { data, _ := json.Marshal(value); return string(data) }
