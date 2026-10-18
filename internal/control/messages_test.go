package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

func TestUnifiedMessageToolBindsSenderAndRemovesWaitAndGuide(t *testing.T) {
	s := taskResourceService(t)
	for _, worker := range []bool{false, true} {
		names := map[string]bool{}
		for _, tool := range s.productTools(worker) {
			names[tool.Name] = true
		}
		if !names["send_message"] || names["guide_task"] || names["wait_task"] || names["send_assistant_message"] {
			t.Fatal("obsolete or missing tool", names)
		}
		if worker && len(names) != 1 {
			t.Fatal("worker acquired task management authority", names)
		}
	}
	dispatch := s.productDispatch("main", "turn", 1, false)["send_message"]
	_, err := dispatch(context.Background(), core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "spoof", Name: "send_message", Arguments: json.RawMessage(`{"to":{"control_id":"local","kind":"assistant","agent_id":"main"},"text":"fake","sender":{"kind":"runtime","agent_id":"runtime"}}`)}, func(core.Update) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "sender") {
		t.Fatal("model supplied sender identity", err)
	}
}

func TestTitleOnlyTaskBriefNeverStartsGuessingWorker(t *testing.T) {
	s := taskResourceService(t)
	_, err := s.productCall(context.Background(), "main", "brief", "create_task", json.RawMessage(`{"text":"Task A: execute the long operation\n"}`))
	if err == nil || !strings.Contains(err.Error(), "complete execution instructions") {
		t.Fatal("title-only handoff accepted", err)
	}
	var count int
	if err = s.Tasks.DB().QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count); err != nil || count != 0 {
		t.Fatal("incomplete brief launched work", count, err)
	}
}
