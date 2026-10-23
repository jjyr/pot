package control

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/assistant"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/integrations"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/plugin"
)

type settingsFixture struct {
	AccountID     string `json:"account_id"`
	Revision      int64
	Subscriptions []struct {
		ID        string
		Assistant string
		Enabled   bool
		Revision  int64
		AccountID string `json:"account_id"`
		Filters   map[string]any
	}
	SubscriptionID string `json:"subscription_id"`
}

func TestPluginMCPSubscriptionsDiscoveryCRUDDefaultAndSharedUI(t *testing.T) {
	ctx := context.Background()
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root, _ := filepath.Abs("../../plugins")
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	p, err := plugin.Load(filepath.Join(root, "github"))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method string, args any) settingsFixture {
		t.Helper()
		raw, _ := json.Marshal(args)
		_, r, e := s.invokePluginMCP(ctx, p, method, raw)
		if e != nil || r.IsError {
			t.Fatalf("%s: %s %v", method, r.StructuredContent, e)
		}
		var v settingsFixture
		if e = json.Unmarshal(mustMCPJSON(r.StructuredContent), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	if _, err = s.ManageMCP(ctx, MCPCommand{Action: "save", Server: MCPServer{ID: "github", Name: "Colliding server", Enabled: true, Transport: "stdio", Command: "node"}}); err == nil {
		t.Fatal("MCP package namespace collision accepted")
	}
	tools, _, err := s.invokePluginMCP(ctx, p, "", nil)
	if err != nil || len(tools) != 5 {
		t.Fatal(tools, err)
	}
	for _, tool := range s.productTools(false) {
		if strings.HasPrefix(tool.Name, "github_") {
			t.Fatal("native GitHub control tool survived", tool.Name)
		}
	}
	if v := call("list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 {
		t.Fatal(v)
	}
	if _, err = s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "123", Username: "fixture"}, SecretRef: "missing-fixture-secret"}, 0); err != nil {
		t.Fatal(err)
	}
	if v := call("list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 {
		t.Fatal("initialized without assistant", v)
	}
	a, _ := s.Registry.Create(ctx, "first", "First")
	b, _ := s.Registry.Create(ctx, "second", "B")
	c, _ := s.Registry.Create(ctx, "third", "C")
	var initialization sync.WaitGroup
	for range 8 {
		initialization.Add(1)
		go func() {
			defer initialization.Done()
			_, result, e := s.invokePluginMCP(ctx, p, "list_subscriptions", json.RawMessage(`{}`))
			if e != nil || result.IsError {
				t.Errorf("concurrent initialization: %s %v", result.StructuredContent, e)
			}
		}()
	}
	initialization.Wait()
	defaults := call("list_subscriptions", map[string]any{})
	if len(defaults.Subscriptions) != 1 || defaults.Subscriptions[0].Assistant != a.ID || defaults.Subscriptions[0].Filters["reasons"].([]any)[0] != "mention" {
		t.Fatal(defaults)
	}
	filters := map[string]any{"repositories": []string{"nervosnetwork/fiber"}, "reasons": nil, "subject_types": nil, "threads": nil}
	create := map[string]any{"request_id": "create-B", "account_id": "123", "assistant": b.ID, "enabled": true, "filters": filters}
	made := call("create_subscription", create)
	if len(made.Subscriptions) != 2 {
		t.Fatal(made)
	}
	id := made.SubscriptionID
	// A completely different execution owner may create/update any target. The
	// real stdio MCP path, not an owner-specific native handler, performs the write.
	dispatch := map[string]agent.Dispatch{}
	defs, e := s.pluginMCPTools(ctx, dispatch)
	if e != nil || len(defs) != 1 || defs[0].Name != "mcp_github" {
		t.Fatal(defs, e)
	}
	update := map[string]any{"id": id, "revision": 1, "request_id": "update-C", "account_id": "123", "assistant": c.ID, "enabled": false, "filters": filters}
	encoded, _ := json.Marshal(update)
	args, _ := json.Marshal(map[string]any{"tool": "update_subscription", "arguments": string(encoded)})
	result, e := dispatch["mcp_github"](ctx, core.ExecutionContext{Owner: b.ID}, modelprotocol.ToolCall{Name: "mcp_github", Arguments: args}, nil)
	if e != nil || strings.Contains(string(result), `"isError":true`) || len(result) == 0 {
		t.Fatal(string(result), e)
	}
	current := call("list_subscriptions", map[string]any{})
	if current.Subscriptions[1].Assistant != c.ID || current.Subscriptions[1].Enabled || current.Subscriptions[1].Revision != 2 {
		t.Fatal(current)
	}
	// UI bridge reads the same plugin projection and writes through MCP too.
	projection, e := s.PluginNotifications(ctx)
	raw, _ := json.Marshal(projection)
	if e != nil || !strings.Contains(string(raw), id) {
		t.Fatal(string(raw), e)
	}
	update["revision"] = 2
	update["request_id"] = "ui-update"
	update["enabled"] = true
	encoded, _ = json.Marshal(update)
	if _, e = s.PluginNotificationCall(ctx, "github", "update_subscription", encoded); e != nil {
		t.Fatal(e)
	}
	if v := call("list_subscriptions", map[string]any{}); v.Subscriptions[1].Revision != 3 || !v.Subscriptions[1].Enabled {
		t.Fatal(v)
	}
	// Old exact retries return the final effective data without overwriting it.
	call("create_subscription", create)
	oldUpdate := map[string]any{"id": id, "revision": 1, "request_id": "update-C", "account_id": "123", "assistant": c.ID, "enabled": false, "filters": filters}
	if v := call("update_subscription", oldUpdate); !v.Subscriptions[1].Enabled {
		t.Fatal("retry overwrote newer save", v)
	}
	bad := map[string]any{"id": id, "revision": 1, "request_id": "stale", "account_id": "123", "assistant": c.ID, "enabled": false, "filters": filters}
	encoded, _ = json.Marshal(bad)
	_, r, e := s.invokePluginMCP(ctx, p, "update_subscription", encoded)
	if e != nil || !r.IsError {
		t.Fatal("stale accepted", r, e)
	}
	changed := map[string]any{"request_id": "create-B", "account_id": "123", "assistant": c.ID, "enabled": true, "filters": filters}
	encoded, _ = json.Marshal(changed)
	_, r, e = s.invokePluginMCP(ctx, p, "create_subscription", encoded)
	if e != nil || !r.IsError {
		t.Fatal("conflicting request accepted", r, e)
	}
	call("delete_subscription", map[string]any{"id": defaults.Subscriptions[0].ID, "revision": 1, "request_id": "delete-default"})
	call("delete_subscription", map[string]any{"id": id, "revision": 3, "request_id": "delete-B"})
	call("create_subscription", create) // An old create retry cannot resurrect a deleted subscription.
	if v := call("list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 {
		t.Fatal("default resurrected", v)
	}
	// Re-running bootstrap ignores an existing empty subscription collection.
	s.pluginBootstrap = nil
	if err = s.ensurePluginNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	if v := call("list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 {
		t.Fatal(v)
	}
	create["request_id"] = "before-account-replacement"
	bound := call("create_subscription", create)
	replacementID := bound.SubscriptionID
	accounts, _ := s.integrationAccounts.List(ctx)
	_, err = s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "456", Username: "other"}, SecretRef: "missing"}, accounts[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	if v := call("list_subscriptions", map[string]any{}); len(v.Subscriptions) != 1 || v.AccountID != "456" || v.Subscriptions[0].AccountID != "123" {
		t.Fatal("account replacement rebound a subscription or recreated default", v)
	}
	rebound := call("update_subscription", map[string]any{"id": replacementID, "revision": 1, "request_id": "explicit-rebind", "account_id": "456", "assistant": c.ID, "enabled": true, "filters": filters})
	if rebound.Subscriptions[0].AccountID != "456" {
		t.Fatal(rebound)
	}
}

