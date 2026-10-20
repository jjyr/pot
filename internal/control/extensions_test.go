package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/pluginhost"
)

func TestExtensionDefaultsDisableFilteringAndRevisionFences(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	s.options.PluginRoot = t.TempDir()
	s.Profiles.pluginRoot = s.options.PluginRoot
	installDefaultToolFixture(t, s.options.PluginRoot, "example")
	s.options.BuiltinSkills = t.TempDir()
	writeTaskFixture(t, filepath.Join(s.options.BuiltinSkills, "builtin", "SKILL.md"), "---\nname: builtin\ndescription: Built in\n---\nInstructions")
	entries, _, err := s.extensionCatalog(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	for _, item := range entries {
		if !item.Enabled || item.Revision != 0 {
			t.Fatal(item)
		}
		command := ExtensionCommand{Action: "toggle", Kind: item.Kind, ID: item.ID, Revision: 0, Enabled: false}
		if _, err = s.ManageExtension(ctx, "main", command); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ManageExtension(ctx, "main", command); err == nil {
			t.Fatal("stale toggle accepted")
		}
	}
	settings, err := s.Profiles.defaultAgentSettings()
	if err != nil || settings.Plugins["example"].Enabled {
		t.Fatal(settings, err)
	}
	execution, err := s.prepareExecution(ctx, "main", core.RuntimeTarget("local"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := agent.DiscoverSkills(execution.SkillResources)
	if err != nil || len(catalog.Skills) != 0 {
		t.Fatal(catalog, err)
	}
	for _, item := range entries {
		if _, err = s.ManageExtension(ctx, "main", ExtensionCommand{Action: "remove", Kind: item.Kind, ID: item.ID, Revision: 1}); err == nil {
			t.Fatal("builtin removed")
		}
	}
}
func TestUserSkillCreateEditConflictAndRecoverableRemove(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	text := "---\nname: notes\ndescription: Notes\n---\nOriginal"
	if _, err := s.ManageExtension(ctx, "main", ExtensionCommand{Action: "create", Kind: "skills", Name: "notes", Content: text}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := s.extensionCatalog(ctx, "main")
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	item := entries[1]
	command := ExtensionCommand{Action: "save", Kind: "skills", ID: item.ID, Revision: item.Revision, Digest: extensionID(text), Content: strings.ReplaceAll(text, "Original", "Revised")}
	if _, err = s.ManageExtension(ctx, "main", command); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageExtension(ctx, "main", command); err == nil {
		t.Fatal("stale skill edit accepted")
	}
	if _, err = s.ManageExtension(ctx, "main", ExtensionCommand{Action: "remove", Kind: "skills", ID: item.ID, Revision: item.Revision + 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(item.Location); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	trash, err := os.ReadDir(filepath.Join(s.options.AgentHome, "extension-trash"))
	if err != nil || len(trash) != 1 {
		t.Fatal(trash, err)
	}
}
func TestMCPHTTPDiscoveryCallToolSelectionAndEncryptedCredentials(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	calls := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-private" {
			t.Error("credential absent")
			w.WriteHeader(401)
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]string{"type": "string"}}}}}}
		case "tools/call":
			calls++
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "worked"}}}
		default:
			t.Error(req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer endpoint.Close()
	value, err := s.ManageMCP(ctx, MCPCommand{Action: "save", Server: MCPServer{ID: "docs", Name: "Docs", Transport: "http", URL: endpoint.URL, Enabled: true}, Credentials: &MCPCredentials{Headers: map[string]string{"Authorization": "Bearer fixture-private"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), "fixture-private") || strings.Contains(string(raw), "secret_ref") {
		t.Fatal("management response leaked secret", string(raw))
	}
	servers, err := s.mcpServers(ctx)
	if err != nil || len(servers) != 1 {
		t.Fatal(err)
	}
	server := servers[0]
	value, err = s.ManageMCP(ctx, MCPCommand{Action: "test", Server: server})
	if err != nil {
		t.Fatal(err)
	}
	server = value.(MCPServer)
	if server.Status != "ready" || len(server.Tools) != 1 {
		t.Fatal(server)
	}
	settings, err := s.Profiles.defaultAgentSettings()
	if err != nil {
		t.Fatal(err)
	}
	bundle := settings.Plugins["mcp-docs"].Bundle
	if bundle == nil {
		t.Fatal(settings)
	}
	raw, _ = json.Marshal(bundle)
	if strings.Contains(string(raw), "fixture-private") {
		t.Fatal("bundle leaked secret")
	}
	servers, _ = s.mcpServers(ctx)
	secret, err := s.Credential(ctx, "test", servers[0].SecretRef, false)
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := os.Executable()
	host, err := pluginhost.New(pluginhost.Config{Identity: bundle.Digest, Files: bundle.Files, Entrypoint: "mcp-client.mjs", Mode: "text", ProcessWorker: []string{worker}, Environment: map[string]string{"MCP_CREDENTIALS": secret}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.Execute(ctx, "mcp_docs", map[string]any{"tool": "echo", "arguments": `{"message":"hello"}`})
	closeErr := host.Close()
	if err != nil || closeErr != nil || result.IsError || len(result.Content) != 1 || calls != 1 {
		t.Fatal(result, err, closeErr, calls)
	}
	server.DisabledTools = []string{"echo"}
	if _, err = s.ManageMCP(ctx, MCPCommand{Action: "tools", Server: server}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ManageMCP(ctx, MCPCommand{Action: "toggle", Server: server}); err == nil {
		t.Fatal("stale MCP update accepted")
	}
	var plain string
	if err = s.integrationDB.QueryRow("SELECT body FROM mcp_servers WHERE id='docs'").Scan(&plain); err != nil || strings.Contains(plain, "fixture-private") {
		t.Fatal(plain, err)
	}
}

func TestSameProviderRevisionRefreshUsesCurrentExtensionSettings(t *testing.T) {
	s, profile := configurationService(t)
	ctx := context.Background()
	s.options.PluginRoot = t.TempDir()
	s.Profiles.pluginRoot = s.options.PluginRoot
	installDefaultToolFixture(t, s.options.PluginRoot, "example")
	selection := AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: profile.Model}
	before, err := s.Profiles.ResolveAssistant(selection)
	if err != nil || !before.Params.Agent.Plugins["example"].Enabled {
		t.Fatal(before, err)
	}
	if _, err = s.ManageExtension(ctx, "main", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "example", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	after, err := s.refreshAssistantConfiguration(ctx, core.Thread{Config: before}, nil)
	if err != nil || after.Params.Agent.Plugins["example"].Enabled {
		t.Fatal(after, err)
	}
	if !before.Params.Agent.Plugins["example"].Enabled {
		t.Fatal("prepared snapshot mutated")
	}
}

func TestLocalImportRejectsLinksAndPreservesUpdateIdentity(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	source := t.TempDir()
	bundle := filepath.Join(source, "notes")
	writeTaskFixture(t, filepath.Join(bundle, "SKILL.md"), "---\nname: notes\ndescription: Notes\n---\nOriginal")
	if err := os.Symlink("/etc/passwd", filepath.Join(bundle, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManageExtension(ctx, "main", ExtensionCommand{Action: "install", Kind: "skills", Source: bundle}); err == nil {
		t.Fatal("skill link accepted")
	}
	os.Remove(filepath.Join(bundle, "outside"))
	if _, err := s.ManageExtension(ctx, "main", ExtensionCommand{Action: "install", Kind: "skills", Source: bundle}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := s.extensionCatalog(ctx, "main")
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	item := entries[1]
	writeTaskFixture(t, filepath.Join(bundle, "SKILL.md"), "---\nname: notes\ndescription: Notes\n---\nUpdated")
	if _, err = s.ManageExtension(ctx, "main", ExtensionCommand{Action: "update", Kind: "skills", ID: item.ID, Revision: item.Revision}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(item.Location)
	if err != nil || !strings.Contains(string(raw), "Updated") {
		t.Fatal(string(raw), err)
	}
}

func TestFailedMCPTestKeepsEditableConfigurationRevision(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer endpoint.Close()
	value, err := s.ManageMCP(ctx, MCPCommand{Action: "save", Server: MCPServer{ID: "expired", Name: "Expired", Transport: "http", URL: endpoint.URL, Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	server := value.(MCPServer)
	if _, err = s.ManageMCP(ctx, MCPCommand{Action: "test", Server: server}); err == nil {
		t.Fatal("unavailable server tested successfully")
	}
	current, _ := s.mcpServers(ctx)
	if current[0].Status != "unavailable" || current[0].Revision != server.Revision {
		t.Fatal("test failure advanced configuration revision", current)
	}
	server.Name = "Updated after failed test"
	if _, err = s.ManageMCP(ctx, MCPCommand{Action: "save", Server: server}); err != nil {
		t.Fatal("could not edit failed connection", err)
	}
}
