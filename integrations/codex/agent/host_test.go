package agent

import (
	"context"
	"encoding/json"
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

	"crypto/ed25519"
	"crypto/rand"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type hostRoundTripper func(*http.Request) (*http.Response, error)

func (f hostRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAgentHostMCPDirectoryWithoutCompetingInboxReader(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	if err != nil || len(listed.Tools) != 5 {
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
	if !strings.Contains(string(raw), "default_assistant_id") || strings.Contains(string(raw), h.token) {
		t.Fatal("bad directory result", string(raw))
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
	return h, root
}
func TestAgentHostRestartStableIdentityAndMailbox(t *testing.T) {
	ctx := context.Background()
	h, root := testHost(t)
	identity, owner := h.identity, h.token
	peer := protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "helper"}
	outgoing, err := h.mailbox.send(ctx, peer, "request-1", "reference")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := h.mailbox.send(ctx, peer, "request-1", "reference")
	if err != nil || outgoing.ID != retry.ID {
		t.Fatal(retry, err)
	}
	if _, err = h.mailbox.send(ctx, peer, "request-1", "changed"); err == nil {
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
	h.close()
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if h.identity != identity || h.token != owner {
		t.Fatal("identity changed")
	}
	messages, err := h.mailbox.messages(ctx)
	if err != nil || len(messages) != 1 || messages[0].State != "queued" {
		t.Fatal(messages, err)
	}
	if err = h.backend.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	messages, err = h.mailbox.messages(ctx)
	if err != nil || messages[0].State != "read" {
		t.Fatal(messages, err)
	}
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
	if value["self"].(map[string]any)["status"] != "needs_access" {
		t.Fatal(value)
	}
	if value["self"].(map[string]any)["node_id"] != h.identity.Address.NodeID || value["peer_identity"].(map[string]any)["node_id"] != h.peerIdentity.NodeID || strings.Contains(w.Body.String(), `"control_id"`) {
		t.Fatal("message host exposed a Control identity", w.Body.String())
	}
}

func TestAgentHostPreviewDoesNotConnectOrConsumeInvitation(t *testing.T) {
	ctx := context.Background()
	remote, _ := testHost(t)
	defer remote.close()
	local, _ := testHost(t)
	defer local.close()
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", remote.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := "tcp://" + listener.Addr()
	invitation, err := remote.links.Invite(ctx, "codex", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	preview := func(address string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]string{"action": "preview", "address": address})
		r := httptest.NewRequest(http.MethodPost, "http://"+hostListen+"/api/v1/agent-links", strings.NewReader(string(payload)))
		r.Host = hostListen
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Authorization", "Bearer "+local.token)
		w := httptest.NewRecorder()
		local.handler().ServeHTTP(w, r)
		return w
	}
	w := preview(invitation.Address)
	var result struct {
		Peer struct {
			ID     string `json:"id"`
			NodeID string `json:"node_id"`
			Kind   string `json:"kind"`
			Name   string `json:"name"`
		} `json:"peer"`
		Endpoint    string `json:"endpoint"`
		Fingerprint string `json:"fingerprint"`
		ExpiresAt   int64  `json:"expires_at_ms"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if result.Peer.ID != "codex" || result.Peer.NodeID != remote.identity.Address.NodeID || result.Peer.Kind != "codex" || result.Peer.Name != remote.identity.Name || result.Endpoint != endpoint || result.Fingerprint == "" || result.ExpiresAt != invitation.ExpiresAt.UnixMilli() {
		t.Fatal(result)
	}
	if strings.Contains(w.Body.String(), "#") || strings.Contains(w.Body.String(), local.token) {
		t.Fatal("preview exposed invitation or owner credential")
	}
	for _, host := range []*agentHost{local, remote} {
		connections, err := host.links.List(ctx)
		if err != nil || len(connections) != 0 {
			t.Fatal("preview created a connection", connections, err)
		}
	}
	if invalid := preview("https://untrusted.example"); invalid.Code != 400 {
		t.Fatal("invalid address accepted", invalid.Code)
	}
	if _, err = local.links.Connect(ctx, "codex", invitation.Address); err != nil {
		t.Fatal("preview consumed invitation", err)
	}
}

func TestAgentHostPeerDeliveryRemainsUnreadUntilCodexReads(t *testing.T) {
	ctx := context.Background()
	a, _ := testHost(t)
	defer a.close()
	b, _ := testHost(t)
	defer b.close()
	remote, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", a.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	endpoint := "tcp://" + remote.Addr()
	invitation, err := a.links.Invite(ctx, "codex", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := b.links.Connect(ctx, "codex", invitation.Address)
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "http://"+hostListen+"/api/v1/agent-links", nil)
	get.Host = hostListen
	get.RemoteAddr = "127.0.0.1:12345"
	get.Header.Set("Authorization", "Bearer "+b.token)
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, get)
	var snapshot struct {
		Directory agentlink.Directory `json:"directory"`
		Items     []struct {
			Assistants         []agentlink.Identity `json:"assistants"`
			DefaultAssistantID string               `json:"default_assistant_id"`
			Revision           int64                `json:"revision"`
			Peer               struct {
				NodeID  string `json:"node_id"`
				Address string `json:"address"`
			}
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || len(snapshot.Items) != 1 {
		t.Fatal(w.Body.String())
	}
	if snapshot.Directory.NodeID != b.identity.Address.NodeID || snapshot.Directory.DefaultAssistantID != "codex" || len(snapshot.Directory.Assistants) != 1 || snapshot.Directory.Assistants[0].Address != b.identity.Address {
		t.Fatal("local node directory mismatch", w.Body.String())
	}
	item := snapshot.Items[0]
	if item.DefaultAssistantID != "codex" || len(item.Assistants) != 1 || item.Assistants[0].Address != a.identity.Address || item.Revision != connection.CreatedAt.UnixMilli() || item.Peer.NodeID != a.identity.Address.NodeID || item.Peer.Address != endpoint {
		t.Fatal("connected node directory mismatch", w.Body.String())
	}
	outgoing, err := a.mailbox.send(ctx, b.identity.Address, "from-a", "peer reference")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.links.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	messages, err := b.mailbox.messages(ctx)
	if err != nil || len(messages) != 1 || messages[0].Message.ID != outgoing.ID || messages[0].State != "queued" {
		t.Fatal(messages, err)
	}
	// Durable transport delivery must not pretend that a Codex model read it.
	if err = b.backend.receiveMessages(ctx); err != nil {
		t.Fatal(err)
	}
	messages, err = b.mailbox.messages(ctx)
	if err != nil || messages[0].State != "read" {
		t.Fatal(messages, err)
	}
	if _, err = b.mailbox.send(ctx, a.identity.Address, "from-b", "authorized reply"); err != nil {
		t.Fatal(err)
	}
	if err = b.links.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	messages, err = a.mailbox.messages(ctx)
	if err != nil || len(messages) != 1 || messages[0].Message.Text != "authorized reply" {
		t.Fatal(messages, err)
	}
}

func TestAgentHostConfigurePinsTCPIdentity(t *testing.T) {
	ctx := context.Background()
	h, _ := testHost(t)
	defer h.close()
	own, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", h.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()
	endpoint := "tcp://" + own.Addr()
	configure := func(endpoint string) int {
		payload, _ := json.Marshal(map[string]string{"action": "configure", "mode": "address", "endpoint": endpoint, "listen": hostPeerListen})
		r := httptest.NewRequest(http.MethodPost, "http://"+hostListen+"/api/v1/agent-links", strings.NewReader(string(payload)))
		r.Host = hostListen
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Authorization", "Bearer "+h.token)
		w := httptest.NewRecorder()
		h.handler().ServeHTTP(w, r)
		return w.Code
	}
	if status := configure(endpoint); status != 200 {
		t.Fatal(status)
	}
	foreign, _ := testHost(t)
	defer foreign.close()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clear(foreign.peerKey)
	foreign.peerKey = key
	foreign.peerIdentity = peer.NewIdentity(h.peerIdentity.NodeID, pub)
	foreign.peerRegistry = linkhost.NewRegistry(foreign.peerIdentity, foreign.peerKey)
	stranger, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", foreign.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.Close()
	for _, invalid := range []string{"tcp://" + stranger.Addr(), "http://127.0.0.1:8793", "https://agent.example", "tcp://8.8.8.8:8793", "tcp://user:password@127.0.0.1:8793", "tcp://127.0.0.1:8793?token=secret"} {
		if status := configure(invalid); status != 400 {
			t.Fatal("unverified peer accepted", status)
		}
		var saved string
		if err = h.db.QueryRow("SELECT endpoint FROM codex_agent_host").Scan(&saved); err != nil || saved != endpoint {
			t.Fatal("invalid peer replaced verified address", saved, err)
		}
	}
}
