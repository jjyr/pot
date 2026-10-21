package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
)

func TestDiscoveredAddressesPreferTailscaleThenPublic(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	h.discoveryAt = time.Now()
	h.discovered = []hostAddress{
		{Endpoint: "tcp://127.0.0.1:8793", Kind: "loopback"},
		{Endpoint: "tcp://192.168.1.4:8793", Kind: "private"},
		{Endpoint: "tcp://203.0.113.10:8793", Kind: "public"},
		{Endpoint: "tcp://100.88.0.4:8793", Kind: "tailscale", Interface: "utun"},
		{Endpoint: "tcp://codex.tail.ts.net:8793", Kind: "tailscale"},
	}
	if _, err := h.db.Exec("INSERT INTO codex_agent_endpoints VALUES('tcp://saved.example:8793')"); err != nil {
		t.Fatal(err)
	}
	got := h.addresses(context.Background())
	want := []string{"tcp://codex.tail.ts.net:8793", "tcp://100.88.0.4:8793", "tcp://203.0.113.10:8793", "tcp://192.168.1.4:8793", "tcp://127.0.0.1:8793", "tcp://saved.example:8793"}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for index, address := range got {
		if address.Endpoint != want[index] {
			t.Fatalf("rank %d: %s", index, address.Endpoint)
		}
	}
}

func TestAddressSelectionRetainsMultipleListenersAndRecovers(t *testing.T) {
	h, root := testHost(t)
	endpoints := []hostAddress{{Endpoint: "tcp://" + freePeerAddress(t), Kind: "loopback"}, {Endpoint: "tcp://" + freePeerAddress(t), Kind: "private"}}
	h.discover = func(context.Context) []hostAddress { return endpoints }
	ctx := context.Background()
	_ = h.addresses(ctx)
	for _, address := range endpoints {
		listener, err := net.Listen("tcp", strings.TrimPrefix(address.Endpoint, "tcp://"))
		if err != nil {
			h.close()
			t.Fatal("discovery bound an interface", err)
		}
		listener.Close()
	}
	for _, address := range endpoints {
		endpoint, err := h.selectAddress(ctx, address.Endpoint)
		if err != nil || endpoint != address.Endpoint {
			h.close()
			t.Fatal(endpoint, err)
		}
		if err = h.probeEndpoint(ctx, endpoint); err != nil {
			h.close()
			t.Fatal(err)
		}
	}
	if _, err := h.selectAddress(ctx, "tcp://8.8.8.8:8793"); err == nil {
		h.close()
		t.Fatal("undiscovered endpoint admitted")
	}
	h.close()
	h, err := openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if err = h.restoreAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	for _, address := range endpoints {
		if err = h.probeEndpoint(ctx, address.Endpoint); err != nil {
			t.Fatal("selected listener not restored", err)
		}
	}
}

func TestSelectedListenerIsClosedWhenPersistenceFails(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	address := freePeerAddress(t)
	h.db.Close()
	if err := h.enableAddress(context.Background(), "tcp://"+address); err == nil {
		t.Fatal("closed store accepted an address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed persistence left a listener", err)
	}
	listener.Close()
}

func TestTailscaleAddressConnectsWithoutServePermission(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	binary := filepath.Join(t.TempDir(), "tailscale")
	script := `#!/bin/sh
if [ "$1" = status ]; then echo '{"BackendState":"Running","Self":{"DNSName":"localhost."}}'; exit 0; fi
if [ "$2" = status ]; then echo '{}'; exit 0; fi
echo mutation > "$0.mutated"
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary))
	address, err := linkhost.DiscoverTailscaleAddress(context.Background(), binary, freePeerAddress(t))
	if err != nil {
		t.Fatal(err)
	}
	h.discover = func(context.Context) []hostAddress { return []hostAddress{address} }
	if _, err = h.selectAddress(context.Background(), address.Endpoint); err != nil {
		t.Fatal("direct Tailscale listener required Serve permission", err)
	}
	if err = h.probeEndpoint(context.Background(), address.Endpoint); err != nil {
		t.Fatal("direct listener did not authenticate", err)
	}
	if _, err = os.Stat(binary + ".mutated"); !os.IsNotExist(err) {
		t.Fatal("address selection attempted to modify Tailscale")
	}
}

func TestInvitationUsesSelectedAddress(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	endpoint := "tcp://" + freePeerAddress(t)
	h.discover = func(context.Context) []hostAddress { return []hostAddress{{Endpoint: endpoint, Kind: "loopback"}} }
	body, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	request := httptest.NewRequest("POST", "http://"+hostListen+"/api/v1/agent-links/invitation", strings.NewReader(string(body)))
	request.Host = hostListen
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer "+h.token)
	response := httptest.NewRecorder()
	h.handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var invitation struct{ Address string }
	if err := json.Unmarshal(response.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(invitation.Address)
	if err != nil {
		t.Fatal(err)
	}
	if "tcp://"+parsed.Host != endpoint {
		t.Fatal("invitation did not bind selected endpoint")
	}
	var saved string
	if err = h.db.QueryRow("SELECT endpoint FROM codex_agent_endpoints").Scan(&saved); err != nil || saved != endpoint {
		t.Fatal(saved, err)
	}
}

func TestBrowserPageCredentialIsScopedAndExpires(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	h.discover = func(context.Context) []hostAddress { return []hostAddress{} }
	page := h.browserPage()
	parsed, _ := url.Parse(page["url"].(string))
	token := parsed.Fragment
	if token == "" || token == h.token {
		t.Fatal("durable owner credential exposed")
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/v1/agent-links", 200}, {"/api/v1/agent-host/health", 401}, {"/api/v1/agent-host/browser", 401}, {"/api/v1/agent-host/assistants", 401},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://"+hostListen+tc.path, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		request.Host = hostListen
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		h.handler().ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatal(tc.path, response.Code)
		}
	}
	h.browserTokens[token] = time.Now().Add(-time.Second)
	request := httptest.NewRequest(http.MethodGet, "http://"+hostListen+"/api/v1/agent-links", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if h.browserOwner(request) {
		t.Fatal("expired page access accepted")
	}
}
