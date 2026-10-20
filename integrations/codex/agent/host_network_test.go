package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/peer"
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
func TestCodexTCPPeerListenerAndRestart(t *testing.T) {
	ctx := context.Background()
	h, root := testHost(t)
	address := freePeerAddress(t)
	endpoint := "tcp://" + address
	requestCtx, finishRequest := context.WithCancel(ctx)
	if err := h.configureEndpoint(requestCtx, endpoint); err != nil {
		finishRequest()
		h.close()
		t.Fatal(err)
	}
	finishRequest()
	session, err := peer.Dial(ctx, endpoint, peer.DialOptions{Identity: h.peerIdentity, PrivateKey: h.peerKey, Expected: &h.peerIdentity, Service: "owner", Auth: json.RawMessage(`{"token":"` + h.token + `"}`)})
	if err == nil {
		session.Close()
		h.close()
		t.Fatal("peer listener exposed owner service")
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
	foreign, _ := testHost(t)
	defer foreign.close()
	stranger, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", foreign.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.Close()
	if err = h.configureEndpoint(ctx, "tcp://"+stranger.Addr()); err == nil {
		t.Fatal("accepted another host identity")
	}
	var saved string
	if err = h.db.QueryRow("SELECT endpoint FROM codex_agent_host").Scan(&saved); err != nil || saved != endpoint {
		t.Fatal(saved, err)
	}
	if err = h.probeEndpoint(ctx, endpoint); err != nil {
		t.Fatal("failed configuration stopped original listener", err)
	}
	for _, invalid := range []string{"tcp://0.0.0.0:8793", "tcp://user:password@127.0.0.1:8793", "tcp://127.0.0.1:8793?token=secret"} {
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
	if err := h.configureEndpoint(ctx, "tcp://"+address); err == nil {
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
	if err := h.configureEndpoint(context.Background(), "tcp://"+address); err == nil {
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
	if _, err = h.db.Exec("UPDATE codex_agent_host SET endpoint=? WHERE singleton=1", "tcp://"+hostListen); err != nil {
		t.Fatal(err)
	}
	if err = h.startPeerNetwork(context.Background()); err != nil {
		t.Fatal("remote access failure stopped local host", err)
	}
	if h.networkError == "" {
		t.Fatal("missing network failure state")
	}
	if err = h.probeEndpoint(context.Background(), "tcp://"+hostPeerListen); err != nil {
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
