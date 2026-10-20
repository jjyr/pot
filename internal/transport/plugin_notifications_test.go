package transport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

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
	request := Request{Method: "POST", Path: "/api/v1/plugin-notifications/github", Body: json.RawMessage(`{"enabled":false,"assistant":"main","config":{},"revision":0}`)}
	if _, err = app.Call(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Call(ctx, request); err == nil {
		t.Fatal("stale revision accepted")
	}
	if !webui.Allowed("GET", "/api/v1/plugin-notifications") || !webui.Allowed("POST", request.Path) || webui.Allowed("POST", "/api/v1/plugin-notifications/github/foreign") || webui.Allowed("DELETE", request.Path) {
		t.Fatal("browser port mismatch")
	}
}
