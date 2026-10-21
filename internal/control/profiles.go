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
	"github.com/jjyr/pot/pkg/storage"
	"github.com/jjyr/pot/pkg/storage/migrations"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type Connection struct {
	Catalog      *core.ProviderModelCatalog      `json:"catalog,omitempty"`
	TypeID       string                          `json:"type_id,omitempty"`
	PluginID     string                          `json:"plugin_id,omitempty"`
	Capabilities *core.ModelProviderCapabilities `json:"capabilities,omitempty"`
	Available    *bool                           `json:"available,omitempty"`
	Icon         string                          `json:"icon,omitempty"`
	ProfileID    string                          `json:"profile_id"`
	ProviderID   string                          `json:"provider_id"`
	Endpoint     string                          `json:"endpoint"`
	Protocol     string                          `json:"protocol"`
	HasKey       bool                            `json:"has_key"`
}
type ProfileSnapshot struct {
	ProviderTypes     []ProviderType                   `json:"provider_types,omitempty"`
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
	metadataWake     chan struct{}
	mu               sync.Mutex
	path, pluginRoot string
	registry         *gateway.Registry
	directory        *modeldirectory.Directory
	state            profileFile
	ownerAction      func(func() error) error
	extensions       *Service
	discoveryMu      sync.Mutex
	discoveries      map[string]core.ModelProviderResponse
	metadataFlights  map[string]*providerMetadataFlight
}

