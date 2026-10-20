package core

import (
	"errors"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

// PluginModelProviders contributes local connection types, never inference code
// or transferable Task resources. Definitions may use shared readers or a local adapter.
type PluginModelProviders struct {
	Providers []ModelProviderDefinition `json:"providers"`
	Adapter   *ModelProviderAdapter     `json:"adapter,omitempty"`
}
type ModelProviderDefinition struct {
	ID                string                    `json:"id"`
	Name              string                    `json:"name"`
	Endpoint          string                    `json:"endpoint,omitempty"`
	DefaultCredential string                    `json:"default_credential,omitempty"`
	Protocol          string                    `json:"protocol"`
	Protocols         []string                  `json:"protocols,omitempty"`
	DirectoryProvider string                    `json:"directory_provider,omitempty"`
	Capabilities      ModelProviderCapabilities `json:"capabilities"`
}
type ModelProviderCapabilities struct {
	Catalog    string `json:"catalog"`
	Allowances bool   `json:"allowances,omitempty"`
}
type ModelProviderAdapter struct {
	Runtime       string   `json:"runtime"`
	Execution     string   `json:"execution"`
	Entrypoint    string   `json:"entrypoint"`
	Files         []string `json:"files"`
	CredentialEnv string   `json:"credential_env,omitempty"`
}

func (p PluginModelProviders) Validate() error {
	if len(p.Providers) == 0 || len(p.Providers) > 512 {
		return errors.New("invalid model provider count")
	}
	seen := map[string]bool{}
	needsAdapter := false
	for _, d := range p.Providers {
		if !Identifier(d.ID) || seen[d.ID] || strings.TrimSpace(d.Name) == "" || len(d.Name) > 256 || len(d.DirectoryProvider) > 128 {
			return errors.New("invalid model provider definition")
		}
		seen[d.ID] = true
		if !validProviderProtocol(d.Protocol) {
			return errors.New("invalid model provider protocol")
		}
		protocols := map[string]bool{}
		for _, protocol := range d.Protocols {
			if !validProviderProtocol(protocol) || protocols[protocol] {
				return errors.New("invalid model provider supported protocols")
			}
			protocols[protocol] = true
		}
		if len(d.Protocols) > 0 && !protocols[d.Protocol] {
			return errors.New("model provider default protocol unavailable")
		}
		if len(d.DefaultCredential) > 4096 || strings.ContainsFunc(d.DefaultCredential, unicode.IsControl) {
			return errors.New("invalid model provider public default credential")
		}
		if d.Endpoint != "" && !modelProviderEndpoint(d.Endpoint) {
			return errors.New("invalid model provider endpoint")
		}
		if !slices.Contains([]string{"none", "models_dev", "http", "plugin"}, d.Capabilities.Catalog) {
			return errors.New("invalid model provider catalog capability")
		}
		if d.Capabilities.Catalog == "models_dev" && d.DirectoryProvider == "" {
			return errors.New("model directory binding required")
		}
		needsAdapter = needsAdapter || d.Capabilities.Catalog == "plugin" || d.Capabilities.Allowances
	}
	if needsAdapter && p.Adapter == nil {
		return errors.New("model provider adapter required")
	}
	if a := p.Adapter; a != nil {
		if a.Runtime != "node" || a.Execution != "control" || len(a.Files) == 0 || len(a.Files) > 32 || !slices.Contains(a.Files, a.Entrypoint) || a.CredentialEnv != "" && !CredentialEnvironmentName(a.CredentialEnv) {
			return errors.New("invalid model provider adapter")
		}
		seen := map[string]bool{}
		for _, f := range a.Files {
			if !SafeRelativePath(f) || seen[f] {
				return errors.New("invalid model provider file")
			}
			seen[f] = true
		}
	}
	return nil
}
func validProviderProtocol(protocol string) bool {
	return slices.Contains([]string{"openai_chat", "openai_responses", "anthropic_messages", "gemini"}, protocol)
}
func modelProviderEndpoint(endpoint string) bool {
	if len(endpoint) > 2048 {
		return false
	}
	u, err := url.Parse(endpoint)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == "" && u.RawQuery == ""
}

type ModelProviderConnection struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Revision uint64 `json:"revision"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"`
}
type ModelProviderRequest struct {
	Version    int                     `json:"version"`
	Method     string                  `json:"method"`
	Connection ModelProviderConnection `json:"connection"`
}

func (r ModelProviderRequest) Validate(p PluginModelProviders) error {
	if r.Version != 1 || !providerID(r.Connection.ID, 256) || r.Connection.Revision > MaxSafeInteger || !modelProviderEndpoint(r.Connection.Endpoint) {
		return errors.New("invalid model provider request")
	}
	for _, d := range p.Providers {
		if d.ID != r.Connection.Type {
			continue
		}
		if d.Protocol != r.Connection.Protocol && !slices.Contains(d.Protocols, r.Connection.Protocol) {
			return errors.New("model provider protocol mismatch")
		}
		if r.Method == "list_models" && d.Capabilities.Catalog == "plugin" || r.Method == "read_allowances" && d.Capabilities.Allowances {
			return nil
		}
		return errors.New("model provider method unavailable")
	}
	return errors.New("model provider type unavailable")
}

