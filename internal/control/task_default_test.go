package control

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func taskDefaultProfile(t *testing.T, ctx context.Context, service *Service, id string) core.Profile {
	t.Helper()
	catalog := "fixture"
	profile, err := service.Profiles.Save(ctx, SaveProfile{
		RequestID:       "profile-" + id,
		ProfileID:       id,
		Name:            id,
		Model:           "first",
		Endpoint:        "https://fixture.invalid/v1/chat/completions",
		Protocol:        "openai_chat",
		APIKey:          "key-" + id,
		MaxOutputTokens: 1000,
		ModelSettings:   &core.ModelSettings{CatalogProvider: &catalog},
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func saveTaskDefaultSelection(t *testing.T, ctx context.Context, service *Service, assistantID, requestID string, taskDefault *TaskModelSelection) {
	t.Helper()
	before, err := service.AssistantConfiguration(ctx, assistantID)
	if err != nil {
		t.Fatal(err)
	}
	selection := before.Selection
	selection.TaskDefault = taskDefault
	if _, err = service.SaveAssistantConfiguration(ctx, assistantID, SaveAssistantConfiguration{
		RequestID:          requestID,
		ExpectedGeneration: before.Generation,
		ExpectedRevision:   before.Revision,
		Selection:          selection,
	}); err != nil {
		t.Fatal(err)
	}
}

func mustSelectTaskConfig(t *testing.T, ctx context.Context, service *Service, owner string, request protocol.CreateTask) core.ResolvedSpec {
	t.Helper()
	selection, err := service.selectTask(ctx, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	return selection.Config
}

func requireTaskConfig(t *testing.T, spec core.ResolvedSpec, sourceProfile, model string, contextWindow, maxOutput uint64) {
	t.Helper()
	if spec.SourceProfileID != sourceProfile || spec.Model != model || spec.ContextWindow != contextWindow || spec.Params.MaxOutputTokens != maxOutput {
		t.Fatalf("task config = source %q model %q context %d output %d, want source %q model %q context %d output %d", spec.SourceProfileID, spec.Model, spec.ContextWindow, spec.Params.MaxOutputTokens, sourceProfile, model, contextWindow, maxOutput)
	}
}

func removeSavedProfile(t *testing.T, service *Service, id string) {
	t.Helper()
	service.Profiles.mu.Lock()
	defer service.Profiles.mu.Unlock()
	kept := service.Profiles.state.Profiles[:0]
	for _, profile := range service.Profiles.state.Profiles {
		if profile.ID != id {
			kept = append(kept, profile)
		}
	}
	service.Profiles.state.Profiles = kept
}

func TestTaskDefaultInheritsSavedSelectionAndAppliesImmediately(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "task-default", &TaskModelSelection{ProfileID: profile.ID, Model: "second"})
	view, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Selection.TaskDefault == nil || view.Selection.TaskDefault.ProfileID != profile.ID || view.Selection.TaskDefault.Model != "second" {
		t.Fatalf("task default intent not saved: %+v", view.Selection.TaskDefault)
	}
	if view.Config.Model != "first" || view.Config.ContextWindow != 60000 {
		t.Fatalf("main assistant configuration changed: %+v", view.Config)
	}
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, profile.ID, "second", 128000, 8000)
}

func TestTaskDefaultCrossProviderAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	other := taskDefaultProfile(t, ctx, service, "other")
	first, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "first", Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "second", Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, first.ID, "first-default", &TaskModelSelection{ProfileID: other.ID, Model: "second"})
	spec := mustSelectTaskConfig(t, ctx, service, first.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, other.ID, "second", 128000, 8000)
	spec = mustSelectTaskConfig(t, ctx, service, second.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, profile.ID, "first", 60000, 2000)
	view, err := service.AssistantConfiguration(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Selection.TaskDefault != nil {
		t.Fatalf("task default leaked between assistants: %+v", view.Selection.TaskDefault)
	}
	firstView, err := service.AssistantConfiguration(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstView.Config.Model != "first" || firstView.Config.ContextWindow != 60000 {
		t.Fatalf("task default mutated main assistant: %+v", firstView.Config)
	}
}

func TestTaskRouteExplicitSelectionOverridesTaskDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	other := taskDefaultProfile(t, ctx, service, "other")
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "task-default", &TaskModelSelection{ProfileID: other.ID, Model: "second"})

	explicitProfile := profile.ID
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{Profile: &explicitProfile})
	requireTaskConfig(t, spec, profile.ID, "first", 60000, 2000)

	explicitModel := "second"
	spec = mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{Model: &explicitModel})
	requireTaskConfig(t, spec, profile.ID, "second", 128000, 8000)

	bothProfile := profile.ID
	bothModel := "second"
	spec = mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{Profile: &bothProfile, Model: &bothModel})
	requireTaskConfig(t, spec, profile.ID, "second", 128000, 8000)
}

