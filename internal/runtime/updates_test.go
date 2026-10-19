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
		record := tasks.Record{SchemaVersion: 8, Origin: protocol.TaskOrigin{Kind: "manual"}, AssistantID: owner.ID, Config: config, Plugins: []string{}, RunID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Generation: 1, Info: protocol.TaskInfo{ID: fmt.Sprintf("task%d", i), AssistantID: owner.ID, Title: "Queued work", Phase: protocol.TaskQueued}, Request: protocol.CreateTask{RequestID: fmt.Sprintf("request%d", i), Text: "retained task input"}}
		data, _ := json.Marshal(record)
		if _, err = r.Control.Tasks.DB().Exec("INSERT INTO tasks(assistant_id,id,phase,created,body) VALUES(?,?,?,?,?)", owner.ID, record.Info.ID, record.Info.Phase, i, string(data)); err != nil {
			t.Fatal(err)
		}
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
	if err = restarted.Control.Tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := restarted.Control.Tasks.Page(ctx, owner.ID, protocol.TaskQuery{Scope: "active", Limit: 1})
	if err != nil || active.Counts.Active != 0 {
		t.Fatal("interrupted work became dispatchable", active, err)
	}
}
