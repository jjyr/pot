package bridge

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
)

func freePeerAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}
func TestCodexPrivateHTTPPeerListenerAndRestart(t *testing.T) {
	ctx := context.Background()
	h, root := testHost(t)
	address := freePeerAddress(t)
	endpoint := "http://" + address
	if err := h.configureEndpoint(ctx, endpoint); err != nil {
		h.close()
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, endpoint+"/api/v1/agent-host/health", nil)
	request.Header.Set("Authorization", "Bearer "+h.token)
	response, err := agentlink.NewHTTPClient(nil).Do(request)
	if err != nil {
		h.close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		h.close()
		t.Fatal("peer listener exposed owner API")
	}
	if err = h.probeEndpoint(ctx, endpoint); err != nil {
		h.close()
		t.Fatal(err)
	}
	h.close()
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	// Restoring the persisted endpoint recreates only the message listener.
	h.accessMu.Lock()
	candidate, err := h.preparePeer(ctx, endpoint)
	h.directPeer = candidate
	h.accessMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = h.probeEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"control_id": "someone-else"})
	}))
	defer foreign.Close()
	if err = h.configureEndpoint(ctx, foreign.URL); err == nil {
		t.Fatal("accepted another host's identity")
	}
	var saved string
	if err = h.db.QueryRow("SELECT endpoint FROM codex_agent_host").Scan(&saved); err != nil || saved != endpoint {
		t.Fatal(saved, err)
	}
	if err = h.probeEndpoint(ctx, endpoint); err != nil {
		t.Fatal("failed configuration stopped original listener", err)
	}
	for _, invalid := range []string{"http://8.8.8.8:8793", "http://user:password@127.0.0.1:8793", "http://127.0.0.1:8793?token=secret"} {
		if err = h.configureEndpoint(ctx, invalid); err == nil {
			t.Fatal("invalid private network endpoint accepted")
		}
	}
}

func TestCodexFailedProbeClosesCandidateListener(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	address := freePeerAddress(t)
	if err := h.configureEndpoint(ctx, "http://"+address); err == nil {
		t.Fatal("cancelled configuration unexpectedly succeeded")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed candidate retained listener", err)
	}
	listener.Close()
	var endpoint string
	if err = h.db.QueryRow("SELECT endpoint FROM codex_agent_host").Scan(&endpoint); err != nil || endpoint != "" {
		t.Fatal(endpoint, err)
	}
}

func TestCodexFailedPersistenceClosesCandidateListener(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	address := freePeerAddress(t)
	h.db.Close()
	if err := h.configureEndpoint(context.Background(), "http://"+address); err == nil {
		t.Fatal("closed storage accepted configuration")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed persistence left candidate running", err)
	}
	listener.Close()
}

func TestCodexNetworkRestoreFailureKeepsLocalHostUsable(t *testing.T) {
	available, err := net.Listen("tcp", hostPeerListen)
	if err != nil {
		t.Skip("peer preview port already in use")
	}
	available.Close()
	h, _ := testHost(t)
	defer h.close()
	if _, err = h.db.Exec("UPDATE codex_agent_host SET endpoint=? WHERE singleton=1", "http://"+hostListen); err != nil {
		t.Fatal(err)
	}
	if err = h.startPeerNetwork(context.Background()); err != nil {
		t.Fatal("remote access failure stopped local host", err)
	}
	if h.networkError == "" {
		t.Fatal("missing network failure state")
	}
	if err = h.probeEndpoint(context.Background(), "http://"+hostPeerListen); err != nil {
		t.Fatal("loopback peer unavailable", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://"+hostListen+"/api/v1/agent-host/health", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = hostListen
	request.Header.Set("Authorization", "Bearer "+h.token)
	response := httptest.NewRecorder()
	h.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("owner repair UI unavailable", response.Code)
	}
}
