package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

type liveBatchRequest struct {
	Category string          `json:"category"`
	Body     json.RawMessage `json:"body"`
	Response string          `json:"response_sse"`
}

// Fail the continuation after one real model response and its authorized tools.
type liveBatchFailingContinuation struct {
	agent.Services
	started bool
}

func (s *liveBatchFailingContinuation) Model(ctx context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	if s.started {
		return nil, fmt.Errorf("controlled worker continuation failure")
	}
	s.started = true
	return s.Services.Model(ctx, request)
}

// A transparent recording proxy forwards every response from the real model.
// The first foreground request is held before forwarding so actual Send calls
// can fill the production queues. No fixture model output is returned.
func TestDeepSeekCategoryBatchesLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" {
		t.Skip("explicit POT_LIVE=1 required for paid requests")
	}
	path, err := liveEnvironmentPath()
	if err != nil {
		t.Fatal(err)
	}
	if override := os.Getenv("POT_LIVE_ENV"); override != "" {
		path = override
	}
	config, err := readLiveModelConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var gate sync.Once
	openGate := func() { gate.Do(func() { close(release) }) }
	defer openGate()
	var mu sync.Mutex
	var requests []*liveBatchRequest
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	const gateText = "请仅回复：BATCH_READY。"
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "request read failed", 500)
			return
		}
		category, last := batchRequestCategory(body)
		record := &liveBatchRequest{Category: category, Body: append(json.RawMessage(nil), body...)}
		mu.Lock()
		requests = append(requests, record)
		count := len(requests)
		mu.Unlock()
		if count > 24 {
			http.Error(w, "24-request live cap reached", 429)
			return
		}
		if last == gateText {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		upstream, requestErr := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
		if requestErr != nil {
			http.Error(w, "upstream request invalid", 502)
			return
		}
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Authorization", r.Header.Get("Authorization"))
		response, forwardErr := client.Do(upstream)
		if forwardErr != nil {
			http.Error(w, "upstream transport failed", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		var captured bytes.Buffer
		buf := make([]byte, 8192)
		for {
			n, e := response.Body.Read(buf)
			if n > 0 {
				captured.Write(buf[:n])
				if _, e = w.Write(buf[:n]); e != nil {
					break
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if e != nil {
				break
			}
		}
		mu.Lock()
		record.Response = captured.String()
		mu.Unlock()
	}))
	defer proxy.Close()
	root := t.TempDir()
	manager, err := execution.New("live", "batch-observation", execution.DefaultLimits())
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
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "live", BootID: "batch-observation", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		openGate()
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
	if _, err = s.Registry.Create(ctx, "main-create", "Batch observation"); err != nil {
		t.Fatal(err)
	}
	alpha, err := s.Registry.Create(ctx, "alpha-create", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := s.Registry.Create(ctx, "beta-create", "Beta")
	if err != nil {
		t.Fatal(err)
	}
	none, provider := "none", "deepseek"
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "live-profile", Name: "DeepSeek batch observation", Model: config.Model, Endpoint: proxy.URL, Protocol: "openai_chat", APIKey: config.APIKey, MaxOutputTokens: 768, ReasoningEffort: &none, ModelSettings: &core.ModelSettings{CatalogProvider: &provider}})
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
	if err = s.Permissions.Configure("live", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	// Inject a model-service failure after the real model requests the harmless
	// command. Main/Summary and peer services are retained.
	prepare := s.Runner.Prepare
	s.Runner.Prepare = func(ctx context.Context, r runner.Run) (runner.Prepared, error) {
		p, e := prepare(ctx, r)
		if e == nil && strings.HasPrefix(r.Launch.Text, "TASK_BETA") {
			p.Agent.Services = &liveBatchFailingContinuation{Services: p.Agent.Services}
		}
		return p, e
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("POT_LIVE_OUTPUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "observation")
	}
	if err = os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	var snapshot protocol.Snapshot
	defer func() {
		final, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		snapshot, _ = c.Snapshot(final)
		history, _ := s.Mailbox.History(final, "main", 0)
		mu.Lock()
		data, _ := json.MarshalIndent(map[string]any{"model": config.Model, "requests": requests, "snapshot": snapshot, "mail_history": history}, "", "  ")
		mu.Unlock()
		if err := os.WriteFile(filepath.Join(out, "observation.json"), data, 0600); err != nil {
			t.Error(err)
		}
		t.Log("observation:", filepath.Join(out, "observation.json"))
	}()
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "gate", ConversationID: "main", Text: gateText}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	goals := []string{
		"TASK_ALPHA 命令测试\n请执行一次 exec_command，命令原样为 printf 'TASK_ALPHA_PROOF\\n'。报告实际输出，不要重复执行或发送消息。",
		"TASK_BETA 失败测试\n请先执行一次 exec_command，命令原样为 printf 'TASK_BETA_PROOF\\n'。不要重复执行或发送消息。",
	}
	var taskIDs []string
	for i, goal := range goals {
		r, e := s.Tasks.Create(ctx, "main", protocol.CreateTask{RequestID: fmt.Sprintf("task-%d", i), Text: goal})
		if e != nil {
			t.Fatal(e)
		}
		taskIDs = append(taskIDs, r.Info.ID)
	}
	taskSub := s.Tasks.Subscribe()
	defer taskSub.Close()
	for {
		ended := true
		for _, id := range taskIDs {
			r, e := s.Tasks.Get(ctx, "main", id)
			if e != nil {
				t.Fatal(e)
			}
			ended = ended && r.Info.Phase.Scope() != "active"
		}
		if ended {
			break
		}
		if _, e := taskSub.Next(ctx); e != nil {
			t.Fatal(e)
		}
	}
	a, _ := s.Tasks.Get(ctx, "main", taskIDs[0])
	b, _ := s.Tasks.Get(ctx, "main", taskIDs[1])
	if a.Info.Phase != protocol.TaskComplete || b.Info.Phase != protocol.TaskFailed || !strings.Contains(b.Error, "controlled worker continuation failure") {
		t.Fatal("controlled success/failure did not occur", a.Info.Phase, b.Info.Phase, b.Error)
	}
	first, err := c.Send(ctx, protocol.SendMessage{RequestID: "user-one", ConversationID: "main", Text: "请直接回答简单算式 7+5，标记 USER_ONE。无需工具。"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Send(ctx, protocol.SendMessage{RequestID: "user-two", ConversationID: "main", Text: "再回答 9×3，标记 USER_TWO；与前一条一起简短回答。无需工具。"})
	if err != nil {
		t.Fatal(err)
	}
	destination := protocol.AgentRef{ControlID: "live", Kind: "assistant", AgentID: "main"}
	peerInputs := []struct{ owner, text string }{
		{alpha.ID, "请协助确认简单算式 6×6；反馈中标记 PEER_ALPHA_ONE。"},
		{beta.ID, "请确认 8×8，以 PEER_BETA_ONE 标记，只反馈本发送者内容。"},
		{alpha.ID, "补充同一批反馈：3×7，标记 PEER_ALPHA_TWO；两项一起简短反馈即可。"},
	}
	var mail []protocol.Envelope
	for i, input := range peerInputs {
		args, _ := json.Marshal(protocol.SendMail{To: destination, Text: input.text})
		call := modelprotocol.ToolCall{ID: fmt.Sprintf("send-%d", i), Name: "send_message", Arguments: args}
		dispatch := s.productDispatch(input.owner, "fixture-origin", 1, false)["send_message"]
		raw, e := dispatch(ctx, core.ExecutionContext{Owner: input.owner}, call, func(core.Update) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		var message protocol.Envelope
		if e = json.Unmarshal(raw, &message); e != nil {
			t.Fatal(e)
		}
		mail = append(mail, message)
	}
	openGate()
	changes, stop, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	mailSub := s.Mailbox.Changes.Subscribe()
	defer mailSub.Close()
	for {
		snapshot, err = c.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		notices, e := s.Tasks.Notifications(ctx, "main")
		if e != nil {
			t.Fatal(e)
		}
		history, e := s.Mailbox.History(ctx, "main", 0)
		if e != nil {
			t.Fatal(e)
		}
		processed := 0
		for _, item := range history {
			for _, expected := range mail {
				if item.Message.ID == expected.ID {
					if item.State == "processed" {
						processed++
					}
					if item.State == "failed" || item.State == "interrupted" {
						t.Fatal("peer failed", item.State, item.Detail)
					}
				}
			}
		}
		if snapshot.Turn == nil && len(notices) == 0 && processed == 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("batch deadline", ctx.Err())
		case <-changes:
		case <-time.After(100 * time.Millisecond):
		}
	}
	var userReply string
	summaryInputs := map[string]bool{}
	for _, m := range snapshot.Messages {
		if m.Role == protocol.System && m.RequestID != nil && strings.HasPrefix(*m.RequestID, "pot-result-") {
			summaryInputs[m.ID] = true
		}
	}
	var summaries []string
	for _, m := range snapshot.Messages {
		if m.Role == protocol.Assistant && m.Status == protocol.Complete {
			if m.ReplyTo == first.MessageID {
				userReply += m.Text
			}
			if summaryInputs[m.ReplyTo] {
				summaries = append(summaries, m.Text)
			}
		}
		if (m.ID == first.MessageID || m.ID == second.MessageID) && (m.Status != protocol.Complete || m.Queued) {
			t.Fatal("user member not acknowledged", m.Status, m.Queued)
		}
	}
	if !containsAll(userReply, "USER_ONE", "12", "USER_TWO", "27") || strings.Contains(userReply, "TASK_") {
		t.Fatal("real user response lost or mixed inputs", userReply)
	}
	if len(summaries) != 1 || !containsAll(summaries[0], "TASK_ALPHA", "TASK_BETA") || !strings.Contains(summaries[0], "失败") {
		t.Fatal("real task summary lost success/failure", summaries)
	}
	for _, group := range []struct {
		seed      protocol.Envelope
		want      []string
		forbidden string
	}{
		{mail[0], []string{"PEER_ALPHA_ONE", "36", "PEER_ALPHA_TWO", "21"}, "PEER_BETA"},
		{mail[1], []string{"PEER_BETA_ONE", "64"}, "PEER_ALPHA"},
	} {
		_, raw, e := s.Mailbox.Session(ctx, group.seed, spec)
		if e != nil {
			t.Fatal(e)
		}
		var checkpoint agent.Checkpoint
		if e = json.Unmarshal(raw, &checkpoint); e != nil {
			t.Fatal(e)
		}
		text := ""
		for _, m := range checkpoint.Messages {
			if m.Role == "assistant" {
				text += m.Text
			}
		}
		history, e := s.Mailbox.History(ctx, group.seed.From.AgentID, 0)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range history {
			if item.Message.From == destination && item.Message.To == group.seed.From {
				text += item.Message.Text
			}
		}
		if !containsAll(text, group.want...) || strings.Contains(text, group.forbidden) {
			t.Fatal("real peer output lost or mixed data", text)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	counts := map[string]int{}
	for _, r := range requests {
		counts[r.Category]++
		_, last := batchRequestCategory(r.Body)
		if r.Category == "user" && last != gateText && (!containsAll(last, "USER_ONE", "USER_TWO") || strings.Contains(last, "artifact_task_id")) {
			t.Fatal("user inputs were not one clean model request")
		}
		if r.Category == "summary" && !containsAll(last, "TASK_ALPHA", "TASK_BETA", `"phase":"failed"`, `"phase":"complete"`) {
			t.Fatal("task results did not share one model request")
		}
		if r.Category == "peer-alpha" && (!containsAll(string(r.Body), "PEER_ALPHA_ONE", "PEER_ALPHA_TWO") || strings.Contains(string(r.Body), "PEER_BETA")) {
			t.Fatal("Alpha model context split or mixed")
		}
		if r.Category == "peer-beta" && strings.Contains(string(r.Body), "PEER_ALPHA") {
			t.Fatal("Beta model context mixed")
		}
	}
	if counts["user"] != 2 || counts["summary"] != 1 || counts["peer-alpha"] == 0 || counts["peer-beta"] == 0 {
		t.Fatal("unexpected real model request counts", counts)
	}
	var bindings int
	if err = s.Tasks.DB().QueryRow("SELECT COUNT(DISTINCT batch) FROM agent_delivery_items WHERE message IN (?,?,?)", mail[0].ID, mail[1].ID, mail[2].ID).Scan(&bindings); err != nil || bindings != 2 {
		t.Fatal("peer messages did not use two turns", bindings, err)
	}
	t.Logf("PASS: user batch=1 turn; task results=1 summary; peer batches=2; real model requests=%d, categories=%v", len(requests), counts)
}

func batchRequestCategory(body []byte) (string, string) {
	var fields struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &fields) != nil || len(fields.Messages) == 0 {
		return "invalid", ""
	}
	last := ""
	for _, m := range fields.Messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	if strings.Contains(fields.Messages[0].Content, "Summarize background task results") {
		return "summary", last
	}
	if strings.Contains(fields.Messages[0].Content, "Complete the assigned task.") {
		return "worker", last
	}
	for _, m := range fields.Messages {
		if strings.Contains(m.Content, `key="assistant-mail"`) {
			if strings.Contains(m.Content, "PEER_ALPHA") {
				return "peer-alpha", last
			}
			if strings.Contains(m.Content, "PEER_BETA") {
				return "peer-beta", last
			}
			return "peer-other", last
		}
	}
	// Chat batches now retain separate original messages and stable IDs. Ignore
	// context sections and inspect the newest consecutive conversation inputs.
	latest := []string{}
	previousUser := false
	for _, message := range fields.Messages {
		if message.Role != "user" {
			previousUser = false
			continue
		}
		text := message.Content
		if strings.HasPrefix(text, "<pot_context ") {
			previousUser = false
			continue
		}
		if strings.HasPrefix(text, "<pot_message_metadata>") {
			if _, body, ok := strings.Cut(text, "</pot_message_metadata>\n"); ok {
				text = body
			}
		}
		if !previousUser {
			latest = nil
		}
		latest = append(latest, text)
		previousUser = true
	}
	return "user", strings.Join(latest, "\n")
}

func containsAll(text string, values ...string) bool {
	for _, v := range values {
		if !strings.Contains(text, v) {
			return false
		}
	}
	return true
}
