package control

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestTaskTargetReceiverUpgradeReturnsOriginalAcceptedTask(t *testing.T) {
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
	old := map[string]any{"request_id": request.RequestID, "from": request.From, "text": request.Text}
	payload, _ := json.Marshal(old)
	if _, err = backend.s.integrationDB.Exec("UPDATE agent_link_received_tasks SET payload=? WHERE id=?", string(payload), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = backend.s.integrationDB.Exec("UPDATE agent_link_node_settings SET default_assistant_id=? WHERE singleton=1", second); err != nil {
		t.Fatal(err)
	}
	if _, err = backend.s.integrationDB.Exec("DELETE FROM pot_migrations WHERE id LIKE '0031_%'"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = storage.Migrate(ctx, backend.s.integrationDB, storage.ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	retry, err := backend.Submit(ctx, request)
	if err != nil || retry.ID != first.ID || retry.Receiver.Address != request.Target {
		t.Fatal("upgraded retry changed receiver", retry, err)
	}
	changed := request
	changed.Target.AgentID = second
	if _, err = backend.Submit(ctx, changed); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("new target reused original receipt", err)
	}
	var count int
	if err = backend.s.Tasks.DB().QueryRow("SELECT count(*) FROM tasks").Scan(&count); err != nil || count != 1 {
		t.Fatal("upgrade duplicated work", count, err)
	}
	var canonical string
	encoded, _ := json.Marshal(request)
	if err = backend.s.integrationDB.QueryRow("SELECT payload FROM agent_link_received_tasks WHERE id=?", first.ID).Scan(&canonical); err != nil || canonical != string(encoded) {
		t.Fatal(canonical, string(encoded), err)
	}
	var decoded protocol.CreateTask
	if err = protocol.DecodeStrict([]byte(`{"request_id":"legacy","text":"work","link_id":"connection"}`), &decoded); err == nil {
		t.Fatal("retired task selector accepted")
	}
}