func TestPluginMCPDefaultDeletionSurvivesServiceRestartAndReconnect(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	packages, _ := filepath.Abs("../../plugins")
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	open := func() *Service {
		t.Helper()
		manager, _ := execution.New("local", "boot", execution.DefaultLimits())
		s, err := Open(Options{Root: dataRoot, RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys, PluginRoot: packages})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	p, err := plugin.Load(filepath.Join(packages, "github"))
	if err != nil {
		t.Fatal(err)
	}
	call := func(s *Service, name string, args any) settingsFixture {
		t.Helper()
		raw, _ := json.Marshal(args)
		_, r, e := s.invokePluginMCP(ctx, p, name, raw)
		if e != nil || r.IsError {
			t.Fatal(e, fmt.Sprint(r.StructuredContent))
		}
		var v settingsFixture
		if e = json.Unmarshal(mustMCPJSON(r.StructuredContent), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	s := open()
	s.pluginMu.Lock()
	if _, err = s.Registry.Create(ctx, "first", "Pot"); err != nil {
		t.Fatal(err)
	}
	saveAccount := func(s *Service) {
		t.Helper()
		revision, e := s.integrationAccounts.Revision(ctx, "github")
		if e != nil {
			t.Fatal(e)
		}
		if _, e := s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "123", Username: "fixture"}, SecretRef: "missing-fixture-secret"}, revision); e != nil {
			t.Fatal(e)
		}
	}
	saveAccount(s)
	v := call(s, "list_subscriptions", map[string]any{})
	if len(v.Subscriptions) != 1 {
		t.Fatal(v)
	}
	call(s, "delete_subscription", map[string]any{"id": v.Subscriptions[0].ID, "revision": 1, "request_id": "delete"})
	s.pluginMu.Unlock()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if v = call(s, "list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 {
		t.Fatal("restart resurrected default", v)
	}
	accounts, _ := s.integrationAccounts.List(ctx)
	if err = s.integrationAccounts.Delete(ctx, "github", accounts[0].Revision); err != nil {
		t.Fatal(err)
	}
	if v = call(s, "list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 || v.AccountID != "" {
		t.Fatal(v)
	}
	saveAccount(s)
	if v = call(s, "list_subscriptions", map[string]any{}); len(v.Subscriptions) != 0 {
		t.Fatal("reconnect resurrected default", v)
	}
}

