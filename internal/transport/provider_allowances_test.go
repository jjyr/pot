package transport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
)

func TestProviderAllowanceRouteDecodesOwnerConfiguration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	directory := filepath.Join(pluginRoot, "quota-reader")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":3,"name":"quota-reader","version":"1","contributes":{"model_providers":{"providers":[{"id":"gateway","name":"Quota Gateway","protocol":"openai_chat","capabilities":{"catalog":"none","allowances":true}}],"adapter":{"runtime":"node","execution":"control","entrypoint":"reader.mjs","files":["reader.mjs"]}}}}`
	for name, source := range map[string]string{"plugin.json": manifest, "reader.mjs": "export async function read(){throw Error('fixture must use the saved cache')}"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := execution.New("quota-control", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: root, RuntimeID: "quota-control", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: pluginRoot, Maintenance: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	owner, err := service.Registry.Create(ctx, "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.Profiles.Save(ctx, control.SaveProfile{RequestID: "profile", ProfileID: "connection", Name: "Connection", Model: "fixture", ProviderTypeID: "quota-reader/gateway", Endpoint: "https://quota.invalid/v1", Protocol: "openai_chat", APIKey: "fixture-key", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, route, typ, err := service.Profiles.ProviderConnection(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(core.ModelProviderResponse{Allowances: &core.AllowanceSnapshot{ReadAt: time.Now().UTC().Format(time.RFC3339Nano), Accounts: []core.ProviderAllowance{}}})
	if err = service.Providers.SaveMetadata(ctx, route.ID, "read_allowances", typ.Identity.Digest, payload); err != nil {
		t.Fatal(err)
	}
	app := Application{Control: service}
	value, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/providers/allowances?profile_id=connection"})
	if err != nil {
		t.Fatal(err)
	}
	before := value.(control.ConnectionAllowanceView)
	request := map[string]any{"profile_id": profile.ID, "action": "configure", "enabled": false, "threshold": 75, "assistant": owner.ID, "revision": before.Monitor.Revision}
	body, _ := json.Marshal(request)
	value, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/providers/allowances", Body: body})
	if err != nil {
		t.Fatal("owner request rejected by transport decoder", err)
	}
	after := value.(control.ConnectionAllowanceView)
	if after.Monitor.Enabled || after.Monitor.Threshold != 75 || after.Monitor.Assistant != owner.ID || after.Monitor.Revision != before.Monitor.Revision+1 {
		t.Fatal(after)
	}
	if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/providers/allowances", Body: body}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("stale owner revision accepted", err)
	}
	request["revision"] = after.Monitor.Revision
	request["plugin_id"] = "forged-source"
	body, _ = json.Marshal(request)
	if _, err = app.Call(ctx, Request{Method: "POST", Path: "/api/v1/providers/allowances", Body: body}); err == nil || !strings.Contains(err.Error(), "unknown JSON field") {
		t.Fatal("unknown source field accepted", err)
	}
}
