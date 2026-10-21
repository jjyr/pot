package modelprotocol

import "testing"

func TestAnthropicCacheWriteSurvivesFinalUsage(t *testing.T) {
	var observer UsageObserver
	for _, raw := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":1200}}}`,
		`{"type":"message_delta","usage":{"output_tokens":50}}`,
	} {
		usage, err := observer.Observe(AnthropicMessages, []byte(raw))
		if err != nil || usage == nil || usage.CacheWriteTokens == nil || *usage.CacheWriteTokens != 1200 || usage.InputTokens == nil || *usage.InputTokens != 1220 {
			t.Fatal(usage, err)
		}
	}
}

func TestUnreportedCacheUsageStaysUnknown(t *testing.T) {
	for _, protocol := range []Protocol{OpenAIChat, OpenAIResponses, AnthropicMessages, Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			var observer UsageObserver
			raw := map[Protocol]string{
				OpenAIChat:        `{"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
				OpenAIResponses:   `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":10}}}`,
				AnthropicMessages: `{"type":"message_start","message":{"usage":{"input_tokens":100}}}`,
				Gemini:            `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10}}`,
			}[protocol]
			report, err := observer.Observe(protocol, []byte(raw))
			if err != nil || report == nil || report.CacheReadTokens != nil || report.CacheWriteTokens != nil {
				t.Fatal(report, err)
			}
		})
	}
}