func TestPluginMCPHostScopesOpaqueInstancesToInstalledPackage(t *testing.T) {
	ctx := context.Background()
	s := taskResourceService(t)
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	receiver, err := s.Registry.Create(ctx, "receiver", "Pot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.integrationAccounts.Save(ctx, integrations.Account{Service: "github", Identity: integrations.Identity{ID: "123", Username: "fixture"}, SecretRef: "missing"}, 0); err != nil {
		t.Fatal(err)
	}
	original := assistant.BackgroundBinding{ID: "github", PluginID: "github", Enabled: true, Config: json.RawMessage(`{"initialized":true,"subscriptions":[]}`)}
	if err = s.Mailbox.ConfigureBackground(ctx, original); err != nil {
		t.Fatal(err)
	}
	source, _ := filepath.Abs("../../plugins/github")
	p, err := plugin.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range append(p.Manifest.Contributes.MCP.Files, p.Manifest.Contributes.Background.Files...) {
		raw, e := os.ReadFile(filepath.Join(source, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	p.Manifest.ID = "other-package"
	raw, _ := json.Marshal(plugin.Definition{SchemaVersion: 3, Name: p.Manifest.ID, Version: p.Manifest.Version, Contributes: p.Manifest.Contributes})
	if err = os.WriteFile(filepath.Join(root, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	p, err = plugin.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"request_id": "foreign-instance", "account_id": "123", "assistant": receiver.ID, "enabled": true, "filters": map[string]any{"repositories": nil, "reasons": nil, "subject_types": nil, "threads": nil}})
	_, result, err := s.invokePluginMCP(ctx, p, "create_subscription", args)
	if err != nil || !result.IsError || !strings.Contains(fmt.Sprint(result.StructuredContent), "plugin_instance_mismatch") {
		t.Fatal(fmt.Sprint(result.StructuredContent), err)
	}
	rows, err := s.Mailbox.Backgrounds(ctx)
	if err != nil || len(rows) != 1 || rows[0].Revision != 1 || string(rows[0].Config) != string(original.Config) {
		t.Fatal(rows, err)
	}
}

func mustMCPJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
