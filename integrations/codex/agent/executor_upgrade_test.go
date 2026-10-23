package agent

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

func TestExecutorUpgradeRetainsExactRetriesAndIndependentSessions(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	// The preceding schema has the same tables; restore its receipt prefix and
	// insert historical requests without dispatching any work.
	if _, err := b.db.Exec("DELETE FROM pot_migrations WHERE id LIKE '0025_%'"); err != nil {
		t.Fatal(err)
	}
	from := protocol.AgentRef{NodeID: "caller", Kind: "assistant", AgentID: "owner"}
	for _, state := range []string{"queued", "completed", "cancelling"} {
		old := agentlink.TaskRequest{Executor: "codex", RequestID: state, From: from, Text: "任务 <tag> & \"quoted\"\n继续"}
		identity := agentlink.Identity{Address: protocol.AgentRef{NodeID: b.nodeID, Kind: "task", AgentID: state}, Name: "Codex", Adapter: "codex"}
		j := job{Task: agentlink.Task{ID: state, RequestID: state, RunID: "run-" + state, Execution: &identity, State: state, Result: "saved"}, Request: old, ThreadID: "thread-" + state, TurnID: "turn-" + state, CleanupUncertain: true}
		if _, err := b.db.Exec("INSERT INTO codex_link_jobs(id,caller,request_id,payload,data) VALUES(?,?,?,?,?)", state, callerKey(from), state, encoded(old), encoded(j)); err != nil {
			t.Fatal(err)
		}
		if _, err := b.db.Exec("INSERT INTO codex_task_sessions VALUES(?,?,?,?)", state, "/original/workspace", "read-only", j.ThreadID); err != nil {
			t.Fatal(err)
		}
		if _, err := b.db.Exec("INSERT INTO codex_task_inputs VALUES(?,?,?,?)", state, "previous-input", "saved guidance", "previous-run"); err != nil {
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
		request := agentlink.TaskRequest{Executor: "agent", RequestID: state, From: from, Text: "任务 <tag> & \"quoted\"\n继续"}
		result, err := reopened.Submit(ctx, request)
		if err != nil || result.ID != state || result.RunID != "run-"+state || result.State != state || result.Result != "saved" {
			t.Fatal("exact retry lost original work", result, err)
		}
		var payload, raw, thread, workspace, mode string
		if err = b.db.QueryRow("SELECT payload,data FROM codex_link_jobs WHERE id=?", state).Scan(&payload, &raw); err != nil {
			t.Fatal(err)
		}
		if payload != encoded(request) {
			t.Fatal("payload differs from real wire encoding", payload, encoded(request))
		}
		var j job
		if err = json.Unmarshal([]byte(raw), &j); err != nil || j.Request.Executor != "agent" || !j.CleanupUncertain || j.ThreadID != "thread-"+state || j.TurnID != "turn-"+state {
			t.Fatal(j, err)
		}
		if err = b.db.QueryRow("SELECT thread_id,workspace,mode FROM codex_task_sessions WHERE task_id=?", state).Scan(&thread, &workspace, &mode); err != nil || thread != j.ThreadID || workspace != "/original/workspace" || mode != "read-only" {
			t.Fatal(thread, workspace, mode, err)
		}
		request.Text += "changed"
		if _, err = reopened.Submit(ctx, request); !errors.Is(err, agentlink.ErrConflict) {
			t.Fatal("changed retry accepted", err)
		}
		request.Executor = "codex"
		if _, err = reopened.Submit(ctx, request); !errors.Is(err, agentlink.ErrUnsupported) {
			t.Fatal("retired public selector accepted", err)
		}
	}
	var jobs, inputs int
	if err = b.db.QueryRow("SELECT count(*) FROM codex_link_jobs").Scan(&jobs); err != nil || jobs != 3 {
		t.Fatal(jobs, err)
	}
	if err = b.db.QueryRow("SELECT count(*) FROM codex_task_inputs").Scan(&inputs); err != nil || inputs != 3 {
		t.Fatal(inputs, err)
	}
}