func OpenProfiles(path, pluginRoot string, registry *gateway.Registry) (*ProfileStore, error) {
	p := &ProfileStore{metadataWake: make(chan struct{}, 1), path: path, pluginRoot: pluginRoot, registry: registry, state: profileFile{Version: 3, ProfileSnapshot: ProfileSnapshot{Profiles: []core.Profile{}, Connections: []Connection{}, ConfiguredModels: map[string][]string{}}, Requests: map[string]profileReceipt{}}}
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
	body, upgraded, err := migrations.UpgradeProfileCacheTTL(body)
	if err != nil {
		return nil, err
	}
	if err = core.DecodeJSON(body, &p.state); err != nil {
		return nil, err
	}
	if p.state.Version != 3 || p.state.Requests == nil {
		return nil, errors.New("unsupported profile store")
	}
	settings, err := p.defaultAgentSettings()
	if err != nil {
		return nil, err
	}
	for i := range p.state.Profiles {
		profile := &p.state.Profiles[i]
		// Connection records cannot retain old assistant instructions or tool filters.
		// Model intent and already persisted execution snapshots remain independent.
		defaults := settings
		defaults.ModelCatalog = profile.Params.Agent.ModelCatalog
		if profile.ModelSettings == nil {
			defaults.ContextWindow = profile.Params.Agent.ContextWindow
		}
		profile.Params.Agent = defaults
		if err = profile.Validate(); err != nil {
			return nil, err
		}
	}
	if upgraded {
		if err := instance.WriteJSON(path, p.state); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// defaultAgentSettings discovers installed tool packages and pins execution snapshots.
func (p *ProfileStore) defaultAgentSettings() (core.AgentSettings, error) {
	settings := core.DefaultAgentSettings()
	if p.pluginRoot == "" && p.extensions == nil {
		return settings, nil
	}
	roots := []string{p.pluginRoot}
	if p.extensions != nil {
		p.extensions.extensionsMu.Lock()
		defer p.extensions.extensionsMu.Unlock()
		roots = append(roots, filepath.Join(p.extensions.options.AgentHome, "plugins"))
	}
	packages, err := discoverPlugins(roots...)
	if err != nil {
		return settings, err
	}
	for _, installed := range packages {
		if installed.Manifest.Contributes.Tools == nil {
			continue
		}
		bundle, err := installed.ToolBundle()
		if err != nil {
			return settings, err
		}
		enabled := true
		if p.extensions != nil {
			enabled, err = p.extensions.enabled(context.Background(), "plugins", installed.Manifest.ID)
			if err != nil {
				return settings, err
			}
		}
		root := installed.Root
		settings.Plugins[installed.Manifest.ID] = core.PluginConfig{Enabled: enabled, Source: &root, Bundle: &bundle}
	}
	if p.extensions != nil {
		servers, err := p.extensions.mcpServers(context.Background())
		if err != nil {
			return settings, err
		}
		for _, server := range servers {
			if !server.Enabled || len(server.Tools) == 0 || len(server.DisabledTools) == len(server.Tools) {
				continue
			}
			bundle, err := mcpBundle(server)
			if err != nil {
				return settings, err
			}
			settings.Plugins[bundle.Manifest.ID] = core.PluginConfig{Enabled: true, Bundle: &bundle}
		}
	}
	return settings, nil
}

// savedSnapshot copies saved connection intent without building UI directory
// metadata. Execution paths must not enumerate every known model to read IDs.
func (p *ProfileStore) savedSnapshot() ProfileSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, _ := json.Marshal(p.state.ProfileSnapshot)
	var result ProfileSnapshot
	_ = json.Unmarshal(body, &result)
	return result
}

func (p *ProfileStore) Snapshot() ProfileSnapshot { return p.connectionSnapshot(true) }

// routingSnapshot retains cached provider discovery without expanding the
// public model directory and all of its default limits on every model turn.
func (p *ProfileStore) routingSnapshot() ProfileSnapshot { return p.connectionSnapshot(false) }

func (p *ProfileStore) connectionSnapshot(includeDirectory bool) ProfileSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, _ := json.Marshal(p.state.ProfileSnapshot)
	var result ProfileSnapshot
	json.Unmarshal(body, &result)
	types, err := p.providerTypes(context.Background())
	if err == nil {
		result.ProviderTypes = types
		if includeDirectory {
			result.Directory = p.registeredDirectory(types)
		}
		byID := map[string]ProviderType{}
		for _, typ := range types {
			byID[typ.ID] = typ
		}
		for i := range result.Connections {
			c := &result.Connections[i]
			available := false
			c.Available = &available
			provider, getErr := p.registry.Get(context.Background(), c.ProviderID)
			if getErr != nil {
				continue
			}
			c.TypeID = provider.TypeID
			if c.TypeID == "" {
				c.TypeID = customProviderType
			}
			if typ, ok := byID[c.TypeID]; ok {
				c.PluginID = typ.PluginID
				c.Icon = typ.Icon
				capabilities := typ.Capabilities
				c.Capabilities = &capabilities
				available = typ.Available
				if available {
					raw, readErr := p.registry.ReadMetadata(context.Background(), provider.ID, "list_models", typ.Identity.Digest)
					var cached core.ModelProviderResponse
					if readErr == nil && len(raw) > 0 && core.DecodeJSON(raw, &cached) == nil && cached.Validate("list_models") == nil {
						c.Catalog = cached.Catalog
						models := []string{}
						for _, model := range cached.Catalog.Models {
							models = append(models, model.ID)
						}
						for _, profile := range result.Profiles {
							if profile.ID == c.ProfileID {
								models = append(models, profile.Model)
								break
							}
						}
						slices.Sort(models)
						models = slices.Compact(models)
						if result.ConfiguredModels == nil {
							result.ConfiguredModels = map[string][]string{}
						}
						result.ConfiguredModels[c.ProfileID] = models
					}
				}
			}
		}
	}
	if includeDirectory {
		result.ModelDefaults = p.directory.Defaults()
	}
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
			resolved, err := p.applyModelMetadata(profile)
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
	ProviderTypeID   string              `json:"provider_type_id"`
	RequestID        string              `json:"request_id"`
	ProfileID        string              `json:"profile_id"`
	ExpectedRevision uint64              `json:"expected_revision"`
	Name             string              `json:"name"`
	Model            string              `json:"model"`
	Models           []string            `json:"models,omitempty"`
	Endpoint         string              `json:"endpoint"`
	Protocol         string              `json:"protocol"`
	APIKey           string              `json:"api_key"`
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
	if !core.Identifier(r.RequestID) || strings.TrimSpace(r.Name) == "" || len(r.Name) > 512 || r.Model == "" {
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
	resolved, err := resolveProviderEndpoint(r.Endpoint, r.Protocol)
	if err != nil {
		return core.Profile{}, err
	}
	r.Endpoint = resolved.Endpoint
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
	settings, err := p.defaultAgentSettings()
	if err != nil {
		return core.Profile{}, err
	}
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
	profile := core.Profile{ID: r.ProfileID, Revision: revision, Name: r.Name, Provider: routeID, Model: r.Model, AuthRef: "provider:" + routeID, ModelSettings: r.ModelSettings, Params: core.Parameters{MaxOutputTokens: output, ReasoningEffort: r.ReasoningEffort, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	typeID := r.ProviderTypeID
	if typeID == "" && index >= 0 {
		old, lookupErr := p.registry.Get(ctx, previous.Provider)
		if lookupErr != nil {
			return core.Profile{}, lookupErr
		}
		typeID = old.TypeID
	}
	typ, typeErr := p.providerType(ctx, typeID, r.Protocol)
	if typeErr != nil {
		return core.Profile{}, typeErr
	}
	identity := typ.Identity
	provider := gateway.Provider{ID: routeID, Endpoint: r.Endpoint, Protocol: r.Protocol, SecretRef: "provider/" + routeID, TypeID: typ.ID, Source: &identity}
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
	if provider.APIProvider == "" && typ.DirectoryProvider != "" {
		provider.APIProvider = typ.DirectoryProvider
	}
	provider.AllowedHosts = []string{parsed}
	key := []byte(r.APIKey)
	defer clear(key)
	if len(key) == 0 && index >= 0 {
		old, err := p.registry.Get(ctx, previous.Provider)
		if err != nil {
			return core.Profile{}, err
		}
		oldType := old.TypeID
		if oldType == "" {
			oldType = customProviderType
		}
		if oldType != typ.ID {
			return core.Profile{}, errors.New("enter a credential when changing the provider type")
		}
		key, err = p.registry.Credential(ctx, old)
		if err != nil {
			return core.Profile{}, err
		}
		defer clear(key)
	}
	if len(key) == 0 {
		key = []byte(typ.definition.DefaultCredential)
		defer clear(key)
	}
	if err = p.registry.Save(ctx, provider, key); err != nil {
		return core.Profile{}, err
	}
	if cached := p.discoveredCatalog(typ, r.Endpoint, r.Protocol, key); cached != nil {
		payload, _ := json.Marshal(cached)
		if err = p.registry.SaveMetadata(ctx, provider.ID, "list_models", typ.Identity.Digest, payload); err != nil {
			return core.Profile{}, err
		}
	}
	if _, err = p.applyModelMetadata(profile); err != nil {
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
	connection := Connection{ProfileID: profile.ID, ProviderID: routeID, Endpoint: r.Endpoint, Protocol: r.Protocol, HasKey: true}
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
	select {
	case p.metadataWake <- struct{}{}:
	default:
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

// RunMetadata owns both public-directory refresh and optional connection catalogs.
// Neither reader sends inference requests. Cancellation drains all child readers.
func (p *ProfileStore) RunMetadata(ctx context.Context) {
	var lifecycle sync.WaitGroup
	lifecycle.Go(func() { p.directory.Run(ctx) })
	defer lifecycle.Wait()
	for {
		select {
		case <-p.metadataWake:
		default:
		}
		p.refreshPluginCatalogs(ctx)
		timer := time.NewTimer(5 * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-p.metadataWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
