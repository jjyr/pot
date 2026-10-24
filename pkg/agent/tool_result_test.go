package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
)

func TestToolResultFailureClassificationMatchesEvents(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		result string
		failed bool
	}{
		{"nonzero", `{"exit_code":7}`, true},
		{"zero", `{"exit_code":0}`, false},
		{"exec-running", `{"execution_status":"running","session_id":"process","exit_code":null,"reason":null}`, false},
		{"write-stdin-running", `{"session_id":"process","stdout":"partial","exit_code":null}`, false},
		{"write-stdin-finished", `{"execution_status":"finished","exit_code":9,"session_id":null}`, true},
		{"isError", `{"isError":true}`, true},
		{"reason", `{"reason":"refused"}`, true},
		{"empty-reason", `{"reason":""}`, true},
		{"no-markers", `{"isError":false,"exit_code":null,"reason":null}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, s, thread, execution := setup(t)
			a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
			a.Dispatch = func(context.Context, core.ExecutionContext, m.ToolCall, func(core.Update) error) (json.RawMessage, error) {
				return json.RawMessage(test.result), nil
			}
			s.model = func(_ core.ModelRequest, index int) (string, error) {
				if index == 0 {
					return response("", m.ToolCall{ID: "result", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
				}
				return response("done"), nil
			}
			finished := 0
			if err := a.Execute(t.Context(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "probe"}, false, func(event core.AgentEvent) error {
				if event.Update.Type == "tool_finished" {
					finished++
					if event.Update.Failed != test.failed || ToolResultFailed(event.Update.Result) != test.failed {
						t.Errorf("unexpected classification: %+v", event.Update)
					}
				}
				return nil
			}); err != nil || finished != 1 {
				t.Fatal(err, finished)
			}
		})
	}
}
