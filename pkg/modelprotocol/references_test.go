package modelprotocol

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"strings"
	"testing"
)

func TestMessageReferenceEncodingAcrossProtocols(t *testing.T) {
	for _, protocol := range []Protocol{OpenAIChat, OpenAIResponses, AnthropicMessages, Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			source := TextMessage("assistant", "完整的原消息不应重复复制")
			source.ID = "INTERNAL-SOURCE-ID"
			input := TextMessage("user", "为什么？")
			input.ID = "INTERNAL-INPUT-ID"
			input.References = []core.MessageReference{{MessageID: source.ID, Quote: "选中🙂 <tag>"}}
			input.ReferenceContents = []ReferenceContent{{Role: "assistant", Quote: "选中🙂 <tag>", Context: "选中🙂 <tag> 附近的上下文", Truncated: true}}
			for _, replay := range []bool{false, true} {
				if replay {
					switch protocol {
					case OpenAIChat:
						source.Replay = json.RawMessage(`{"role":"assistant","content":"完整的原消息不应重复复制","reasoning_content":"retained"}`)
					case OpenAIResponses:
						source.Replay = json.RawMessage(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"完整的原消息不应重复复制"}]}]`)
					case AnthropicMessages:
						source.Replay = json.RawMessage(`[{"type":"thinking","thinking":"retained","signature":"signature"},{"type":"text","text":"完整的原消息不应重复复制"}]`)
					case Gemini:
						source.Replay = json.RawMessage(`[{"text":"完整的原消息不应重复复制","thoughtSignature":"signature"}]`)
					}
				}
				before := string(source.Replay)
				raw, err := Encode(Request{Protocol: protocol, Model: "model", Messages: []Message{source, input}, MaxOutputTokens: 100})
				if err != nil {
					t.Fatal(err)
				}
				var decoded any
				if err = json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				// Decode strings rather than relying on JSON's HTML escaping.
				texts := []string{}
				var walk func(any)
				walk = func(v any) {
					switch x := v.(type) {
					case string:
						texts = append(texts, x)
					case map[string]any:
						for _, x := range x {
							walk(x)
						}
					case []any:
						for _, x := range x {
							walk(x)
						}
					}
				}
				walk(decoded)
				joined := strings.Join(texts, "\n")
				if strings.Count(joined, "完整的原消息不应重复复制") != 1 || !strings.Contains(joined, `<quoted_messages>`) || !strings.Contains(joined, `"quote":"选中🙂 \u003ctag\u003e"`) || !strings.Contains(joined, `"context":`) || !strings.Contains(joined, `"truncated":true`) {
					t.Fatal("quoted content or context lost", joined)
				}
				if strings.Contains(joined, source.ID) || strings.Contains(joined, input.ID) || strings.Contains(joined, "pot_message_metadata") || strings.Contains(joined, "read_chat_messages") {
					t.Fatal("internal message identity leaked", joined)
				}
				if source.Text != "完整的原消息不应重复复制" || input.Text != "为什么？" || string(source.Replay) != before {
					t.Fatal("encoding mutated canonical history")
				}
				if replay && protocol == AnthropicMessages && !strings.Contains(joined, "signature") {
					t.Fatal("signed reasoning lost")
				}
			}
		})
	}
}

func TestUnresolvedMessageReferencesAreRejected(t *testing.T) {
	for _, protocol := range protocols() {
		input := TextMessage("user", "Why?")
		input.References = []core.MessageReference{{MessageID: "source"}}
		if _, err := Encode(Request{Protocol: protocol, Model: "model", Messages: []Message{input}, MaxOutputTokens: 100}); err == nil {
			t.Fatal(protocol, "unresolved references silently omitted")
		}
	}
}
