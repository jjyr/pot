package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type hostRoundTripper func(*http.Request) (*http.Response, error)

func (f hostRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAgentHostMCPDirectoryWithoutCompetingInboxReader(t *testing.T) {
	t.Setenv("POT_AGENT_ASSISTANT_ID", "codex")
	h, _ := testHost(t)
	defer h.close()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
	defer cancel()
	client := hostClient{token: h.token, client: &http.Client{Transport: hostRoundTripper(func(r *http.Request) (*http.Response, error) {
		r.RemoteAddr = "127.0.0.1:12345"
		r.Host = hostListen
		w := httptest.NewRecorder()
		h.handler().ServeHTTP(w, r)
		return w.Result(), nil
	})}}
	a, b := mcp.NewInMemoryTransports()
	go hostMCPServer(client).Run(ctx, a)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "codex-host-test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 3 {
		t.Fatal(listed, err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "pot_messages" || tool.Name == "pot_message_ack" || strings.Contains(tool.Name, "invite") {
			t.Fatal("session inbox or invitation authority exposed", tool.Name)
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "pot_assistants", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "default_target") || strings.Contains(string(raw), h.token) {
		t.Fatal("bad directory result", string(raw))
	}
	updated, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "pot_update_profile", Arguments: map[string]any{"assistant_id": "codex", "name": "MCP assistant"}})
	if err != nil || updated.IsError {
		t.Fatal(updated, err)
	}
	saved, err := h.backend.resolve(ctx, "codex")
	if err != nil || saved.Name != "MCP assistant" {
		t.Fatal("MCP rename was not saved", err)
	}
	blocked, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "pot_update_profile", Arguments: map[string]any{"assistant_id": "another", "name": "Wrong identity"}})
	if err != nil || !blocked.IsError {
		t.Fatal("bound MCP server accepted a different assistant", blocked, err)
	}
	for _, name := range []string{"pot_open_connections", "pot_connections", "pot_send"} {
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if e == nil && !result.IsError {
			t.Fatal("legacy connection tool remains callable", name)
		}
	}
}

