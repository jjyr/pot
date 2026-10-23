package transport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAssistantCatalogueProjectsLocalOwnerScopedTaskExecution(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	runtime, err := product.Open(ctx, product.Options{Root: filepath.Join(t.TempDir(), "runtime"), DisableMetadataRefresh: true, Control: control.Options{Keys: keys, Maintenance: func() bool { return true }}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	first, err := runtime.Control.Registry.Create(ctx, "first", "First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Control.Registry.Create(ctx, "second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(tasks.Record{SchemaVersion: 13, NodeID: runtime.Control.Tasks.NodeID(), AssistantID: first.ID, Plugins: []string{}, Origin: protocol.TaskOrigin{Kind: "manual"}, Request: protocol.CreateTask{RequestID: "activity-request", Text: "Activity"}, Info: protocol.TaskInfo{ID: "activity", AssistantID: first.ID, Phase: protocol.TaskQueued}})
	_, err = runtime.Control.Tasks.DB().ExecContext(ctx, "INSERT INTO tasks(assistant_id,id,phase,created,body) VALUES(?,?,'queued',1,?)", first.ID, "activity", string(body))
	if err != nil {
		t.Fatal(err)
	}
	app := &Application{Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID}
	for _, test := range []struct {
		phase                               string
		paused, archived, linked, executing bool
	}{{"queued", false, false, false, false}, {"waiting", false, false, false, false}, {"waiting_approval", false, false, false, false}, {"running", false, false, false, true}, {"running", true, false, false, false}, {"complete", false, false, false, false}, {"running", false, true, false, false}, {"running", false, false, true, false}} {
		execution, origin := "null", "null"
		if test.linked {
			data, _ := json.Marshal(tasks.Execution{Request: agentlink.TaskRequest{From: protocol.AgentRef{NodeID: runtime.Control.Tasks.NodeID(), Kind: "assistant", AgentID: first.ID}}, Receipt: agentlink.Task{ID: "remote-activity", State: "running"}})
			execution = string(data)
			data, _ = json.Marshal(protocol.AgentLinkOrigin{LinkID: "activity-peer", NodeID: "peer", AssistantID: "remote"})
			origin = string(data)
		}
		_, err := runtime.Control.Tasks.DB().ExecContext(ctx, `UPDATE tasks SET phase=?,body=json_set(body,'$.info.phase',?,'$.info.paused',json(?),'$.info.archived',json(?),'$.execution',json(?),'$.info.agent_link',json(?)) WHERE assistant_id=? AND id=?`, test.phase, test.phase, boolJSON(test.paused), boolJSON(test.archived), execution, origin, first.ID, "activity")
		if err != nil {
			t.Fatal(err)
		}
		value, err := app.Call(ctx, Request{Method: "GET", Path: "/api/v1/assistants"})
		if err != nil {
			t.Fatal(err)
		}
		states := map[string]bool{}
		for _, item := range value.([]map[string]any) {
			states[item["id"].(string)] = item["executing"].(bool)
		}
		if states[first.ID] != test.executing || states[second.ID] {
			t.Fatalf("%+v: %v", test, states)
		}
	}
}

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
