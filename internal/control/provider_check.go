package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/gateway"
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
	Supported bool     `json:"supported"`
	Models    []string `json:"models"`
}

func (p *ProfileStore) Check(ctx context.Context, r CheckProvider) (ProviderCheck, error) {
	result := ProviderCheck{Models: []string{}}
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return result, errors.New("invalid model-list endpoint")
	}
	key := []byte(r.APIKey)
	defer clear(key)
	if len(key) == 0 && r.ProfileID != "" {
		spec, err := p.Resolve(r.ProfileID)
		if err != nil {
			return result, err
		}
		provider, err := p.registry.Get(ctx, spec.Provider)
		if err != nil {
			return result, err
		}
		if provider.Endpoint != r.Endpoint || provider.Protocol != r.Protocol {
			return result, errors.New("enter a credential when checking a changed endpoint")
		}
		key, err = p.registry.Credential(ctx, provider)
		if err != nil {
			return result, err
		}
		defer clear(key)
	}
	if len(key) == 0 || strings.ContainsAny(string(key), "\r\n") {
		return result, errors.New("provider credential required")
	}
	path := u.Path
	modelPath := ""
	switch r.Protocol {
	case "openai_chat":
		if strings.HasSuffix(path, "/chat/completions") {
			modelPath = strings.TrimSuffix(path, "/chat/completions") + "/models"
		}
	case "openai_responses":
		if strings.HasSuffix(path, "/responses") {
			modelPath = strings.TrimSuffix(path, "/responses") + "/models"
		}
	case "anthropic_messages":
		if strings.HasSuffix(path, "/messages") {
			modelPath = strings.TrimSuffix(path, "/messages") + "/models"
		}
	case "gemini":
		if prefix, _, ok := strings.Cut(path, "/models/"); ok {
			modelPath = prefix + "/models"
		}
	}
	if modelPath == "" {
		return result, nil
	}
	u.Path = modelPath
	u.RawPath = ""
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
		return result, gateway.ErrConnection
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 262145))
	if err != nil || len(data) > 262144 {
		return result, errors.New("invalid or oversized model-list response")
	}
	var value struct {
		Data []struct {
			ID string `json:"id"`
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