func TestAgentHostDaemonProcess(t *testing.T) {
	if root := os.Getenv("POT_HOST_TEST_DAEMON"); root != "" {
		os.WriteFile(filepath.Join(root, "test.pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
		if err := agentServe(root); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}
func TestAgentHostConcurrentEnsureAndForeignPort(t *testing.T) {
	listener, err := net.Listen("tcp", hostListen)
	if err != nil {
		t.Skip("host port already occupied")
	}
	listener.Close()
	original := hostDaemonCommand
	defer func() { hostDaemonCommand = original }()
	var started atomic.Int32
	hostDaemonCommand = func(binary, root string) *exec.Cmd {
		started.Add(1)
		cmd := exec.Command(binary, "-test.run=TestAgentHostDaemonProcess")
		cmd.Env = append(os.Environ(), "POT_HOST_TEST_DAEMON="+root)
		return cmd
	}
	root := filepath.Join(t.TempDir(), "host")
	var wg sync.WaitGroup
	errorsCh := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := ensureAgentHost(root); errorsCh <- err }()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "test.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	process, _ := os.FindProcess(pid)
	defer process.Signal(syscall.SIGTERM)
	if started.Load() != 1 {
		t.Fatal("duplicate daemon starts", started.Load())
	}
	process.Signal(syscall.SIGTERM)
	process.Wait()
	// A foreign service, even on the expected port, is never reused as an Agent Link host.
	listener, err = net.Listen("tcp", hostListen)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"version":"foreign"}`)) })}
	go server.Serve(listener)
	defer server.Close()
	_, err = ensureAgentHost(filepath.Join(t.TempDir(), "other"))
	if err == nil || !strings.Contains(err.Error(), "port_in_use_or_version_mismatch") {
		t.Fatal(err)
	}
}

func testHost(t *testing.T) (*agentHost, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "host")
	h, err := openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for name, db := range map[string]*sql.DB{"control": h.db, "tasks": h.tasks} {
			if stats := db.Stats(); stats.OpenConnections != 0 {
				t.Errorf("host close left %s database connections active: %+v", name, stats)
			}
		}
	})
	return h, root
}
func TestAgentHostRestartStableIdentityAndMailbox(t *testing.T) {
	ctx := context.Background()
	h, root := testHost(t)
	identity, owner := h.identity, h.token
	peer := protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "helper"}
	outgoing, err := h.mailbox.sendFrom(ctx, h.identity.Address, peer, "request-1", "reference", "")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := h.mailbox.sendFrom(ctx, h.identity.Address, peer, "request-1", "reference", "")
	if err != nil || outgoing.ID != retry.ID {
		t.Fatal(retry, err)
	}
	if _, err = h.mailbox.sendFrom(ctx, h.identity.Address, peer, "request-1", "changed", ""); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	incoming := outgoing
	incoming.ID = "incoming-1"
	incoming.From = peer
	incoming.To = identity.Address
	if _, err = h.mailbox.Deliver(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	if _, err = h.mailbox.Deliver(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	wrong := incoming
	wrong.ID = "wrong-target"
	wrong.To.AgentID = "other"
	if _, err = h.mailbox.Deliver(ctx, wrong); err == nil {
		t.Fatal("wrong target accepted")
	}
	wrong = incoming
	wrong.ID = "wrong-user-target"
	wrong.To.Kind = "user"
	wrong.ReplyTo = &outgoing.ID
	if err = wrong.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err = h.mailbox.Deliver(ctx, wrong); err == nil {
		t.Fatal("user address consumed as a Codex assistant")
	}
	h.close()
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if h.identity != identity || h.token != owner {
		t.Fatal("identity changed")
	}
	assertIncomingEnvelope(t, h.tasks, incoming, "queued")
	if err = h.backend.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	assertIncomingEnvelope(t, h.tasks, incoming, "read")
	if _, err = h.tool(ctx, hostToolInput{Operation: "acknowledge", MessageID: incoming.ID}); err == nil {
		t.Fatal("manual consumer can steal a session message")
	}

	pending, err := h.mailbox.PendingNode(ctx, identity.Address.NodeID, peer.NodeID)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	pending, err = h.mailbox.PendingNode(ctx, identity.Address.NodeID, "other")
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if err = h.mailbox.Acknowledge(ctx, protocol.Receipt{Message: outgoing}); err != nil {
		t.Fatal(err)
	}
	pending, err = h.mailbox.PendingNode(ctx, identity.Address.NodeID, peer.NodeID)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if _, err = os.Stat(filepath.Join(root, "local-pair.json")); !os.IsNotExist(err) {
		t.Fatal("bootstrap depends on a pair file")
	}
}
func TestAgentHostOwnerIsolation(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	handler := h.handler()
	for _, tc := range []struct {
		name, host, remote, token, path, origin string
		want                                    int
	}{
		{"owner", hostListen, "127.0.0.1:12345", h.token, "/api/v1/agent-host/health", "", 200},
		{"missing owner", hostListen, "127.0.0.1:12345", "", "/api/v1/agent-host/health", "", 401},
		{"tailnet proxy", "agent.example", "127.0.0.1:12345", h.token, "/api/v1/agent-host/health", "", 403},
		{"remote", hostListen, "100.64.0.2:12345", h.token, "/api/v1/agent-host/health", "", 403},
		{"origin", hostListen, "127.0.0.1:12345", h.token, "/api/v1/agent-host/health", "https://untrusted.invalid", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+tc.path, nil)
			req.Host = tc.host
			req.RemoteAddr = tc.remote
			req.Header.Set("Authorization", "Bearer "+tc.token)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+hostListen+"/api/v1/agent-links?assistant_id=codex", nil)
	req.Host = hostListen
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+h.token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var value map[string]any
	if json.Unmarshal(w.Body.Bytes(), &value) != nil || w.Code != 200 || strings.Contains(w.Body.String(), h.token) {
		t.Fatal(w.Code, w.Body.String())
	}
	if value["identity"] == nil {
		t.Fatal(value)
	}
	if value["identity"].(map[string]any)["address"].(map[string]any)["node_id"] != h.identity.Address.NodeID || strings.Contains(w.Body.String(), `"control_id"`) {
		t.Fatal("message host exposed a Control identity", w.Body.String())
	}
}

func assertIncomingEnvelope(t *testing.T, db *sql.DB, envelope protocol.Envelope, want string) {
	t.Helper()
	var raw, state string
	if err := db.QueryRow("SELECT envelope,state FROM agent_messages WHERE id=? AND direction='incoming'", envelope.ID).Scan(&raw, &state); err != nil {
		t.Fatal(err)
	}
	if raw != encoded(envelope) || state != want {
		t.Fatal("incoming envelope/state changed", raw, state, want)
	}
}

func TestCodexMailboxRejectsDirectUserInputs(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	e := protocol.Envelope{ID: "user-input", Kind: "message", From: protocol.AgentRef{NodeID: "pot", Kind: "user", AgentID: "user-session"}, To: h.identity.Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "direct chat"}
	if _, err := h.mailbox.Deliver(context.Background(), e); !errors.Is(err, agentlink.ErrUnsupported) {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://"+hostListen+"/api/v1/agent-host/messages", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Authorization", "Bearer "+h.token)
	response := httptest.NewRecorder()
	h.handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatal("manual inbox API remains exposed", response.Code, response.Body.String())
	}
	var count int
	if err := h.tasks.QueryRow("SELECT COUNT(*) FROM agent_messages").Scan(&count); err != nil || count != 0 {
		t.Fatal("direct user input was accepted", count, err)
	}
	for _, kind := range []string{"assistant", "task"} {
		e.ID, e.From.Kind = "input-"+kind, kind
		if _, err := h.mailbox.Deliver(context.Background(), e); err != nil {
			t.Fatal(kind, err)
		}
	}
}
