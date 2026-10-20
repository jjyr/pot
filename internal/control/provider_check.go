package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type CheckProvider struct {
	ProviderTypeID string `json:"provider_type_id"`
	Endpoint       string `json:"endpoint"`
	Protocol       string `json:"protocol"`
	APIKey         string `json:"api_key"`
	ProfileID      string `json:"profile_id"`
}
type ProviderCheck struct {
	Catalog         *core.ProviderModelCatalog `json:"catalog,omitempty"`
	Supported       bool                       `json:"supported"`
	Models          []string                   `json:"models"`
	InputModalities map[string][]string        `json:"input_modalities"`
}

func (p *ProfileStore) Check(ctx context.Context, r CheckProvider) (ProviderCheck, error) {
	result := ProviderCheck{Models: []string{}, InputModalities: map[string][]string{}}
	resolved, err := resolveProviderEndpoint(r.Endpoint, r.Protocol)
	if err != nil {
		return result, err
	}
	r.Endpoint = resolved.Endpoint
	var startingProfile *core.Profile
	if r.ProfileID != "" {
		profile, _, lookupErr := p.savedProvider(ctx, r.ProfileID)
		if lookupErr != nil {
			return result, lookupErr
		}
		startingProfile = &profile
	}
	u, _ := url.Parse(r.Endpoint)
	if u.RawQuery != "" {
		return result, errors.New("model-list checks do not support query parameters")
	}
	typ, err := p.requestProviderType(ctx, r.ProviderTypeID, r.ProfileID, r.Protocol)
	if err != nil {
		return result, err
	}
	if typ.Capabilities.Catalog == "models_dev" {
		return p.directoryCatalog(typ)
	}
	key, err := p.checkCredential(ctx, r)
	if err != nil {
		return result, err
	}
	defer clear(key)
	if typ.Capabilities.Catalog == "none" {
		return result, nil
	}
	if typ.Capabilities.Catalog == "plugin" {
		response, err := p.pluginRead(ctx, typ, core.ModelProviderConnection{ID: requestConnectionID(r.ProfileID), Type: typ.definition.ID, Revision: 1, Endpoint: r.Endpoint, Protocol: r.Protocol}, "list_models", key)
		if err != nil {
			return result, err
		}
		result.Catalog = response.Catalog
		result.Supported = true
		for _, model := range response.Catalog.Models {
			result.Models = append(result.Models, model.ID)
			if len(model.InputModalities) > 0 {
				result.InputModalities[model.ID] = conversationModalities(model.InputModalities)
			}
		}
		slices.Sort(result.Models)
		if err = p.checkedCatalogFence(ctx, r, typ, startingProfile); err != nil {
			return ProviderCheck{}, err
		}
		if r.ProfileID != "" {
			if err = p.cacheCheckedCatalog(ctx, r, typ, response, key, startingProfile); err != nil {
				return ProviderCheck{}, err
			}
		}
		p.rememberCatalog(typ, r.Endpoint, r.Protocol, key, response)
		return result, nil
	}
	result, err = p.checkHTTP(ctx, r, key)
	if err == nil {
		models := []core.ProviderModel{}
		for _, id := range result.Models {
			models = append(models, core.ProviderModel{ID: id, InputModalities: result.InputModalities[id]})
		}
		catalog := result.Catalog
		if catalog == nil {
			catalog = &core.ProviderModelCatalog{Models: models, ReadAt: time.Now().UTC().Format(time.RFC3339Nano)}
		}
		response := core.ModelProviderResponse{Catalog: catalog}
		result.Catalog = response.Catalog
		if err = p.checkedCatalogFence(ctx, r, typ, startingProfile); err != nil {
			return ProviderCheck{}, err
		}
		if r.ProfileID != "" {
			if err = p.cacheCheckedCatalog(ctx, r, typ, response, key, startingProfile); err != nil {
				return ProviderCheck{}, err
			}
		}
		p.rememberCatalog(typ, r.Endpoint, r.Protocol, key, response)
	}
	return result, err
}
func requestConnectionID(id string) string {
	if id == "" {
		return "connection-draft"
	}
	return id
}
func (p *ProfileStore) checkHTTP(ctx context.Context, r CheckProvider, key []byte) (ProviderCheck, error) {
	result := ProviderCheck{Models: []string{}, InputModalities: map[string][]string{}}
	resolved, err := resolveProviderEndpoint(r.Endpoint, r.Protocol)
	if err != nil {
		return result, err
	}
	if resolved.ModelsEndpoint == "" {
		return result, nil
	}
	u, _ := url.Parse(resolved.ModelsEndpoint)
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
			ID            string          `json:"id"`
			DisplayName   string          `json:"display_name"`
			ContextWindow int64           `json:"context_window"`
			MaxOutput     int64           `json:"max_output_tokens"`
			Modalities    json.RawMessage `json:"modalities"`
		} `json:"data"`
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			MaxInput    int64  `json:"inputTokenLimit"`
			MaxOutput   int64  `json:"outputTokenLimit"`
		} `json:"models"`
	}
	if err = json.Unmarshal(data, &value); err != nil {
		return result, errors.New("invalid model-list JSON")
	}
	result.Catalog = &core.ProviderModelCatalog{Models: []core.ProviderModel{}, ReadAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if r.Protocol == "gemini" {
		if value.Models == nil {
			return result, errors.New("model list missing")
		}
		for _, m := range value.Models {
			id := strings.TrimPrefix(m.Name, "models/")
			result.Models = append(result.Models, id)
			result.Catalog.Models = append(result.Catalog.Models, core.ProviderModel{ID: id, Name: m.DisplayName, MaxInput: m.MaxInput, MaxOutput: m.MaxOutput})
		}
	} else {
		if value.Data == nil {
			return result, errors.New("model list missing")
		}
		for _, m := range value.Data {
			result.Models = append(result.Models, m.ID)
			model := core.ProviderModel{ID: m.ID, Name: m.DisplayName, ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutput}
			// Optional gateway extension, not part of the OpenAI model schema.
			// A missing or incompatible extension must not reject a valid model list.
			var modalities struct {
				Input  []string `json:"input"`
				Output []string `json:"output"`
			}
			if json.Unmarshal(m.Modalities, &modalities) != nil {
				modalities.Input = nil
				modalities.Output = nil
			}
			for _, kind := range []string{"text", "image", "audio", "pdf", "video"} {
				if slices.Contains(modalities.Input, kind) {
					model.InputModalities = append(model.InputModalities, kind)
				}
				if slices.Contains(modalities.Output, kind) {
					model.OutputModalities = append(model.OutputModalities, kind)
				}
			}
			if slices.Contains(modalities.Input, "text") {
				inputs := []string{"text"}
				for _, kind := range []string{"image", "audio", "pdf"} {
					if slices.Contains(modalities.Input, kind) {
						inputs = append(inputs, kind)
					}
				}
				result.InputModalities[m.ID] = inputs
			}
			result.Catalog.Models = append(result.Catalog.Models, model)
		}
	}
	for _, id := range result.Models {
		if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") {
			return ProviderCheck{}, errors.New("invalid model identity")
		}
	}
	slices.Sort(result.Models)
	result.Models = slices.Compact(result.Models)
	seen := map[string]bool{}
	models := []core.ProviderModel{}
	for _, model := range result.Catalog.Models {
		if !seen[model.ID] {
			models = append(models, model)
			seen[model.ID] = true
		}
	}
	result.Catalog.Models = models
	if err = (core.ModelProviderResponse{Catalog: result.Catalog}).Validate("list_models"); err != nil {
		return ProviderCheck{}, err
	}
	result.Supported = true
	return result, nil
}

func (p *ProfileStore) checkCredential(ctx context.Context, r CheckProvider) ([]byte, error) {
	typ, err := p.requestProviderType(ctx, r.ProviderTypeID, r.ProfileID, r.Protocol)
	if err != nil {
		return nil, err
	}
	key := []byte(r.APIKey)
	if len(key) == 0 && r.ProfileID != "" {
		_, provider, err := p.savedProvider(ctx, r.ProfileID)
		if err != nil {
			return nil, err
		}
		if provider.Endpoint != r.Endpoint || provider.Protocol != r.Protocol || provider.TypeID != "" && provider.TypeID != typ.ID {
			return nil, errors.New("enter a credential when checking a changed endpoint")
		}
		key, err = p.registry.Credential(ctx, provider)
		if err != nil {
			return nil, err
		}
	}
	if len(key) == 0 {
		key = []byte(typ.definition.DefaultCredential)
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
