package control

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"
	"path/filepath"
	"testing"
)

func TestCompletionsUseOwnerResourcesAndBoundMetadataWithoutExecution(t *testing.T) {
	t.Parallel()
	s := taskResourceService(t)
	ctx := context.Background()
	if _, err := legacyMainAssistant(ctx, s, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	if err := s.Machines.SetLabels(ctx, "local", []string{"desktop", "task-runner"}); err != nil {
		t.Fatal(err)
	}
	// Resolving metadata must never invoke tool execution or require a Provider.
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		writeTaskFixture(t, filepath.Join(s.options.AgentHome, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: Fixture skill\n---\nInstructions")
	}
	lookup := func(query string) []protocol.Completion {
		t.Helper()
		result, err := s.Completions(ctx, "main", query)
		if err != nil {
			t.Fatal(err)
		}
		return result["items"].([]protocol.Completion)
	}
	if items := lookup("/machine:"); len(items) != 0 {
		t.Fatal(items)
	}
	if items := lookup("/tag:desk"); len(items) != 0 {
		t.Fatal(items)
	}
	if items := lookup("/skill:"); len(items) != 50 || items[0].Token != "/skill:skill-00" || items[49].Token != "/skill:skill-49" {
		t.Fatal(items)
	}
	if items := lookup("SKILL-59"); len(items) != 1 || items[0].Token != "/skill:skill-59" {
		t.Fatal(items)
	}
	for _, item := range lookup("/") {
		if item.Token[:7] != "/skill:" || item.Avatar != "" {
			t.Fatal("slash offers a non-skill", item)
		}
	}
	other, err := s.Registry.Create(ctx, "another", "研究 助手")
	if err != nil {
		t.Fatal(err)
	}
	sameName, err := s.Registry.Create(ctx, "same-name", "研究 助手")
	if err != nil {
		t.Fatal(err)
	}
	items := lookup("@研究")
	if len(items) != 2 || items[0].Token == items[1].Token {
		t.Fatal("same names must retain separate identities", items)
	}
	for _, item := range items {
		if item.Avatar == "" || item.Detail != "local" || !item.Local || item.Offline || item.Token != "@assistant:local/"+other.ID && item.Token != "@assistant:local/"+sameName.ID {
			t.Fatal("missing assistant identity/profile", item)
		}
	}
	if items := lookup("@Pot"); len(items) != 0 {
		t.Fatal("current assistant must not mention itself", items)
	}
	if items := lookup("@skill"); len(items) != 0 {
		t.Fatal("mention includes a skill", items)
	}
	if _, err := s.Completions(ctx, "unknown", ""); err == nil {
		t.Fatal("unknown owner accepted")
	}
	if _, err := s.Completions(ctx, "main", "bad\nquery"); err == nil {
		t.Fatal("multiline query accepted")
	}

}
