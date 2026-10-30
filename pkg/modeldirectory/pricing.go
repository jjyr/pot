package modeldirectory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// PriceQuote is public catalog evidence, not a provider invoice. Cost retains
// unknown dimensions so consumers cannot silently discard unsupported pricing.
type PriceQuote struct {
	Source    string          `json:"source"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Match     string          `json:"match"`
	Version   string          `json:"version"`
	UpdatedAt int64           `json:"updated_at"`
	Cost      json.RawMessage `json:"cost"`
}

func (d *Directory) Price(providerID, modelID string) *PriceQuote {
	d.mu.RLock()
	defer d.mu.RUnlock()
	quote := func(pid, mid, match string) *PriceQuote {
		p, ok := d.state.Providers[pid]
		if !ok {
			return nil
		}
		id, m, ok := pricedModel(p, mid)
		if !ok {
			return nil
		}
		hash := sha256.Sum256(m.Cost)
		return &PriceQuote{Source: "models.dev", Provider: pid, Model: id, Match: match, Version: hex.EncodeToString(hash[:]), UpdatedAt: d.state.UpdatedAt, Cost: append(json.RawMessage(nil), m.Cost...)}
	}
	if providerID != "" {
		if q := quote(providerID, modelID, "provider"); q != nil {
			return q
		}
	}
	name := modelID
	// Routing namespaces preserve model identity; never strip a version or date.
	for _, prefix := range []string{"openrouter/", "anthropic/", "deepseek/", "openai/", "google/", "x-ai/", "xai/", "mistralai/"} {
		name = strings.TrimPrefix(name, prefix)
	}
	origin := ""
	switch {
	case strings.HasPrefix(name, "claude-"):
		origin = "anthropic"
	case strings.HasPrefix(name, "deepseek-"):
		origin = "deepseek"
	case strings.HasPrefix(name, "gpt-"), strings.HasPrefix(name, "chatgpt-"), strings.HasPrefix(name, "o1"), strings.HasPrefix(name, "o3"), strings.HasPrefix(name, "o4"):
		origin = "openai"
	case strings.HasPrefix(name, "gemini-"):
		origin = "google"
	case strings.HasPrefix(name, "grok-"):
		origin = "xai"
	case strings.HasPrefix(name, "mistral-"), strings.HasPrefix(name, "codestral-"), strings.HasPrefix(name, "devstral-"):
		origin = "mistral"
	}
	if origin == "" {
		return nil
	}
	return quote(origin, name, "origin_fallback")
}
func pricedModel(p provider, id string) (string, model, bool) {
	if m, ok := p.Models[id]; ok && len(m.Cost) > 0 && string(m.Cost) != "null" {
		return id, m, true
	}
	// Directory channels use both claude-opus-4.6 and claude-opus-4-6.
	// Only punctuation is normalized; require a unique complete ID match.
	normalized := strings.ReplaceAll(id, ".", "-")
	var found string
	var result model
	for key, m := range p.Models {
		if strings.ReplaceAll(key, ".", "-") == normalized && len(m.Cost) > 0 && string(m.Cost) != "null" {
			if found != "" {
				return "", model{}, false
			}
			found = key
			result = m
		}
	}
	return found, result, found != ""
}
