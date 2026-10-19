package control

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/integrations/codex/bridge"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/integrations"
	codexintegration "github.com/jjyr/pot/pkg/integrations/codex"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/pluginhost"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestCodexBridgeToolsSSEBusyInboxAndMCPReceipt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	fake := filepath.Join(t.TempDir(), "codex")
	build := exec.Command("go", "build", "-o", fake, "../../integrations/codex/bridge/testdata/fake-codex")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	store, err := bridge.Open(filepath.Join(t.TempDir(), "bridge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exe, _ := os.Executable()
	backend := bridge.NewServer(ctx, store, bridge.Config{Codex: fake, Workspaces: map[string]string{"repo": t.TempDir()}}, []string{exe})
	backend.Start()
	defer func() { cancel(); backend.Wait() }()
	var subscriptions atomic.Int32
	var connectionsMu sync.Mutex
	connections := map[string]net.Conn{}
	var streamConnection net.Conn
	httpBridge := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" {
			subscriptions.Add(1)
			connectionsMu.Lock()
			streamConnection = connections[r.RemoteAddr]
			connectionsMu.Unlock()
		}
		backend.ServeHTTP(w, r)
	}))
	httpBridge.Config.ConnState = func(c net.Conn, state http.ConnState) {
		connectionsMu.Lock()
		defer connectionsMu.Unlock()
		if state == http.StateNew {
			connections[c.RemoteAddr().String()] = c
		}
		if state == http.StateClosed {
			delete(connections, c.RemoteAddr().String())
		}
	}
	httpBridge.StartTLS()
	defer httpBridge.Close()
	remote, local, err := store.Pair(bridge.Pair{Device: "pot-test", Control: "control", Assistant: "main", Workspaces: []string{"repo"}})
	if err != nil {
		t.Fatal(err)
	}
	remote.Endpoint = httpBridge.URL
	remote.CA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: httpBridge.Certificate().Raw}))
	local.Endpoint = httpBridge.URL
	local.CA = remote.CA
	var modelCalls atomic.Int32
	foreground := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		n := modelCalls.Add(1)
		text := "foreground"
		if n == 1 {
			close(foreground)
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
		} else {
			text = "Codex result summarized"
			if !strings.Contains(string(raw), "result: literal $(not-a-shell)") || !strings.Contains(string(raw), "untrusted reference data") {
				t.Error("missing original result or reference-data boundary")
			}
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			if tools := strings.TrimSpace(string(fields["tools"])); tools != "" && tools != "null" && tools != "[]" {
				t.Error("summary acquired tools")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}))
	defer model.Close()
	manager, _ := execution.New("control", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	pluginRoot, _ := filepath.Abs("../../plugins")
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "control", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: pluginRoot, IntegrationAdapters: []integrations.Adapter{codexintegration.Adapter{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Registry.Create(ctx, "main", "Pot"); err != nil {
		t.Fatal(err)
	}
	other, err := s.Registry.Create(ctx, "other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: model.URL, Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = conversation.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(remote)
	if _, err = s.PairCodex(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PairCodex(ctx, raw); err != nil {
		t.Fatal("pairing retry", err)
	}
	wrong := remote
	wrong.Route = "other-control/main"
	bad, _ := json.Marshal(wrong)
	if _, err = s.PairCodex(ctx, bad); err == nil {
		t.Fatal("accepted foreign control pairing")
	}
	s.runBackgrounds(ctx, storage.Now())
	if _, err = conversation.Send(ctx, protocol.SendMessage{RequestID: "foreground", ConversationID: "main", Text: "Authorized foreground request"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-foreground:
	case <-ctx.Done():
		t.Fatal("foreground timeout")
	}
	installed, err := plugin.Load(filepath.Join(pluginRoot, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := installed.ToolBundle()
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Integrations.Credential(ctx, "fixture", "codex", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Integrations.Forget("fixture")
	node, _ := exec.LookPath("node")
	host, err := pluginhost.New(pluginhost.Config{Node: node, Identity: bundle.Identity().Digest, Mode: "text", Entrypoint: "index.mjs", Files: bundle.Files, Environment: map[string]string{"CODEX_BRIDGE_PAIR": token}, ProcessWorker: []string{exe}})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	input := map[string]any{"request_id": "independent", "workspace": "repo", "mode": "read-only", "prompt": "literal $(not-a-shell)"}
	if err = bundle.ValidateInput("codex_submit", input); err != nil {
		t.Fatal(err)
	}
	result, err := host.Execute(ctx, "codex_submit", input)
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	var task bridge.Task
	if err = json.Unmarshal([]byte(result.Content[0].Text), &task); err != nil {
		t.Fatal(err)
	}
	again, err := host.Execute(ctx, "codex_submit", input)
	if err != nil {
		t.Fatal(err)
	}
	var duplicate bridge.Task
	_ = json.Unmarshal([]byte(again.Content[0].Text), &duplicate)
	if task.ID != duplicate.ID {
		t.Fatal("duplicated task on retry")
	}
	wait := func(fn func() bool) {
		t.Helper()
		for !fn() {
			select {
			case <-ctx.Done():
				t.Fatal("bridge delivery timeout")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	wait(func() bool { events, _ := s.Mailbox.Notifications(ctx, "main"); return len(events) == 1 })
	if modelCalls.Load() != 1 {
		t.Fatal("progress woke model or busy foreground was interrupted", modelCalls.Load())
	}
	if subscriptions.Load() != 1 {
		t.Fatal("not a held SSE subscription", subscriptions.Load())
	}
	if events, _ := s.Mailbox.Notifications(ctx, other.ID); len(events) != 0 {
		t.Fatal("event routed to another assistant")
	}
	close(release)
	awaitConversation(t, conversation, func(snapshot protocol.Snapshot) bool {
		for _, m := range snapshot.Messages {
			if m.Text == "Codex result summarized" && snapshot.Turn == nil {
				return true
			}
		}
		return false
	})
	if modelCalls.Load() != 2 {
		t.Fatal("duplicate result summary", modelCalls.Load())
	}
	// Drop the actual TLS connection: the plugin must reconnect with its durable
	// Last-Event-ID, without a new task or a new summary.
	connectionsMu.Lock()
	connection := streamConnection
	connectionsMu.Unlock()
	if connection == nil {
		t.Fatal("missing real SSE connection")
	}
	_ = connection.Close()
	wait(func() bool { return subscriptions.Load() == 2 })
	if modelCalls.Load() != 2 {
		t.Fatal("network reconnect duplicated summary")
	}
	// Disable/re-enable the stream to exercise a fresh supervised process with the
	// durable cursor. The task is neither resubmitted nor summarized again.
	bindings, _ := s.Mailbox.Backgrounds(ctx)
	binding := bindings[0]
	binding.Enabled = false
	if err = s.ConfigurePluginNotifications(ctx, binding); err != nil {
		t.Fatal(err)
	}
	s.runBackgrounds(ctx, storage.Now())
	wait(func() bool { s.pluginMu.Lock(); defer s.pluginMu.Unlock(); return len(s.pluginStreams) == 0 })
	bindings, _ = s.Mailbox.Backgrounds(ctx)
	binding = bindings[0]
	binding.Enabled = true
	if err = s.ConfigurePluginNotifications(ctx, binding); err != nil {
		t.Fatal(err)
	}
	s.runBackgrounds(ctx, storage.Now()+600000)
	wait(func() bool { return subscriptions.Load() == 3 })
	s.deliverNotifications(ctx, "main")
	if modelCalls.Load() != 2 {
		t.Fatal("reconnect duplicated summary")
	}
	// Actual newline-delimited MCP dispatch, not a direct message handler call.
	localPath := filepath.Join(t.TempDir(), "local-pair.json")
	localJSON, _ := json.Marshal(local)
	if err = os.WriteFile(localPath, localJSON, 0600); err != nil {
		t.Fatal(err)
	}
	request := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"test\",\"version\":\"1\"}}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"pot_notify\",\"arguments\":{\"request_id\":\"mcp-e2e\",\"body\":\"reference data\"}}}\n")
	var output strings.Builder
	if err = bridge.MCP(request, &output, localPath); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err = json.Unmarshal([]byte(lines[1]), &rpc); err != nil {
		t.Fatal(err)
	}
	var message bridge.Message
	if err = json.Unmarshal([]byte(rpc.Result.Content[0].Text), &message); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "pot_receipt", "arguments": map[string]string{"message_id": message.ID}}})
		req, _ := http.NewRequest("POST", httpBridge.URL+"/mcp", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		req.Header.Set("Authorization", "Bearer "+local.Token)
		resp, e := httpBridge.Client().Do(req)
		if e != nil {
			return false
		}
		defer resp.Body.Close()
		var envelope struct {
			Result struct {
				StructuredContent bridge.Message `json:"structuredContent"`
			} `json:"result"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		return envelope.Result.StructuredContent.Status == "received"
	})
}
