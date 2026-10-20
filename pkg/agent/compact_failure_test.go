package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
)

type rejectSummarySave struct{ *feedback }

func (f rejectSummarySave) SaveContext(ctx context.Context, raw json.RawMessage) error {
	var state Checkpoint
	_ = json.Unmarshal(raw, &state)
	for _, message := range state.Messages {
		if message.Kind.Type == "summary" {
			return errors.New("checkpoint disk failure")
		}
	}
	return f.feedback.SaveContext(ctx, raw)
}
func TestCompactFailurePreservesOriginalAndNeverDispatches(t *testing.T) {
	for _, kind := range []string{"empty", "truncated", "tools", "save"} {
		t.Run(kind, func(t *testing.T) {
			a, f, s, thread, execution := setup(t)
			a.CompactOnly = true
			thread.Config.Params.Agent.UseProviderUsage = false
			a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
			a.Dispatch = func(context.Context, core.ExecutionContext, m.ToolCall, func(core.Update) error) (json.RawMessage, error) {
				t.Fatal("compact dispatched a tool")
				return nil, nil
			}
			state := Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{}, Receipts: []ToolReceipt{}, SteeringIDs: []string{}}
			for range 4 {
				state.Messages = append(state.Messages, m.TextMessage("user", strings.Repeat("goal ", 1000)), m.TextMessage("assistant", strings.Repeat("observed ", 1000)))
			}
			f.body, _ = json.Marshal(state)
			if kind == "save" {
				a.Feedback = rejectSummarySave{f}
			}
			s.model = func(r core.ModelRequest, _ int) (string, error) {
				if r.CompactionID == nil || !strings.Contains(string(r.Body), `"tool_choice":"none"`) || !strings.Contains(string(r.Body), `"name":"probe"`) {
					t.Fatal("catalog was removed or enabled")
				}
				switch kind {
				case "empty":
					return response(""), nil
				case "truncated":
					return strings.Replace(response("incomplete"), `"finish_reason":"stop"`, `"finish_reason":"length"`, 1), nil
				case "tools":
					return response("", m.ToolCall{ID: "call", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
				default:
					return response("small valid summary"), nil
				}
			}
			if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "idle"}, false, func(core.AgentEvent) error { return nil }); err == nil {
				t.Fatal("bad summary committed")
			}
			var after Checkpoint
			if err := json.Unmarshal(f.body, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(state.Messages, after.Messages) || after.ModelRequestID != nil || after.Started != nil {
				t.Fatal("failed compaction changed history or blocked retry")
			}
		})
	}
}
