package transport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAssistantCatalogueProjectsOwnerScopedTaskExecution(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	runtime, err := product.Open(ctx, product.Options{Root: filepath.Join(t.TempDir(), "runtime"), DisableMetadataRefresh: true, Control: control.Options{Keys: keys}})
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
	body, _ := json.Marshal(map[string]any{"info": protocol.TaskInfo{ID: "activity", AssistantID: first.ID, Phase: protocol.TaskQueued}})
	_, err = runtime.Control.Tasks.DB().ExecContext(ctx, "INSERT INTO tasks(assistant_id,id,phase,created,body) VALUES(?,?,'queued',1,?)", first.ID, "activity", string(body))
	if err != nil {
		t.Fatal(err)
	}
	app := &Application{Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID}
	for _, test := range []struct {
		phase                       string
		paused, archived, executing bool
	}{{"queued", false, false, false}, {"waiting", false, false, false}, {"waiting_approval", false, false, false}, {"running", false, false, true}, {"running", true, false, false}, {"complete", false, false, false}, {"running", false, true, false}} {
		_, err := runtime.Control.Tasks.DB().ExecContext(ctx, `UPDATE tasks SET phase=?,body=json_set(body,'$.info.phase',?,'$.info.paused',json(?),'$.info.archived',json(?)) WHERE assistant_id=? AND id=?`, test.phase, test.phase, boolJSON(test.paused), boolJSON(test.archived), first.ID, "activity")
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
