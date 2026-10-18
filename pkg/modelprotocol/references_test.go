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
			source.ID = "source"
			input := TextMessage("user", "为什么？")
			input.ID = "input"
			input.References = []core.MessageReference{{MessageID: "source", Quote: "选中🙂 <tag>"}}
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
				if strings.Count(joined, "完整的原消息不应重复复制") != 1 || !strings.Contains(joined, `"message_id":"source"`) || !strings.Contains(joined, `"references":[`) {
					t.Fatal("identity or reference lost", joined)
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
