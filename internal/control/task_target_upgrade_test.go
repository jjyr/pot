package control

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
)

func TestTaskReceiverReopenPreservesRetriesAndCallerScope(t *testing.T) {
	t.Parallel()
	backend, second := linkBackendFixture(t)
	source := agentControl(t, "caller")
	receiver := &agentControlFixture{service: backend.s, endpoint: controlPeerEndpoint(backend.s)}
	connectAgentControls(t, source, receiver)
	ctx := context.Background()
	request := agentlink.TaskRequest{Target: agentlink.AssistantTarget(backend.s.options.RuntimeID, "main"), RequestID: "original", From: agentlink.AssistantTarget("caller", "main"), Text: "Original task\n任务 <tag> & \"quoted\""}
	first, err := backend.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backend.s.integrationDB.Exec("UPDATE agent_link_node_settings SET default_assistant_id=? WHERE singleton=1", second); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = storage.Migrate(ctx, backend.s.integrationDB, storage.ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	retry, err := backend.Submit(ctx, request)
	if err != nil || retry.ID != first.ID || retry.Receiver.Address != request.Target {
		t.Fatal("reopened retry changed receiver", retry, err)
	}
	conflict := request
	conflict.Text = "Changed intent"
	if _, err = backend.Submit(ctx, conflict); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("changed request accepted", err)
	}
	outsider := request.From
	outsider.NodeID = "other-caller"
	if _, err = backend.Status(ctx, outsider, first.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("foreign caller read original task", err)
	}
	if _, err = backend.Cancel(ctx, outsider, first.ID); !errors.Is(err, agentlink.ErrNotFound) {
		t.Fatal("foreign caller cancelled original task", err)
	}
	changed := request
	changed.Target.AgentID = second
	if _, err = backend.Submit(ctx, changed); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("new target reused original receipt", err)
	}
	var count int
	if err = backend.s.Tasks.DB().QueryRow("SELECT count(*) FROM tasks").Scan(&count); err != nil || count != 1 {
		t.Fatal("reopen duplicated work", count, err)
	}
	var canonical string
	encoded, _ := json.Marshal(request)
	if err = backend.s.integrationDB.QueryRow("SELECT payload FROM agent_link_received_tasks WHERE id=?", first.ID).Scan(&canonical); err != nil || canonical != string(encoded) {
		t.Fatal(canonical, string(encoded), err)
	}
	request.From = outsider
	separate, err := backend.Submit(ctx, request)
	if err != nil || separate.ID == first.ID || separate.Receiver.Address != first.Receiver.Address {
		t.Fatal("callers shared an accepted task", separate, err)
	}
}
