package sandboxprovider

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxresources"
)

type Registry struct {
	Providers       []sandbox.Provider
	Catalog         sandboxresources.Catalog
	DefaultProvider string
}

// Load registers Sandbox capabilities from locally installed common Plugin
// packages. The RPC below is Sandbox-specific, not a second plugin framework.
// Tool selections and transferred Task bundles never reach this entry point.
func Load(ctx context.Context, packages []plugin.Package, config Config) (registry Registry, result error) {
	registry.Catalog = sandboxresources.Empty()
	registry.Providers = []sandbox.Provider{}
	defer func() {
		if result != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, p := range registry.Providers {
				result = errors.Join(result, p.Shutdown(shutdown))
			}
		}
	}()
	supported := map[string]bool{}
	for _, p := range packages {
		if err := p.Manifest.Validate(); err != nil {
			return registry, err
		}
		for _, c := range p.Manifest.Contributes.SandboxProviders {
			if _, exists := supported[c.ID]; exists {
				return registry, errors.New("duplicate Sandbox Provider ID")
			}
			supported[c.ID] = slices.Contains(c.HostOS, runtime.GOOS)
		}
	}
	// Catalog-only image plugins can contribute bundles for an installed provider.
	for _, p := range packages {
		for _, c := range p.Manifest.Contributes.Resources {
			available, exists := supported[c.Provider]
			if !exists {
				return registry, fmt.Errorf("resource catalog requires installed provider %s", c.Provider)
			}
			if !available {
				continue
			}
			data, err := p.ReadFile(c.Catalog, 2*1024*1024)
			if err != nil {
				return registry, err
			}
			var catalog sandboxresources.Catalog
			if err = core.DecodeJSON(data, &catalog); err != nil {
				return registry, err
			}
			if err = registry.Catalog.Merge(catalog, c.Provider); err != nil {
				return registry, err
			}
		}
	}
	for _, p := range packages {
		for _, c := range p.Manifest.Contributes.SandboxProviders {
			if !supported[c.ID] {
				continue
			}
			executable, err := p.Executable(c.Executable)
			if err != nil {
				return registry, err
			}
			client, err := start(ctx, executable, c.Args, config)
			if err != nil {
				return registry, fmt.Errorf("provider %s: %w", c.ID, err)
			}
			registry.Providers = append(registry.Providers, client)
			if client.Descriptor().ID != c.ID || client.Descriptor().HostOS != runtime.GOOS {
				return registry, errors.New("provider handshake identity differs from capability")
			}
			if c.DefaultShared {
				if registry.DefaultProvider != "" || !client.Descriptor().Capabilities.SharedExecution {
					return registry, errors.New("ambiguous or unsupported default shared provider")
				}
				registry.DefaultProvider = c.ID
			}
		}
	}
	return registry, nil
}
