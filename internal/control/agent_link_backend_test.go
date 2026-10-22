package control

import (
	"context"
	"errors"
	"testing"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func linkBackendFixture(t *testing.T) (potLinkBackend, string) {
	t.Helper()
	manager, err := execution.New("node", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "node", BootID: "boot", Manager: manager, Keys: keys, Maintenance: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	main, err := s.Registry.Create(ctx, "main", "Main")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Registry.Create(ctx, "second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "model", Name: "Model", Model: "fixture", Endpoint: "http://127.0.0.1:1", Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{main.ID, second.ID} {
		c, err := s.Conversation(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Configure(ctx, spec, 0); err != nil {
			t.Fatal(err)
		}
	}
	return potLinkBackend{s}, second.ID
}
func TestPotLinkBackendDeduplicatesBeforeDefaultChangesAndScopesCaller(t *testing.T) {
	b, second := linkBackendFixture(t)
	ctx := context.Background()
	directory, err := b.Directory(ctx)
	if err != nil || directory.DefaultAssistantID != "main" {
		t.Fatal(directory, err)
	}
	request := agentlink.TaskRequest{RequestID: "request-one", From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "sender"}, Text: "One task\nKeep the original binding"}
	first, err := b.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.s.integrationDB.ExecContext(ctx, "UPDATE agent_link_node_settings SET default_assistant_id=? WHERE singleton=1", second); err != nil {
		t.Fatal(err)
	}
	again, err := b.Submit(ctx, request)
	if err != nil || again.ID != first.ID || again.Assistant.Address.AgentID != "main" {
		t.Fatal(first, again, err)
	}
	conflict := request
	conflict.Text = "changed"
	if _, err = b.Submit(ctx, conflict); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("changed request accepted", err)
	}
	outsider := request.From
	outsider.NodeID = "other-peer"
	if _, err = b.Status(ctx, outsider, first.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("foreign caller read original task", err)
	}
	if _, err = b.Cancel(ctx, outsider, first.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("foreign caller cancelled original task", err)
	}
	request.From = outsider
	separate, err := b.Submit(ctx, request)
	if err != nil || separate.ID == first.ID || separate.Assistant.Address.AgentID != second {
		t.Fatal(separate, err)
	}
}
func TestPotLinkBackendDeletedDefaultDoesNotRetargetOrHideHistory(t *testing.T) {
	b, second := linkBackendFixture(t)
	ctx := context.Background()
	if _, err := b.Directory(ctx); err != nil {
		t.Fatal(err)
	}
	request := agentlink.TaskRequest{RequestID: "original", From: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "sender"}, Text: "Original task\nRetain its original owner"}
	accepted, err := b.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.s.Registry.Delete(ctx, "main", 1); err != nil {
		t.Fatal(err)
	}
	directory, err := b.Directory(ctx)
	if err != nil || len(directory.Assistants) != 1 || directory.DefaultAssistantID != "" {
		t.Fatal(directory, err)
	}
	request.RequestID = "new-default-request"
	if _, err = b.Submit(ctx, request); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("missing default silently retargeted", err)
	}
	request.AssistantID = second
	if _, err = b.Submit(ctx, request); err != nil {
		t.Fatal("explicit surviving assistant unavailable", err)
	}
	historical, err := b.Status(ctx, acceptedFrom(), accepted.ID)
	if err != nil || historical.Assistant.Address != accepted.Assistant.Address {
		t.Fatal(historical, err)
	}
	cancelled, err := b.Cancel(ctx, acceptedFrom(), accepted.ID)
	if err != nil || cancelled.Assistant.Address != accepted.Assistant.Address || cancelled.State != "cancelled" {
		t.Fatal(cancelled, err)
	}
	if err = b.s.Registry.Delete(ctx, second, 1); err != nil {
		t.Fatal(err)
	}
	directory, err = b.Directory(ctx)
	if err != nil || len(directory.Assistants) != 0 || directory.DefaultAssistantID != "" {
		t.Fatal(directory, err)
	}
	if _, err = b.Status(ctx, acceptedFrom(), accepted.ID); err != nil {
		t.Fatal("empty directory hid history", err)
	}
}
func acceptedFrom() protocol.AgentRef {
	return protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "sender"}
}

func TestLocalBackendCannotClaimLinkedExecution(t *testing.T) {
	b, _ := linkBackendFixture(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "node", Kind: "assistant", AgentID: "main"}
	record := tasks.Record{AssistantID: "main", Request: protocol.CreateTask{RequestID: "linked-request", Text: "Remote work"}, Info: protocol.TaskInfo{ID: "linked-task", AssistantID: "main", Phase: protocol.TaskRunning, AgentLink: &protocol.AgentLinkOrigin{LinkID: "peer-link", NodeID: "peer"}}, Execution: &tasks.Execution{Request: agentlink.TaskRequest{From: from}, Receipt: agentlink.Task{ID: "remote-task", RunID: "remote-run", State: "running"}}}
	if err := b.s.Tasks.AcceptExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(ctx, from, record.Info.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("local adapter claimed remote execution", err)
	}
	if _, err := b.Cancel(ctx, from, record.Info.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("local adapter cancelled remote execution", err)
	}
	if err := b.s.Tasks.Stop(ctx, "main", record.Info.ID, "interrupt"); !errors.Is(err, agentlink.ErrUnsupported) {
		t.Fatal("local lifecycle mutated linked task", err)
	}
	stored, err := b.s.Tasks.Get(ctx, "main", record.Info.ID)
	if err != nil || stored.Info.Phase != protocol.TaskRunning || stored.Result != nil || stored.Execution.StopRequested {
		t.Fatal(stored, err)
	}
}
