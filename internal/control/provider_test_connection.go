package control

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

type TestProvider struct {
	ProviderTypeID  string  `json:"provider_type_id"`
	Endpoint        string  `json:"endpoint"`
	Protocol        string  `json:"protocol"`
	APIKey          string  `json:"api_key"`
	ProfileID       string  `json:"profile_id"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoning_effort"`
}

// Test makes one bounded inference request without saving a profile or writing
// conversation history. A successful HTTP status alone is not a successful test.
func (p *ProfileStore) Test(ctx context.Context, r TestProvider) (map[string]bool, error) {
	resolved, err := resolveProviderEndpoint(r.Endpoint, r.Protocol)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Model) == "" || len(r.Model) > 256 || strings.ContainsAny(r.Model, "\r\n\x00") {
		return nil, errors.New("model ID required")
	}
	r.Endpoint = resolved.Endpoint
	key, err := p.checkCredential(ctx, CheckProvider{ProviderTypeID: r.ProviderTypeID, Endpoint: r.Endpoint, Protocol: r.Protocol, APIKey: r.APIKey, ProfileID: r.ProfileID})
	if err != nil {
		return nil, err
	}
	defer clear(key)
	protocol := modelprotocol.Protocol(r.Protocol)
	body, err := modelprotocol.Encode(modelprotocol.Request{Protocol: protocol, Model: r.Model, Messages: []modelprotocol.Message{modelprotocol.TextMessage("user", "Reply with OK.")}, MaxOutputTokens: 1024, ReasoningEffort: r.ReasoningEffort})
	if err != nil {
		return nil, err
	}
	endpoint := r.Endpoint
	if protocol == modelprotocol.Gemini {
		endpoint = modelprotocol.BindEndpointModel(endpoint, r.Model)
	}
	u, _ := url.Parse(endpoint)
	provider := gateway.Provider{ID: "connection-test", SecretRef: "connection-test", Endpoint: endpoint, Protocol: r.Protocol, AllowedHosts: []string{u.Hostname()}}
	response, err := gateway.Forward(ctx, provider, key, body, "", 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, gateway.StatusError(response.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return nil, errors.New("provider did not return a model event stream")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 262145))
	if err != nil {
		return nil, errors.New("model test stream was interrupted")
	}
	if len(data) > 262144 {
		return nil, errors.New("model test response exceeds limit")
	}
	var sse modelprotocol.SSE
	frames, err := sse.Push(data)
	if err != nil {
		return nil, errors.New("invalid model event stream")
	}
	if err = sse.Finish(); err != nil {
		return nil, errors.New("incomplete model event stream")
	}
	decoder := modelprotocol.NewDecoder(protocol)
	for _, frame := range frames {
		if _, err = decoder.Push(frame); err != nil {
			return nil, errors.New("model test stream reported an invalid response")
		}
	}
	message, err := decoder.Finish()
	if err != nil || strings.TrimSpace(message.Text) == "" {
		return nil, errors.New("model test ended without a complete text reply")
	}
	return map[string]bool{"ok": true}, nil
}
