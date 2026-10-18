package modelprotocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func metadataRequest() Request {
	source := TextMessage("assistant", "Accepted.")
	source.ID = "source"
	return Request{Protocol: OpenAIResponses, Model: "model", MaxOutputTokens: 64, Messages: []Message{source, TextMessage("user", "Confirm the queued task.")}}
}
func responseItem(id, phase, text string) map[string]any {
	return map[string]any{"id": id, "type": "message", "role": "assistant", "phase": phase, "content": []any{map[string]any{"type": "output_text", "text": text}}}
}
func responseEvent(kind string, fields map[string]any) string {
	fields["type"] = kind
	b, _ := json.Marshal(fields)
	return string(b)
}
func responseWire(items []map[string]any, mode string) []byte {
	var events []string
	if mode != "snapshot" {
		for i, item := range items {
			added := map[string]any{}
			for k, v := range item {
				added[k] = v
			}
			added["content"] = []any{}
			events = append(events, responseEvent("response.output_item.added", map[string]any{"output_index": i, "item": added}))
			text := (&responseText{item: item, done: true}).text()
			// Split the annotation itself across SSE events as well as network chunks.
			for _, r := range text {
				events = append(events, responseEvent("response.output_text.delta", map[string]any{"output_index": i, "content_index": 0, "item_id": item["id"], "delta": string(r)}))
			}
			done := responseEvent("response.output_item.done", map[string]any{"output_index": i, "item": item})
			events = append(events, done)
			if mode == "duplicates" {
				events = append(events, done)
			}
		}
	}
	terminal := responseEvent("response.completed", map[string]any{"response": map[string]any{"output": items}})
	events = append(events, terminal)
	if mode == "duplicates" {
		events = append(events, terminal)
	}
	return []byte("data: " + strings.Join(events, "\n\ndata: ") + "\n\n")
}
func decodeResponse(t *testing.T, request Request, chunks ...[]byte) (Message, string) {
	t.Helper()
	d := NewRequestDecoder(request)
	var sse SSE
	var text strings.Builder
	for _, chunk := range chunks {
		events, err := sse.Push(chunk)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			event, err := d.Push(e)
			if err != nil {
				t.Fatal(err)
			}
			text.WriteString(event.TextDelta)
		}
	}
	if err := sse.Finish(); err != nil {
		t.Fatal(err)
	}
	message, err := d.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if message.Text != text.String() {
		t.Fatalf("stream=%q finish=%q", text.String(), message.Text)
	}
	return message, text.String()
}

func TestResponsesMetadataItemProjection(t *testing.T) {
	request := metadataRequest()
	tag := messageMetadata(request.Messages[0])
	items := []map[string]any{responseItem("meta", "commentary", tag), responseItem("progress", "commentary", "Queued. 中文🙂"), responseItem("final", "final_answer", "Separate branch and worktree.")}
	want := "Queued. 中文🙂\n\nSeparate branch and worktree."
	for _, mode := range []string{"stream", "snapshot", "duplicates"} {
		t.Run(mode, func(t *testing.T) {
			wire := responseWire(items, mode)
			message, _ := decodeResponse(t, request, wire)
			if message.Text != want || strings.Contains(string(message.Replay), "pot_message_metadata") {
				t.Fatalf("metadata entered output/checkpoint: %+v", message)
			}
			var replay []map[string]any
			if err := json.Unmarshal(message.Replay, &replay); err != nil {
				t.Fatal(err)
			}
			if len(replay) != 2 || replay[0]["phase"] != "commentary" || replay[1]["phase"] != "final_answer" {
				t.Fatalf("lost item/phase: %s", message.Replay)
			}
			// Every possible boundary is covered using a compact snapshot fixture; the
			// full stream additionally uses byte packets and splits within every tag rune.
			if mode == "snapshot" {
				for split := 0; split <= len(wire); split++ {
					got, _ := decodeResponse(t, request, wire[:split], wire[split:])
					if !reflect.DeepEqual(got, message) {
						t.Fatalf("split %d", split)
					}
				}
			}
			chunks := make([][]byte, len(wire))
			for i := range wire {
				chunks[i] = wire[i : i+1]
			}
			got, _ := decodeResponse(t, request, chunks...)
			if !reflect.DeepEqual(got, message) {
				t.Fatal("byte packets changed output")
			}
		})
	}
}

