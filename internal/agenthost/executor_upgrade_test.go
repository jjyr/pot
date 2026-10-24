package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestInitialSchemaReopenRetainsExactRetriesAndIndependentSessions(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "caller", Kind: "assistant", AgentID: "owner"}
	for _, state := range []string{"queued", "completed", "cancelling"} {
		old := agentlink.TaskRequest{Target: agentlink.NodeTarget(b.nodeID), RequestID: state, From: from, Text: "任务 <tag> & \"quoted\"\n继续"}
		identity := agentlink.Identity{Address: protocol.AgentRef{NodeID: b.nodeID, Kind: "task", AgentID: state}, Name: "Codex", Adapter: "codex"}
		thread := "thread-" + state
		data := map[string]any{"task": map[string]any{"id": state, "request_id": state, "run_id": "run-" + state, "receiver": identity, "state": state, "result": "saved"}, "request": old, "thread_id": thread, "turn_id": "turn-" + state, "cleanup_uncertain": true}
		if _, err := b.db.Exec("INSERT INTO agent_adapter_jobs(id,caller,request_id,payload,data) VALUES(?,?,?,?,?)", state, callerKey(from), state, encoded(old), encoded(data)); err != nil {
			t.Fatal(err)
		}
		if _, err := b.db.Exec("INSERT INTO agent_adapter_sessions(task_id,workspace,mode,thread_id) VALUES(?,?,?,?)", state, "/original/workspace", "read-only", thread); err != nil {
			t.Fatal(err)
		}
		if _, err := b.db.Exec("INSERT INTO agent_adapter_inputs VALUES(?,?,?,?)", state, "previous-input", "saved guidance", "previous-run"); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := storage.Migrate(ctx, b.db, storage.TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	a, err := b.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := newBackend(b.db, b.nodeID, filepath.Dir(a.Workspace))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "completed", "cancelling"} {
		request := agentlink.TaskRequest{Target: agentlink.NodeTarget(b.nodeID), RequestID: state, From: from, Text: "任务 <tag> & \"quoted\"\n继续"}
		result, err := reopened.Submit(ctx, request)
		if err != nil || result.ID != state || result.RunID != "run-"+state || result.State != state || result.Result != "saved" {
			t.Fatal("exact retry lost original work", result, err)
		}
		var payload, raw, thread, workspace, mode string
		if err = b.db.QueryRow("SELECT payload,data FROM agent_adapter_jobs WHERE id=?", state).Scan(&payload, &raw); err != nil {
			t.Fatal(err)
		}
		if payload != encoded(request) {
			t.Fatal("payload differs from real wire encoding", payload, encoded(request))
		}
		var j job
		if err = json.Unmarshal([]byte(raw), &j); err != nil || j.Request.Target != agentlink.NodeTarget(b.nodeID) || !j.CleanupUncertain || j.ThreadID != "thread-"+state || j.TurnID != "turn-"+state {
			t.Fatal(j, err)
		}
		if err = b.db.QueryRow("SELECT thread_id,workspace,mode FROM agent_adapter_sessions WHERE task_id=?", state).Scan(&thread, &workspace, &mode); err != nil || thread != j.ThreadID || workspace != "/original/workspace" || mode != "read-only" {
			t.Fatal(thread, workspace, mode, err)
		}
		request.Text += "changed"
		if _, err = reopened.Submit(ctx, request); !errors.Is(err, agentlink.ErrConflict) {
			t.Fatal("changed retry accepted", err)
		}
		request.Target = agentlink.AssistantTarget(b.nodeID, "codex")
		if _, err = reopened.Submit(ctx, request); !errors.Is(err, agentlink.ErrUnsupported) {
			t.Fatal("retired public selector accepted", err)
		}
	}
	var jobs, inputs int
	if err = b.db.QueryRow("SELECT count(*) FROM agent_adapter_jobs").Scan(&jobs); err != nil || jobs != 3 {
		t.Fatal(jobs, err)
	}
	if err = b.db.QueryRow("SELECT count(*) FROM agent_adapter_inputs").Scan(&inputs); err != nil || inputs != 3 {
		t.Fatal(inputs, err)
	}
}
