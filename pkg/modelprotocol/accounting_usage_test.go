package modelprotocol

import (
	"encoding/json"
	"testing"
)

func TestAccountingNormalizedUsage(t *testing.T) {
	for _, v := range []struct {
		p               Protocol
		body            string
		in, out, reason uint64
	}{
		{OpenAIChat, `{"usage":{"prompt_tokens":100,"completion_tokens":30,"completion_tokens_details":{"reasoning_tokens":20}}}`, 100, 30, 20},
		{OpenAIResponses, `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":30,"output_tokens_details":{"reasoning_tokens":20}}}}`, 100, 30, 20},
		{Gemini, `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":20}}`, 100, 30, 20},
	} {
		var o UsageObserver
		u, e := o.Observe(v.p, json.RawMessage(v.body))
		if e != nil || u == nil || u.InputTokens == nil || *u.InputTokens != v.in || u.OutputTokens == nil || *u.OutputTokens != v.out || u.ReasoningTokens == nil || *u.ReasoningTokens != v.reason {
			t.Fatal(u, e)
		}
	}
	var o UsageObserver
	u, e := o.Observe(AnthropicMessages, json.RawMessage(`{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":300,"cache_creation_input_tokens":200}}}`))
	if e != nil || u.InputTokens == nil || *u.InputTokens != 600 || u.CacheWriteTokens == nil || *u.CacheWriteTokens != 200 {
		t.Fatal(u, e)
	}
}
