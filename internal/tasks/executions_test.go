package tasks

import (
	"context"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTaskCatalogueSharesLocalAndAgentLinkLifecycle(t *testing.T) {
	s, _, path := fixture(t)
	ctx := context.Background()
	local, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "local", Text: "Local work"})
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.CreateTask{RequestID: "linked", Target: &protocol.AgentRef{NodeID: "codex", Kind: "node", AgentID: "codex"}, Text: "Codex work"}
	linked := Record{AssistantID: "assistant", Request: request, Info: protocol.TaskInfo{ID: "linked-task", AssistantID: "assistant", Title: "Codex work", Phase: protocol.TaskQueued, AgentLink: &protocol.AgentLinkOrigin{LinkID: "connection", NodeID: "codex", Kind: "codex"}}, Execution: &Execution{Request: agentlink.TaskRequest{Target: agentlink.NodeTarget("codex"), RequestID: "linked", Text: "Codex work", From: protocol.AgentRef{NodeID: "machine", Kind: "assistant", AgentID: "assistant"}}}}
	if err = s.AcceptExecution(ctx, linked); err != nil {
		t.Fatal(err)
	}
	stored, err := s.FindRequest(ctx, "assistant", request)
	if err != nil || stored.Config != nil || stored.Execution == nil {
		t.Fatal(stored, err)
	}
	s.options.MaxTasks = 2
	extra := linked
	extra.Info.ID = "extra-task"
	extra.Request.RequestID = "extra-request"
	if err = s.AcceptExecution(ctx, extra); err == nil {
		t.Fatal("linked task bypassed shared catalogue limit")
	}
	page, err := s.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all"})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	filtered, err := s.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all", ExecutionTarget: `{"runtime_id":"codex"}`})
	if err != nil || filtered.Total != 1 || filtered.Items[0].ID != linked.Info.ID {
		t.Fatal("linked execution filter", filtered, err)
	}
	changed := request
	changed.Text = "different"
	if _, err = s.FindRequest(ctx, "assistant", changed); err == nil {
		t.Fatal("accepted conflicting intent")
	}
	if err = s.StopExecution(ctx, "assistant", linked.Info.ID); err != nil {
		t.Fatal(err)
	}
	stored.Execution.Receipt = agentlink.Task{ID: "thread", RequestID: "linked", RunID: "turn-one", State: "completed", Result: "Codex result"}
	stored.Info.Phase = protocol.TaskComplete
	stored.Info.RunID = "run-one"
	if ok, err := s.UpdateExecution(ctx, *stored, ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	updated, err := s.Get(ctx, "assistant", linked.Info.ID)
	if err != nil || !updated.Execution.StopRequested || updated.Result.Text != "Codex result" || updated.Outcome.Text != "Codex result" {
		t.Fatal(updated, err)
	}
	if ok, err := s.UpdateExecution(ctx, *stored, ""); err != nil || ok {
		t.Fatal("stale execution replaced current run", ok, err)
	}
	stale := *updated
	stale.Info.Phase = protocol.TaskRunning
	stale.Execution = &Execution{Request: updated.Execution.Request, Receipt: updated.Execution.Receipt}
	stale.Execution.Receipt.State = "running"
	if ok, err := s.UpdateExecution(ctx, stale, "turn-one"); err != nil || ok {
		t.Fatal("delayed poll reopened completed execution", ok, err)
	}
	if err = s.finish(ctx, local, protocol.TaskComplete, &Terminal{Outcome: core.Success, Text: "Local result"}, 0, ""); err != nil {
		t.Fatal(err)
	}
	decisions := []protocol.TaskDisposition{{TaskID: local.Info.ID, RunID: local.RunID, Archived: true}, {TaskID: linked.Info.ID, RunID: "wrong", Archived: true}}
	if _, err = s.SetDispositions(ctx, "assistant", decisions); err == nil {
		t.Fatal("accepted stale mixed archive batch")
	}
	check, err := s.Get(ctx, "assistant", local.Info.ID)
	if err != nil || check.Info.Archived {
		t.Fatal("partially committed archive batch", check, err)
	}
	decisions[1].RunID = "run-one"
	if items, err := s.SetDispositions(ctx, "assistant", decisions); err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	var outcomes int
	if err = s.DB().QueryRow("SELECT count(*) FROM task_outcomes").Scan(&outcomes); err != nil || outcomes != 2 {
		t.Fatal("duplicate or missing outcomes", outcomes, err)
	}
	options := s.options
	s.Close()
	s, err = Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	updated, err = s.Get(ctx, "assistant", linked.Info.ID)
	if err != nil || updated.Config != nil || updated.Execution.Receipt.ID != "thread" || !updated.Info.Archived {
		t.Fatal("reopen changed execution binding", updated, err)
	}
	// A fast continuation may already be complete at its first observation.
	// It still has a distinct outcome even though the displayed phase is unchanged.
	next := *updated
	execution := *updated.Execution
	next.Execution = &execution
	next.Execution.Receipt.RunID = "turn-two"
	next.Execution.Receipt.Result = "Second result"
	next.Info.RunID = "run-two"
	if ok, err := s.UpdateExecution(ctx, next, "turn-one"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	continued, err := s.Get(ctx, "assistant", linked.Info.ID)
	if err != nil || continued.Info.Archived || continued.Execution.StopRequested || continued.Result.Text != "Second result" {
		t.Fatal("continuation kept previous lifecycle", continued, err)
	}
	if err = s.DB().QueryRow("SELECT count(*) FROM task_outcomes").Scan(&outcomes); err != nil || outcomes != 3 {
		t.Fatal("continuation lost its outcome", outcomes, err)
	}

}

func TestLinkedTaskDoesNotRequireLocalExecutionServices(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir()+"/tasks.sqlite", Options{NodeID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	request := protocol.CreateTask{RequestID: "codex", Text: "work", Target: &protocol.AgentRef{NodeID: "codex", Kind: "node", AgentID: "codex"}}
	record := Record{AssistantID: "assistant", Request: request, Info: protocol.TaskInfo{ID: "task", AssistantID: "assistant", Phase: protocol.TaskQueued, AgentLink: &protocol.AgentLinkOrigin{LinkID: "connection", NodeID: "codex", Kind: "codex"}}, Execution: &Execution{Request: agentlink.TaskRequest{Target: agentlink.NodeTarget("codex"), RequestID: "codex", Text: "work", From: protocol.AgentRef{NodeID: "source", Kind: "assistant", AgentID: "assistant"}}}}
	if err = s.AcceptExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx); err != nil {
		t.Fatal("linked task required a local executor", err)
	}
	if err = s.AwaitExecution(ctx, "assistant", "task", ""); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Get(ctx, "assistant", "task")
	if err != nil || !waiting.Info.RemoteWaiting || waiting.Info.Phase != protocol.TaskQueued || waiting.Execution.Receipt.ID != "" {
		t.Fatal("unavailable owner changed execution facts", waiting, err)
	}
}
