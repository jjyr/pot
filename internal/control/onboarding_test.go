package control

import (
	"context"
	"strings"
	"sync"
	"testing"

	webassets "github.com/jjyr/pot/web"
)

func TestBootstrapWaitsForProviderAndCreatesExactlyOneRandomAssistant(t *testing.T) {
	t.Parallel()
	s, profile := configurationService(t)
	ctx := context.Background()
	s.Profiles.mu.Lock()
	s.Profiles.state.DefaultProviderID = ""
	s.Profiles.mu.Unlock()
	result, err := s.BootstrapAssistant(ctx, "zh-CN")
	if err != nil || result["assistant"] != nil {
		t.Fatal(result, err)
	}
	s.Profiles.mu.Lock()
	s.Profiles.state.DefaultProviderID = profile.ID
	s.Profiles.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			if _, err := s.BootstrapAssistant(ctx, "zh-CN"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil || len(definitions) != 1 || strings.ToLower(strings.ReplaceAll(definitions[0].Name, " ", "-")) != definitions[0].ID || strings.Count(definitions[0].ID, "-") != 1 {
		t.Fatal(definitions, err)
	}
	a := definitions[0]
	c, _ := s.Conversation(ctx, a.ID)
	snapshot, _ := c.Snapshot(ctx)
	if len(snapshot.Messages) != 1 || !strings.HasPrefix(snapshot.Messages[0].Text, "你好，我是 "+a.Name+"。") {
		t.Fatal(snapshot)
	}
	config, err := s.AssistantConfiguration(ctx, a.ID)
	if err != nil || config.Config.Model != profile.Model || config.Config.SourceProfileID != profile.ID {
		t.Fatal(config, err)
	}
	if _, err = s.Registry.Rename(ctx, a.ID, "小白", a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BootstrapAssistant(ctx, "en"); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = c.Snapshot(ctx)
	if len(snapshot.Messages) != 1 || !strings.HasPrefix(snapshot.Messages[0].Text, "你好，我是 "+a.Name+"。") {
		t.Fatal("locale switch changed history", snapshot)
	}
	other, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "second", Name: "Research", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	otherChat, _ := s.Conversation(ctx, other.ID)
	otherSnapshot, _ := otherChat.Snapshot(ctx)
	if len(otherSnapshot.Messages) != 1 || !strings.HasPrefix(otherSnapshot.Messages[0].Text, "Hi, I'm Research.") {
		t.Fatal(otherSnapshot)
	}
}

func TestQuickCreationUsesDefaultsAndLocalizedWelcomeOnce(t *testing.T) {
	t.Parallel()
	s, profile := configurationService(t)
	ctx := context.Background()
	if _, err := s.BootstrapAssistant(ctx, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN"} {
		request := CreateAssistant{RequestID: "quick-" + locale, Locale: locale}
		d, err := s.CreateAssistant(ctx, request)
		if err != nil || d.Name == "Pot" || d.Name == "" {
			t.Fatal(d, err)
		}
		config, err := s.AssistantConfiguration(ctx, d.ID)
		if err != nil || config.Config.SourceProfileID != profile.ID || config.Config.Model != profile.Model {
			t.Fatal("quick creation did not use Space defaults", config, err)
		}
		c, _ := s.Conversation(ctx, d.ID)
		rules, _ := c.Instructions(ctx)
		if rules.Text != "" {
			t.Fatal("quick creation inherited instructions", rules)
		}
		snapshot, _ := c.Snapshot(ctx)
		want := strings.ReplaceAll(webassets.Translate(locale, "assistant.welcome"), "{name}", d.Name)
		if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != want {
			t.Fatal("welcome did not introduce allocated name and settings", snapshot.Messages)
		}
		retry, err := s.CreateAssistant(ctx, request)
		if err != nil || retry != d {
			t.Fatal("quick creation retry changed name/avatar", retry, err)
		}
		snapshot, _ = c.Snapshot(ctx)
		if len(snapshot.Messages) != 1 {
			t.Fatal("retry repeated welcome", snapshot.Messages)
		}
	}
}
