package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This opt-in qualification runs the installed Codex CLI with an isolated home,
// the real configured model and the production MCP server. Provider credentials
// remain in this process's memory and never enter the CLI's environment or home.
func TestConfiguredCodexCLIAgentMessagesLive(t *testing.T) {
	production, cli := os.Getenv("POT_CONFIGURED_LIVE_ROOT"), os.Getenv("POT_CODEX_LIVE_CLI")
	if os.Getenv("POT_LIVE") != "1" || production == "" || cli == "" {
		t.Skip("requires explicit configured-model and installed-Codex live opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	profile, provider, credential := codexLiveProvider(t, ctx, production)
	defer clear(credential)
	if provider.Protocol != "openai_responses" {
		t.Fatal("Codex CLI qualification requires the configured Responses provider")
	}
	var requests, cliRequests, potRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || requests.Add(1) > 24 {
			http.Error(w, "live request bound reached", http.StatusTooManyRequests)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/cli/") {
			cliRequests.Add(1)
		} else if strings.HasPrefix(r.URL.Path, "/pot/") {
			potRequests.Add(1)
		} else {
			http.Error(w, "unknown qualification client", http.StatusBadRequest)
			return
		}
		t.Logf("bounded configured-model request: cli=%d pot=%d", cliRequests.Load(), potRequests.Load())
		raw, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024+1))
		var body map[string]json.RawMessage
		if err != nil || len(raw) > 8*1024*1024 || json.Unmarshal(raw, &body) != nil {
			http.Error(w, "invalid bounded model request", http.StatusBadRequest)
			return
		}
		var model string
		if json.Unmarshal(body["model"], &model) != nil || model != profile.Model {
			http.Error(w, "configured model mismatch", http.StatusBadRequest)
			return
		}
		body["max_output_tokens"] = json.RawMessage("2048")
		bounded, _ := json.Marshal(body)
		response, err := gateway.Forward(r.Context(), provider, credential, bounded, "", 60*time.Second)
		if err != nil {
			http.Error(w, "configured provider request failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			http.Error(w, "configured provider rejected request", response.StatusCode)
			return
		}
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		io.Copy(codexLiveFlusher{w}, io.LimitReader(response.Body, 8*1024*1024))
	}))
	defer proxy.Close()
	root := t.TempDir()
	manager, err := execution.New("codex-cli-live-pot", "live-boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	pot, err := control.Open(control.Options{Context: ctx, Root: filepath.Join(root, "pot"), RuntimeID: "codex-cli-live-pot", BootID: "live-boot", PeerListen: "127.0.0.1:0", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		pot.Close()
		manager.Stop()
		drain, finish := context.WithTimeout(context.Background(), 10*time.Second)
		defer finish()
		if err := manager.Drain(drain); err != nil {
			t.Error(err)
		}
	}()
	if _, err = pot.Registry.Create(ctx, "create-live-main", "Main"); err != nil {
		t.Fatal(err)
	}
	settings := core.ModelSettings{}
	if profile.ModelSettings != nil {
		settings = *profile.ModelSettings
	}
	outputTokens := uint64(2048)
	settings.MaxOutputTokens = &outputTokens
	saved, err := pot.Profiles.Save(ctx, control.SaveProfile{RequestID: "live-profile", ProfileID: "live", Name: "Codex receiver qualification", Model: profile.Model, Endpoint: proxy.URL + "/pot/responses", Protocol: provider.Protocol, APIKey: "isolated-proxy", ModelSettings: &settings, MaxOutputTokens: outputTokens, ReasoningEffort: profile.Params.ReasoningEffort})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := pot.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{}
	spec.Params.Agent.AllowedTools = &allowed
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = false
	spec.Params.Agent.RequestTimeoutMS = 90000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	conversation, err := pot.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = conversation.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	hostRoot := filepath.Join(root, "codex-host")
	h, err := openAgentHost(hostRoot)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", h.peerServer())
	if err != nil {
		h.close()
		t.Fatal(err)
	}
	hostCtx, stopHost := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { h.links.Run(hostCtx); close(done) }()
	defer func() { stopHost(); listener.Close(); <-done; h.close() }()
	invitation, err := h.links.Invite(ctx, "codex", "tcp://"+listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := pot.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "preview", Address: invitation.Address})
	if err != nil || preview.(map[string]any)["peer"].(map[string]any)["node_id"] != h.peerIdentity.NodeID {
		t.Fatal("live peer identity preview failed", err)
	}
	connected, err := pot.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", AssistantID: "main", Address: invitation.Address})
	if err != nil || connected.(agentlink.Connection).PeerHost != h.peerIdentity {
		t.Fatal("live signed TCP connection failed", err)
	}
	nonce := identifier()
	expected := "codex-live-reply-" + nonce
	challenge, err := pot.Mailbox.Send(ctx, "challenge-"+nonce, protocol.AgentRef{NodeID: "codex-cli-live-pot", Kind: "assistant", AgentID: "main"}, protocol.SendMail{To: h.identity.Address, Text: "Synthetic qualification challenge " + nonce}, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitCodexLive(t, ctx, func() bool {
		items, err := h.mailbox.messages(ctx)
		return err == nil && len(items) == 1 && items[0].Message.ID == challenge.ID && items[0].State == "queued"
	})
	var operationsMu sync.Mutex
	operations := []hostToolInput{}
	failedOperations := 0
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/agent-host/tool" {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 128*1024))
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var operation hostToolInput
			if json.Unmarshal(raw, &operation) == nil {
				response := httptest.NewRecorder()
				h.handler().ServeHTTP(response, r)
				operationsMu.Lock()
				if response.Code == http.StatusOK {
					operations = append(operations, operation)
				} else {
					failedOperations++
				}
				operationsMu.Unlock()
				t.Logf("real Codex MCP operation: %s successful=%t", operation.Operation, response.Code == http.StatusOK)
				for name, values := range response.Header() {
					w.Header()[name] = values
				}
				w.WriteHeader(response.Code)
				w.Write(response.Body.Bytes())
				return
			}
		}
		h.handler().ServeHTTP(w, r)
	}))
	defer owner.Close()
	home, workspace := filepath.Join(root, "cli-home"), filepath.Join(root, "cli-workspace")
	for _, path := range []string{home, workspace} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reasoning := ""
	if profile.Params.ReasoningEffort != nil {
		reasoning = fmt.Sprintf("model_reasoning_effort = %q\n", *profile.Params.ReasoningEffort)
	}
	config := fmt.Sprintf(`model = %q
model_provider = "qualification"
%sapproval_policy = "never"
sandbox_mode = "read-only"
web_search = "disabled"
[features]
shell_tool = false
shell_snapshot = false
enable_request_compression = false
[model_providers.qualification]
name = "Isolated configured model"
base_url = %q
env_key = "POT_CODEX_LIVE_PROXY_KEY"
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
[mcp_servers.pot]
command = %q
args = ["-test.run=^TestCodexCLILiveMCPProcess$"]
env_vars = ["POT_CODEX_LIVE_STANDALONE", "POT_CODEX_LIVE_MCP_ROOT", "POT_CODEX_LIVE_MCP_HTTP"]
startup_timeout_sec = 15
tool_timeout_sec = 30
`, profile.Model, reasoning, proxy.URL+"/cli", binary)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := "This is an authorized isolated message qualification. Use only the Pot MCP message tools, no shell or file changes. List connections and read incoming messages. Find the synthetic qualification challenge ending " + nonce + ". Explicitly send exactly " + expected + " back to that established connection, using request_id live-reply. Acknowledge that incoming challenge as handled. Inspect your outgoing message receipt, repeating if needed until received. Finish only after these real tool operations. Do not open a browser."
	command := exec.CommandContext(ctx, cli, "exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--cd", workspace, "--json", prompt)
	command.Env = []string{"PATH=" + filepath.Dir(cli) + ":" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + home, "POT_CODEX_LIVE_PROXY_KEY=isolated-proxy", "POT_CODEX_LIVE_MCP_ROOT=" + hostRoot, "POT_CODEX_LIVE_MCP_HTTP=" + owner.URL, "POT_CODEX_LIVE_STANDALONE=1"}
	stdout, stderr := &codexLiveBuffer{limit: 1024 * 1024}, &codexLiveBuffer{limit: 64 * 1024}
	command.Stdout, command.Stderr = stdout, stderr
	t.Log("signed TCP preview/connect and Control challenge delivery passed; starting isolated real Codex CLI")
	if err = command.Run(); err != nil {
		codexLiveDiagnostics(stdout, stderr)
		t.Fatalf("isolated Codex CLI failed: %v; model_requests=%d stdout_bytes=%d stderr_bytes=%d", err, requests.Load(), stdout.Len(), stderr.Len())
	}
	if bytes.Contains(stdout.Bytes(), []byte(`"type":"command_execution"`)) || bytes.Contains(stdout.Bytes(), []byte(`"type":"file_change"`)) {
		t.Fatal("live CLI attempted an unrelated local action")
	}
	operationsMu.Lock()
	counts := map[string]int{}
	for _, operation := range operations {
		counts[operation.Operation]++
	}
	failedCount := failedOperations
	operationsMu.Unlock()
	t.Logf("real CLI exited: cli_requests=%d successful_MCP_operations=%v failed_MCP_operations=%d", cliRequests.Load(), counts, failedCount)
	for _, name := range []string{"connections", "messages", "send", "acknowledge", "receipt"} {
		if counts[name] == 0 {
			codexLiveDiagnostics(stdout, stderr)
			t.Fatal("live CLI did not call required MCP operation", name)
		}
	}
	var handled string
	var sentCount int
	if h.tasks.QueryRowContext(ctx, "SELECT state FROM agent_messages WHERE id=?", challenge.ID).Scan(&handled) != nil || handled != "processed" || h.tasks.QueryRowContext(ctx, "SELECT count(*) FROM agent_messages WHERE direction='outgoing' AND json_extract(envelope,'$.text')=?", expected).Scan(&sentCount) != nil || sentCount != 1 {
		codexLiveDiagnostics(stdout, stderr)
		t.Fatalf("CLI operations did not finish the authorized message: challenge_state=%s expected_replies=%d", handled, sentCount)
	}
	var reply protocol.Envelope
	awaitCodexLive(t, ctx, func() bool {
		items, err := pot.Mailbox.History(ctx, "main", 0)
		if err != nil {
			return false
		}
		for _, item := range items {
			if item.Message.From == h.identity.Address && item.Message.Text == expected {
				reply = item.Message
				return true
			}
		}
		return false
	})
	if reply.ReplyTo == nil || *reply.ReplyTo != challenge.ID || reply.ConversationID != challenge.ConversationID || reply.Hops != challenge.Hops+1 {
		t.Fatal("live reply lost its claimed processing parent")
	}
	awaitCodexLive(t, ctx, func() bool {
		var incoming, outgoing string
		var potState string
		var potModelCalls int
		if pot.Tasks.DB().QueryRowContext(ctx, "SELECT state FROM agent_messages WHERE id=?", reply.ID).Scan(&potState) == nil && (potState == "failed" || potState == "interrupted") {
			t.Fatal("real Pot model receiver did not complete", potState)
		}
		return h.tasks.QueryRowContext(ctx, "SELECT state FROM agent_messages WHERE id=?", challenge.ID).Scan(&incoming) == nil && incoming == "processed" && h.tasks.QueryRowContext(ctx, "SELECT state FROM agent_messages WHERE id=?", reply.ID).Scan(&outgoing) == nil && outgoing == "received" && potState == "processed" && pot.Tasks.DB().QueryRowContext(ctx, "SELECT count(*) FROM assistant_mail_models WHERE message=?", reply.ID).Scan(&potModelCalls) == nil && potModelCalls > 0
	})
	if cliRequests.Load() == 0 || potRequests.Load() == 0 || requests.Load() > 24 {
		t.Fatal("invalid live model request count")
	}
	evidence := map[string]any{"model": profile.Model, "reasoning_effort": profile.Params.ReasoningEffort, "protocol": provider.Protocol, "requests": requests.Load(), "cli_requests": cliRequests.Load(), "pot_requests": potRequests.Load(), "successful_mcp_operations": counts, "failed_mcp_operations": failedCount, "signed_tcp_preview_and_connection": true, "harness_seeded_control_challenge": true, "control_to_codex_processed_by_cli": true, "codex_reply_processed_by_pot_model": true, "codex_transport_receipt": "received", "causal_reply_preserved": true, "real_codex_cli": true, "app_chat_injection": false}
	if output := os.Getenv("POT_CODEX_LIVE_OUTPUT"); output != "" {
		if err = os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(evidence, "", "  ")
		if err = os.WriteFile(filepath.Join(output, "codex-message-live.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("real Codex CLI + Pot model receiver passed: model=%s protocol=%s requests=%d cli=%d pot=%d operations=%v", profile.Model, provider.Protocol, requests.Load(), cliRequests.Load(), potRequests.Load(), counts)
}

func TestCodexCLILiveMCPProcess(t *testing.T) {
	root, endpoint := os.Getenv("POT_CODEX_LIVE_MCP_ROOT"), os.Getenv("POT_CODEX_LIVE_MCP_HTTP")
	if root == "" || endpoint == "" {
		t.Skip("private live MCP subprocess only")
	}
	client, err := existingAgentHostClient(root)
	if err != nil {
		os.Exit(2)
	}
	target, err := url.Parse(endpoint)
	if err != nil || target.Scheme != "http" || !net.ParseIP(target.Hostname()).IsLoopback() {
		os.Exit(2)
	}
	client.client.Transport = codexLiveOwnerTransport{target: target, transport: http.DefaultTransport.(*http.Transport).Clone()}
	err = hostMCPServer(client).Run(context.Background(), &mcp.IOTransport{Reader: os.Stdin, Writer: os.Stdout, MaxLineLength: 128 * 1024})
	client.client.CloseIdleConnections()
	if err != nil && !errors.Is(err, io.EOF) {
		os.Exit(2)
	}
	os.Exit(0)
}

func codexLiveStandaloneSelection() bool {
	if os.Getenv("POT_CODEX_LIVE_STANDALONE") != "1" {
		return false
	}
	for _, argument := range os.Args[1:] {
		selection := strings.TrimPrefix(argument, "-test.run=")
		if selection == "^TestConfiguredCodexCLIAgentMessagesLive$" || selection == "^TestCodexCLILiveMCPProcess$" {
			return true
		}
	}
	return false
}

type codexLiveOwnerTransport struct {
	target    *url.URL
	transport *http.Transport
}

func (tr codexLiveOwnerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	forward := request.Clone(request.Context())
	forward.URL.Scheme, forward.URL.Host, forward.Host = tr.target.Scheme, tr.target.Host, hostListen
	return tr.transport.RoundTrip(forward)
}

func (tr codexLiveOwnerTransport) CloseIdleConnections() { tr.transport.CloseIdleConnections() }

type codexLiveFlusher struct{ http.ResponseWriter }

func (w codexLiveFlusher) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

type codexLiveBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *codexLiveBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.limit-b.Len() {
		p = p[:b.limit-b.Len()]
	}
	b.buffer.Write(p)
	return n, nil
}

func (b *codexLiveBuffer) Len() int      { return b.buffer.Len() }
func (b *codexLiveBuffer) Bytes() []byte { return b.buffer.Bytes() }

func TestCodexLiveOutputBufferIsBounded(t *testing.T) {
	buffer := &codexLiveBuffer{limit: 8}
	// LimitedReader makes io.Copy exercise destination interface detection;
	// embedding bytes.Buffer would promote ReadFrom and bypass the Write cap.
	n, err := io.Copy(buffer, io.LimitReader(strings.NewReader(strings.Repeat("x", 64)), 64))
	if err != nil || n != 64 || buffer.Len() != 8 {
		t.Fatal("live diagnostic output exceeded its memory cap", n, buffer.Len(), err)
	}
}

func codexLiveDiagnostics(stdout, stderr *codexLiveBuffer) {
	if output := os.Getenv("POT_CODEX_LIVE_OUTPUT"); output != "" {
		if os.MkdirAll(output, 0700) == nil {
			// The CLI only knows synthetic messages and a placeholder credential.
			os.WriteFile(filepath.Join(output, "codex-cli-failure.jsonl"), stdout.Bytes(), 0600)
			os.WriteFile(filepath.Join(output, "codex-cli-failure.stderr"), stderr.Bytes(), 0600)
		}
	}
}

func awaitCodexLive(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("live Agent Link condition timed out")
		case <-ticker.C:
		}
	}
}

