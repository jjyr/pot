package control

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modeldirectory"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/pluginhost"
)

const customProviderType = "standard-model-providers/custom"

type ProviderType struct {
	ID                string                         `json:"id"`
	PluginID          string                         `json:"plugin_id"`
	Name              string                         `json:"name"`
	Endpoint          string                         `json:"endpoint,omitempty"`
	Protocol          string                         `json:"protocol"`
	Protocols         []string                       `json:"protocols"`
	DirectoryProvider string                         `json:"directory_provider,omitempty"`
	DefaultCredential string                         `json:"default_credential,omitempty"`
	Capabilities      core.ModelProviderCapabilities `json:"capabilities"`
	Icon              string                         `json:"icon,omitempty"`
	Available         bool                           `json:"available"`
	Identity          core.PluginIdentity            `json:"identity"`
	definition        core.ModelProviderDefinition
	installed         plugin.Package
}

func (p *ProfileStore) providerTypes(ctx context.Context) ([]ProviderType, error) {
	roots := []string{p.pluginRoot}
	if p.extensions != nil {
		p.extensions.extensionsMu.Lock()
		defer p.extensions.extensionsMu.Unlock()
		roots = append(roots, filepath.Join(p.extensions.options.AgentHome, "plugins"))
	}
	packages, err := discoverPlugins(roots...)
	if err != nil {
		return nil, err
	}
	options := p.directory.Options()
	result := []ProviderType{}
	seen := map[string]bool{}
	for _, installed := range packages {
		contribution := installed.Manifest.Contributes.ModelProviders
		if contribution == nil {
			continue
		}
		identity, capabilityErr := installed.ModelProviderIdentity()
		enabled := true
		if p.extensions != nil {
			enabled, err = p.extensions.enabled(ctx, "plugins", installed.Manifest.ID)
			if err != nil {
				return nil, err
			}
		}
		if capabilityErr != nil {
			enabled = false
		}
		icon, _ := installed.Icon()
		definitions := []core.ModelProviderDefinition{}
		for _, d := range contribution.Providers {
			if d.DirectoryProvider != "*" {
				definitions = append(definitions, d)
				continue
			}
			for _, option := range options {
				expanded := d
				expanded.ID = option.ID
				expanded.Name = option.Name
				expanded.Endpoint = option.Endpoint
				expanded.Protocol = option.Protocol
				expanded.DirectoryProvider = option.ID
				expanded.Protocols = []string{option.Protocol}
				for _, m := range option.Models {
					if !slices.Contains(expanded.Protocols, m.Protocol) {
						expanded.Protocols = append(expanded.Protocols, m.Protocol)
					}
				}
				definitions = append(definitions, expanded)
			}
		}
		for _, d := range definitions {
			typeIcon := icon
			if typeIcon == "" && d.DirectoryProvider != "" {
				for _, option := range options {
					if option.ID == d.DirectoryProvider && option.Logo != "" {
						typeIcon = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(option.Logo))
						break
					}
				}
			}
			id := installed.Manifest.ID + "/" + d.ID
			if seen[id] {
				return nil, errors.New("duplicate model provider type")
			}
			seen[id] = true
			protocols := append([]string{}, d.Protocols...)
			if len(protocols) == 0 {
				protocols = []string{d.Protocol}
			}
			result = append(result, ProviderType{ID: id, PluginID: installed.Manifest.ID, Name: d.Name, Endpoint: d.Endpoint, Protocol: d.Protocol, Protocols: protocols, DirectoryProvider: d.DirectoryProvider, Capabilities: d.Capabilities, Icon: typeIcon, DefaultCredential: d.DefaultCredential, Available: enabled, Identity: identity, definition: d, installed: installed})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (p *ProfileStore) providerType(ctx context.Context, id, protocol string) (ProviderType, error) {
	if id == "" {
		id = customProviderType
	}
	types, err := p.providerTypes(ctx)
	if err != nil {
		return ProviderType{}, err
	}
	for _, t := range types {
		if t.ID == id {
			if !t.Available {
				return t, errors.New("model provider plugin unavailable or disabled")
			}
			if protocol != "" && !slices.Contains(t.Protocols, protocol) {
				return t, errors.New("model provider request format unavailable")
			}
			return t, nil
		}
	}
	return ProviderType{}, errors.New("model provider type unavailable")
}

func (p *ProfileStore) sourceAvailable(ctx context.Context, routeID string) error {
	provider, err := p.registry.Get(ctx, routeID)
	if err != nil {
		return err
	}
	_, err = p.providerType(ctx, provider.TypeID, provider.Protocol)
	return err
}

func (p *ProfileStore) requestProviderType(ctx context.Context, id, profileID, protocol string) (ProviderType, error) {
	if id == "" && profileID != "" {
		_, provider, err := p.savedProvider(ctx, profileID)
		if err != nil {
			return ProviderType{}, err
		}
		id = provider.TypeID
	}
	return p.providerType(ctx, id, protocol)
}

func (p *ProfileStore) savedProvider(ctx context.Context, id string) (core.Profile, gateway.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, profile := range p.state.Profiles {
		if profile.ID == id {
			provider, err := p.registry.Get(ctx, profile.Provider)
			return profile, provider, err
		}
	}
	return core.Profile{}, gateway.Provider{}, errors.New("model profile is unavailable")
}

// ProviderConnection returns the current saved route and current installed
// contribution. Callers must fence its route and identity before accepting reads.
func (p *ProfileStore) ProviderConnection(ctx context.Context, id string) (core.Profile, gateway.Provider, ProviderType, error) {
	profile, provider, err := p.savedProvider(ctx, id)
	if err != nil {
		return profile, provider, ProviderType{}, err
	}
	typ, err := p.providerType(ctx, provider.TypeID, provider.Protocol)
	return profile, provider, typ, err
}

func (p *ProfileStore) registeredDirectory(types []ProviderType) []modeldirectory.ProviderOption {
	options := p.directory.Options()
	byID := map[string]modeldirectory.ProviderOption{}
	for _, option := range options {
		byID[option.ID] = option
	}
	result := []modeldirectory.ProviderOption{}
	for _, typ := range types {
		if !typ.Available {
			continue
		}
		if option, ok := byID[typ.DirectoryProvider]; ok {
			result = append(result, option)
		}
	}
	return result
}

func (p *ProfileStore) pluginRead(ctx context.Context, typ ProviderType, connection core.ModelProviderConnection, method string, key []byte) (core.ModelProviderResponse, error) {
	env := map[string]string{}
	if adapter := typ.installed.Manifest.Contributes.ModelProviders.Adapter; adapter != nil && adapter.CredentialEnv != "" && len(key) > 0 {
		env[adapter.CredentialEnv] = string(key)
	}
	node := ""
	var worker []string
	if p.extensions != nil {
		node = p.extensions.options.Node
		executable, err := os.Executable()
		if err != nil {
			return core.ModelProviderResponse{}, err
		}
		worker = []string{executable}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(p.extensions.ctx, cancel)
		defer stop()
	}
	result, err := pluginhost.RunModelProvider(ctx, node, typ.installed, core.ModelProviderRequest{Version: 1, Method: method, Connection: connection}, env, worker)
	if err != nil {
		return result, err
	}
	current, err := p.providerType(ctx, typ.ID, connection.Protocol)
	if err != nil {
		return core.ModelProviderResponse{}, err
	}
	if current.Identity != typ.Identity {
		return core.ModelProviderResponse{}, errors.New("model provider package changed")
	}
	return result, nil
}

func metadataRecent(response core.ModelProviderResponse, method string) bool {
	var stamp string
	if method == "list_models" && response.Catalog != nil {
		stamp = response.Catalog.ReadAt
	}
	if method == "read_allowances" && response.Allowances != nil {
		stamp = response.Allowances.ReadAt
	}
	at, err := time.Parse(time.RFC3339, stamp)
	age := time.Since(at)
	return err == nil && age >= 0 && age < time.Minute
}

type providerMetadataFlight struct {
	done     chan struct{}
	response core.ModelProviderResponse
	err      error
}

func (p *ProfileStore) readSavedMetadata(ctx context.Context, id, method string, refresh bool) (core.ModelProviderResponse, error) {
	profile, provider, typ, err := p.ProviderConnection(ctx, id)
	if err != nil {
		return core.ModelProviderResponse{}, err
	}
	flightKey, _ := core.HashJSON([]any{profile.ID, profile.Revision, provider.ID, typ.Identity, method})
	p.discoveryMu.Lock()
	if p.metadataFlights == nil {
		p.metadataFlights = map[string]*providerMetadataFlight{}
	}
	if active := p.metadataFlights[flightKey]; active != nil {
		p.discoveryMu.Unlock()
		select {
		case <-ctx.Done():
			return core.ModelProviderResponse{}, ctx.Err()
		case <-active.done:
			if err := p.metadataFence(ctx, profile, provider, typ); err != nil {
				return core.ModelProviderResponse{}, err
			}
			return active.response, active.err
		}
	}
	active := &providerMetadataFlight{done: make(chan struct{})}
	p.metadataFlights[flightKey] = active
	p.discoveryMu.Unlock()
	active.response, active.err = p.readSavedMetadataFresh(ctx, id, method, refresh)
	p.discoveryMu.Lock()
	delete(p.metadataFlights, flightKey)
	close(active.done)
	p.discoveryMu.Unlock()
	if err := p.metadataFence(ctx, profile, provider, typ); err != nil {
		return core.ModelProviderResponse{}, err
	}
	return active.response, active.err
}
func (p *ProfileStore) readSavedMetadataFresh(ctx context.Context, id, method string, refresh bool) (core.ModelProviderResponse, error) {
	profile, provider, typ, err := p.ProviderConnection(ctx, id)
	if err != nil {
		return core.ModelProviderResponse{}, err
	}
	if method == "read_allowances" && !typ.Capabilities.Allowances || method == "list_models" && typ.Capabilities.Catalog == "none" {
		return core.ModelProviderResponse{}, errors.New("model provider capability unavailable")
	}
	if !refresh {
		raw, err := p.registry.ReadMetadata(ctx, provider.ID, method, typ.Identity.Digest)
		if err != nil {
			return core.ModelProviderResponse{}, err
		}
		var cached core.ModelProviderResponse
		if len(raw) > 0 && core.DecodeJSON(raw, &cached) == nil && cached.Validate(method) == nil && metadataRecent(cached, method) {
			if err := p.metadataFence(ctx, profile, provider, typ); err != nil {
				return core.ModelProviderResponse{}, err
			}
			return cached, nil
		}
	}
	key, err := p.registry.Credential(ctx, provider)
	if err != nil {
		return core.ModelProviderResponse{}, err
	}
	defer clear(key)
	connection := core.ModelProviderConnection{ID: profile.ID, Type: typ.definition.ID, Revision: profile.Revision, Endpoint: provider.Endpoint, Protocol: provider.Protocol}
	var response core.ModelProviderResponse
	if method == "list_models" && typ.Capabilities.Catalog == "models_dev" {
		checked, err := p.directoryCatalog(typ)
		if err != nil {
			return response, err
		}
		response.Catalog = checked.Catalog
	} else if method == "list_models" && typ.Capabilities.Catalog != "plugin" {
		checked, err := p.checkHTTP(ctx, CheckProvider{Endpoint: provider.Endpoint, Protocol: provider.Protocol}, key)
		if err != nil {
			return response, err
		}
		models := []core.ProviderModel{}
		for _, id := range checked.Models {
			models = append(models, core.ProviderModel{ID: id, InputModalities: checked.InputModalities[id]})
		}
		response.Catalog = checked.Catalog
		if response.Catalog == nil {
			response.Catalog = &core.ProviderModelCatalog{Models: models, ReadAt: time.Now().UTC().Format(time.RFC3339Nano)}
		}
	} else {
		response, err = p.pluginRead(ctx, typ, connection, method, key)
		if err != nil {
			var typed *core.ModelProviderError
			if errors.As(err, &typed) && (typed.Code == "authorization" || typed.Code == "permission") {
				_ = p.registry.ClearMetadata(ctx, provider.ID, method)
			}
			return response, err
		}
	}
	// A changed connection, disabled package, or replaced adapter invalidates the
	// response even when its network request already completed.
	p.mu.Lock()
	defer p.mu.Unlock()
	found := false
	for _, current := range p.state.Profiles {
		if current.ID == id && current.Provider == provider.ID && current.Revision == profile.Revision {
			found = true
			break
		}
	}
	if !found {
		return core.ModelProviderResponse{}, errors.New("model provider connection changed")
	}
	current, err := p.providerType(ctx, typ.ID, provider.Protocol)
	if err != nil {
		return core.ModelProviderResponse{}, err
	}
	if current.Identity != typ.Identity {
		return core.ModelProviderResponse{}, errors.New("model provider package changed")
	}
	if err = response.Validate(method); err != nil {
		return core.ModelProviderResponse{}, err
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return core.ModelProviderResponse{}, err
	}
	if err = p.registry.SaveMetadata(ctx, provider.ID, method, typ.Identity.Digest, payload); err != nil {
		return core.ModelProviderResponse{}, err
	}
	return response, nil
}

func (p *ProfileStore) ReadAllowances(ctx context.Context, id string, refresh bool) (core.AllowanceSnapshot, error) {
	response, err := p.readSavedMetadata(ctx, id, "read_allowances", refresh)
	if err != nil {
		return core.AllowanceSnapshot{}, err
	}
	return *response.Allowances, nil
}
func (s *Service) ReadProviderAllowances(ctx context.Context, id string, refresh bool) (core.AllowanceSnapshot, error) {
	return s.Profiles.ReadAllowances(ctx, id, refresh)
}

// applyModelMetadata only uses the exact ID from a current contribution's cache.
// User token overrides remain intent, but must fit the discovered model limits.
func (p *ProfileStore) applyModelMetadata(profile core.Profile) (core.Profile, error) {
	provider, err := p.registry.Get(context.Background(), profile.Provider)
	if err != nil {
		return profile, err
	}
	typ, err := p.providerType(context.Background(), provider.TypeID, provider.Protocol)
	if err != nil {
		return profile, err
	}
	raw, err := p.registry.ReadMetadata(context.Background(), provider.ID, "list_models", typ.Identity.Digest)
	if err != nil {
		return profile, err
	}
	if len(raw) == 0 {
		return p.directory.Apply(profile)
	}
	var response core.ModelProviderResponse
	if core.DecodeJSON(raw, &response) != nil || response.Validate("list_models") != nil {
		return profile, errors.New("invalid cached model metadata")
	}
	for _, model := range response.Catalog.Models {
		if model.ID != profile.Model {
			continue
		}
		if len(model.InputModalities) > 0 && !slices.Contains(model.InputModalities, "text") || len(model.OutputModalities) > 0 && !slices.Contains(model.OutputModalities, "text") {
			return profile, errors.New("model does not support text conversations")
		}
		var contextOverride, outputOverride *uint64
		settings := core.ModelSettings{}
		if profile.ModelSettings != nil {
			settings = *profile.ModelSettings
			contextOverride = settings.ContextWindow
			outputOverride = settings.MaxOutputTokens
		}
		// Resolve fallback facts with the actual connection's known context. A public
		// suffix alias must not reject an override before exact gateway limits apply.
		if contextOverride == nil && model.ContextWindow > 0 {
			v := uint64(model.ContextWindow)
			settings.ContextWindow = &v
		}
		if model.MaxOutput > 0 {
			settings.MaxOutputTokens = nil
		}
		temporary := profile
		temporary.ModelSettings = &settings
		result, err := p.directory.Apply(temporary)
		if err != nil {
			return result, err
		}
		result.ModelSettings = profile.ModelSettings
		window := result.Params.Agent.ContextWindow
		if window < 2 || model.ContextWindow > 0 && window > uint64(model.ContextWindow) {
			return result, errors.New("context window exceeds model maximum")
		}
		if model.MaxOutput > 0 {
			result.Params.MaxOutputTokens = uint64(model.MaxOutput)
		}
		if outputOverride != nil {
			result.Params.MaxOutputTokens = *outputOverride
		} else {
			result.Params.MaxOutputTokens = min(result.Params.MaxOutputTokens, window-1)
		}
		if result.Params.MaxOutputTokens == 0 || result.Params.MaxOutputTokens >= window || model.MaxOutput > 0 && result.Params.MaxOutputTokens > uint64(model.MaxOutput) {
			return result, errors.New("output limit exceeds model maximum")
		}
		if model.MaxInput > 0 {
			v := uint64(model.MaxInput)
			result.Params.Agent.MaxInputTokens = &v
		}
		result.Params.Agent.ContextReserve = min(result.Params.Agent.ContextReserve, window/4)
		if len(model.InputModalities) > 0 {
			catalog := result.Params.Agent.ModelCatalog
			if catalog == nil {
				catalog = &core.ModelCatalog{SchemaVersion: 1, Default: json.RawMessage(`{}`), Models: map[string]map[string]json.RawMessage{}}
			}
			defaults := map[string]any{"input_modalities": conversationModalities(model.InputModalities)}
			var overrides any
			if profile.Params.Agent.ModelCatalog != nil {
				if err = json.Unmarshal(profile.Params.Agent.ModelCatalog.Default, &overrides); err != nil {
					return result, err
				}
			}
			catalog.Default, _ = json.Marshal(core.MergeJSON(defaults, overrides))
			result.Params.Agent.ModelCatalog = catalog
		}
		return result, result.Validate()
	}
	return p.directory.Apply(profile)
}

// ResolveProviderEndpoint validates source availability without performing IO.
func (p *ProfileStore) ResolveProviderEndpoint(ctx context.Context, r CheckProvider) (any, error) {
	if _, err := p.requestProviderType(ctx, r.ProviderTypeID, r.ProfileID, r.Protocol); err != nil {
		return nil, err
	}
	return resolveProviderEndpoint(r.Endpoint, r.Protocol)
}

func discoveryKey(typ ProviderType, endpoint, protocol string, key []byte) string {
	// This digest is transient process memory only. It is never persisted or
	// returned, and cannot be used as a secret reference by clients.
	digest, _ := core.HashJSON([]any{typ.ID, typ.Identity, endpoint, protocol, string(key)})
	return digest
}
func (p *ProfileStore) rememberCatalog(typ ProviderType, endpoint, protocol string, key []byte, response core.ModelProviderResponse) {
	p.discoveryMu.Lock()
	defer p.discoveryMu.Unlock()
	if p.discoveries == nil {
		p.discoveries = map[string]core.ModelProviderResponse{}
	}
	if len(p.discoveries) >= 64 {
		clear(p.discoveries)
	}
	p.discoveries[discoveryKey(typ, endpoint, protocol, key)] = response
}
func (p *ProfileStore) discoveredCatalog(typ ProviderType, endpoint, protocol string, key []byte) *core.ModelProviderResponse {
	p.discoveryMu.Lock()
	defer p.discoveryMu.Unlock()
	response, ok := p.discoveries[discoveryKey(typ, endpoint, protocol, key)]
	if !ok || response.Catalog == nil {
		return nil
	}
	at, err := time.Parse(time.RFC3339, response.Catalog.ReadAt)
	if err != nil || time.Since(at) > 10*time.Minute {
		return nil
	}
	return &response
}
func (p *ProfileStore) checkedCatalogFence(ctx context.Context, r CheckProvider, typ ProviderType, starting *core.Profile) error {
	if starting != nil {
		current, _, err := p.savedProvider(ctx, starting.ID)
		if err != nil {
			return err
		}
		if current.Provider != starting.Provider || current.Revision != starting.Revision {
			return errors.New("model provider connection changed")
		}
	}
	current, err := p.providerType(ctx, typ.ID, r.Protocol)
	if err != nil {
		return err
	}
	if current.Identity != typ.Identity {
		return errors.New("model provider package changed")
	}
	return nil
}
func (p *ProfileStore) cacheCheckedCatalog(ctx context.Context, r CheckProvider, typ ProviderType, response core.ModelProviderResponse, key []byte, starting *core.Profile) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, profile := range p.state.Profiles {
		if profile.ID == r.ProfileID {
			if starting == nil || profile.Provider != starting.Provider || profile.Revision != starting.Revision {
				return errors.New("model provider connection changed")
			}
			provider, err := p.registry.Get(ctx, profile.Provider)
			if err != nil {
				return err
			}
			if provider.Endpoint != r.Endpoint || provider.Protocol != r.Protocol || provider.TypeID != "" && provider.TypeID != typ.ID {
				return nil
			}
			current, err := p.providerType(ctx, typ.ID, r.Protocol)
			if err != nil {
				return err
			}
			if current.Identity != typ.Identity {
				return errors.New("model provider package changed")
			}
			savedKey, err := p.registry.Credential(ctx, provider)
			if err != nil {
				return err
			}
			defer clear(savedKey)
			// A draft can check another key, but those models never become facts for
			// the saved route. Always compare the actual resolved key, even when omitted
			// from the public request and obtained from a previous saved revision.
			if subtle.ConstantTimeCompare(savedKey, key) != 1 {
				return nil
			}
			payload, _ := json.Marshal(response)
			return p.registry.SaveMetadata(ctx, provider.ID, "list_models", typ.Identity.Digest, payload)
		}
	}
	return errors.New("model provider connection changed")
}

func (p *ProfileStore) metadataFence(ctx context.Context, profile core.Profile, provider gateway.Provider, typ ProviderType) error {
	current, currentRoute, currentType, err := p.ProviderConnection(ctx, profile.ID)
	if err != nil {
		return err
	}
	if current.Provider != provider.ID || current.Revision != profile.Revision || currentRoute.ID != provider.ID || currentType.Identity != typ.Identity {
		return errors.New("model provider connection changed")
	}
	return nil
}

func conversationModalities(values []string) []string {
	result := []string{}
	for _, value := range values {
		if slices.Contains([]string{"text", "image", "audio", "pdf"}, value) && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

// refreshPluginCatalogs is independent of quota monitoring and UI reads. Four
// workers bound concurrency across connections; the host bounds every exchange.
func (p *ProfileStore) refreshPluginCatalogs(ctx context.Context) {
	p.mu.Lock()
	profiles := append([]core.Profile{}, p.state.Profiles...)
	p.mu.Unlock()
	jobs := make(chan string)
	var workers sync.WaitGroup
	var changed atomic.Bool
	for range 4 {
		workers.Go(func() {
			for id := range jobs {
				_, provider, typ, err := p.ProviderConnection(ctx, id)
				if err != nil || typ.Capabilities.Catalog != "plugin" {
					continue
				}
				previous, _ := p.registry.ReadMetadata(ctx, provider.ID, "list_models", typ.Identity.Digest)
				var old core.ModelProviderResponse
				_ = core.DecodeJSON(previous, &old)
				response, err := p.readSavedMetadata(ctx, id, "list_models", true)
				if err != nil {
					continue
				}
				oldHash, _ := core.HashJSON(old.Catalog)
				if old.Catalog != nil {
					oldHash, _ = core.HashJSON(old.Catalog.Models)
				}
				newHash, _ := core.HashJSON(response.Catalog.Models)
				if oldHash != newHash {
					changed.Store(true)
				}
			}
		})
	}
dispatch:
	for _, profile := range profiles {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- profile.ID:
		}
	}
	close(jobs)
	workers.Wait()
	if changed.Load() && p.extensions != nil {
		p.extensions.Changes.Notify("providers")
	}
}

func (p *ProfileStore) directoryCatalog(typ ProviderType) (ProviderCheck, error) {
	result := ProviderCheck{Models: []string{}, InputModalities: map[string][]string{}, Catalog: &core.ProviderModelCatalog{Models: []core.ProviderModel{}, ReadAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	for _, option := range p.directory.Options() {
		if option.ID != typ.DirectoryProvider {
			continue
		}
		for _, model := range option.Models {
			inputs := []string{"text"}
			if model.Image {
				inputs = append(inputs, "image")
			}
			if model.PDF {
				inputs = append(inputs, "pdf")
			}
			result.Models = append(result.Models, model.ID)
			result.InputModalities[model.ID] = inputs
			result.Catalog.Models = append(result.Catalog.Models, core.ProviderModel{ID: model.ID, Name: model.Name, InputModalities: inputs, OutputModalities: []string{"text"}, ContextWindow: int64(model.Context), MaxInput: int64(model.Input), MaxOutput: int64(model.Output)})
		}
		result.Supported = true
		return result, nil
	}
	return result, errors.New("model directory source unavailable")
}
