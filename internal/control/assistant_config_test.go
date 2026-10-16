package control

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/instance"
	"github.com/jjyr/pot/internal/modeldirectory"
	"github.com/jjyr/pot/internal/secrets"
)

func fixtureDirectory(t *testing.T, window uint64) *modeldirectory.Directory {
	t.Helper()
	root := t.TempDir()
	data := map[string]any{"providers": map[string]any{"fixture": map[string]any{"name": "Fixture", "npm": "@ai-sdk/openai-compatible", "api": "https://fixture.invalid/v1", "models": map[string]any{"first": map[string]any{"name": "First", "limit": map[string]uint64{"context": 64000, "output": 4000}}, "second": map[string]any{"name": "Second", "limit": map[string]uint64{"context": window, "output": 8000}}}}}}
	if err := instance.WriteJSON(filepath.Join(root, "catalog.json"), data); err != nil {
		t.Fatal(err)
	}
	directory, err := modeldirectory.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}
func configurationService(t *testing.T) (*Service, core.Profile) {
	t.Helper()
	manager, err := execution.New("runtime", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := Open(Options{Root: t.TempDir(), RuntimeID: "runtime", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	service.Profiles.directory = fixtureDirectory(t, 128000)
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	effort := "low"
	catalog := "fixture"
	window, output := uint64(60000), uint64(2000)
	profile, err := service.Profiles.Save(context.Background(), SaveProfile{RequestID: "provider", Name: "Provider", Model: "first", Endpoint: "https://fixture.invalid/v1/chat/completions", Protocol: "openai_chat", APIKey: "isolated-key", Agent: &settings, ReasoningEffort: &effort, ModelSettings: &core.ModelSettings{CatalogProvider: &catalog, ContextWindow: &window, MaxOutputTokens: &output}})
	if err != nil {
		t.Fatal(err)
	}
	return service, profile
}
func TestAssistantSelectionDoesNotMutateProviderOrOtherAssistant(t *testing.T) {
	ctx := context.Background()
	service, profile := configurationService(t)
	first, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "first-assistant", Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "second-assistant", Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := service.AssistantConfiguration(ctx, first.ID)
	effort := "  custom-effort  "
	window, output := uint64(32000), uint64(1000)
	selection := AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: "second", ContextWindow: &window, MaxOutputTokens: &output, ReasoningEffort: &effort}
	request := SaveAssistantConfiguration{RequestID: "save", ExpectedGeneration: before.Generation, ExpectedRevision: before.Revision, Selection: selection}
	saved, err := service.SaveAssistantConfiguration(ctx, first.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Config.Model != "second" || saved.Config.ContextWindow != window || saved.Config.Params.MaxOutputTokens != output || *saved.Config.Params.ReasoningEffort != "custom-effort" {
		t.Fatalf("assistant overrides: %+v", saved)
	}
	other, _ := service.AssistantConfiguration(ctx, second.ID)
	if other.Config.Model != "first" || other.Config.ContextWindow != 60000 {
		t.Fatal("other assistant changed", other)
	}
	stored := service.Profiles.Snapshot().Profiles[0]
	if stored.Model != "first" || *stored.Params.ReasoningEffort != "low" || *stored.ModelSettings.ContextWindow != 60000 {
		t.Fatal("Provider was mutated", stored)
	}
	duplicate, err := service.SaveAssistantConfiguration(ctx, first.ID, request)
	if err != nil || duplicate.Revision != saved.Revision || duplicate.Generation != saved.Generation {
		t.Fatal("save retry not reconciled", duplicate, err)
	}
	stale := request
	stale.RequestID = "stale"
	if _, err = service.SaveAssistantConfiguration(ctx, first.ID, stale); err == nil {
		t.Fatal("stale selection overwrote configuration")
	}
}
func TestModelDefaultsRefreshOnlySameProfileRevisionAndExplicitOverridesSurvive(t *testing.T) {
	ctx := context.Background()
	service, profile := configurationService(t)
	selection := AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: "second"}
	initial, err := service.Profiles.ResolveAssistant(selection)
	if err != nil || initial.ContextWindow != 128000 || initial.Params.MaxOutputTokens != 8000 {
		t.Fatal(initial, err)
	}
	intent, _ := json.Marshal(selection)
	thread := core.Thread{Owner: "main", Generation: 1, Config: initial}
	service.Profiles.mu.Lock()
	service.Profiles.directory = fixtureDirectory(t, 256000)
	service.Profiles.mu.Unlock()
	fresh, err := service.refreshAssistantConfiguration(ctx, thread, intent)
	if err != nil || fresh == nil || fresh.ContextWindow != 256000 || thread.Config.ContextWindow != 128000 {
		t.Fatal("metadata did not refresh a copy", fresh, err)
	}
	window := uint64(48000)
	selection.ContextWindow = &window
	intent, _ = json.Marshal(selection)
	fresh, err = service.refreshAssistantConfiguration(ctx, thread, intent)
	if err != nil || fresh.ContextWindow != window {
		t.Fatal("explicit override lost", fresh, err)
	}
	service.Profiles.mu.Lock()
	service.Profiles.state.Profiles[0].Revision++
	service.Profiles.mu.Unlock()
	fresh, err = service.refreshAssistantConfiguration(ctx, thread, intent)
	if err != nil || fresh != nil {
		t.Fatal("updated Provider revision silently adopted", fresh, err)
	}
}
func TestCreationBindsFullSelectionAndNeverReappliesOnRetry(t *testing.T) {
	ctx := context.Background()
	service, profile := configurationService(t)
	window := uint64(48000)
	request := CreateAssistant{RequestID: "created", Name: "Configured", Selection: &AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: "second", ContextWindow: &window}, Text: "initial rules"}
	first, err := service.CreateAssistant(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := service.AssistantConfiguration(ctx, first.ID)
	if view.Config.Model != "second" || view.Selection.ContextWindow == nil || *view.Selection.ContextWindow != window {
		t.Fatal("initial intent lost", view)
	}
	service.Profiles.mu.Lock()
	service.Profiles.state.Profiles[0].Revision++
	service.Profiles.mu.Unlock()
	duplicate, err := service.CreateAssistant(ctx, request)
	if err != nil || duplicate.ID != first.ID {
		t.Fatal("retry re-resolved changed Provider", duplicate, err)
	}
	request.Selection.Model = "first"
	if _, err = service.CreateAssistant(ctx, request); err == nil {
		t.Fatal("conflicting creation selection accepted")
	}
}
