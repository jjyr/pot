package control

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestRetiredRemoteLaunchRemainsBoundAndNeverReplaysLocally(t *testing.T) {
	t.Parallel()
	s := taskControl(t, "node", "")
	ctx := context.Background()
	launch := tasks.Launch{ControlID: "node", AssistantID: "main", RuntimeID: "retired-runner", TaskID: "historic-task", RunID: "original-run", Generation: 1, ExecutionTarget: core.RuntimeTarget("retired-runner"), Config: taskConfig(t), Plugins: []string{}, Text: "original work"}
	record := tasks.Record{SchemaVersion: 13, NodeID: launch.RuntimeID, AssistantID: "main", Info: protocol.TaskInfo{ID: launch.TaskID, AssistantID: "main", RunID: launch.RunID, MachineID: launch.RuntimeID, Phase: protocol.TaskRunning, ExecutionTarget: &launch.ExecutionTarget}, Request: protocol.CreateTask{Text: launch.Text}, Config: &launch.Config, Plugins: []string{}, RunID: launch.RunID, Generation: 1, Launch: &launch}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO tasks(assistant_id,id,phase,created,body)VALUES(?,?,?,0,?)", "main", launch.TaskID, protocol.TaskRunning, string(raw)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.Tasks.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.Tasks.Get(ctx, "main", launch.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Launch.RuntimeID != launch.RuntimeID || saved.RunID != launch.RunID || !saved.Info.RemoteWaiting || saved.Info.Phase != protocol.TaskWaiting {
		t.Fatal(saved)
	}
	local, err := s.RunJournal.Runs(ctx)
	if err != nil || len(local) != 0 {
		t.Fatal("retired remote work was launched locally", local, err)
	}
	if _, err = s.TaskRecovery(ctx, "main", launch.TaskID, launch.RunID, "reconcile"); err == nil {
		t.Fatal("missing original remote evidence was fabricated")
	}
}
