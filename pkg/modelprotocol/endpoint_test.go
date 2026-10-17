package modelprotocol

import "testing"

func TestEndpointResolution(t *testing.T) {
	for _, tt := range []struct {
		input            string
		protocol         Protocol
		endpoint, models string
		detected         Protocol
	}{
		{"https://example.com/a%2Fb/v1", OpenAIResponses, "https://example.com/a%2Fb/v1/responses", "https://example.com/a%2Fb/v1/models", OpenAIResponses},
		{"https://example.com", OpenAIChat, "https://example.com/v1/chat/completions", "https://example.com/v1/models", OpenAIChat},
		{" http://[::1]:9000/proxy/v1/ ", OpenAIResponses, "http://[::1]:9000/proxy/v1/responses", "http://[::1]:9000/proxy/v1/models", OpenAIResponses},
		{"https://example.com/v1/messages", AnthropicMessages, "https://example.com/v1/messages", "https://example.com/v1/models", AnthropicMessages},
		{"https://example.com/v1beta", Gemini, "https://example.com/v1beta/models/{model}:streamGenerateContent", "https://example.com/v1beta/models", Gemini},
		{"https://example.com", Gemini, "https://example.com/v1beta/models/{model}:streamGenerateContent", "https://example.com/v1beta/models", Gemini},
		{"https://example.com/v1/chat/completions/", OpenAIResponses, "https://example.com/v1/chat/completions", "https://example.com/v1/models", OpenAIChat},
		{"https://example.com/custom/invoke/?route=a", OpenAIResponses, "https://example.com/custom/invoke/?route=a", "", ""},
		{"https://example.com/v1/responses?route=a", OpenAIResponses, "https://example.com/v1/responses?route=a", "", OpenAIResponses},
	} {
		t.Run(tt.input+string(tt.protocol), func(t *testing.T) {
			got, err := ResolveEndpoint(tt.input, tt.protocol)
			if err != nil || got.Endpoint != tt.endpoint || got.ModelsEndpoint != tt.models || got.DetectedProtocol != tt.detected {
				t.Fatalf("%+v %v", got, err)
			}
			again, err := ResolveEndpoint(got.Endpoint, tt.protocol)
			if err != nil || again != got {
				t.Fatalf("resolution is not idempotent: %+v %v", again, err)
			}
		})
	}
	for _, address := range []string{"ftp://example.com/v1", "https://user:secret@example.com/v1", "https://example.com/v1#fragment", "https://example.com/v1?api_key=secret", "https://example.com/v1?token=secret", "/v1", "https://example.com:bad"} {
		if _, err := ResolveEndpoint(address, OpenAIChat); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	if _, err := ResolveEndpoint("https://example.com", Unknown); err == nil {
		t.Fatal("accepted unknown protocol")
	}
	endpoint := "https://example.com/v1beta/models/{model}:streamGenerateContent"
	if got := BindEndpointModel(endpoint, "model/a ?"); got != "https://example.com/v1beta/models/model%2Fa%20%3F:streamGenerateContent" {
		t.Fatal(got)
	}
}
