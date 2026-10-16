package modelprotocol_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	m "github.com/jjyr/pot/pkg/modelprotocol"
)

// Exercise the framing/codec boundary together: packet boundaries may split
// UTF-8, JSON escapes, argument fragments, CRLF or the terminal event itself.
func TestProviderStreamPacketizationConsistency(t *testing.T) {
	for _, fixture := range streamFixtures() {
		t.Run(string(fixture.protocol), func(t *testing.T) {
			wire := fixture.wire()
			baseline, err := decodeStream(fixture.protocol, [][]byte{wire})
			if err != nil {
				t.Fatal(err)
			}
			if baseline.Text != "中文🙂" || len(baseline.Calls) != 1 ||
				baseline.Calls[0].Name != "probe" || string(baseline.Calls[0].Arguments) != `{"text":"中文🙂"}` ||
				baseline.Usage == nil || baseline.Usage.Input != 33 || baseline.Usage.Output != 10 {
				t.Fatalf("unexpected response: %+v", baseline)
			}
			for split := 0; split <= len(wire); split++ {
				got, err := decodeStream(fixture.protocol, [][]byte{wire[:split], wire[split:]})
				if err != nil || !reflect.DeepEqual(got, baseline) {
					t.Fatalf("packet split %d: response=%+v error=%v", split, got, err)
				}
			}
			chunks := make([][]byte, len(wire))
			for i := range wire {
				chunks[i] = wire[i : i+1]
			}
			if got, err := decodeStream(fixture.protocol, chunks); err != nil || !reflect.DeepEqual(got, baseline) {
				t.Fatalf("one-byte packets: response=%+v error=%v", got, err)
			}
			// A complete assistant/tool exchange must remain encodable using the
			// native replay payload, including provider reasoning/signatures.
			result := m.TextMessage("tool", "observed")
			result.CallID = baseline.Calls[0].ID
			body, err := m.Encode(m.Request{Protocol: fixture.protocol, Model: "model", MaxOutputTokens: 64, Messages: []m.Message{baseline, result}})
			if err != nil || !strings.Contains(string(body), "observed") || !strings.Contains(string(body), "probe") {
				t.Fatalf("native replay: %s %v", body, err)
			}
		})
	}
}

func TestProviderTruncationCannotReleaseToolCalls(t *testing.T) {
	for _, fixture := range streamFixtures() {
		t.Run(string(fixture.protocol), func(t *testing.T) {
			wire := fixture.wire()
			// Every incomplete prefix ends before the terminal frame delimiter.
			// Even complete tool JSON is insufficient to authorize execution.
			for end := 0; end < len(wire); end++ {
				got, err := decodeStream(fixture.protocol, [][]byte{wire[:end]})
				if err == nil || len(got.Calls) != 0 {
					t.Fatalf("truncated at %d released response %+v (error=%v)", end, got, err)
				}
			}
		})
	}
}

type streamFixture struct {
	protocol m.Protocol
	events   []string
}

func (f streamFixture) wire() []byte {
	var wire strings.Builder
	wire.WriteString(": heartbeat\r\n\r\n")
	for _, event := range f.events {
		fmt.Fprintf(&wire, "data: %s\r\n\r\n", event)
	}
	return []byte(wire.String())
}

func decodeStream(protocol m.Protocol, chunks [][]byte) (m.Message, error) {
	var framing m.SSE
	decoder := m.NewDecoder(protocol)
	for _, chunk := range chunks {
		events, err := framing.Push(chunk)
		if err != nil {
			return m.Message{}, err
		}
		for _, event := range events {
			if _, err := decoder.Push(json.RawMessage(event)); err != nil {
				return m.Message{}, err
			}
		}
	}
	if err := framing.Finish(); err != nil {
		return m.Message{}, err
	}
	return decoder.Finish()
}

func streamFixtures() []streamFixture {
	return []streamFixture{
		{m.OpenAIChat, []string{
			`{"choices":[{"delta":{"content":"中文🙂","reasoning_content":"private","tool_calls":[{"index":0,"id":"call","function":{"name":"probe","arguments":"{\"text\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"中文🙂\"}"}}]}}],"usage":{"prompt_tokens":33,"completion_tokens":10}}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}},
		{m.OpenAIResponses, []string{
			`{"type":"response.output_text.delta","delta":"中文🙂"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call","name":"probe","arguments":"{\"text\":\"中文🙂\"}"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"reasoning","encrypted_content":"opaque"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"中文🙂"}]},{"type":"function_call","call_id":"call","name":"probe","arguments":"{\"text\":\"中文🙂\"}"}],"usage":{"input_tokens":33,"output_tokens":10}}}`,
		}},
		{m.AnthropicMessages, []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":10,"cache_creation_input_tokens":3,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"中文🙂"}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call","name":"probe","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"text\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"中文🙂\"}"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}`,
			`{"type":"message_stop"}`,
		}},
		{m.Gemini, []string{
			`{"candidates":[{"content":{"parts":[{"text":"中文🙂"},{"text":"private","thought":true,"thoughtSignature":"signed"}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call","name":"probe","args":{"text":"中文🙂"}}}]}}]}`,
			`{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":33,"candidatesTokenCount":7,"thoughtsTokenCount":3}}`,
		}},
	}
}
