package control

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/modeldirectory"
	"github.com/jjyr/pot/pkg/resource"
	"github.com/jjyr/pot/pkg/storage"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Connection struct {
	ProfileID  string `json:"profile_id"`
	ProviderID string `json:"provider_id"`
	Endpoint   string `json:"endpoint"`
	Protocol   string `json:"protocol"`
	HasKey     bool   `json:"has_key"`
}
type ProfileSnapshot struct {
	Profiles          []core.Profile                   `json:"profiles"`
	Connections       []Connection                     `json:"connections"`
	ConfiguredModels  map[string][]string              `json:"configured_models"`
	DefaultProviderID string                           `json:"default_provider_id"`
	Directory         []modeldirectory.ProviderOption  `json:"directory,omitempty"`
	ModelDefaults     map[string]modeldirectory.Limits `json:"model_defaults,omitempty"`
}
type profileReceipt struct {
	InputHash string       `json:"input_hash"`
	Profile   core.Profile `json:"profile"`
}
type profileFile struct {
	Version         int `json:"version"`
	ProfileSnapshot `json:"snapshot"`
	Requests        map[string]profileReceipt `json:"requests"`
}
type ProfileStore struct {
	mu               sync.Mutex
	path, pluginRoot string
	registry         *gateway.Registry
	directory        *modeldirectory.Directory
	state            profileFile
	ownerAction      func(func() error) error
}

func OpenProfiles(path, pluginRoot string, registry *gateway.Registry) (*ProfileStore, error) {
	p := &ProfileStore{path: path, pluginRoot: pluginRoot, registry: registry, state: profileFile{Version: 1, ProfileSnapshot: ProfileSnapshot{Profiles: []core.Profile{}, Connections: []Connection{}, ConfiguredModels: map[string][]string{}}, Requests: map[string]profileReceipt{}}}
	directory, err := modeldirectory.Open(filepath.Join(filepath.Dir(path), "models-dev"))
	if err != nil {
		return nil, err
	}
	p.directory = directory
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err = core.DecodeJSON(body, &p.state); err != nil {
		return nil, err
	}
	if p.state.Version != 1 || p.state.Requests == nil {
		return nil, errors.New("unsupported profile store")
	}
	for _, profile := range p.state.Profiles {
		if err = profile.Validate(); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func (p *ProfileStore) Snapshot() ProfileSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, _ := json.Marshal(p.state.ProfileSnapshot)
	var result ProfileSnapshot
	json.Unmarshal(body, &result)
	result.Directory = p.directory.Options()
	result.ModelDefaults = p.directory.Defaults()
	return result
}
func (p *ProfileStore) Resolve(id string) (core.ResolvedSpec, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" {
		id = p.state.DefaultProviderID
	}
	for _, profile := range p.state.Profiles {
		if profile.ID == id {
			resolved, err := p.directory.Apply(profile)
			if err != nil {
				return core.ResolvedSpec{}, err
			}
			spec, err := resolved.Resolve()
			if err != nil {
				return spec, err
			}
			for _, c := range p.state.Connections {
				if c.ProfileID == id {
					spec.GatewayProtocol = c.Protocol
					err = spec.Rehash()
					return spec, err
				}
			}
		}
	}
	return core.ResolvedSpec{}, errors.New("model profile is unavailable")
}

type SaveProfile struct {
	RequestID        string              `json:"request_id"`
	ProfileID        string              `json:"profile_id"`
	ExpectedRevision uint64              `json:"expected_revision"`
	Name             string              `json:"name"`
	Model            string              `json:"model"`
	Models           []string            `json:"models,omitempty"`
	Endpoint         string              `json:"endpoint"`
	Protocol         string              `json:"protocol"`
	APIKey           string              `json:"api_key"`
	Agent            *core.AgentSettings `json:"agent,omitempty"`
	MaxOutputTokens  uint64              `json:"max_output_tokens"`
	ReasoningEffort  *string             `json:"reasoning_effort"`
	ModelSettings    *core.ModelSettings `json:"model_settings,omitempty"`
}

