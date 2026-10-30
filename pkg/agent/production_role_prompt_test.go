package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
)

type roleFeedback struct{ body json.RawMessage }

func (f *roleFeedback) LoadContext(context.Context) (json.RawMessage, error) { return f.body, nil }
func (f *roleFeedback) SaveContext(_ context.Context, b json.RawMessage) error {
	f.body = append(json.RawMessage(nil), b...)
	return nil
}
func (*roleFeedback) RecordTurn(context.Context, string) error                      { return nil }
func (*roleFeedback) InputStreaming(context.Context, string) error                  { return nil }
func (*roleFeedback) Ready(context.Context) error                                   { return nil }
func (*roleFeedback) Steering(context.Context, bool) ([]agent.SteeringInput, error) { return nil, nil }
func (*roleFeedback) AcknowledgeSteering(context.Context, []string) error           { return nil }
func (*roleFeedback) Observe(context.Context, agent.Observation) error              { return nil }
func (*roleFeedback) Settle(context.Context) error                                  { return nil }

type roleServices struct {
	request  core.ModelRequest
	calls    int
	protocol m.Protocol
}

func (s *roleServices) Model(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	s.request = r
	s.calls++
	text := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":33,\"completion_tokens\":10}}\n\ndata: [DONE]\n\n"
	if s.protocol == m.OpenAIResponses {
		text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":33,\"output_tokens\":10}}}\n\n"
	}
	return io.NopCloser(strings.NewReader(text)), nil
}
func (*roleServices) Compaction(context.Context, json.RawMessage) error { return nil }

// Exercise production policy selection through both real protocol serializers.
func TestProductionRolePromptRequests(t *testing.T) {
	for _, protocol := range []m.Protocol{m.OpenAIChat, m.OpenAIResponses} {
		for _, input := range []struct {
			name         string
			role         core.ExecutionRole
			notification bool
		}{{"main", core.Main, false}, {"notification", core.Main, true}, {"worker", core.Worker, false}} {
			role := input.role
			for _, override := range []string{"default", "custom", "template"} {
				t.Run(string(protocol)+"/"+input.name+"/"+override, func(t *testing.T) {
					settings := core.DefaultAgentSettings()
					settings.Plugins = map[string]core.PluginConfig{}
					settings.BuiltinTools = []string{}
					custom := "CUSTOM_SYSTEM_MARKER"
					if override != "default" {
						settings.SystemPrompt = &custom
					}
					if override == "template" {
						settings.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Models: map[string]map[string]json.RawMessage{}, Default: json.RawMessage(`{"model_messages":{"instructions_template":"TEMPLATE_SYSTEM_MARKER"}}`)}
					}
					profile := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
					spec, err := profile.Resolve()
					if err != nil {
						t.Fatal(err)
					}
					spec.GatewayProtocol = string(protocol)
					if err := spec.Rehash(); err != nil {
						t.Fatal(err)
					}
					s := &roleServices{protocol: protocol}
					a := agent.Agent{Services: s, Feedback: &roleFeedback{}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
					execution := core.ExecutionContext{Owner: "owner", Role: role, RolePrompt: assistant.PromptForRole(role)}
					err = a.Execute(context.Background(), execution, core.Thread{Owner: "owner", Generation: 3, Config: spec}, core.TurnRequest{RequestID: "role-test", Text: "hello", Notification: input.notification}, false, func(core.AgentEvent) error { return nil })
					if err != nil {
						t.Fatal(err)
					}
					if s.calls != 1 {
						t.Fatalf("model calls = %d", s.calls)
					}
					var body struct {
						Instructions string                           `json:"instructions"`
						Messages     []struct{ Role, Content string } `json:"messages"`
					}
					if err := json.Unmarshal(s.request.Body, &body); err != nil {
						t.Fatal(err)
					}
					system := body.Instructions
					if protocol == m.OpenAIChat {
						if len(body.Messages) == 0 || body.Messages[0].Role != "system" {
							t.Fatal("missing system message")
						}
						system = body.Messages[0].Content
					}
					wantMain := 0
					if role != core.Worker && override == "default" {
						wantMain = 1
					}
					wantTask := 0
					if role == core.Worker {
						wantTask = 1
					}
					if got := strings.Count(system, assistant.MainPrompt); got != wantMain {
						t.Fatalf("main policy count = %d, want %d", got, wantMain)
					}
					if got := strings.Count(system, assistant.TaskPrompt); got != wantTask {
						t.Fatalf("task policy count = %d, want %d", got, wantTask)
					}
					if strings.Contains(system, custom) != (override == "custom") {
						t.Fatal("custom override precedence changed")
					}
					if strings.Contains(system, "TEMPLATE_SYSTEM_MARKER") != (override == "template") {
						t.Fatal("template override precedence changed")
					}
				})
			}
		}
	}
}
