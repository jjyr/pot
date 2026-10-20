package modelprotocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCompactionRetainsPrefixAndRestoresToolPolicy(t *testing.T) {
	for _, protocol := range protocols() {
		t.Run(string(protocol), func(t *testing.T) {
			request := Request{Protocol: protocol, Model: "fixture", System: "stable instructions", MaxOutputTokens: 500, Tools: []ToolDefinition{{Name: "probe", Description: "same catalog", InputSchema: json.RawMessage(`{"type":"object"}`)}}, Messages: []Message{TextMessage("user", "original"), TextMessage("assistant", "confirmed")}}
			original, err := ContextPayload(request)
			if err != nil {
				t.Fatal(err)
			}
			request.DisableTools = true
			prefix, err := ContextPayload(request)
			if err != nil || string(prefix) != string(original) {
				t.Fatal("policy changed prefix", err)
			}
			request.Messages = append(request.Messages, TextMessage("user", "compact now"))
			raw, err := Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := object(raw)
			originalBody, _ := object(original)
			tools, _ := json.Marshal(body["tools"])
			want, _ := json.Marshal(originalBody["tools"])
			if string(tools) != string(want) {
				t.Fatal("tool catalog changed")
			}
			switch protocol {
			case OpenAIChat, OpenAIResponses:
				if body["tool_choice"] != "none" {
					t.Fatal(body)
				}
			case AnthropicMessages:
				if !reflect.DeepEqual(body["tool_choice"], map[string]any{"type": "none"}) {
					t.Fatal(body)
				}
			case Gemini:
				if body["toolConfig"] == nil {
					t.Fatal(body)
				}
			}
			request.DisableTools = false
			raw, _ = Encode(request)
			body, _ = object(raw)
			if body["tool_choice"] != nil || body["toolConfig"] != nil {
				t.Fatal("compact tool policy leaked")
			}
		})
	}
}