func (p *ProfileStore) Save(ctx context.Context, r SaveProfile) (core.Profile, error) {
	if p.ownerAction != nil {
		var profile core.Profile
		err := p.ownerAction(func() error { var err error; profile, err = p.save(ctx, r); return err })
		return profile, err
	}
	return p.save(ctx, r)
}
func (p *ProfileStore) save(ctx context.Context, r SaveProfile) (core.Profile, error) {
	if !core.Identifier(r.RequestID) || r.Name == "" || r.Model == "" {
		return core.Profile{}, errors.New("profile request, name and model are required")
	}
	if len(r.Models) > 10000 {
		return core.Profile{}, errors.New("saved model list exceeds limit")
	}
	for _, model := range r.Models {
		if strings.TrimSpace(model) == "" || len(strings.TrimSpace(model)) > 256 {
			return core.Profile{}, errors.New("invalid saved model ID")
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	sanitized := r
	sanitized.APIKey = ""
	inputHash, err := core.HashJSON([]any{sanitized, r.APIKey != ""})
	if err != nil {
		return core.Profile{}, err
	}
	// Bind retries without persisting plaintext credentials or their public digests.
	if receipt, ok := p.state.Requests[r.RequestID]; ok {
		if receipt.InputHash != inputHash {
			return core.Profile{}, errors.New("profile request ID reused with different content")
		}
		if r.APIKey != "" {
			provider, err := p.registry.Get(ctx, receipt.Profile.Provider)
			if err != nil {
				return core.Profile{}, err
			}
			key, err := p.registry.Credential(ctx, provider)
			if err != nil {
				return core.Profile{}, err
			}
			defer clear(key)
			if subtle.ConstantTimeCompare(key, []byte(r.APIKey)) != 1 {
				return core.Profile{}, errors.New("profile request ID reused with another credential")
			}
		}
		return cloneProfile(receipt.Profile)
	}
	index := -1
	var previous core.Profile
	for i, profile := range p.state.Profiles {
		if profile.ID == r.ProfileID {
			index = i
			previous = profile
			break
		}
	}
	if index < 0 && r.ExpectedRevision != 0 || index >= 0 && previous.Revision != r.ExpectedRevision {
		return core.Profile{}, errors.New("profile changed; refresh before saving")
	}
	if r.ProfileID == "" {
		r.ProfileID = storage.ID()
	}
	if !core.Identifier(r.ProfileID) {
		return core.Profile{}, errors.New("invalid profile identity")
	}
	settings := core.DefaultAgentSettings()
	if index >= 0 {
		settings = previous.Params.Agent
	}
	if r.Agent != nil {
		settings = *r.Agent
	}
	// Pin configured bundles before saving a resolved configuration. A new run uses
	// this snapshot; changes on disk never mutate an already running Agent.
	copiedPlugins := map[string]core.PluginConfig{}
	for name, plugin := range settings.Plugins {
		if plugin.Enabled {
			directory := filepath.Join(p.pluginRoot, name)
			if plugin.Source != nil {
				directory = *plugin.Source
			}
			bundle, err := resource.LoadPlugin(directory)
			if err != nil {
				return core.Profile{}, err
			}
			plugin.Bundle = &bundle
		}
		copiedPlugins[name] = plugin
	}
	settings.Plugins = copiedPlugins
	if r.ReasoningEffort != nil {
		effort := strings.TrimSpace(*r.ReasoningEffort)
		if effort == "" {
			r.ReasoningEffort = nil
		} else {
			r.ReasoningEffort = &effort
		}
	}
	output := r.MaxOutputTokens
	if output == 0 {
		output = 4096
	}
	revision := uint64(1)
	if index >= 0 {
		revision = previous.Revision + 1
	}
	routeID := storage.ID()
	profile := core.Profile{ID: r.ProfileID, Revision: revision, Name: r.Name, Provider: routeID, Model: r.Model, AuthRef: "provider:" + routeID, ModelSettings: r.ModelSettings, Params: core.Parameters{MaxOutputTokens: output, ReasoningEffort: r.ReasoningEffort, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "guest_vm"}}
	if _, err := p.directory.Apply(profile); err != nil {
		return core.Profile{}, err
	}
	provider := gateway.Provider{ID: routeID, Endpoint: r.Endpoint, Protocol: r.Protocol, SecretRef: "provider/" + routeID}
	parsed, err := parseProviderHost(r.Endpoint)
	if err != nil {
		return core.Profile{}, err
	}
	if r.ModelSettings != nil && r.ModelSettings.CatalogProvider != nil {
		provider.APIProvider = *r.ModelSettings.CatalogProvider
	}
	if provider.APIProvider == "" {
		if endpoint, parseErr := url.Parse(r.Endpoint); parseErr == nil && strings.EqualFold(endpoint.Hostname(), "api.deepseek.com") {
			provider.APIProvider = "deepseek"
		}
	}
	provider.AllowedHosts = []string{parsed}
	key := []byte(r.APIKey)
	defer clear(key)
	if len(key) == 0 && index >= 0 {
		old, err := p.registry.Get(ctx, previous.Provider)
		if err != nil {
			return core.Profile{}, err
		}
		key, err = p.registry.Credential(ctx, old)
		if err != nil {
			return core.Profile{}, err
		}
		defer clear(key)
	}
	if err = p.registry.Save(ctx, provider, key); err != nil {
		return core.Profile{}, err
	}
	body, _ := json.Marshal(p.state)
	var next profileFile
	if err = json.Unmarshal(body, &next); err != nil {
		return core.Profile{}, err
	}
	if index >= 0 {
		next.Profiles[index] = profile
	} else {
		next.Profiles = append(next.Profiles, profile)
	}
	if next.ConfiguredModels == nil {
		next.ConfiguredModels = map[string][]string{}
	}
	models := append([]string{}, r.Models...)
	if r.Models == nil {
		models = append(models, next.ConfiguredModels[profile.ID]...)
	}
	models = append(models, profile.Model)
	if len(models) > 10000 {
		return core.Profile{}, errors.New("saved model list exceeds limit")
	}
	seenModels := map[string]bool{}
	normalizedModels := []string{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 256 {
			return core.Profile{}, errors.New("invalid saved model ID")
		}
		if !seenModels[model] {
			seenModels[model] = true
			normalizedModels = append(normalizedModels, model)
		}
	}
	sort.Strings(normalizedModels)
	next.ConfiguredModels[profile.ID] = normalizedModels
	connection := Connection{profile.ID, routeID, r.Endpoint, r.Protocol, true}
	found := false
	for i, c := range next.Connections {
		if c.ProfileID == profile.ID {
			next.Connections[i] = connection
			found = true
		}
	}
	if !found {
		next.Connections = append(next.Connections, connection)
	}
	if next.DefaultProviderID == "" {
		next.DefaultProviderID = profile.ID
	}
	next.Requests[r.RequestID] = profileReceipt{inputHash, profile}
	if err = instance.WriteJSON(p.path, next); err != nil {
		return core.Profile{}, err
	}
	saved, _ := json.Marshal(next)
	if err = json.Unmarshal(saved, &p.state); err != nil {
		return core.Profile{}, err
	}
	return cloneProfile(profile)
}
func cloneProfile(p core.Profile) (core.Profile, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return core.Profile{}, err
	}
	var clone core.Profile
	err = json.Unmarshal(data, &clone)
	return clone, err
}
func parseProviderHost(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("invalid provider endpoint")
	}
	return parsed.Hostname(), nil
}
func (p *ProfileStore) SetDefault(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	found := false
	for _, profile := range p.state.Profiles {
		if profile.ID == id {
			found = true
		}
	}
	if !found {
		return errors.New("profile unavailable")
	}
	next := p.state
	next.DefaultProviderID = id
	if err := instance.WriteJSON(p.path, next); err != nil {
		return err
	}
	p.state = next
	return nil
}

func (p *ProfileStore) RunMetadata(ctx context.Context) { p.directory.Run(ctx) }
