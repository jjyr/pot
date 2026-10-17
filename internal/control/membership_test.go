package control

import (
	"context"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/pkg/core"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEmbeddedControlCannotChangeMembershipRole(t *testing.T) {
	s := taskResourceService(t)
	s.options.Embedded = true
	for _, action := range []string{"preview", "join", "leave", "create_invitation", "remove_member", "owner_credential"} {
		if _, err := s.ManageMembership(context.Background(), MembershipCommand{Action: action, URL: "http://127.0.0.1:1"}); err == nil {
			t.Fatal("embedded role admitted", action)
		}
	}
	if s.ControlRole() != "embedded" || s.Machines.Membership() != nil {
		t.Fatal("embedded role changed")
	}
	if _, err := s.ManageMembership(context.Background(), MembershipCommand{Action: "set_tags", RuntimeID: "local", Tags: []string{"desktop"}}); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredProviderOrAssistantCannotBecomeRunnerAndRunnerCannotSaveProvider(t *testing.T) {
	ctx := context.Background()
	control := taskControl(t, "control", "")
	if err := control.Machines.EnableControl(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(control.ControlServer(nil))
	defer server.Close()
	enroll := func(s *Service) error {
		t.Helper()
		invitation, err := control.Machines.Invitation(ctx, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		connector := controlplane.Connector{Directory: s.Machines}
		preview, err := connector.Preview(ctx, server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, err = connector.Enroll(ctx, server.URL, preview, invitation.Code, "join-"+s.options.RuntimeID)
		return err
	}
	save := func(s *Service) (core.Profile, error) {
		return s.Profiles.Save(ctx, SaveProfile{RequestID: "provider", ProfileID: "fixture", Name: "Fixture", Model: "model", Endpoint: "https://example.test", Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	}
	configured := taskControl(t, "configured", "")
	profile, err := save(configured)
	if err != nil {
		t.Fatal(err)
	}
	if err = enroll(configured); err == nil {
		t.Fatal("neutral Runtime with Provider became Runner")
	}
	snapshot := configured.Profiles.Snapshot()
	if configured.Machines.Role() != "neutral" || len(snapshot.Profiles) != 1 || snapshot.Profiles[0].ID != profile.ID {
		t.Fatal("rejected join changed AI state", snapshot)
	}
	assistantOwner := taskControl(t, "assistant-owner", "")
	if _, err = assistantOwner.CreateAssistant(ctx, CreateAssistant{RequestID: "create", Name: "Assistant"}); err != nil {
		t.Fatal(err)
	}
	if err = enroll(assistantOwner); err == nil {
		t.Fatal("assistant owner became Runner")
	}
	runner := taskControl(t, "runner", "")
	if err = enroll(runner); err != nil {
		t.Fatal(err)
	}
	if _, err = save(runner); err == nil {
		t.Fatal("Runner acquired Provider configuration")
	}
	if len(runner.Profiles.Snapshot().Profiles) != 0 {
		t.Fatal("rejected Runner save published profile")
	}
	if _, err = runner.CreateAssistant(ctx, CreateAssistant{RequestID: "create", Name: "Assistant"}); err == nil {
		t.Fatal("Runner acquired assistant")
	}
}
