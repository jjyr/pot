package agent

import (
	"encoding/json"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"strings"
	"testing"
)

func TestSingleRecordEstimatesMatchContext(t *testing.T) {
	text := "中文🙂\nquoted \"text\""
	call := m.ToolCall{ID: "call-1", Name: "inspect_file", Arguments: json.RawMessage(`{ "path": "中文.txt", "line": 2 }`)}
	for _, p := range []m.Protocol{m.OpenAIChat, m.OpenAIResponses, m.AnthropicMessages, m.Gemini} {
		for _, test := range []struct {
			message m.Message
			want    uint64
		}{
			{m.TextMessage("user", text), EstimateTextTokens(text)},
			{m.Message{Role: "assistant", Calls: []m.ToolCall{call}}, EstimateToolCallTokens(call)},
		} {
			got, err := EstimateTokens(m.Request{Protocol: p, Model: "fixture", Messages: []m.Message{test.message}})
			if err != nil || got != test.want {
				t.Fatal(p, got, test.want, err)
			}
		}
	}
	// Base64 size must not affect image token estimates.
	var prior uint64
	for _, imageData := range []string{"AAAA", strings.Repeat("AAAA", 10000)} {
		raw, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}, map[string]any{"type": "image", "mimeType": "image/png", "width": 1, "height": 1, "data": imageData}}})
		got, err := EstimateToolResultTokens(call.ID, raw)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := toolResultMessage(call.ID, raw, true)
		if err != nil {
			t.Fatal(err)
		}
		want, err := EstimateTokens(m.Request{Protocol: m.OpenAIChat, Model: "fixture", Messages: []m.Message{msg}})
		if err != nil || got != want || got < 1844 {
			t.Fatal(got, want, err)
		}
		if prior != 0 && got != prior {
			t.Fatal("base64 size changed content estimate", got, prior)
		}
		prior = got
	}
	if _, err := EstimateToolResultTokens("call", json.RawMessage(`{"content":[{"type":"image","mimeType":"image/png","data":"bad"}]}`)); err == nil {
		t.Fatal("invalid image estimated as known")
	}
}