func TestTaskDefaultCanBeCleared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "set-default", &TaskModelSelection{ProfileID: profile.ID, Model: "second"})
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, profile.ID, "second", 128000, 8000)
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "clear-default", nil)
	view, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Selection.TaskDefault != nil {
		t.Fatalf("task default not cleared: %+v", view.Selection.TaskDefault)
	}
	spec = mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, profile.ID, "first", 60000, 2000)
}

func TestTaskDefaultValidationRejectsInvalidMissingAndBlankSelections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		def  *TaskModelSelection
	}{
		{name: "missing-profile", def: &TaskModelSelection{ProfileID: "missing", Model: "first"}},
		{name: "blank-profile", def: &TaskModelSelection{ProfileID: "   ", Model: "first"}},
		{name: "blank-model", def: &TaskModelSelection{ProfileID: profile.ID, Model: "   "}},
	}
	for _, tc := range cases {
		selection := before.Selection
		selection.TaskDefault = tc.def
		_, err := service.SaveAssistantConfiguration(ctx, assistant.ID, SaveAssistantConfiguration{
			RequestID:          "invalid-" + tc.name,
			ExpectedGeneration: before.Generation,
			ExpectedRevision:   before.Revision,
			Selection:          selection,
		})
		if err == nil {
			t.Fatalf("invalid task default %s accepted", tc.name)
		}
	}
	_, err = service.CreateAssistant(ctx, CreateAssistant{
		RequestID: "invalid-create",
		Name:      "Invalid",
		Selection: &AssistantSelection{
			ProfileID:       profile.ID,
			ProfileRevision: profile.Revision,
			Model:           "first",
			TaskDefault:     &TaskModelSelection{ProfileID: "missing", Model: "first"},
		},
	})
	if err == nil {
		t.Fatal("CreateAssistant accepted invalid task default")
	}
}

func TestTaskDefaultMissingProviderDoesNotFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	other := taskDefaultProfile(t, ctx, service, "other")
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "task-default", &TaskModelSelection{ProfileID: other.ID, Model: "second"})
	removeSavedProfile(t, service, other.ID)
	if _, err := service.selectTask(ctx, assistant.ID, protocol.CreateTask{}); err == nil {
		t.Fatal("missing task default provider fell back to active assistant configuration")
	}
	activeProfile := profile.ID
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{Profile: &activeProfile})
	requireTaskConfig(t, spec, profile.ID, "first", 60000, 2000)
}

func TestTaskDefaultSaveReceiptRetryAfterProviderRemoval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _ := configurationService(t)
	other := taskDefaultProfile(t, ctx, service, "other")
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection := before.Selection
	selection.TaskDefault = &TaskModelSelection{ProfileID: other.ID, Model: "second"}
	request := SaveAssistantConfiguration{
		RequestID:          "task-default-receipt",
		ExpectedGeneration: before.Generation,
		ExpectedRevision:   before.Revision,
		Selection:          selection,
	}
	saved, err := service.SaveAssistantConfiguration(ctx, assistant.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	removeSavedProfile(t, service, other.ID)
	duplicate, err := service.SaveAssistantConfiguration(ctx, assistant.ID, request)
	if err != nil {
		t.Fatalf("saved receipt was revalidated after provider removal: %v", err)
	}
	if duplicate.Revision != saved.Revision || duplicate.Generation != saved.Generation {
		t.Fatalf("receipt changed: saved=%+v duplicate=%+v", saved, duplicate)
	}
}

func TestTaskDefaultUsesLatestProviderRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "task-default", &TaskModelSelection{ProfileID: profile.ID, Model: "second"})
	first := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, first, profile.ID, "second", 128000, 8000)
	service.Profiles.mu.Lock()
	service.Profiles.state.Profiles[0].Revision++
	service.Profiles.directory = fixtureDirectory(t, 256000)
	service.Profiles.mu.Unlock()
	second := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, second, profile.ID, "second", 256000, 8000)
	view, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Config.Model != "first" || view.Config.ContextWindow != 60000 {
		t.Fatalf("latest task default revision changed main assistant configuration: %+v", view.Config)
	}
}

