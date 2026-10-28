package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestPendingMainAndTaskDefaultReadAtomically(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 15*time.Second))
	defer cancel()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { started <- struct{}{} })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }

	service, _ := configurationService(t)
	defer service.Close()
	defer server.Close()
	defer closeRelease()

	mainProfile, err := service.Profiles.Save(ctx, SaveProfile{
		RequestID:       "pending-main-profile",
		ProfileID:       "pending-main",
		Name:            "pending-main",
		Model:           "first",
		Endpoint:        server.URL,
		Protocol:        "openai_chat",
		APIKey:          "key-pending-main",
		MaxOutputTokens: 1000,
		ModelSettings:   &core.ModelSettings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	other := taskDefaultProfile(t, ctx, service, "other")
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{
		RequestID: "pending-assistant",
		Name:      "Pending Assistant",
		Selection: &AssistantSelection{ProfileID: mainProfile.ID, ProfileRevision: mainProfile.Revision, Model: "first"},
	})
	if err != nil {
		t.Fatal(err)
	}

	c, err := service.Conversation(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "held-turn", ConversationID: assistant.ID, Text: "hold"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	before, err := service.AssistantConfiguration(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection := before.Selection
	selection.Model = "second"
	selection.TaskDefault = &TaskModelSelection{ProfileID: other.ID, Model: "second"}
	saved, err := service.SaveAssistantConfiguration(ctx, assistant.ID, SaveAssistantConfiguration{
		RequestID:          "pending-save",
		ExpectedGeneration: before.Generation,
		ExpectedRevision:   before.Revision,
		Selection:          selection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Pending {
		t.Fatalf("active turn should keep new main selection pending: %+v", saved)
	}
	if saved.Config.Model != "first" {
		t.Fatalf("active main model changed while held: %q", saved.Config.Model)
	}

	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, other.ID, "second", 128000, 8000)

	explicitModel := "first"
	spec = mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{Model: &explicitModel})
	requireTaskConfig(t, spec, mainProfile.ID, "first", saved.Config.ContextWindow, saved.Config.Params.MaxOutputTokens)

	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "clear-default", nil)
	spec = mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	requireTaskConfig(t, spec, mainProfile.ID, "first", saved.Config.ContextWindow, saved.Config.Params.MaxOutputTokens)

	closeRelease()
}

func TestTaskDefaultCustomOpaqueTaskModelResolvesFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, profile := configurationService(t)
	assistant, err := service.CreateAssistant(ctx, CreateAssistant{RequestID: "opaque-assistant", Name: "Opaque"})
	if err != nil {
		t.Fatal(err)
	}
	saveTaskDefaultSelection(t, ctx, service, assistant.ID, "opaque-default", &TaskModelSelection{ProfileID: profile.ID, Model: "third-model"})
	spec := mustSelectTaskConfig(t, ctx, service, assistant.ID, protocol.CreateTask{})
	if spec.Model != "third-model" || spec.ContextWindow == 0 {
		t.Fatalf("opaque task model did not resolve: %+v", spec)
	}
}
