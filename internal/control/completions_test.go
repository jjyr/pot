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

func TestAssistantCompletionsRetireDefinitionsWithoutUsingHistoricalContacts(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "retire-local"), agentControl(t, "retire-remote")
	owner, err := a.service.Registry.Create(ctx, "observer", "Observer")
	if err != nil {
		t.Fatal(err)
	}
	connection := connectAgentControls(t, a, b)
	// Contacts are historical identity metadata, not an active peer directory.
	for _, directory := range []protocol.Directory{
		{NodeID: "retire-remote", Assistants: []protocol.MailAssistant{
			{Address: protocol.AgentRef{NodeID: "retire-remote", Kind: "assistant", AgentID: "main"}, Name: "Historical remote"},
			{Address: protocol.AgentRef{NodeID: "retire-remote", Kind: "assistant", AgentID: "deleted"}, Name: "Deleted contact"},
		}},
		{NodeID: "old-node", Assistants: []protocol.MailAssistant{{Address: protocol.AgentRef{NodeID: "old-node", Kind: "assistant", AgentID: "main"}, Name: "Old contact"}}},
	} {
		if err := a.service.Mailbox.Register(ctx, directory); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(query string) []protocol.Completion {
		t.Helper()
		value, err := a.service.Completions(ctx, owner.ID, query)
		if err != nil {
			t.Fatal(err)
		}
		return value["items"].([]protocol.Completion)
	}
	if items := lookup("@"); len(items) != 2 {
		t.Fatal("contacts entered current candidates", items)
	}
	retire := func(service *Service) {
		t.Helper()
		definitions, err := service.Registry.Definitions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, definition := range definitions {
			if definition.ID == "main" {
				if err := service.DeleteAssistant(ctx, definition.ID, definition.Revision); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatal("historical main assistant missing")
	}
	retire(a.service)
	if items := lookup("@"); len(items) != 1 || items[0].Token != "@assistant:retire-remote/main" {
		t.Fatal("local retirement removed the wrong same-ID identity", items)
	}
	if items := lookup("@contact"); len(items) != 0 {
		t.Fatal("historical contact matched a new query", items)
	}
	if _, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "disconnect", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	retire(b.service)
	// The source has no deletion evidence until it receives a new directory.
	if items := lookup("@"); len(items) != 1 || !items[0].Offline {
		t.Fatal("disconnection fabricated a remote deletion", items)
	}
	if _, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "reconnect", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.agentLinks.service.Directory(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"@", "@Main", "@Historical", "@contact"} {
		if items := lookup(query); len(items) != 0 {
			t.Fatal("confirmed directory deletion resurrected from history", query, items)
		}
	}
	contacts, err := a.service.Mailbox.Contacts(ctx)
	if err != nil || len(contacts) != 3 {
		t.Fatal("selection refresh erased historical identities", contacts, err)
	}
}
