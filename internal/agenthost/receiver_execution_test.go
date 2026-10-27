package agenthost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestReceiverExecutionContextUsesActualActiveTaskActorRun(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
	task, err := h.backend.Submit(ctx, agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(h.backend.nodeID), RequestID: "bound-context", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.backend.update(ctx, task.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	h.backend.active[task.ID] = func() {}
	input := hostToolInput{Operation: "execution_context", JobID: task.ID, RunID: task.RunID}
	binding, err := h.executionContext(ctx, input)
	if err != nil || binding.Actor.Kind != "task" || binding.Actor != binding.Task || binding.Task.AgentID != task.ID || binding.RunID != task.RunID {
		t.Fatal("wrong execution actor", binding, err)
	}
	for _, mutate := range []func(*hostToolInput){func(in *hostToolInput) { in.RunID = "old-run" }, func(in *hostToolInput) { in.JobID = "foreign-task" }, func(in *hostToolInput) { in.FromAssistantID = "codex" }} {
		invalid := input
		mutate(&invalid)
		if _, err = h.executionContext(ctx, invalid); err == nil {
			t.Fatal("foreign context accepted")
		}
	}
	delete(h.backend.active, task.ID)
	if _, err = h.executionContext(ctx, input); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatal("inactive execution accepted", err)
	}
}

func TestReceiverDisconnectPreservesNativeExecutionAndHistory(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
	task, err := h.backend.Submit(ctx, agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(h.backend.nodeID), RequestID: "disconnect-context", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.backend.update(ctx, task.ID, func(j *job) error { j.Task.State = "running"; return nil }); err != nil {
		t.Fatal(err)
	}
	var cancelled atomic.Bool
	h.backend.active[task.ID] = func() { cancelled.Store(true) }
	if _, err = h.db.Exec("INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES('connection','http://fixture.invalid/mcp',?,'fixture-secret','active')", h.backend.nodeID); err != nil {
		t.Fatal(err)
	}
	client := agentHostClient(h.token)
	defer client.client.CloseIdleConnections()
	client.client.Transport = hostRoundTripper(func(r *http.Request) (*http.Response, error) {
		r.RemoteAddr = "127.0.0.1:23456"
		r.Host = hostListen
		w := httptest.NewRecorder()
		h.handler().ServeHTTP(w, r)
		return w.Result(), nil
	})
	var result any
	if err = client.call(ctx, "/api/v1/agent-host/receiver/disconnect", struct {
		ConnectionID string `json:"connection_id"`
	}{"connection"}, &result); err != nil {
		t.Fatal(err)
	}
	var state string
	h.db.QueryRow("SELECT state FROM mcp_receiver_connections WHERE id='connection'").Scan(&state)
	current, err := h.backend.read(ctx, from, task.ID)
	if err != nil || cancelled.Load() || current.Task.State != "running" || state != "disconnected" {
		t.Fatal("disconnect altered execution", current, state, err)
	}
}

func TestReceiverNativeReviewCannotApproveAnotherRun(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	caller := protocol.AgentRef{NodeID: "pot", Kind: "assistant", AgentID: "director"}
	task, err := b.Submit(ctx, agentlink.TaskRequest{From: caller, Target: agentlink.NodeTarget(b.nodeID), RequestID: "review-run", Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := b.read(ctx, caller, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.reviews["request-id"] = &pendingReview{value: agentlink.ExecutionApproval{ID: "request-id", JobID: task.ID}, job: original, ctx: ctx, done: make(chan string, 1)}
	if err = b.update(ctx, task.ID, func(j *job) error { j.Task.RunID = "new-run"; j.Task.State = "waiting_approval"; return nil }); err != nil {
		t.Fatal(err)
	}
	_, err = b.Review(ctx, agentlink.ReviewRequest{From: protocol.AgentRef{NodeID: "pot", Kind: "user", AgentID: "operator"}, Caller: caller, ID: "request-id", Action: "allow_once"})
	if !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("old review crossed into new run", err)
	}
	if b.reviews["request-id"].resolved {
		t.Fatal("stale request consumed a decision")
	}
}
