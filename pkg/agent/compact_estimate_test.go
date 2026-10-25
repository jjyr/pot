package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
)

func TestCompactSummaryUsesProviderEstimateAndPreservesLimits(t *testing.T) {
	for _, kind := range []string{
		"valid", "missing", "invalid-length", "stale-prefix", "different-model",
		"different-tools", "different-route", "disabled", "window", "output-budget",
		"appended-instruction", "max-input", "empty-summary",
	} {
		t.Run(kind, func(t *testing.T) {
			a, f, s, thread, execution := setup(t)
			thread.Config.ContextWindow = 500000
			thread.Config.Params.Agent.ContextWindow = 500000
			thread.Config.Params.MaxOutputTokens = 128000
			thread.Config.Params.Agent.AutoCompact = true
			thread.Config.Params.Agent.UseProviderUsage = kind != "disabled"
			thread.Config.Params.Agent.ContextReserve = 4096
			state := Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{}, Receipts: []ToolReceipt{}, SteeringIDs: []string{}}
			for range 4 {
				state.Messages = append(state.Messages, m.TextMessage("user", "goal"), m.TextMessage("assistant", "verified work"))
			}
			// Synthetic replay is deliberately much larger than reported provider
			// usage. Exercise the real content estimator, not an injected total.
			state.Messages[1].Replay = json.RawMessage(`{"content":"` + strings.Repeat("r", 2160000) + `"}`)
			replayRoute := route(thread.Config)
			state.Messages[1].ReplayRoute = &replayRoute
			state.Messages[len(state.Messages)-1].Usage = &m.TokenUsage{Input: 368500, Output: 66}
			input := m.Request{
				Protocol: m.OpenAIChat, Model: thread.Config.Model, System: "system",
				Messages: append([]m.Message(nil), state.Messages...), MaxOutputTokens: 128000,
				Tools: []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}},
			}
			anchor, err := captureAnchor(input, input.Messages)
			if err != nil || anchor == nil {
				t.Fatal("capture provider usage", err)
			}
			state.UsageAnchor = anchor
			switch kind {
			case "missing":
				state.UsageAnchor = nil
			case "invalid-length":
				anchor.PrefixLength = len(input.Messages) + 2
			case "stale-prefix":
				input.Messages[0].Text = "edited goal"
			case "different-model":
				input.Model = "different-model"
			case "different-tools":
				input.Tools[0].Description = "changed definition"
			case "different-route":
				input.Messages[1].ReplayRoute = nil
			case "window":
				anchor.Tokens = 500001
			case "output-budget":
				anchor.Tokens = 495000
			case "appended-instruction":
				anchor.Tokens = 500000 - 8192
			case "max-input":
				limit := uint64(368566)
				thread.Config.Params.Agent.MaxInputTokens = &limit
			}
			if err := thread.Config.Rehash(); err != nil {
				t.Fatal(err)
			}
			raw, err := EstimateTokens(input)
			if err != nil || raw <= thread.Config.ContextWindow {
				t.Fatal("fixture must exceed the raw context window", raw, err)
			}
			if kind == "valid" {
				calibrated, err := a.estimate(input, state.UsageAnchor, true)
				if err != nil || calibrated != 368566 {
					t.Fatal("fixture provider estimate", calibrated, err)
				}
				t.Logf("raw replay estimate=%d, provider-calibrated estimate=%d", raw, calibrated)
			}
			original, _ := json.Marshal(state)
			f.body = append(json.RawMessage(nil), original...)
			ordinary, err := m.Encode(input)
			if err != nil {
				t.Fatal(err)
			}
			var prefix map[string]any
			if err := json.Unmarshal(ordinary, &prefix); err != nil {
				t.Fatal(err)
			}
			s.model = func(r core.ModelRequest, index int) (string, error) {
				var body map[string]any
				if err := json.Unmarshal(r.Body, &body); err != nil {
					t.Fatal(err)
				}
				if index > 0 {
					if r.CompactionID != nil || body["max_tokens"] != float64(128000) {
						t.Fatal("continuation compacted again or changed the ordinary output cap")
					}
					return response("continued"), nil
				}
				messages := body["messages"].([]any)
				if r.CompactionID == nil || body["tool_choice"] != "none" || body["max_tokens"] != float64(8192) ||
					!reflect.DeepEqual(body["tools"], prefix["tools"]) ||
					len(messages) != len(prefix["messages"].([]any))+1 ||
					!reflect.DeepEqual(messages[:len(messages)-1], prefix["messages"]) {
					t.Fatal("summary changed the ordinary prefix, catalog, tool policy or output cap")
				}
				if kind == "empty-summary" {
					return response(""), nil
				}
				return response("Goal and verified work retained."), nil
			}
			err = a.compact(context.Background(), execution, thread, &state, &input, nil, false, func(core.Update) error { return nil })
			if kind != "valid" {
				want := "compaction request exceeds model context limits"
				calls := 0
				if kind == "empty-summary" {
					want = "compaction returned tools or empty summary"
					calls = 1
				}
				if err == nil || !strings.Contains(err.Error(), want) || s.calls != calls {
					t.Fatalf("err=%v calls=%d; want %q and %d calls", err, s.calls, want, calls)
				}
				var saved, before Checkpoint
				if err := json.Unmarshal(f.body, &saved); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(original, &before); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(saved.Messages, before.Messages) || !reflect.DeepEqual(saved.UsageAnchor, before.UsageAnchor) {
					t.Fatal("failed summary changed history or its usage anchor")
				}
				return
			}
			if err != nil || s.calls != 1 || state.UsageAnchor != nil || state.Messages[0].Kind.Type != "summary" {
				t.Fatal("provider-calibrated context did not compact", err, s.calls)
			}
			if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "next", Text: "continue"}, false, func(core.AgentEvent) error { return nil }); err != nil {
				t.Fatal(err)
			}
			var saved Checkpoint
			if err := json.Unmarshal(f.body, &saved); err != nil || saved.CompletedRequest != "next" || s.calls != 2 {
				t.Fatal("compacted task did not finish continuation", err, saved.CompletedRequest, s.calls)
			}
		})
	}
}