func TestResponsesCopiedJSONNewlineAnnotation(t *testing.T) {
	request := metadataRequest()
	tag := strings.TrimSpace(messageMetadata(request.Messages[0])) + `\n`
	items := []map[string]any{responseItem("meta", "commentary", tag), responseItem("final", "final_answer", "Queued.")}
	for _, mode := range []string{"stream", "snapshot"} {
		message, _ := decodeResponse(t, request, responseWire(items, mode))
		if message.Text != "Queued." || strings.Contains(string(message.Replay), "pot_message_metadata") {
			t.Fatalf("copied annotation leaked: %+v", message)
		}
	}
}

func TestResponsesMetadataIsNotAContentFilter(t *testing.T) {
	base := metadataRequest()
	tag := strings.TrimSpace(messageMetadata(base.Messages[0]))
	for _, tc := range []struct {
		name, phase, text, user string
		quote                   bool
	}{
		{name: "final annotation", phase: "final_answer", text: tag},
		{name: "unknown phase", text: tag},
		{name: "unknown ID", phase: "commentary", text: strings.ReplaceAll(tag, "source", "other")},
		{name: "prose", phase: "commentary", text: "Example: " + tag},
		{name: "code", phase: "commentary", text: "```xml\n" + tag + "\n```"},
		{name: "tag plus prose", phase: "commentary", text: tag + "\nThe task is queued."},
		{name: "explicit user tag", phase: "commentary", text: tag, user: "Show " + tag},
		{name: "quoted tag", phase: "commentary", text: tag, quote: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := metadataRequest()
			if tc.user != "" {
				request.Messages[1].Text = tc.user
			}
			if tc.quote {
				request.Messages[1].References = []core.MessageReference{{MessageID: "source", Quote: tag}}
			}
			for _, mode := range []string{"stream", "snapshot"} {
				message, _ := decodeResponse(t, request, responseWire([]map[string]any{responseItem("example", tc.phase, tc.text)}, mode))
				if message.Text != tc.text {
					t.Fatalf("lost legitimate content: %q", message.Text)
				}
			}
		})
	}
}

func TestResponsesMetadataKeepsToolsAndNativeReplay(t *testing.T) {
	request := metadataRequest()
	tag := messageMetadata(request.Messages[0])
	items := []map[string]any{
		{"id": "reason", "type": "reasoning", "encrypted_content": "opaque-signature"},
		responseItem("meta", "commentary", tag),
		responseItem("progress", "commentary", "Working."),
		{"id": "fc", "type": "function_call", "call_id": "call", "name": "probe", "arguments": "{}"},
	}
	message, _ := decodeResponse(t, request, responseWire(items, "snapshot"))
	if len(message.Calls) != 1 || message.Calls[0].ID != "call" || !strings.Contains(string(message.Replay), "opaque-signature") {
		t.Fatalf("lost native blocks: %+v", message)
	}
	message.ID = "generated"
	result := TextMessage("tool", tag)
	result.CallID = "call"
	request.Messages = append(request.Messages, message, result)
	body, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	input := array(wire["input"])
	for _, v := range input {
		item := obj(v)
		if item["type"] == "function_call_output" && item["output"] != tag {
			t.Fatal("tool body changed")
		}
	}
	if !strings.Contains(string(body), "opaque-signature") || !strings.Contains(string(body), `"phase":"commentary"`) {
		t.Fatalf("replay lost phase/signature: %s", body)
	}
}

func TestResponsesReferenceAnnotationDoesNotInventAssistantItem(t *testing.T) {
	for _, withText := range []bool{false, true} {
		t.Run(fmt.Sprint(withText), func(t *testing.T) {
			request := metadataRequest()
			source := &request.Messages[0]
			items := []map[string]any{{"type": "reasoning", "encrypted_content": "signature"}, {"type": "function_call", "call_id": "call", "name": "probe", "arguments": "{}"}}
			if withText {
				items = append(items, responseItem("native", "commentary", source.Text))
			}
			source.Replay, _ = json.Marshal(items)
			before := string(source.Replay)
			raw, err := Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := object(raw)
			for _, v := range array(body["input"]) {
				item := obj(v)
				if item["role"] == "assistant" && item["type"] != "message" {
					t.Fatalf("invented assistant item: %s", raw)
				}
			}
			if string(source.Replay) != before {
				t.Fatal("canonical replay mutated")
			}
			if withText && !strings.Contains(string(raw), `"phase":"commentary"`) {
				t.Fatal("native phase lost")
			}
		})
	}
}

