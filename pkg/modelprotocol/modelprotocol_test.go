package modelprotocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func push(t *testing.T, d *Decoder, event string) StreamEvent {
	t.Helper()
	value, err := d.Push(json.RawMessage(event))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func protocols() []Protocol {
	return []Protocol{OpenAIChat, OpenAIResponses, AnthropicMessages, Gemini}
}
func TestResponsesToolsKeepOmissionSemantics(t *testing.T) {
	schemas := []json.RawMessage{
		json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"rows":{"type":"integer","minimum":1},"cols":{"type":"integer","minimum":1}},"required":["session_id"],"additionalProperties":false}`),
		json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"},"session_id":{"type":"string"},"reset":{"type":"boolean"}},"additionalProperties":false}`),
	}
	for _, schema := range schemas {
		raw, err := Encode(Request{Protocol: OpenAIResponses, Model: "model", MaxOutputTokens: 100, Tools: []ToolDefinition{{"probe", "test", schema}}})
		if err != nil {
			t.Fatal(err)
		}
		body, err := object(raw)
		if err != nil {
			t.Fatal(err)
		}
		tool := obj(array(body["tools"])[0])
		if strict, exists := tool["strict"]; !exists || strict != false {
			t.Fatal("Responses must explicitly preserve optional arguments", tool)
		}
		want, _ := object(schema)
		expected, _ := json.Marshal(want)
		actual, _ := json.Marshal(tool["parameters"])
		if string(actual) != string(expected) {
			t.Fatalf("optional argument schema changed: %s", actual)
		}
	}
}
func TestFourEncodersPreserveSchemasImagesAndReasoning(t *testing.T) {
	effort := "vendor-mode"
	schema := json.RawMessage(`{"type":"object","properties":{"partition":{"type":"string","enum":["left","right"]}},"required":["partition"],"additionalProperties":false}`)
	call := ToolCall{"call", "browser", json.RawMessage(`{}`)}
	assistant := TextMessage("assistant", "")
	assistant.Calls = []ToolCall{call}
	tool := TextMessage("tool", "observation")
	tool.CallID = "call"
	tool.Images = []ModelImage{{"image/jpeg", "AQID", 1280, 800}}
	for _, p := range protocols() {
		t.Run(string(p), func(t *testing.T) {
			raw, err := Encode(Request{Protocol: p, Model: "model", System: "system", Messages: []Message{assistant, tool}, Tools: []ToolDefinition{{"dispatch", "Dispatch", schema}}, MaxOutputTokens: 100, ReasoningEffort: &effort})
			if err != nil {
				t.Fatal(err)
			}
			body, err := object(raw)
			if err != nil {
				t.Fatal(err)
			}
			var actual any
			switch p {
			case OpenAIChat:
				actual = get(array(body["tools"])[0], "function", "parameters")
			case OpenAIResponses:
				actual = get(array(body["tools"])[0], "parameters")
			case AnthropicMessages:
				actual = get(array(body["tools"])[0], "input_schema")
			case Gemini:
				actual = get(array(get(array(body["tools"])[0], "functionDeclarations"))[0], "parameters")
			}
			encoded, _ := json.Marshal(actual)
			expected, _ := object(schema)
			actualSchema, _ := object(encoded)
			a, _ := json.Marshal(expected)
			b, _ := json.Marshal(actualSchema)
			if string(a) != string(b) {
				t.Fatalf("schema changed %s", encoded)
			}
			for _, part := range []string{"AQID", "partition", "vendor-mode"} {
				if p == Gemini && part == "vendor-mode" {
					part = "VENDOR-MODE"
				}
				if !strings.Contains(string(raw), part) {
					t.Fatalf("missing %s: %s", part, raw)
				}
			}
		})
	}
}
func TestInvalidSchemasAndOrphanToolResults(t *testing.T) {
	for _, p := range protocols() {
		for _, schema := range []string{`null`, `{"type":"string"}`, `{"type":"object","properties":[]}`, `{"type":"object","required":[123]}`} {
			if _, err := Encode(Request{Protocol: p, Model: "model", MaxOutputTokens: 100, Tools: []ToolDefinition{{"tool", "test", json.RawMessage(schema)}}}); err == nil {
				t.Fatal(p, schema)
			}
		}
		tool := ToolDefinition{"tool", "test", json.RawMessage(`{"type":"object"}`)}
		if _, err := Encode(Request{Protocol: p, Model: "model", MaxOutputTokens: 100, Tools: []ToolDefinition{tool, tool}}); err == nil {
			t.Fatal("duplicate tool")
		}
	}
	m := TextMessage("tool", "output")
	m.CallID = "orphan"
	if _, err := Encode(Request{Protocol: Gemini, Model: "model", MaxOutputTokens: 100, Messages: []Message{m}}); err == nil {
		t.Fatal("orphan accepted")
	}
}
func TestFragmentedChatToolsAndNativeReplay(t *testing.T) {
	d := NewDecoder(OpenAIChat)
	push(t, d, `{"choices":[{"delta":{"reasoning_content":"analysis","tool_calls":[{"index":0,"id":"call","function":{"name":"exec_command","arguments":"{\"cmd\":"}}]}}]}`)
	push(t, d, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"true\"}"}}]},"finish_reason":"tool_calls"}]}`)
	push(t, d, `{"usage":{"prompt_tokens":33,"completion_tokens":10}}`)
	m, err := d.Finish()
	if err != nil || len(m.Calls) != 1 || string(m.Calls[0].Arguments) != `{"cmd":"true"}` {
		t.Fatalf("message=%+v err=%v", m, err)
	}
	if m.Usage == nil || m.Usage.Input != 33 || m.Usage.Output != 10 {
		t.Fatal(m.Usage)
	}
	m.Text = "DISPLAY_ONLY"
	raw, err := ContextPayload(Request{Protocol: OpenAIChat, Model: "model", System: "SYSTEM", Messages: []Message{m}})
	if err != nil || !strings.Contains(string(raw), "analysis") || strings.Contains(string(raw), "DISPLAY_ONLY") || strings.Contains(string(raw), "33") || strings.Contains(string(raw), "max_tokens") {
		t.Fatal(string(raw), err)
	}
}
func TestAnthropicThinkingSignatureAndInitialText(t *testing.T) {
	d := NewDecoder(AnthropicMessages)
	push(t, d, `{"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":10,"cache_creation_input_tokens":3,"output_tokens":1}}}`)
	first := push(t, d, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`)
	if first.TextDelta != "hello" {
		t.Fatal(first)
	}
	push(t, d, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`)
	push(t, d, `{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
	push(t, d, `{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"private reasoning"}}`)
	push(t, d, `{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"signed"}}`)
	push(t, d, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`)
	push(t, d, `{"type":"message_stop"}`)
	m, err := d.Finish()
	if err != nil || m.Text != "hello world" || m.Usage == nil || m.Usage.Input != 33 || m.Usage.Output != 10 || !strings.Contains(string(m.Replay), "signed") {
		t.Fatal(m, err)
	}
}
func TestResponsesFinalTextAndGeminiReplay(t *testing.T) {
	d := NewDecoder(OpenAIResponses)
	push(t, d, `{"type":"response.completed","response":{"output":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"message","content":[{"type":"output_text","text":"final only"}]}],"usage":{"input_tokens":33,"output_tokens":10}}}`)
	m, err := d.Finish()
	if err != nil || m.Text != "final only" || !strings.Contains(string(m.Replay), "opaque") {
		t.Fatal(m, err)
	}
	d = NewDecoder(Gemini)
	push(t, d, `{"candidates":[{"content":{"parts":[{"text":"hidden","thought":true,"thoughtSignature":"signature"},{"functionCall":{"name":"probe","args":{}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":33,"candidatesTokenCount":7,"thoughtsTokenCount":3}}`)
	m, err = d.Finish()
	if err != nil || m.Text != "" || len(m.Calls) != 1 || m.Calls[0].ID != "gemini-1" || m.Usage.Output != 10 {
		t.Fatal(m, err)
	}
	raw, err := Encode(Request{Protocol: Gemini, Model: "model", MaxOutputTokens: 10, Messages: []Message{m}})
	if err != nil || !strings.Contains(string(raw), "thoughtSignature") || !strings.Contains(string(raw), "gemini-1") {
		t.Fatal(string(raw), err)
	}
}
func TestFailuresNeverProduceExecutableCalls(t *testing.T) {
	for _, reason := range []string{"null", `"length"`, `"content_filter"`} {
		d := NewDecoder(OpenAIChat)
		push(t, d, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"probe","arguments":"{}"}}]},"finish_reason":`+reason+`}]}`)
		if _, err := d.Finish(); err == nil {
			t.Fatal(reason)
		}
	}
	for _, event := range []string{`{"type":"response.failed"}`, `{"type":"response.incomplete"}`} {
		d := NewDecoder(OpenAIResponses)
		push(t, d, event)
		if _, err := d.Finish(); err == nil {
			t.Fatal(event)
		}
	}
	d := NewDecoder(OpenAIResponses)
	push(t, d, `{"type":"response.completed","response":{"output":[{"type":"function_call","call_id":"call","name":"probe","arguments":"[]"}]}}`)
	if _, err := d.Finish(); err == nil {
		t.Fatal("array arguments accepted")
	}
	d = NewDecoder(OpenAIChat)
	if _, err := d.Push(json.RawMessage(`{"error":"failed"}`)); err == nil {
		t.Fatal("error ignored")
	}
	if _, err := d.Finish(); err == nil {
		t.Fatal("poisoned decoder finished")
	}
}
func TestUsagePartialCumulativeAndOverflow(t *testing.T) {
	observer := UsageObserver{}
	start, err := observer.Observe(AnthropicMessages, json.RawMessage(`{"type":"message_start","message":{"usage":{"input_tokens":20,"output_tokens":0}}}`))
	if err != nil || start.OutputTokens != nil || *start.InputTokens != 20 {
		t.Fatal(start, err)
	}
	end, err := observer.Observe(AnthropicMessages, json.RawMessage(`{"type":"message_delta","usage":{"output_tokens":5}}`))
	if err != nil || *end.OutputTokens != 5 {
		t.Fatal(end, err)
	}
	for _, test := range []struct {
		p    Protocol
		data string
	}{{AnthropicMessages, `{"type":"message_start","message":{"usage":{"input_tokens":18446744073709551615,"cache_read_input_tokens":1}}}`}, {Gemini, `{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":18446744073709551615,"thoughtsTokenCount":1}}`}} {
		if _, err := observer.Observe(test.p, json.RawMessage(test.data)); err == nil {
			t.Fatal("usage overflow")
		}
	}
}
func TestClampNativeFieldsAndPathBinding(t *testing.T) {
	for _, p := range protocols() {
		data := json.RawMessage(`{"max_tokens":100,"max_output_tokens":100,"generationConfig":{"maxOutputTokens":100,"temperature":0.2},"native":"keep"}`)
		raw, n, err := p.Clamp(data, 10)
		if err != nil || n != 10 || !strings.Contains(string(raw), `"native":"keep"`) || !strings.Contains(string(raw), `"temperature":0.2`) {
			t.Fatal(string(raw), n, err)
		}
	}
	if Gemini.PathMatches("v1/models/other:streamGenerateContent", "model") || OpenAIResponses.PathMatches("https://elsewhere/responses", "model") {
		t.Fatal("path escape")
	}
	raw, n, err := OpenAIChat.Clamp(json.RawMessage(`{"max_tokens":40,"max_completion_tokens":100}`), 80)
	if err != nil || n != 40 || strings.Contains(string(raw), `"max_tokens"`) {
		t.Fatal(string(raw), n, err)
	}
}
func TestSSEUnicodeMultilineAndTruncation(t *testing.T) {
	parser := SSE{}
	events := []json.RawMessage{}
	for _, b := range []byte("data: {\r\ndata: \"text\":\"中文\"}\r\n\r\ndata: [DONE]\n\n") {
		value, err := parser.Push([]byte{b})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, value...)
	}
	if err := parser.Finish(); err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	_, _ = parser.Push([]byte("data: {}\n"))
	if parser.Finish() == nil {
		t.Fatal("partial frame accepted")
	}
}
