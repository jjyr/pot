package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/webui"
	webassets "github.com/jjyr/pot/web"
)

// Opt-in isolated visual fixture. This uses the real browser login component,
// synthetic credentials and a maintenance-only Pot; it never calls a model.
func TestMCPAccessBrowserFixture(t *testing.T) {
	path := os.Getenv("POT_MCP_BROWSER_FIXTURE_FILE")
	if path == "" {
		t.Skip("opt-in browser fixture")
	}
	backend, _ := linkBackendFixture(t)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	config := webui.NewConfig()
	config.Token = "fixture-mcp-browser-access-only"
	browser, e := webui.New(webui.Options{Origin: server.URL, Config: config, Upstream: webui.Upstream{URL: server.URL + "/control/v1/owner", Credential: "fixture-unused-upstream-credential", Client: http.DefaultClient}, Assets: webassets.FS()})
	if e != nil {
		t.Fatal(e)
	}
	defer browser.Close()
	access, e := backend.s.NewMCPAccess(MCPOAuthOptions{Origin: server.URL, LoginURL: "/mcp-connect.html", OwnerSession: browser.MCPOwnerSession}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer access.Close()
	for _, path := range []string{"/mcp", "/oauth/", "/.well-known/"} {
		mux.Handle(path, access)
	}
	mux.HandleFunc("/before/mcp-connect.html", http.NotFound)
	done := make(chan struct{}, 1)
	mux.HandleFunc("/fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	mux.Handle("/", browser)
	fixture := &mcpAccessFixture{http: server}
	client := fixture.registerClient(t, "http://127.0.0.1:9876/callback")
	values := fixture.authorizationValues(client, "http://127.0.0.1:9876/callback")
	raw, e := json.Marshal(map[string]string{"url": server.URL, "authorize": server.URL + "/oauth/authorize?" + values.Encode()})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
