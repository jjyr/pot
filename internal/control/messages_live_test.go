package control

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// The real model chooses the recipient and arguments from the production tools.
// Only Worker startup is gated so the existing task survives until guidance arrives.
func TestDeepSeekTaskGuidanceLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" {
		t.Skip("explicit POT_LIVE=1 required for paid requests")
	}
	path := os.Getenv("POT_LIVE_ENV")
	if path == "" {
		var err error
		path, err = liveEnvironmentPath()
		if err != nil {
			t.Fatal(err)
		}
	}
	config, err := readLiveModelConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	var requests atomic.Int32
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 12 {
			http.Error(w, "12-request live cap reached", http.StatusTooManyRequests)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request read failed", 500)
			return
		}
		upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			http.Error(w, "upstream request invalid", 502)
			return
		}
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Authorization", r.Header.Get("Authorization"))
		response, err := client.Do(upstream)
		if err != nil {
			http.Error(w, "upstream transport failed", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		if _, err := io.Copy(w, response.Body); err != nil {
			t.Error("forward model response:", err)
		}
	}))
	defer proxy.Close()
	manager, err := execution.New("live", "guidance-observation", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Options{Context: ctx, Root: t.TempDir(), RuntimeID: "live", BootID: "guidance-observation", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		manager.Stop()
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := manager.Drain(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if _, err = legacyMainAssistant(ctx, s, "main-create", "Guidance observation"); err != nil {
		t.Fatal(err)
	}
	none, provider := "none", "deepseek"
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "live-profile", Name: "DeepSeek guidance", Model: config.Model, Endpoint: proxy.URL, Protocol: "openai_chat", APIKey: config.APIKey, MaxOutputTokens: 768, ReasoningEffort: &none, ModelSettings: &core.ModelSettings{CatalogProvider: &provider}})
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
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.Permissions.Configure("live", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var gate sync.Once
	openGate := func() { gate.Do(func() { close(release) }) }
	defer openGate()
	prepare := s.TaskRuns.Prepare
	s.TaskRuns.Prepare = func(ctx context.Context, run tasks.Run) (tasks.Prepared, error) {
		select {
		case <-release:
			return prepare(ctx, run)
		case <-ctx.Done():
			return tasks.Prepared{}, ctx.Err()
		}
	}
	record, err := s.Tasks.AcceptLocal(ctx, "main", protocol.CreateTask{RequestID: "existing-task", Text: "CI 触发规则调整\n这是隔离测试，不修改文件、不执行命令或工具。读取已收到的补充规则，在最终答复中逐项复述；不要自行假定触发条件。"})
	if err != nil {
		t.Fatal(err)
	}
	const guidance = "补充正在进行的「CI 触发规则调整」任务：其他分支 push 也不执行；push tag 触发；release 不必触发。请把这三个条件完整转交给已有任务，不要创建新任务。"
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "guidance", ConversationID: "main", Text: guidance}); err != nil {
		t.Fatal(err)
	}
	changes, stop, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var snapshot protocol.Snapshot
	for {
		snapshot, err = c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Turn == nil {
			break
		}
		select {
		case <-changes:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	sent := 0
	for _, message := range snapshot.Messages {
		if message.Tool == nil {
			continue
		}
		tool := message.Tool
		if tool.Phase != "completed" {
			t.Fatalf("real model tool failed: %s %s %s", tool.Name, tool.Phase, tool.Result)
		}
		if tool.Name == "send_message" {
			sent++
			var send protocol.SendMail
			if err := core.DecodeJSON(tool.Args, &send); err != nil || send.To != *record.Info.Address {
				t.Fatal("real model did not use the simplified task address", string(tool.Args), err)
			}
			t.Logf("real %s send_message args=%s result=%s", config.Model, tool.Args, tool.Result)
		}
	}
	if sent != 1 {
		t.Fatalf("expected one real model send_message, got %d", sent)
	}
	inputs, err := s.Tasks.Inputs(ctx, "main", record.Info.ID)
	if err != nil || len(inputs) != 2 || inputs[1].Kind != "guidance" || !containsAll(inputs[1].Text, "push", "tag", "release", "不") {
		t.Fatal("real guidance was not durably accepted", inputs, err)
	}
	openGate()
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	for {
		final, err := s.Tasks.Get(ctx, "main", record.Info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Info.Phase.Scope() != "active" {
			if final.Info.Phase != protocol.TaskComplete || final.Result == nil || !containsAll(final.Result.Text, "push", "tag", "release", "不") {
				t.Fatalf("real Worker did not apply guidance: phase=%s result=%+v error=%s", final.Info.Phase, final.Result, final.Error)
			}
			inputs, err = s.Tasks.Inputs(ctx, "main", record.Info.ID)
			if err != nil || len(inputs) != 2 || inputs[1].State != "consumed" {
				t.Fatal("guidance was not consumed by the original Worker", inputs, err)
			}
			t.Logf("real Worker consumed guidance and completed: %s; model requests=%d", final.Result.Text, requests.Load())
			break
		}
		if _, err := sub.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
