package consumer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

// These adapters deliberately implement only the public library contracts.
// In particular the model service has no product RPC implementation.
type memoryFeedback struct {
	body    json.RawMessage
	settled bool
}

func (f *memoryFeedback) LoadContext(context.Context) (json.RawMessage, error) { return f.body, nil }
func (f *memoryFeedback) SaveContext(_ context.Context, body json.RawMessage) error {
	f.body = append(f.body[:0], body...)
	return nil
}
func (*memoryFeedback) RecordTurn(context.Context, string) error     { return nil }
func (*memoryFeedback) InputStreaming(context.Context, string) error { return nil }
func (*memoryFeedback) Ready(context.Context) error                  { return nil }
func (*memoryFeedback) Steering(context.Context, bool) ([]agent.SteeringInput, error) {
	return nil, nil
}
func (*memoryFeedback) AcknowledgeSteering(context.Context, []string) error { return nil }
func (*memoryFeedback) Observe(context.Context, agent.Observation) error    { return nil }
func (f *memoryFeedback) Settle(context.Context) error                      { f.settled = true; return nil }

type modelService struct {
	feedback  *memoryFeedback
	calls     int
	responses []string
}

func (s *modelService) Model(_ context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	var checkpoint agent.Checkpoint
	if err := json.Unmarshal(s.feedback.body, &checkpoint); err != nil {
		return nil, err
	}
	if checkpoint.ModelRequestID == nil || *checkpoint.ModelRequestID != request.ID {
		return nil, fmt.Errorf("model called before durable request identity")
	}
	if s.calls >= len(s.responses) {
		return nil, fmt.Errorf("unexpected model round")
	}
	body := s.responses[s.calls]
	s.calls++
	return io.NopCloser(strings.NewReader(body)), nil
}
func (*modelService) Compaction(context.Context, json.RawMessage) error { return nil }

func modelResponse(text string, calls ...modelprotocol.ToolCall) string {
	delta := map[string]any{"content": text}
	reason := "stop"
	if len(calls) > 0 {
		reason = "tool_calls"
		wire := []any{}
		for index, call := range calls {
			wire = append(wire, map[string]any{"index": index, "id": call.ID, "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
		}
		delta["tool_calls"] = wire
	}
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 33, "completion_tokens": 10}})
	return "data: " + string(body) + "\n\ndata: [DONE]\n\n"
}

func TestAgentFromExternalModule(t *testing.T) {
	for _, customTool := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom_tool=%t", customTool), func(t *testing.T) {
			feedback := &memoryFeedback{}
			service := &modelService{feedback: feedback, responses: []string{modelResponse("done")}}
			settings := core.DefaultAgentSettings()
			settings.Plugins = map[string]core.PluginConfig{}
			settings.BuiltinTools = []string{}
			profile := core.Profile{ID: "external", Revision: 1, Name: "external", Provider: "mock", Model: "fixture", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
			spec, err := profile.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			cleanup := false
			a := &agent.Agent{Services: service, Feedback: feedback, Cleanup: func(context.Context, core.ExecutionContext, bool) error { cleanup = true; return nil }}
			dispatches := 0
			if customTool {
				call := modelprotocol.ToolCall{ID: "external-tool", Name: "echo", Arguments: json.RawMessage(`{"value":"fixture"}`)}
				service.responses = []string{modelResponse("", call), modelResponse("done")}
				a.Tools = []modelprotocol.ToolDefinition{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`)}}
				a.Dispatch = func(_ context.Context, _ core.ExecutionContext, received modelprotocol.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
					var checkpoint agent.Checkpoint
					if err := json.Unmarshal(feedback.body, &checkpoint); err != nil {
						return nil, err
					}
					if checkpoint.Started == nil || *checkpoint.Started != call.ID || received.Name != call.Name {
						return nil, fmt.Errorf("tool effect preceded checkpoint")
					}
					dispatches++
					return json.RawMessage(`{"value":"fixture"}`), nil
				}
			}
			var events []core.AgentEvent
			execution := core.ExecutionContext{Owner: "external-owner", Role: core.Main}
			thread := core.Thread{Owner: execution.Owner, Generation: 3, Config: spec}
			request := core.TurnRequest{RequestID: "external-turn", Text: "complete fixture"}
			sink := func(event core.AgentEvent) error { events = append(events, event); return nil }
			if err := a.Execute(context.Background(), execution, thread, request, false, sink); err != nil {
				t.Fatal(err)
			}
			if !cleanup || !feedback.settled || len(events) == 0 {
				t.Fatal("turn did not settle")
			}
			last := events[len(events)-1]
			if last.Update.Outcome == nil || last.Update.Outcome.Kind != "success" {
				t.Fatalf("terminal event: %+v", last)
			}
			if customTool && dispatches != 1 {
				t.Fatalf("tool dispatch count: %d", dispatches)
			}
			calls := service.calls
			if err := a.Execute(context.Background(), execution, thread, request, true, sink); err != nil {
				t.Fatal(err)
			}
			if service.calls != calls {
				t.Fatal("completed turn was sampled again")
			}
		})
	}
}