func TestResponsesSequenceReplayAndTokenStreaming(t *testing.T) {
	d := NewRequestDecoder(metadataRequest())
	push := func(kind string, sequence int, fields map[string]any) string {
		t.Helper()
		fields["sequence_number"] = sequence
		event, err := d.Push(json.RawMessage(responseEvent(kind, fields)))
		if err != nil {
			t.Fatal(err)
		}
		return event.TextDelta
	}
	item := responseItem("normal", "commentary", "go go")
	push("response.output_item.added", 0, map[string]any{"output_index": 0, "item": responseItem("normal", "commentary", "")})
	if got := push("response.output_text.delta", 1, map[string]any{"output_index": 0, "delta": "go "}); got != "go " {
		t.Fatalf("ordinary token was buffered: %q", got)
	}
	if got := push("response.output_text.delta", 1, map[string]any{"output_index": 0, "delta": "go "}); got != "" {
		t.Fatal("reconnected event duplicated", got)
	}
	if got := push("response.output_text.delta", 2, map[string]any{"output_index": 0, "delta": "go"}); got != "go" {
		t.Fatal("identical legitimate tokens lost", got)
	}
	if got := push("response.output_item.done", 3, map[string]any{"output_index": 0, "item": item}); got != "" {
		t.Fatal("done snapshot duplicated", got)
	}
	push("response.completed", 4, map[string]any{"response": map[string]any{"output": []any{item}}})
	message, err := d.Finish()
	if err != nil || message.Text != "go go" {
		t.Fatal(message, err)
	}
}

func TestResponsesToolOnlyPortableHistoryHasNoAnnotationTurn(t *testing.T) {
	request := metadataRequest()
	request.Messages[0].Text = ""
	request.Messages[0].Calls = []ToolCall{{ID: "call", Name: "probe", Arguments: json.RawMessage(`{}`)}}
	body, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := object(body)
	for _, v := range array(decoded["input"]) {
		if obj(v)["role"] == "assistant" {
			t.Fatalf("invented empty assistant turn: %s", body)
		}
	}
}

func TestResponsesUninjectedToolOnlyIdentityRemainsContent(t *testing.T) {
	request := metadataRequest()
	tag := messageMetadata(request.Messages[0])
	request.Messages[0].Text = ""
	request.Messages[0].Replay = json.RawMessage(`[{"type":"message","role":"assistant","content":[]},{"type":"function_call","call_id":"call","name":"probe","arguments":"{}"}]`)
	raw, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pot_message_metadata") {
		t.Fatal("annotation added to empty native item")
	}
	message, _ := decodeResponse(t, request, responseWire([]map[string]any{responseItem("legitimate", "commentary", tag)}, "stream"))
	if message.Text != tag {
		t.Fatal("uninjected annotation was classified as internal")
	}
}

func TestResponsesGeneratedMetadataIDIsNotAChatMessage(t *testing.T) {
	request := metadataRequest()
	// The reported incident used an ID absent from the retained transcript. Do
	// not require an upstream-generated annotation to identify a real message.
	tag := `<pot_message_metadata>{"message_id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}</pot_message_metadata>\n`
	for _, mode := range []string{"stream", "snapshot"} {
		items := []map[string]any{responseItem("meta", "commentary", tag), responseItem("final", "final_answer", "Queued.")}
		message, _ := decodeResponse(t, request, responseWire(items, mode))
		if message.Text != "Queued." || strings.Contains(string(message.Replay), "pot_message_metadata") {
			t.Fatalf("generated metadata ID leaked: %+v", message)
		}
		// Intentional display is content even for the same well-formed ID.
		request.Messages[1].Text = "Discuss " + tag
		message, _ = decodeResponse(t, request, responseWire(items, mode))
		if message.Text != tag+"\n\nQueued." {
			t.Fatal("explicit tag discussion was removed")
		}
		request = metadataRequest()
	}
}