func codexLiveProvider(t *testing.T, ctx context.Context, root string) (core.Profile, gateway.Provider, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "profiles.json"))
	if err != nil {
		t.Fatal("configured profile unavailable")
	}
	var config struct {
		Snapshot struct {
			Profiles []core.Profile `json:"profiles"`
		} `json:"snapshot"`
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Snapshot.Profiles) == 0 {
		t.Fatal("configured profile unavailable")
	}
	profile := config.Snapshot.Profiles[0]
	db, err := storage.OpenDatabaseReadOnly(filepath.Join(root, "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		t.Fatal("configured provider database unavailable")
	}
	defer db.Close()
	var metadata string
	if err = db.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", profile.Provider).Scan(&metadata); err != nil {
		t.Fatal("configured provider unavailable")
	}
	var provider gateway.Provider
	if json.Unmarshal([]byte(metadata), &provider) != nil || provider.Validate() != nil {
		t.Fatal("configured provider invalid")
	}
	store, err := secrets.OpenReadOnly(filepath.Join(root, "control.sqlite"), secrets.SystemKey{Account: "codex-cli-live"})
	if err != nil {
		t.Fatal("configured credentials unavailable")
	}
	defer store.Close()
	credential, err := store.Get(ctx, provider.SecretRef)
	if err != nil {
		t.Fatal("configured credential authentication failed")
	}
	return profile, provider, credential
}
