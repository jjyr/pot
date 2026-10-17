package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func installDefaultToolFixture(t *testing.T, root, id string) {
	t.Helper()
	manifest := `{"schema_version":2,"id":"` + id + `","version":"1","contributes":{"tools":{"runtime":"node","execution":"host_or_sandbox","entrypoint":"tool.mjs","files":["tool.mjs"],"requirements":[],"tools":[{"name":"` + strings.ReplaceAll(id, "-", "_") + `","description":"fixture","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}]},"sandbox_providers":[{"id":"fixture.` + id + `","host_os":["darwin","linux"],"executable":"bin/provider"}]}}`
	writeTaskFixture(t, filepath.Join(root, id, "plugin.json"), manifest)
	writeTaskFixture(t, filepath.Join(root, id, "tool.mjs"), "ORIGINAL_TOOL")
}

func TestProviderDefaultsEnableInstalledToolsAndClearLegacyFilters(t *testing.T) {
	s, _ := configurationService(t)
	root := t.TempDir()
	s.Profiles.pluginRoot = root
	installDefaultToolFixture(t, root, "github")
	installDefaultToolFixture(t, root, "computer-use")
	// Neither tool discovery nor resolution may read or launch Provider binaries.
	writeTaskFixture(t, filepath.Join(root, "provider-only", "plugin.json"), `{"schema_version":2,"id":"provider-only","version":"1","contributes":{"sandbox_providers":[{"id":"fixture.only","host_os":["darwin","linux"],"executable":"bin/not-installed"}]}}`)
	before, _ := s.Registry.Definitions(context.Background())
	profile, err := s.Profiles.Save(context.Background(), SaveProfile{RequestID: "connection-only", Name: "Connection", Model: "first", Endpoint: "https://fixture.invalid/v1/chat/completions", Protocol: "openai_chat", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Registry.Definitions(context.Background())
	if !reflect.DeepEqual(before, after) {
		t.Fatal("saving a connection changed the assistant registry")
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil || spec.Validate() != nil {
		t.Fatal(spec, err)
	}
	if len(spec.Params.Agent.Plugins) != 2 || spec.Params.Agent.AllowedTools != nil || !reflect.DeepEqual(spec.Params.Agent.BuiltinTools, core.DefaultAgentSettings().BuiltinTools) {
		t.Fatal("tools are not enabled by default", spec.Params.Agent)
	}
	for _, installed := range spec.Params.Agent.Plugins {
		if !installed.Enabled || installed.Bundle == nil || installed.Source != nil || len(installed.Bundle.Manifest.Contributes.SandboxProviders) != 0 {
			t.Fatal("execution snapshot is not pinned and tool-only", installed)
		}
	}
	// Simulate records written by the former connection form, including an opt-out.
	for i := range s.Profiles.state.Profiles {
		legacy := &s.Profiles.state.Profiles[i]
		legacy.Params.Agent.BuiltinTools = []string{}
		legacy.Params.Agent.AllowedTools = &[]string{}
		legacy.Params.Agent.Plugins = map[string]core.PluginConfig{"github": {Enabled: false}}
		prompt := "old connection instructions"
		legacy.Params.Agent.SystemPrompt = &prompt
	}
	data, _ := json.Marshal(s.Profiles.state)
	if err := os.WriteFile(s.Profiles.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenProfiles(s.Profiles.path, root, s.Providers)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Resolve(profile.ID)
	if err != nil || restored.Params.Agent.SystemPrompt != nil || restored.Params.Agent.AllowedTools != nil || len(restored.Params.Agent.Plugins) != 2 || !reflect.DeepEqual(restored.Params.Agent.BuiltinTools, spec.Params.Agent.BuiltinTools) {
		t.Fatal("legacy connection settings still restrict assistants", restored, err)
	}
	writeTaskFixture(t, filepath.Join(root, "github", "tool.mjs"), "UPDATED_TOOL")
	if spec.Params.Agent.Plugins["github"].Bundle.Files["tool.mjs"] != "ORIGINAL_TOOL" {
		t.Fatal("existing execution snapshot changed")
	}
	var request SaveProfile
	if core.DecodeJSON([]byte(`{"agent":{"allowed_tools":[]}}`), &request) == nil {
		t.Fatal("connection API still accepts assistant execution settings")
	}
}

func TestOlderProviderRevisionAdoptsToolsOnlyAtFreshTurnBoundary(t *testing.T) {
	s, profile := configurationService(t)
	selection := AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: "second"}
	original, err := s.Profiles.ResolveAssistant(selection)
	if err != nil {
		t.Fatal(err)
	}
	original.Params.Agent.BuiltinTools = []string{}
	original.Params.Agent.AllowedTools = &[]string{}
	original.Rehash()
	s.Profiles.state.Profiles[0].Revision++
	s.Profiles.state.Profiles[0].Model = "first"
	s.Profiles.pluginRoot = t.TempDir()
	installDefaultToolFixture(t, s.Profiles.pluginRoot, "github")
	intent, _ := json.Marshal(selection)
	fresh, err := s.refreshAssistantConfiguration(context.Background(), core.Thread{Owner: "main", Generation: 1, Config: original}, intent)
	if err != nil || fresh == nil || fresh.Validate() != nil {
		t.Fatal(fresh, err)
	}
	if fresh.Model != original.Model || fresh.ContextWindow != original.ContextWindow || fresh.SourceProfileRevision != original.SourceProfileRevision || fresh.Params.Agent.AllowedTools != nil || !fresh.Params.Agent.Plugins["github"].Enabled || len(fresh.Params.Agent.BuiltinTools) != 5 {
		t.Fatal("fresh turn did not keep model intent and adopt tool defaults", fresh)
	}
	if len(original.Params.Agent.BuiltinTools) != 0 || original.Params.Agent.AllowedTools == nil || len(original.Params.Agent.Plugins) != 0 {
		t.Fatal("refresh mutated the existing execution snapshot")
	}
}
