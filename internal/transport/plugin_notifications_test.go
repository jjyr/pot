package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/integrations"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
)

func TestPluginNotificationConfigurationOwnerRoutes(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	root, _ := filepath.Abs("../../plugins")
	s, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.Registry.Create(ctx, "create", "Pot")
	if err != nil {
		t.Fatal(err)
	}
	app := &Application{Control: s}
	if _, err = app.Call(ctx, Request{Method: "GET", Path: "/api/v1/plugin-notifications"}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"enabled":true,"assistant":"foreign","config":{},"revision":0}`, `{"enabled":false,"assistant":"main","config":{},"revision":0,"from":"user"}`, `{"enabled":false,"assistant":"main","config":{"existing":"removed"},"revision":0}`, `{"enabled":false,"assistant":"main","config":{"reasons":["invalid reason"]},"revision":0}`} {
		if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(body)}); err == nil {
			t.Fatal("invalid owner route accepted", body)
		}
	}
	request := Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(fmt.Sprintf(`{"request_id":"save","enabled":false,"assistant":%q,"config":{},"revision":0}`, owner.ID))}
	if _, err = app.Call(ctx, request); err == nil {
		t.Fatal("disconnected account accepted configuration")
	}
	if _, err = app.Call(ctx, request); err == nil {
		t.Fatal("stale revision accepted")
	}
	if !webui.Allowed("GET", "/api/v1/plugin-notifications") || !webui.Allowed("POST", request.Path) || webui.Allowed("POST", "/api/v1/plugin-notifications/github/foreign") || webui.Allowed("DELETE", request.Path) {
		t.Fatal("browser port mismatch")
	}
}

func TestGitHubFilterSettingsPortReturnsEffectiveRevisionAndIdempotentRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 30*time.Second))
	defer cancel()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	done := make(chan struct{})
	close(done)
	// Real MCP server, with a local inert poller: no external API calls.
	pluginRoot := t.TempDir()
	packageDir := filepath.Join(pluginRoot, "github")
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "mcp.mjs", "subscriptions.mjs", "filters.mjs", "read-notifications.mjs", "read-tool.json"} {
		raw, err := os.ReadFile(filepath.Join("../../plugins/github", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(packageDir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, "notifications.mjs"), []byte(`export async function poll(input) { return {state:input.state,events:[],delay_seconds:60,status:'ready'}; }`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: pluginRoot, IntegrationAdapters: []integrations.Adapter{loginAdapter{done}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.Registry.Create(ctx, "create", "Pot")
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Integrations.BeginLogin(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	for {
		state, e := s.Integrations.Login(ctx, "github", login.ID)
		if e != nil {
			t.Fatal(e)
		}
		if state.Status == "succeeded" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("login timeout")
		case <-time.After(time.Millisecond):
		}
	}
	app := &Application{Control: s}
	if _, err = app.Call(ctx, Request{Method: "GET", Path: "/api/v1/plugin-notifications"}); err != nil {
		t.Fatal(err)
	}
	request := Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(fmt.Sprintf(`{"tool":"update_subscription","arguments":{"id":"default","request_id":"ui-save","revision":1,"account_id":"123","enabled":false,"assistant":%q,"filters":{"repositories":null,"reasons":[],"subject_types":null,"threads":null}}}`, owner.ID))}
	for range 2 {
		result, e := app.Call(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(result)

		var projection struct {
			Operation struct {
				SubscriptionID string `json:"subscription_id"`
			}
			Items []struct {
				Settings struct {
					Revision      int
					Subscriptions []struct {
						Revision int
						Enabled  bool
						Filters  struct{ Reasons []string }
					}
				}
			}
		}
		if err := json.Unmarshal(raw, &projection); err != nil {
			t.Fatal(err)
		}
		if projection.Operation.SubscriptionID != "default" || len(projection.Items) != 1 || projection.Items[0].Settings.Revision != 2 || len(projection.Items[0].Settings.Subscriptions) != 1 {
			t.Fatal(string(raw))
		}
		sub := projection.Items[0].Settings.Subscriptions[0]
		if sub.Revision != 2 || sub.Enabled || sub.Filters.Reasons == nil || len(sub.Filters.Reasons) != 0 {
			t.Fatal(string(raw))
		}
	}
	request.Body = json.RawMessage(strings.Replace(string(request.Body), `"ui-save"`, `"another-request"`, 1))
	if _, err = app.Call(ctx, request); err == nil {
		t.Fatal("stale settings saved")
	}
}
