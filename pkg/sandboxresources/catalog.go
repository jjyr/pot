// Package sandboxresources prepares provider-declared immutable resource bundles.
// Image formats, bootstrap contracts and defaults belong to provider packages.
package sandboxresources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/resource"
	"github.com/jjyr/pot/pkg/sandbox"
)

type Bundle struct {
	Provider     string            `json:"provider"`
	Architecture string            `json:"architecture"`
	Repository   string            `json:"repository"`
	Resources    map[string]string `json:"resources"`
	Metadata     json.RawMessage   `json:"metadata"`
}
type Catalog struct {
	SchemaVersion uint32                       `json:"schema_version"`
	Defaults      map[string]map[string]string `json:"defaults"`
	Resources     map[string]resource.Resource `json:"resources"`
	Bundles       map[string]Bundle            `json:"bundles"`
}

func Empty() Catalog {
	return Catalog{SchemaVersion: 3, Defaults: map[string]map[string]string{}, Resources: map[string]resource.Resource{}, Bundles: map[string]Bundle{}}
}
func (c Catalog) DefaultBundle(provider string) string { return c.Defaults[provider][runtime.GOARCH] }

// RequiresResources also covers providers whose bundles must be selected
// explicitly. Lack of a default must never bypass preparation or consent.
func (c Catalog) RequiresResources(provider string) bool {
	for _, bundle := range c.Bundles {
		if bundle.Provider == provider {
			return true
		}
	}
	return false
}
func (c Catalog) Validate() error {
	if c.SchemaVersion != 3 {
		return errors.New("unsupported provider resource catalog schema")
	}
	for id, r := range c.Resources {
		if !core.Identifier(id) {
			return errors.New("invalid resource ID")
		}
		if err := r.Validate(); err != nil {
			return err
		}
	}
	for id, b := range c.Bundles {
		u, err := url.Parse(b.Repository)
		if !core.Identifier(id) || !core.Identifier(b.Provider) || !core.Identifier(b.Architecture) || len(b.Resources) == 0 || len(b.Resources) > 32 || !json.Valid(b.Metadata) || len(b.Metadata) > 65536 || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid provider resource bundle %q", id)
		}
		for name, rid := range b.Resources {
			if !core.Identifier(name) {
				return errors.New("invalid resource role")
			}
			if _, ok := c.Resources[rid]; !ok {
				return errors.New("bundle resource is missing")
			}
		}
	}
	for provider, arches := range c.Defaults {
		if !core.Identifier(provider) {
			return errors.New("invalid default provider")
		}
		for arch, id := range arches {
			b, ok := c.Bundles[id]
			if !ok || b.Provider != provider || b.Architecture != arch {
				return errors.New("invalid provider architecture default")
			}
		}
	}
	return nil
}

// Merge rejects collisions rather than letting discovery order replace resources.
func (c *Catalog) Merge(other Catalog, provider string) error {
	if err := other.Validate(); err != nil {
		return err
	}
	for id := range other.Defaults {
		if id != provider {
			return errors.New("plugin declares another provider's default")
		}
	}
	for id, b := range other.Bundles {
		if b.Provider != provider {
			return errors.New("plugin declares another provider's bundle")
		}
		if _, ok := c.Bundles[id]; ok {
			return errors.New("duplicate bundle ID: " + id)
		}
	}
	for id := range other.Resources {
		if _, ok := c.Resources[id]; ok {
			return errors.New("duplicate resource ID: " + id)
		}
	}
	if _, ok := c.Defaults[provider]; ok {
		return errors.New("duplicate provider resource defaults")
	}
	for id, b := range other.Bundles {
		c.Bundles[id] = b
	}
	for id, r := range other.Resources {
		c.Resources[id] = r
	}
	if defaults, ok := other.Defaults[provider]; ok {
		c.Defaults[provider] = defaults
	}
	return c.Validate()
}
func (b Bundle) ResourceIDs() []string {
	set := map[string]bool{}
	for _, id := range b.Resources {
		set[id] = true
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (c Catalog) Import(ctx context.Context, store *resource.Store, bundle, directory string) (sandbox.PreparedResources, error) {
	return c.prepare(ctx, bundle, func(id string) (resource.Verified, error) {
		r := c.Resources[id]
		return store.Import(ctx, r, filepath.Join(directory, r.Filename))
	})
}
func (c Catalog) Prepare(ctx context.Context, store *resource.Store, bundle string, emit func(string, resource.Progress)) (sandbox.PreparedResources, error) {
	return c.prepare(ctx, bundle, func(id string) (resource.Verified, error) {
		return store.Prepare(ctx, c.Resources[id], c.Bundles[bundle].Repository, func(p resource.Progress) {
			if emit != nil {
				emit(id, p)
			}
		})
	})
}
func (c Catalog) Cached(ctx context.Context, store *resource.Store, bundle string) (sandbox.PreparedResources, error) {
	return c.prepare(ctx, bundle, func(id string) (resource.Verified, error) {
		v, ok, err := store.Cached(ctx, c.Resources[id])
		if err == nil && !ok {
			err = errors.New("Sandbox resources unavailable; explicitly prepare or import resources")
		}
		return v, err
	})
}
func (c Catalog) prepare(ctx context.Context, id string, load func(string) (resource.Verified, error)) (sandbox.PreparedResources, error) {
	if err := c.Validate(); err != nil {
		return sandbox.PreparedResources{}, err
	}
	b, ok := c.Bundles[id]
	if !ok {
		return sandbox.PreparedResources{}, errors.New("unknown resource bundle")
	}
	if b.Architecture != runtime.GOARCH {
		return sandbox.PreparedResources{}, errors.New("bundle does not match Runtime architecture")
	}
	out := sandbox.PreparedResources{BundleID: id, Provider: b.Provider, Architecture: b.Architecture, Metadata: append(json.RawMessage(nil), b.Metadata...), Paths: map[string]string{}}
	paths := map[string]string{}
	for _, rid := range b.ResourceIDs() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		v, err := load(rid)
		if err != nil {
			return out, err
		}
		paths[rid] = v.Path()
	}
	for role, rid := range b.Resources {
		out.Paths[role] = paths[rid]
	}
	return out, nil
}
