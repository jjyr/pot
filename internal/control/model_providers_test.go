package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func installMetadataFixture(t *testing.T, s *Service) string {
	t.Helper()
	root := t.TempDir()
	s.options.PluginRoot = root
	s.Profiles.pluginRoot = root
	target := filepath.Join(root, "acme")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema_version":2,"id":"acme","version":"1","contributes":{"model_providers":{"providers":[{"id":"aggregate","name":"Aggregate","protocol":"openai_chat","capabilities":{"catalog":"plugin","allowances":true}}],"adapter":{"runtime":"node","execution":"control","entrypoint":"reader.mjs","files":["reader.mjs"],"credential_env":"ACME_KEY"}}}}`
	body := `export async function read(input) {
 if (process.env.ACME_KEY !== 'isolated-token') return {error:{code:'authorization'}};
 if(input.method==='list_models') return {catalog:{read_at:new Date().toISOString(),models:[{id:'acme/text',context_window:128000,max_output:16000,input_modalities:['text','image']}]}};
 return {allowances:{read_at:new Date().toISOString(),accounts:[{provider:'vendor',plan:'Pro',read_at:new Date().toISOString(),identity_quality:'ambiguous',windows:[{name:'weekly',used:91,identity_quality:'ambiguous'}]}]}};
 }`
	for name, value := range map[string]string{"plugin.json": manifest, "reader.mjs": body} {
		if err := os.WriteFile(filepath.Join(target, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func TestProviderContributionCatalogCacheAndDisableGates(t *testing.T) {
	ctx := context.Background()
	s, _ := configurationService(t)
	target := installMetadataFixture(t, s)
	request := SaveProfile{RequestID: "aggregate-profile", Name: "Aggregate", Model: "acme/text", ProviderTypeID: "acme/aggregate", Endpoint: "https://aggregate.invalid/v1", Protocol: "openai_chat", APIKey: "isolated-token", ModelSettings: &core.ModelSettings{}}
	profile, err := s.Profiles.Save(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	route, err := s.Providers.Get(ctx, profile.Provider)
	if err != nil {
		t.Fatal(err)
	}
	if route.TypeID != "acme/aggregate" || route.Source == nil || route.Source.ID != "acme" {
		t.Fatal(route)
	}
	checked, err := s.Profiles.Check(ctx, CheckProvider{ProfileID: profile.ID, Endpoint: route.Endpoint, Protocol: route.Protocol})
	if err != nil || checked.Catalog == nil || len(checked.Models) != 1 {
		t.Fatal(checked, err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil || spec.Params.Agent.ContextWindow != 128000 || spec.Params.MaxOutputTokens != 16000 {
		t.Fatal(spec.Params, err)
	}
	allowances, err := s.Profiles.ReadAllowances(ctx, profile.ID, true)
	if err != nil || len(allowances.Accounts) != 1 || allowances.Accounts[0].Plan != "Pro" {
		t.Fatal(allowances, err)
	}
	// Successful metadata does not grant credential reuse for a different route.
	if _, err = s.Profiles.Check(ctx, CheckProvider{ProfileID: profile.ID, Endpoint: "https://other.invalid/v1", Protocol: route.Protocol}); err == nil {
		t.Fatal("changed endpoint reused saved credential")
	}
	// Package replacement fences the old catalog instead of treating it as facts
	// for a new adapter. The saved route's provenance remains unchanged.
	f := filepath.Join(target, "reader.mjs")
	raw, _ := os.ReadFile(f)
	if err = os.WriteFile(f, append(raw, []byte("\n// new adapter revision\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.Profiles.Resolve(profile.ID)
	if err != nil || refreshed.Params.Agent.ContextWindow == 128000 {
		t.Fatal(refreshed.Params, err)
	}
	// Old immutable route snapshots still retain their original key; fresh model
	// selections and metadata reads must honor package enablement.
	if _, err = s.ManageExtension(ctx, "", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "acme", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Profiles.Resolve(profile.ID); err == nil {
		t.Fatal("disabled source resolved")
	}
	if _, err = s.Profiles.ResolveAssistant(AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: profile.Model}); err == nil {
		t.Fatal("disabled assistant source resolved")
	}
	if _, err = s.Profiles.ReadAllowances(ctx, profile.ID, false); err == nil {
		t.Fatal("disabled source used cached allowance")
	}
	if key, err := s.Providers.Credential(ctx, route); err != nil || string(key) != "isolated-token" {
		t.Fatal("immutable route mutated", err)
	} else {
		clear(key)
	}
	disk, _ := os.ReadFile(s.Profiles.path)
	if strings.Contains(string(disk), "provider_types") || strings.Contains(string(disk), "type_id") || strings.Contains(string(disk), "isolated-token") {
		t.Fatal("projection or credential persisted in profiles.json")
	}
}

func TestBuiltinProvidersUseTheSameEnabledContributionRegistry(t *testing.T) {
	ctx := context.Background()
	s, profile := configurationService(t)
	snapshot := s.Profiles.Snapshot()
	found := false
	for _, typ := range snapshot.ProviderTypes {
		if typ.ID == customProviderType {
			found = typ.Available && typ.PluginID == "standard-model-providers"
		}
	}
	if !found {
		t.Fatal(snapshot.ProviderTypes)
	}
	if _, err := s.ManageExtension(ctx, "", ExtensionCommand{Action: "toggle", Kind: "plugins", ID: "standard-model-providers", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Profiles.Resolve(profile.ID); err == nil {
		t.Fatal("builtin provider bypassed disabled contribution")
	}
	if _, err := s.Profiles.ResolveProviderEndpoint(ctx, CheckProvider{Protocol: "openai_chat", Endpoint: "https://fixture.invalid/v1"}); err == nil {
		t.Fatal("endpoint resolution bypassed disabled contribution")
	}
	data, _ := json.Marshal(s.Profiles.Snapshot())
	if strings.Contains(string(data), "isolated-key") {
		t.Fatal("credential exposed")
	}
}
