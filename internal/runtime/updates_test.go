package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestUpdateInterruptsAllQueuedTasksAcrossRestart(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	maintenance := func() bool { return true }
	options := Options{Root: filepath.Join(t.TempDir(), "data"), ResourceDirectory: t.TempDir(), DisableMetadataRefresh: true, Control: control.Options{Keys: keys, Maintenance: maintenance}}
	r, err := Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Maintaining = maintenance
	owner, err := r.Control.Registry.Create(ctx, "owner", "Pot")
	if err != nil {
		t.Fatal(err)
	}
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{}
	profile := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	config, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	// More than one page, with no destination launch or model request.
	for i := range 55 {
		record := tasks.Record{SchemaVersion: 13, NodeID: r.Control.Tasks.NodeID(), Origin: protocol.TaskOrigin{Kind: "manual"}, AssistantID: owner.ID, Config: &config, Plugins: []string{}, RunID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Generation: 1, Info: protocol.TaskInfo{RunID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), ID: fmt.Sprintf("task%d", i), AssistantID: owner.ID, Title: "Queued work", Phase: protocol.TaskQueued}, Request: protocol.CreateTask{RequestID: fmt.Sprintf("request%d", i), Text: "retained task input"}}
		data, _ := json.Marshal(record)
		if _, err = r.Control.Tasks.DB().Exec("INSERT INTO tasks(assistant_id,id,phase,created,body) VALUES(?,?,?,?,?)", owner.ID, record.Info.ID, record.Info.Phase, i, string(data)); err != nil {
			t.Fatal(err)
		}
	}

	remote := tasks.Record{AssistantID: owner.ID, Request: protocol.CreateTask{RequestID: "remote-request", Text: "Remote work"}, Info: protocol.TaskInfo{ID: "remote-task", AssistantID: owner.ID, Phase: protocol.TaskRunning, AgentLink: &protocol.AgentLinkOrigin{LinkID: "unavailable-link", NodeID: "peer"}}, Execution: &tasks.Execution{Request: agentlink.TaskRequest{From: protocol.AgentRef{NodeID: r.Control.Tasks.NodeID(), Kind: "assistant", AgentID: owner.ID}}, Receipt: agentlink.Task{ID: "original-task", RunID: "original-run", State: "running"}}}
	if err := r.Control.Tasks.AcceptExecution(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if executing, err := r.Control.Tasks.Executing(ctx, owner.ID); err != nil || executing {
		t.Fatal("linked receipt counted as local execution", executing, err)
	}
	work, err := r.updateWork()
	if err != nil || work.Tasks != 55 {
		t.Fatal("update counted linked execution as local work", work, err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for i := range 55 {
		record, err := restarted.Control.Tasks.Get(ctx, owner.ID, fmt.Sprintf("task%d", i))
		if err != nil || record == nil {
			t.Fatal(err)
		}
		if record.Info.Phase != protocol.TaskInterrupted || record.Launch != nil || record.Request.Text != "retained task input" {
			t.Fatal("queued work was replayed or lost", record)
		}
	}

	stored, err := restarted.Control.Tasks.Get(ctx, owner.ID, "remote-task")
	if err != nil || stored.Info.Phase != protocol.TaskRunning || stored.Result != nil || stored.Execution.StopRequested || stored.Execution.Receipt.ID != "original-task" {
		t.Fatal("update changed remote execution", stored, err)
	}
	if err = restarted.Control.Tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := restarted.Control.Tasks.Page(ctx, owner.ID, protocol.TaskQuery{Scope: "active", Limit: 1})
	if err != nil || active.Counts.Active != 1 {
		t.Fatal("interrupted work became dispatchable", active, err)
	}
}
