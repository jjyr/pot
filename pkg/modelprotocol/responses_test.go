package modelprotocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func responseItem(id, phase, text string) map[string]any {
	return map[string]any{"id": id, "type": "message", "role": "assistant", "phase": phase, "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func TestResponsesReplayPreservesNativeItemsWithoutAnnotations(t *testing.T) {
	for _, scenario := range []string{"commentary", "final_answer", "empty-native", "tools-only", "portable-text", "portable-tools", "empty-assistant", "null-replay", "invalid-replay", "native-only-text"} {
		t.Run(scenario, func(t *testing.T) {
			source := TextMessage("assistant", "Original 中文🙂")
			source.ID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
			items := []any{
				map[string]any{"id": "reason", "type": "reasoning", "encrypted_content": "opaque-signature", "summary": []any{}},
				map[string]any{"id": "fc", "type": "function_call", "call_id": "call", "name": "probe", "arguments": "{ \"x\": 1 }"},
			}
			switch scenario {
			case "commentary", "final_answer", "native-only-text":
				phase := scenario
				if scenario == "native-only-text" {
					phase = "commentary"
				}
				items = append(items, responseItem("empty", "commentary", ""), responseItem("native", phase, source.Text), responseItem("last", "final_answer", "More text."))
			case "empty-native":
				items = append(items, responseItem("empty", "commentary", ""))
			case "portable-tools", "empty-assistant", "null-replay", "invalid-replay":
				source.Text = ""
			}
			if scenario == "null-replay" {
				source.Replay = json.RawMessage(`null`)
			}
			if scenario == "invalid-replay" {
				source.Replay = json.RawMessage(`{}`)
			}
			if scenario == "portable-tools" {
				source.Calls = []ToolCall{{ID: "call", Name: "probe", Arguments: json.RawMessage(`{}`)}}
			}
			if scenario == "commentary" || scenario == "final_answer" || scenario == "empty-native" || scenario == "tools-only" || scenario == "native-only-text" {
				raw, _ := json.MarshalIndent(items, "", "  ")
				source.Replay = raw
			}
			if scenario == "native-only-text" {
				source.Text = ""
			}
			before, _ := json.Marshal(source)
			replayBefore := string(source.Replay)
			request := Request{Protocol: OpenAIResponses, Model: "model", MaxOutputTokens: 64, Messages: []Message{source}}
			var previous string
			for attempt := 0; attempt < 2; attempt++ {
				raw, err := Encode(request)
				if err != nil {
					t.Fatal(err)
				}
				if attempt > 0 && previous != string(raw) {
					t.Fatal("repeated encoding changed annotations")
				}
				previous = string(raw)
				body, _ := object(raw)
				input := array(body["input"])
				if len(source.Replay) > 0 && scenario != "null-replay" && scenario != "invalid-replay" {
					if len(input) != len(items) {
						t.Fatalf("invented replay item: %s", raw)
					}
					if !reflect.DeepEqual(input, items) {
						t.Fatalf("native fields changed: %#v", input)
					}
				} else if scenario == "portable-text" {
					if len(input) != 1 || obj(input[0])["content"] != source.Text {
						t.Fatalf("portable text changed: %s", raw)
					}
				}
				if strings.Contains(string(raw), "pot_message_metadata") || strings.Contains(string(raw), source.ID) {
					t.Fatalf("internal message metadata added to provider input: %s", raw)
				}
				after, _ := json.Marshal(request.Messages[0])
				if string(before) != string(after) || string(request.Messages[0].Replay) != replayBefore {
					t.Fatal("canonical text/replay bytes mutated")
				}
			}
		})
	}
}

// Split text across SSE events and the wire across individual bytes. Snapshot
// cases also exercise gateways that omit some or all output_text.delta events.
func responseWire(items []any, mode string) []byte {
	var wire strings.Builder
	write := func(event map[string]any) {
		raw, _ := json.Marshal(event)
		wire.WriteString("data: " + string(raw) + "\n\n")
	}
	if mode != "snapshot" {
		for i, v := range items {
			item := obj(v)
			if item["type"] == "message" {
				text := str(obj(array(item["content"])[0])["text"])
				if mode == "partial" {
					text = string([]rune(text)[:len([]rune(text))/2])
				}
				// A partial stream stops after the first message; completion fills
				// the remaining suffix according to the existing concatenation.
				for _, r := range text {
					write(map[string]any{"type": "response.output_text.delta", "output_index": i, "content_index": 0, "delta": string(r)})
				}
				if mode == "partial" {
					break
				}
			}
			write(map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
		}
	}
	terminal := map[string]any{"type": "response.completed", "response": map[string]any{"output": items}}
	write(terminal)
	write(terminal)
	return []byte(wire.String())
}

func TestResponsesReturnedMetadataIsPreserved(t *testing.T) {
	known := "<pot_message_metadata>{\"message_id\":\"source\"}</pot_message_metadata>\n"
	unknown := `<pot_message_metadata>{"message_id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}</pot_message_metadata>\n`
	for _, tc := range []struct{ name, phase, text string }{
		{"known", "commentary", known},
		{"unknown", "commentary", unknown},
		{"prose", "commentary", "Example: " + known + " still text. 中文🙂"},
		{"code", "commentary", "```xml\n" + unknown + "\n```"},
		{"final-tag", "final_answer", known},
		{"no-phase", "", unknown},
	} {
		for _, mode := range []string{"stream", "snapshot", "partial"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				items := []any{
					map[string]any{"id": "reason", "type": "reasoning", "encrypted_content": "opaque-signature"},
					responseItem("text", tc.phase, tc.text),
					responseItem("final", "final_answer", "Queued."),
					map[string]any{"id": "fc", "type": "function_call", "call_id": "call", "name": "probe", "arguments": "{}"},
				}
				d := NewDecoder(OpenAIResponses)
				var sse SSE
				var deltas strings.Builder
				for _, b := range responseWire(items, mode) {
					frames, err := sse.Push([]byte{b})
					if err != nil {
						t.Fatal(err)
					}
					for _, frame := range frames {
						event, err := d.Push(frame)
						if err != nil {
							t.Fatal(err)
						}
						deltas.WriteString(event.TextDelta)
					}
				}
				if err := sse.Finish(); err != nil {
					t.Fatal(err)
				}
				message, err := d.Finish()
				want := tc.text + "Queued."
				if err != nil || message.Text != want || deltas.String() != want {
					t.Fatalf("stream=%q text=%q want=%q err=%v", deltas.String(), message.Text, want, err)
				}
				var replay []any
				if err := json.Unmarshal(message.Replay, &replay); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(replay, items) || len(message.Calls) != 1 || message.Calls[0].ID != "call" {
					t.Fatalf("native content/tools lost: %+v", message)
				}
			})
		}
	}
}

func TestResponsesTagPrefixStreamsImmediately(t *testing.T) {
	d := NewDecoder(OpenAIResponses)
	event, err := d.Push(json.RawMessage(`{"type":"response.output_text.delta","delta":"<pot_"}`))
	if err != nil || event.TextDelta != "<pot_" {
		t.Fatalf("tag prefix buffered: %+v %v", event, err)
	}
}
