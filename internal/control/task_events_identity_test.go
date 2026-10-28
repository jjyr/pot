package control

import (
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestTaskEventPageUsesActualLaunchAndPreviousRun(t *testing.T) {
	s := taskResourceService(t)
	ctx := t.Context()
	launch := tasks.Launch{ControlID: "local", RuntimeID: "local", AssistantID: "main", TaskID: "task", RunID: "previous", Generation: 1, ExecutionTarget: core.RuntimeTarget("local"), Config: taskConfig(t), Plugins: []string{}, Text: "synthetic"}
	if _, err := s.RunJournal.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunJournal.Claim(ctx, "local", "task", "previous"); err != nil {
		t.Fatal(err)
	}
	event := core.AgentEvent{Generation: 1, TurnID: "previous", Seq: 1, Update: core.Update{Type: "text_delta", Text: "previous output"}}
	if err := s.RunJournal.AppendEvent(ctx, "local", "task", "previous", event); err != nil {
		t.Fatal(err)
	}
	previous := tasks.Record{SchemaVersion: 14, Plugins: []string{}, AssistantID: "main", RunID: "previous", Launch: &launch, Info: protocol.TaskInfo{ID: "task", AssistantID: "main", RunID: "previous", Phase: protocol.TaskComplete}}
	body, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO task_run_history(assistant_id,task_id,run_id,body) VALUES(?,?,?,?)", "main", "task", "previous", body); err != nil {
		t.Fatal(err)
	}
	current := previous
	current.RunID, current.PreviousRunID, current.Launch = "not-launched", "previous", nil
	current.Info.RunID = current.RunID
	write := func() {
		t.Helper()
		body, e := json.Marshal(current)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Tasks.DB().ExecContext(ctx, "INSERT INTO tasks(assistant_id,id,phase,created,body) VALUES(?,?,?,?,?) ON CONFLICT(assistant_id,id) DO UPDATE SET body=excluded.body", "main", "task", "queued", 1, body)
		if e != nil {
			t.Fatal(e)
		}
	}
	write()
	page, err := s.TaskEvents(ctx, "main", "task", 0, 100)
	if err != nil || page.RunID != "previous" || len(page.Events) != 1 || page.Events[0].Event.TurnID != "previous" {
		t.Fatal("previous launch lost its own identity", page, err)
	}
	current.Launch = &launch
	write()
	page, err = s.TaskEvents(ctx, "main", "task", 0, 100)
	if err != nil || page.RunID != "previous" {
		t.Fatal("metadata run tag replaced actual journal source", page, err)
	}
	launch.RuntimeID = "unavailable-runner"
	write()
	raw, _ := json.Marshal(event)
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO task_event_cache(task_id,sequence,event) VALUES(?,?,?)", "task", 1, raw); err != nil {
		t.Fatal(err)
	}
	page, err = s.TaskEvents(ctx, "main", "task", 0, 100)
	if err != nil || !page.Unavailable || page.RunID != "previous" || len(page.Events) != 1 {
		t.Fatal("unavailable cache lost the chosen launch identity", page, err)
	}
}

func TestLinkedUnavailableEventPageUsesBoundReceiptRun(t *testing.T) {
	b, _ := linkBackendFixture(t)
	ctx := t.Context()
	record := tasks.Record{AssistantID: "main", Request: protocol.CreateTask{RequestID: "linked-events", Text: "synthetic"}, Info: protocol.TaskInfo{ID: "linked-events", AssistantID: "main", RunID: "metadata-tag", Phase: protocol.TaskRunning, AgentLink: &protocol.AgentLinkOrigin{LinkID: "peer-link", NodeID: "peer"}}, Execution: &tasks.Execution{Receipt: agentlink.Task{ID: "remote-task", RunID: "receipt-run", State: "running"}}}
	if err := b.s.Tasks.AcceptExecution(ctx, record); err != nil {
		t.Fatal(err)
	}
	value, err := (agentPort{b.s}).taskCall(ctx, "main", agentlink.TaskEvents, agentlink.Call{ID: record.Info.ID})
	if err != nil {
		t.Fatal(err)
	}
	page, ok := value.(tasks.EventPage)
	if !ok || !page.Unavailable || page.RunID != "receipt-run" || len(page.Events) != 0 {
		t.Fatal("linked unavailable page invented metadata provenance", value)
	}
}
