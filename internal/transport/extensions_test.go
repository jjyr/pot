package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
	webassets "github.com/jjyr/pot/web"
)

func extensionApplication(t *testing.T) *Application {
	t.Helper()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	plugins, _ := filepath.Abs("../../plugins")
	skills, _ := filepath.Abs("../../skills")
	s, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: plugins, BuiltinSkills: skills})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return &Application{Control: s, RuntimeID: "local", BootID: "boot", Name: "Pot"}
}
func TestExtensionsOwnerRoutesAndBrowserAllowlist(t *testing.T) {
	app := extensionApplication(t)
	ctx := context.Background()
	value, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/extensions"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if !strings.Contains(string(raw), "data:image/svg+xml;base64,") {
		t.Fatal("packaged icons missing")
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/extensions"}, {"POST", "/api/v1/extensions"}, {"POST", "/api/v1/extensions/mcp"}, {"POST", "/api/v1/extensions/mcp/oauth/begin"}, {"POST", "/api/v1/extensions/mcp/oauth/complete"}, {"GET", "/api/v1/extensions/skills/abc"}} {
		if !webui.Allowed(route.method, route.path) {
			t.Fatal("browser route absent", route)
		}
	}
	for _, path := range []string{"/api/v1/extensions/delete", "/api/v1/extensions/skills/abc/other", "/api/v1/extensions/mcp/secret"} {
		if webui.Allowed("POST", path) {
			t.Fatal("unexpected browser route", path)
		}
	}
	for _, body := range []string{`{"action":"toggle","kind":"plugins","id":"github","revision":0,"enabled":false,"unexpected":1}`, `{"action":"remove","kind":"plugins","id":"github","revision":0}`} {
		if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/extensions", Body: json.RawMessage(body)}); err == nil {
			t.Fatal("invalid mutation accepted", body)
		}
	}
	if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/extensions", Body: json.RawMessage(`{"action":"toggle","kind":"plugins","id":"github","revision":0,"enabled":false}`)}); err != nil {
		t.Fatal(err)
	}
}

// Opt-in browser verification uses the real authenticated web adapter and Control
// APIs with a disposable root; it never calls a model or accesses product data.
func TestExtensionsBrowserFixture(t *testing.T) {
	file := os.Getenv("POT_EXTENSIONS_FIXTURE_URL_FILE")
	if file == "" {
		t.Skip("opt-in browser fixture")
	}
	app := extensionApplication(t)
	ctx := context.Background()
	app.Control.Registry.Create(ctx, "fixture-pot", "Pot")
	_, err := app.Control.ManageMCP(ctx, control.MCPCommand{Action: "save", Server: control.MCPServer{ID: "docs", Name: "Team Docs", Transport: "http", URL: "https://docs.example.invalid/mcp", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	app.Control.ManageExtension(ctx, "main", control.ExtensionCommand{Action: "create", Kind: "skills", Name: "research-note", Content: "---\nname: research-note\ndescription: Organize source material into clear research notes.\n---\nKeep original links and uncertainties."})
	upstream := httptest.NewServer(Handler(app, "private-fixture-control"))
	defer upstream.Close()
	var handler http.Handler
	done := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/stop" {
			select {
			case done <- struct{}{}:
			default:
			}
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	origin := "http://" + server.Listener.Addr().String()
	config := webui.NewConfig()
	handler, err = webui.New(webui.Options{Origin: origin, Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "private-fixture-control", Client: upstream.Client(), ControlID: "local"}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	if err = os.WriteFile(file, []byte(origin+"/#access_token="+config.Token), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("fixture expired")
	}
}