func TestTaskDefaultCreatePersistsNestedIntentAndRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	other := taskDefaultProfile(t, ctx, service, "other")
	request := CreateAssistant{
		RequestID: "create-with-task-default",
		Name:      "With Task Default",
		Selection: &AssistantSelection{
			ProfileID:       profile.ID,
			ProfileRevision: profile.Revision,
			Model:           "first",
			TaskDefault:     &TaskModelSelection{ProfileID: other.ID, Model: "second"},
		},
	}
	assistant, err := service.CreateAssistant(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Selection.TaskDefault == nil || view.Selection.TaskDefault.ProfileID != other.ID || view.Selection.TaskDefault.Model != "second" {
		t.Fatalf("created assistant lost task default: %+v", view.Selection.TaskDefault)
	}
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, other.ID, "second", 128000, 8000)
	removeSavedProfile(t, service, other.ID)
	duplicate, err := service.CreateAssistant(ctx, request)
	if err != nil {
		t.Fatalf("CreateAssistant receipt was revalidated after provider removal: %v", err)
	}
	if duplicate.ID != assistant.ID {
		t.Fatalf("CreateAssistant retry returned a different assistant: %s != %s", duplicate.ID, assistant.ID)
	}
	request.Selection.TaskDefault.Model = "first"
	if _, err := service.CreateAssistant(ctx, request); err == nil {
		t.Fatal("conflicting CreateAssistant task default accepted")
	}
}

func TestMainRefreshIgnoresMissingTaskDefaultProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	other := taskDefaultProfile(t, ctx, service, "other")
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "task-default", &TaskModelSelection{ProfileID: other.ID, Model: "second"})
	view, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := json.Marshal(view.Selection)
	if err != nil {
		t.Fatal(err)
	}
	removeSavedProfile(t, service, other.ID)
	fresh, err := service.refreshAssistantConfiguration(ctx, core.Thread{Owner: assistant.ID, Generation: view.Generation, Config: *view.Config}, intent)
	if err != nil {
		t.Fatalf("main configuration refresh failed because task default provider is missing: %v", err)
	}
	if fresh == nil || fresh.SourceProfileID != profile.ID || fresh.Model != view.Config.Model {
		t.Fatalf("main configuration refresh did not ignore task default: %+v", fresh)
	}
}

func TestTaskDefaultIndependentParameters(t *testing.T) {
	ctx := context.Background()
	service, profile := configurationService(t)
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "assistant", Name: "Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	window, output, effort := uint64(64000), uint64(4000), " high "
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "parameters", &TaskModelSelection{ProfileID: profile.ID, Model: "second", ContextWindow: &window, MaxOutputTokens: &output, ReasoningEffort: &effort})
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, profile.ID, "second", window, output)
	if spec.Params.ReasoningEffort == nil || *spec.Params.ReasoningEffort != "high" {
		t.Fatalf("task effort: %v", spec.Params.ReasoningEffort)
	}
	view, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Config.Model != "first" || view.Config.ContextWindow != 60000 {
		t.Fatalf("assistant changed: %+v", view.Config)
	}
	if view.Selection.TaskDefault.ReasoningEffort == nil || *view.Selection.TaskDefault.ReasoningEffort != "high" {
		t.Fatal("task effort not normalized")
	}
	explicit := "first"
	explicitSpec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{Model: &explicit})
	if explicitSpec.ContextWindow == window || explicitSpec.Params.MaxOutputTokens == output {
		t.Fatal("explicit selection inherited task overrides")
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "clear-parameters", nil)
	followed := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, followed, profile.ID, view.Config.Model, view.Config.ContextWindow, view.Config.Params.MaxOutputTokens)
}

func TestTaskDefaultParameterValidation(t *testing.T) {
	zero, tooLarge := uint64(0), core.MaxSafeInteger+uint64(1)
	for _, value := range []*uint64{&zero, &tooLarge} {
		for _, selection := range []TaskModelSelection{{ProfileID: "provider", Model: "model", ContextWindow: value}, {ProfileID: "provider", Model: "model", MaxOutputTokens: value}} {
			if selection.Validate() == nil {
				t.Fatal("invalid token limit accepted")
			}
		}
	}
}
