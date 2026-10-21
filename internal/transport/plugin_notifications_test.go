package transport

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/integrations"
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
	s.Registry.Create(ctx, "create", "Pot")
	app := &Application{Control: s}
	if _, err = app.Call(ctx, Request{Method: "GET", Path: "/api/v1/plugin-notifications"}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"enabled":true,"assistant":"foreign","config":{},"revision":0}`, `{"enabled":false,"assistant":"main","config":{},"revision":0,"from":"user"}`, `{"enabled":false,"assistant":"main","config":{"existing":"removed"},"revision":0}`, `{"enabled":false,"assistant":"main","config":{"reasons":["invalid reason"]},"revision":0}`} {
		if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(body)}); err == nil {
			t.Fatal("invalid owner route accepted", body)
		}
	}
	request := Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(`{"request_id":"save","enabled":false,"assistant":"main","config":{},"revision":0}`)}
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	done := make(chan struct{})
	close(done)
	// No installed background package: the fixture cannot contact GitHub.
	s, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, IntegrationAdapters: []integrations.Adapter{loginAdapter{done}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Registry.Create(ctx, "create", "Pot")
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
	request := Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(`{"request_id":"ui-save","revision":1,"enabled":false,"assistant":"main","config":{"account_id":"123","repositories":null,"reasons":[],"subject_types":null,"threads":null}}`)}
	for range 2 {
		result, e := app.Call(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(result)
		var projection struct {
			Items []struct {
				Revision int  `json:"revision"`
				Enabled  bool `json:"enabled"`
				Config   struct {
					Reasons []string `json:"reasons"`
				} `json:"config"`
			} `json:"items"`
		}
		json.Unmarshal(raw, &projection)
		if len(projection.Items) != 1 || projection.Items[0].Revision != 2 || projection.Items[0].Enabled || projection.Items[0].Config.Reasons == nil || len(projection.Items[0].Config.Reasons) != 0 {
			t.Fatal(string(raw))
		}
	}
	request.Body = json.RawMessage(strings.Replace(string(request.Body), `"ui-save"`, `"another-request"`, 1))
	if _, err = app.Call(ctx, request); err == nil {
		t.Fatal("stale settings saved")
	}
}
