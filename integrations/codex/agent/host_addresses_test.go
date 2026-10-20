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
)

func TestDiscoveredInterfacesIncludePublicAndExcludeUnusableAddresses(t *testing.T) {
	devices := []net.Interface{{Name: "en0", Flags: net.FlagUp}, {Name: "down"}, {Name: "utun", Flags: net.FlagUp}, {Name: "lo0", Flags: net.FlagUp | net.FlagLoopback}}
	got := interfaceAddresses(devices, func(device net.Interface) ([]net.Addr, error) {
		var result []net.Addr
		for _, value := range map[string][]string{"en0": {"192.168.1.4/24", "192.168.1.4/24", "203.0.113.10/24", "2001:db8::10/64", "fe80::1/64", "169.254.1.1/16", "224.0.0.1/24", "fd00::4/64"}, "down": {"10.0.0.2/24"}, "utun": {"100.88.0.4/32", "fd7a:115c:a1e0::4/64"}, "lo0": {"127.0.0.1/8"}}[device.Name] {
			_, prefix, _ := net.ParseCIDR(value)
			prefix.IP = net.ParseIP(strings.Split(value, "/")[0])
			result = append(result, prefix)
		}
		return result, nil
	})
	want := map[string]string{"tcp://127.0.0.1:8793": "loopback", "tcp://192.168.1.4:8793": "private", "tcp://[fd00::4]:8793": "private", "tcp://100.88.0.4:8793": "tailscale", "tcp://[fd7a:115c:a1e0::4]:8793": "tailscale", "tcp://203.0.113.10:8793": "public", "tcp://[2001:db8::10]:8793": "public"}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for _, item := range got {
		if want[item.Endpoint] != item.Kind {
			t.Fatal(item)
		}
	}
}

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

func TestTailscaleDiscoveryNeverChangesServe(t *testing.T) {
	for _, test := range []struct {
		config   string
		setup    bool
		endpoint string
	}{
		{`{"TCP":{}}`, true, "tcp://codex.tail.ts.net:8793"},
		{`{"TCP":{"443":{"TCPForward":"127.0.0.1:8793"}}}`, false, "tcp://codex.tail.ts.net:443"},
	} {
		binary := filepath.Join(t.TempDir(), "tailscale")
		script := `#!/bin/sh
if [ "$1" = status ]; then echo '{"BackendState":"Running","Self":{"DNSName":"codex.tail.ts.net."}}'; exit 0; fi
if [ "$1" = serve ] && [ "$2" = status ]; then cat "$0.config"; exit 0; fi
echo mutation > "$0.mutated"
exit 1
`
		os.WriteFile(binary, []byte(script), 0700)
		os.WriteFile(binary+".config", []byte(test.config), 0600)
		got, err := discoverTailscaleAddress(context.Background(), binary)
		if err != nil || got.Endpoint != test.endpoint || got.RequiresSetup != test.setup {
			t.Fatal(got, err)
		}
		if _, err = os.Stat(binary + ".mutated"); !os.IsNotExist(err) {
			t.Fatal("discovery modified Tailscale")
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