type ProviderModel struct {
	ID               string   `json:"id"`
	Name             string   `json:"name,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	ContextWindow    int64    `json:"context_window,omitempty"`
	MaxInput         int64    `json:"max_input,omitempty"`
	MaxOutput        int64    `json:"max_output,omitempty"`
}
type ProviderModelCatalog struct {
	Models []ProviderModel `json:"models"`
	ReadAt string          `json:"read_at"`
}
type AllowanceWindow struct {
	ID              string   `json:"id,omitempty"`
	Name            string   `json:"name,omitempty"`
	Used            *float64 `json:"used,omitempty"`
	Unlimited       bool     `json:"unlimited,omitempty"`
	ResetsAt        string   `json:"resets_at,omitempty"`
	Pool            string   `json:"pool,omitempty"`
	IdentityQuality string   `json:"identity_quality"`
}
type ProviderAllowance struct {
	ID              string            `json:"id,omitempty"`
	Provider        string            `json:"provider,omitempty"`
	Name            string            `json:"name,omitempty"`
	Kind            string            `json:"kind,omitempty"`
	Plan            string            `json:"plan,omitempty"`
	User            string            `json:"user,omitempty"`
	Windows         []AllowanceWindow `json:"windows"`
	Pool            string            `json:"pool,omitempty"`
	ReadAt          string            `json:"read_at,omitempty"`
	AsOf            string            `json:"as_of,omitempty"`
	Until           string            `json:"until,omitempty"`
	Renew           string            `json:"renew,omitempty"`
	IdentityQuality string            `json:"identity_quality"`
}
type AllowanceSnapshot struct {
	Accounts []ProviderAllowance `json:"accounts"`
	ReadAt   string              `json:"read_at"`
}

// ModelProviderError is a bounded status, never a plugin-supplied diagnostic.
type ModelProviderError struct {
	Code string `json:"code"`
}

func (e *ModelProviderError) Error() string { return "model provider " + e.Code }
func (e *ModelProviderError) Validate() error {
	if !slices.Contains([]string{"authorization", "permission", "rate_limited", "unavailable", "overflow", "invalid_response"}, e.Code) {
		return errors.New("invalid model provider error")
	}
	return nil
}

type ModelProviderResponse struct {
	Error      *ModelProviderError   `json:"error,omitempty"`
	Catalog    *ProviderModelCatalog `json:"catalog,omitempty"`
	Allowances *AllowanceSnapshot    `json:"allowances,omitempty"`
}

func providerTime(value string, required bool) bool {
	if value == "" {
		return !required
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
func providerID(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit && !strings.ContainsFunc(value, unicode.IsControl)
}
func identityQuality(id, quality string) bool {
	return (quality == "stable" && providerID(id, 256) || quality == "ambiguous" && (id == "" || providerID(id, 256)))
}
func (r ModelProviderResponse) Validate(method string) error {
	if !slices.Contains([]string{"list_models", "read_allowances"}, method) {
		return errors.New("unknown model provider method")
	}
	if r.Error != nil {
		if r.Catalog != nil || r.Allowances != nil {
			return errors.New("conflicting model provider response")
		}
		return r.Error.Validate()
	}
	switch method {
	case "list_models":
		if r.Catalog == nil || r.Allowances != nil || !providerTime(r.Catalog.ReadAt, true) || len(r.Catalog.Models) > 8192 {
			return errors.New("invalid model provider catalog")
		}
		seen := map[string]bool{}
		for _, m := range r.Catalog.Models {
			if !providerID(m.ID, 256) || seen[m.ID] || len(m.Name) > 512 || m.ContextWindow < 0 || m.MaxInput < 0 || m.MaxOutput < 0 || uint64(m.ContextWindow) > MaxSafeInteger || uint64(m.MaxInput) > MaxSafeInteger || uint64(m.MaxOutput) > MaxSafeInteger {
				return errors.New("invalid provider model")
			}
			seen[m.ID] = true
			for _, mods := range [][]string{m.InputModalities, m.OutputModalities} {
				seenMod := map[string]bool{}
				for _, mod := range mods {
					if !slices.Contains([]string{"text", "image", "audio", "pdf", "video"}, mod) || seenMod[mod] {
						return errors.New("invalid provider model modalities")
					}
					seenMod[mod] = true
				}
			}
		}
	case "read_allowances":
		if r.Allowances == nil || r.Catalog != nil || !providerTime(r.Allowances.ReadAt, true) || len(r.Allowances.Accounts) > 256 {
			return errors.New("invalid provider allowances")
		}
		seen := map[string]bool{}
		for _, a := range r.Allowances.Accounts {
			if !identityQuality(a.ID, a.IdentityQuality) || a.ID != "" && seen[a.ID] || len(a.Windows) > 64 || !providerTime(a.ReadAt, false) || !providerTime(a.AsOf, false) || !providerTime(a.Until, false) {
				return errors.New("invalid provider allowance")
			}
			if a.ID != "" {
				seen[a.ID] = true
			}
			for _, v := range []string{a.Provider, a.Name, a.Kind, a.Plan, a.User, a.Pool, a.Renew} {
				if len(v) > 512 {
					return errors.New("invalid provider allowance field")
				}
			}
			windows := map[string]bool{}
			for _, w := range a.Windows {
				if !identityQuality(w.ID, w.IdentityQuality) || w.ID != "" && windows[w.ID] || len(w.Name) > 512 || len(w.Pool) > 512 || !providerTime(w.ResetsAt, false) || w.Used != nil && (math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) || *w.Used < 0) {
					return errors.New("invalid allowance window")
				}
				if w.ID != "" {
					windows[w.ID] = true
				}
			}
		}
	}
	return nil
}
