package control

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionsUseOwnerResourcesAndBoundMetadataWithoutExecution(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	if _, err := s.Registry.Create(ctx, "create", "Pot"); err != nil {
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

func TestAssistantCompletionsReadPinnedPeerProfilesAndExcludeRevokedNodes(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "mention-a"), agentControl(t, "mention-b")
	connection := connectAgentControls(t, a, b)
	lookup := func() []protocol.Completion {
		t.Helper()
		value, err := a.service.Completions(ctx, "main", "@")
		if err != nil {
			t.Fatal(err)
		}
		return value["items"].([]protocol.Completion)
	}
	items := lookup()
	if len(items) != 1 || items[0].Token != "@assistant:mention-b/main" || items[0].Label != "Main" || items[0].Avatar == "" || items[0].Offline || items[0].Local || !strings.HasPrefix(items[0].Detail, "Pot · ") {
		t.Fatal("peer mention lost its pinned profile/identity", items)
	}
	for _, service := range []*Service{a.service, b.service} {
		records, err := service.Mailbox.History(ctx, "main", 0)
		if err != nil || len(records) != 0 {
			t.Fatal("opening mention picker sent peer mail", records, err)
		}
	}
	if _, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "disconnect", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	if items := lookup(); len(items) != 1 || !items[0].Offline || items[0].Token != "@assistant:mention-b/main" {
		t.Fatal("offline directory must retain its original identity", items)
	}
	if _, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "reconnect", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "delete", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	if items := lookup(); len(items) != 0 {
		t.Fatal("revoked node remains in mention picker", items)
	}
}
