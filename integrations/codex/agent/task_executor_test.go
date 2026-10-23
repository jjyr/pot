package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

// Assistant session tests admit inbox work without using the explicit task port.
func submitMessageFixture(b *backend, ctx context.Context, r agentlink.TaskRequest) (agentlink.Task, error) {
	if r.AssistantID == "" {
		r.AssistantID = "codex"
	}
	parent := protocol.Envelope{ID: "fixture-" + r.RequestID, Kind: "message", From: r.From, To: protocol.AgentRef{NodeID: b.nodeID, Kind: "assistant", AgentID: r.AssistantID}, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: r.Text}
	return b.submitMessage(ctx, r, parent)
}

func TestDirectTasksOwnIndependentPersistentSessions(t *testing.T) {
	b := testBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}
	request := agentlink.TaskRequest{From: from, Executor: "agent", RequestID: "first", Text: "approve"}
	first, err := b.Submit(ctx, request)
	if err != nil || first.Execution == nil || first.Assistant.Address.Valid() {
		t.Fatal(first, err)
	}
	pending := awaitReview(t, b, reviewQuery(from))
	second, err := b.Submit(ctx, agentlink.TaskRequest{From: from, Executor: "agent", RequestID: "second", Text: "parallel"})
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, b, from, second.ID, "completed")
	var firstThread, secondThread string
	b.db.QueryRow("SELECT thread_id FROM codex_task_sessions WHERE task_id=?", first.ID).Scan(&firstThread)
	b.db.QueryRow("SELECT thread_id FROM codex_task_sessions WHERE task_id=?", second.ID).Scan(&secondThread)
	if firstThread == "" || secondThread == "" || firstThread == secondThread {
		t.Fatal("tasks share a session", firstThread, secondThread)
	}
	a, _ := b.resolve(ctx, "codex")
	if a.ThreadID != "" {
		t.Fatal("task used the assistant's thread", a)
	}
	decision := reviewQuery(from)
	decision.ID, decision.Action = pending.Pending[0].ID, "allow_once"
	if _, err := b.Review(ctx, decision); err != nil {
		t.Fatal(err)
	}
	awaitTask(t, b, from, first.ID, "completed")
	retry, err := b.Submit(ctx, request)
	if err != nil || retry.ID != first.ID {
		t.Fatal("retry duplicated task", retry, err)
	}
	guidance := agentlink.TaskGuidance{From: from, ID: first.ID, RequestID: "follow-up", Text: "continue"}
	resumed, err := b.Input(ctx, guidance)
	if err != nil || resumed.ID != first.ID || resumed.RunID == first.RunID {
		t.Fatal(resumed, err)
	}
	awaitTask(t, b, from, first.ID, "completed")
	j, _ := b.read(ctx, from, first.ID)
	if j.ThreadID != firstThread || j.Task.Result != "result: continue\n" {
		t.Fatal("continuation replaced session", j)
	}
	if _, err = b.Input(ctx, guidance); err != nil {
		t.Fatal(err)
	}
	guidance.Text = "changed"
	if _, err = b.Input(ctx, guidance); err == nil {
		t.Fatal("changed retry accepted")
	}
	other := guidance
	other.From.AgentID = "another"
	if _, err = b.Input(ctx, other); err == nil {
		t.Fatal("foreign continuation accepted")
	}
	var histories int
	b.db.QueryRow("SELECT count(*) FROM codex_task_inputs WHERE task_id=?", first.ID).Scan(&histories)
	if histories != 1 {
		t.Fatal(histories)
	}
	// A restarted host finds the same task/thread and never borrows assistant context.
	b2, err := newBackend(b.db, b.nodeID, filepath.Dir(a.Workspace))
	if err != nil {
		t.Fatal(err)
	}
	b2.binary = fakeBinary
	retry, err = b2.Submit(ctx, request)
	if err != nil || retry.ID != first.ID {
		t.Fatal(retry, err)
	}
	cancel()
	<-done
	done = make(chan struct{})
	close(done)
	guidance.RequestID, guidance.Text = "after-restart", "restart continuation"
	if _, err = b2.Input(context.Background(), guidance); err != nil {
		t.Fatal(err)
	}
	queued, err := b2.read(context.Background(), from, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Stop the original scheduler before another host owns the persistent session.
	b2.runTask(context.Background(), queued)
	j, _ = b2.read(context.Background(), from, first.ID)
	if j.ThreadID != firstThread || j.Task.State != "completed" {
		t.Fatal(j)
	}
}

func TestCodexTaskRequiresExplicitExecutionAndAssistantOwner(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	for _, r := range []agentlink.TaskRequest{
		{From: protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}, AssistantID: "codex", RequestID: "assistant-target", Text: "work"},
		{From: protocol.AgentRef{NodeID: "pot", Kind: "user", AgentID: "user"}, Executor: "agent", RequestID: "direct-user", Text: "work"},
		{From: protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}, Executor: "agent", AssistantID: "codex", RequestID: "mixed", Text: "work"},
	} {
		if _, err := b.Submit(ctx, r); err == nil {
			t.Fatal("ambiguous execution accepted", r)
		}
	}
}

func TestDirectTaskDoesNotRequireCodexAssistant(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	a, _ := b.resolve(ctx, "codex")
	if err := b.DeleteAssistant(ctx, agentlink.DeleteAssistantRequest{AssistantID: a.ID, Revision: a.Revision}); err != nil {
		t.Fatal(err)
	}
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "main"}
	task, err := b.Submit(ctx, agentlink.TaskRequest{From: from, Executor: "agent", RequestID: "no-assistant", Text: "execute independently"})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := b.read(ctx, from, task.ID)
	b.runTask(ctx, j)
	awaitTask(t, b, from, task.ID, "completed")
	directory, err := b.Directory(ctx)
	if err != nil || len(directory.Assistants) != 0 || directory.TaskExecutor == nil {
		t.Fatal(directory, err)
	}
}
