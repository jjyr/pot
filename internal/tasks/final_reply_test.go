package tasks

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

type finalReplyModels struct {
	models
	final string
}

func (s *finalReplyModels) Model(context.Context, core.ModelRequest) (io.ReadCloser, error) {
	delta := map[string]any{"content": s.final}
	finish := "stop"
	if s.calls.Add(1) == 1 {
		delta = map[string]any{"content": "Preparing the report.", "tool_calls": []any{map[string]any{"index": 0, "id": "check", "type": "function", "function": map[string]any{"name": "fixture_check", "arguments": "{}"}}}}
		finish = "tool_calls"
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
	return io.NopCloser(strings.NewReader("data: " + string(data) + "\n\n")), nil
}

func TestTaskResultUsesOnlyTheFinalReply(t *testing.T) {
	for _, final := range []string{"Final report.", ""} {
		t.Run(map[bool]string{true: "empty-final", false: "text-final"}[final == ""], func(t *testing.T) {
			ctx := context.Background()
			journal, err := OpenJournal(filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			manager, err := execution.New("runtime", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Stop()
			launch := launchFixture(t)
			if _, err := journal.Accept(ctx, launch); err != nil {
				t.Fatal(err)
			}
			model := &finalReplyModels{final: final}
			executor := &Executor{Journal: journal, Executions: manager, Context: ctx, Prepare: func(_ context.Context, run Run) (Prepared, error) {
				a := &agent.Agent{Services: model, Feedback: feedback{}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }, Tools: []modelprotocol.ToolDefinition{{Name: "fixture_check", Description: "Check the fixture", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}}, Dispatch: func(context.Context, core.ExecutionContext, modelprotocol.ToolCall, func(core.Update) error) (json.RawMessage, error) {
					return json.RawMessage(`{"checked":true}`), nil
				}}
				return Prepared{Agent: a, Context: core.ExecutionContext{Owner: run.Launch.TaskID, Role: core.Worker, Workspace: t.TempDir()}, Thread: core.Thread{Owner: run.Launch.TaskID, Generation: run.Launch.Generation, Config: run.Launch.Config}, ResourceDigest: pinTestResources(t, journal, run)}, nil
			}}
			result, err := executor.Execute(ctx, "runtime", "task", "run")
			if err != nil || result == nil || result.Outcome.Kind != "success" || result.Text != final {
				t.Fatalf("final reply=%q result=%+v error=%v", final, result, err)
			}
			page, err := journal.Events(ctx, "runtime", "task", "run", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			preparationRetained := false
			for _, event := range page.Events {
				if event.Event.Update.Type == "message_finished" && event.Event.Update.Text == "Preparing the report." {
					preparationRetained = true
				}
			}
			if !preparationRetained {
				t.Fatal("execution commentary disappeared from the transcript")
			}
		})
	}
}
