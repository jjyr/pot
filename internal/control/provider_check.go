package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type CheckProvider struct {
	Endpoint  string `json:"endpoint"`
	Protocol  string `json:"protocol"`
	APIKey    string `json:"api_key"`
	ProfileID string `json:"profile_id"`
}
type ProviderCheck struct {
	Supported       bool                `json:"supported"`
	Models          []string            `json:"models"`
	InputModalities map[string][]string `json:"input_modalities"`
}

func (p *ProfileStore) Check(ctx context.Context, r CheckProvider) (ProviderCheck, error) {
	result := ProviderCheck{Models: []string{}, InputModalities: map[string][]string{}}
	resolved, err := resolveProviderEndpoint(r.Endpoint, r.Protocol)
	if err != nil {
		return result, err
	}
	r.Endpoint = resolved.Endpoint
	u, _ := url.Parse(r.Endpoint)
	if u.RawQuery != "" {
		return result, errors.New("model-list checks do not support query parameters")
	}
	key, err := p.checkCredential(ctx, r)
	if err != nil {
		return result, err
	}
	defer clear(key)
	if resolved.ModelsEndpoint == "" {
		return result, nil
	}
	u, _ = url.Parse(resolved.ModelsEndpoint)
	request, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return result, err
	}
	switch r.Protocol {
	case "anthropic_messages":
		request.Header.Set("x-api-key", string(key))
		request.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		request.Header.Set("x-goog-api-key", string(key))
	default:
		request.Header.Set("Authorization", "Bearer "+string(key))
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return result, gateway.ErrConnection
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, gateway.StatusError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 262145))
	if err != nil || len(data) > 262144 {
		return result, errors.New("invalid or oversized model-list response")
	}
	var value struct {
		Data []struct {
			ID         string          `json:"id"`
			Modalities json.RawMessage `json:"modalities"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err = json.Unmarshal(data, &value); err != nil {
		return result, errors.New("invalid model-list JSON")
	}
	if r.Protocol == "gemini" {
		if value.Models == nil {
			return result, errors.New("model list missing")
		}
		for _, m := range value.Models {
			result.Models = append(result.Models, strings.TrimPrefix(m.Name, "models/"))
		}
	} else {
		if value.Data == nil {
			return result, errors.New("model list missing")
		}
		for _, m := range value.Data {
			result.Models = append(result.Models, m.ID)
			// Optional gateway extension, not part of the OpenAI model schema.
			// A missing or incompatible extension must not reject a valid model list.
			var modalities struct {
				Input []string `json:"input"`
			}
			if json.Unmarshal(m.Modalities, &modalities) == nil && slices.Contains(modalities.Input, "text") {
				inputs := []string{"text"}
				for _, kind := range []string{"image", "audio", "pdf"} {
					if slices.Contains(modalities.Input, kind) {
						inputs = append(inputs, kind)
					}
				}
				result.InputModalities[m.ID] = inputs
			}
		}
	}
	for _, id := range result.Models {
		if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") {
			return ProviderCheck{}, errors.New("invalid model identity")
		}
	}
	slices.Sort(result.Models)
	result.Models = slices.Compact(result.Models)
	result.Supported = true
	return result, nil
}

func (p *ProfileStore) checkCredential(ctx context.Context, r CheckProvider) ([]byte, error) {
	key := []byte(r.APIKey)
	if len(key) == 0 && r.ProfileID != "" {
		spec, err := p.Resolve(r.ProfileID)
		if err != nil {
			return nil, err
		}
		provider, err := p.registry.Get(ctx, spec.Provider)
		if err != nil {
			return nil, err
		}
		if provider.Endpoint != r.Endpoint || provider.Protocol != r.Protocol {
			return nil, errors.New("enter a credential when checking a changed endpoint")
		}
		key, err = p.registry.Credential(ctx, provider)
		if err != nil {
			return nil, err
		}
	}
	if len(key) == 0 || strings.ContainsAny(string(key), "\r\n") {
		return nil, errors.New("provider credential required")
	}
	return key, nil
}

func resolveProviderEndpoint(address, protocol string) (modelprotocol.EndpointResolution, error) {
	resolved, err := modelprotocol.ResolveEndpoint(address, modelprotocol.Protocol(protocol))
	if err == nil && resolved.DetectedProtocol != "" && string(resolved.DetectedProtocol) != protocol {
		err = errors.New("endpoint does not match the selected request format")
	}
	return resolved, err
}
