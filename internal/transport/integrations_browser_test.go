package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/integrations"
	webassets "github.com/jjyr/pot/web"
)

type browserAuthorization struct {
	done chan struct{}
	once sync.Once
}
type browserAdapter struct {
	mu     sync.Mutex
	latest *browserAuthorization
}

func (a *browserAdapter) Descriptor() integrations.Descriptor {
	return integrations.Descriptor{ID: "github", Name: "GitHub", LoginAvailable: true}
}
func (a *browserAdapter) Begin(context.Context) (integrations.Challenge, error) {
	state := &browserAuthorization{done: make(chan struct{})}
	a.mu.Lock()
	a.latest = state
	a.mu.Unlock()
	return integrations.Challenge{URL: "https://github.com/login/device", UserCode: "TEST-CODE", ExpiresAt: time.Now().Add(time.Minute), Private: state}, nil
}
func (a *browserAdapter) Authorize(ctx context.Context, c integrations.Challenge) (integrations.Grant, error) {
	select {
	case <-ctx.Done():
		return integrations.Grant{}, ctx.Err()
	case <-c.Private.(*browserAuthorization).done:
	}
	return integrations.Grant{Identity: integrations.Identity{ID: "123", Username: "fixture-user"}, Credential: integrations.Credential{AccessToken: "browser-fixture-secret"}}, nil
}

// Opt-in real UI test. Only the upstream authorization provider is simulated.
// Run with POT_TEST_INTEGRATIONS_BROWSER=1 and the cached Playwright CLI installed.
func TestIntegrationBrowserAndScreenshots(t *testing.T) {
	if os.Getenv("POT_TEST_INTEGRATIONS_BROWSER") != "1" {
		t.Skip("opt-in real browser integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	key, _ := secrets.NewMemoryKey(make([]byte, 32))
	adapter := &browserAdapter{}
	runtime, err := product.Open(ctx, product.Options{Root: filepath.Join(t.TempDir(), "runtime"), DisableMetadataRefresh: true, Control: control.Options{Keys: key, IntegrationAdapters: []integrations.Adapter{adapter}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	app := &Application{Control: runtime.Control, RuntimeID: "fixture-control", BootID: "fixture-boot", Name: "Local"}
	upstream := httptest.NewServer(Handler(app, "fixture-owner-key"))
	defer upstream.Close()
	browser := httptest.NewUnstartedServer(nil)
	config := webui.NewConfig()
	origin := "http://" + browser.Listener.Addr().String()
	server, err := webui.New(webui.Options{Origin: origin, Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "fixture-owner-key", Client: upstream.Client()}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	browser.Config.Handler = server
	browser.Start()
	defer browser.Close()
	coordinator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-coordinator" {
			http.Error(w, "denied", 403)
			return
		}
		adapter.mu.Lock()
		state := adapter.latest
		adapter.mu.Unlock()
		if state != nil {
			state.once.Do(func() { close(state.done) })
		}
		w.Write([]byte(`{}`))
	}))
	defer coordinator.Close()
	output := os.Getenv("POT_INTEGRATION_SCREENSHOTS")
	if output != "" {
		output, err = filepath.Abs(output)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
	}
	state, _ := json.Marshal(map[string]string{"origin": origin, "token": config.Token, "coord": coordinator.URL, "screenshots": output})
	code, err := os.ReadFile("../../scripts/tests/integrations-browser.js")
	if err != nil {
		t.Fatal(err)
	}
	session := "pot-integrations-" + filepath.Base(t.TempDir())
	cli := func(args ...string) string {
		command := exec.CommandContext(ctx, "npx", append([]string{"--offline", "--yes", "--package", "@playwright/cli", "playwright-cli", "--session", session}, args...)...)
		raw, err := command.CombinedOutput()
		text := strings.Split(string(raw), "### Ran Playwright code")[0]
		if err != nil || strings.Contains(text, "### Error") {
			t.Fatalf("browser failed: %v\n%s", err, text)
		}
		return text
	}
	cli("open", origin)
	defer cli("close")
	result := cli("run-code", strings.ReplaceAll(string(code), "FIXTURE_STATE", string(state)))
	if !strings.Contains(result, `"passed":true`) {
		t.Fatal(result)
	}
	t.Log("Browser authorization, account status, cancellation, logout, secret omission and responsive layout passed")
}
