// Package sandboxresources selects embedded immutable resource bundles and binds
// only verified paths to Sandbox providers. Catalog updates require a rebuild.
package sandboxresources

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/resource"
	"github.com/jjyr/pot/internal/sandbox"
	"github.com/jjyr/pot/resources"
	"path/filepath"
	"regexp"
	"runtime"
)

type Bundle struct {
	Image            string `json:"image"`
	ImageReference   string `json:"image_reference"`
	ManifestDigest   string `json:"manifest_digest"`
	BootstrapVersion string `json:"bootstrap_version"`
	OS               string `json:"os"`
	Architecture     string `json:"architecture"`
}
type Catalog struct {
	SchemaVersion       uint32                       `json:"schema_version"`
	DefaultLinuxBundles map[string]string            `json:"default_linux_bundles"`
	Resources           map[string]resource.Resource `json:"resources"`
	Bundles             map[string]Bundle            `json:"bundles"`
}

var manifestDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// DefaultBundle selects the immutable image for this Runtime's native CPU.
func (c Catalog) DefaultBundle() string { return c.DefaultLinuxBundles[runtime.GOARCH] }

func Embedded() (Catalog, error) {
	var c Catalog
	err := core.DecodeJSON(resources.SandboxCatalog, &c)
	if err != nil {
		return c, err
	}
	return c, c.Validate()
}
func (c Catalog) Validate() error {
	if c.SchemaVersion != 2 || len(c.DefaultLinuxBundles) == 0 {
		return errors.New("invalid image resource catalog")
	}
	for _, r := range c.Resources {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	for _, b := range c.Bundles {
		if b.OS != "linux" || (b.Architecture != "arm64" && b.Architecture != "amd64") || b.BootstrapVersion != "pot-executor-v2" || b.ImageReference == "" || !manifestDigest.MatchString(b.ManifestDigest) {
			return errors.New("unsupported sandbox image bundle")
		}
		image, ok := c.Resources[b.Image]
		if !ok || image.Format != resource.OCITarGzip {
			return errors.New("bundle requires an OCI image resource")
		}
	}
	for arch, id := range c.DefaultLinuxBundles {
		b, ok := c.Bundles[id]
		if !ok || b.Architecture != arch {
			return errors.New("invalid architecture default")
		}
	}
	return nil
}

// Import accepts an explicit owner-provided offline export, with catalog hash
// verification of every byte. Existing user images and stores are never adopted.
func (c Catalog) Import(ctx context.Context, store *resource.Store, bundle, directory string) (sandbox.PreparedResources, error) {
	return c.prepare(ctx, store, bundle, func(id string) (resource.Verified, error) {
		r := c.Resources[id]
		return store.Import(ctx, r, filepath.Join(directory, r.Filename))
	})
}
func (c Catalog) Prepare(ctx context.Context, store *resource.Store, bundle, repository string, emit func(string, resource.Progress)) (sandbox.PreparedResources, error) {
	return c.prepare(ctx, store, bundle, func(id string) (resource.Verified, error) {
		return store.Prepare(ctx, c.Resources[id], repository, func(p resource.Progress) {
			if emit != nil {
				emit(id, p)
			}
		})
	})
}
func (c Catalog) Cached(ctx context.Context, store *resource.Store, bundle string) (sandbox.PreparedResources, error) {
	return c.prepare(ctx, store, bundle, func(id string) (resource.Verified, error) {
		v, ok, err := store.Cached(ctx, c.Resources[id])
		if err == nil && !ok {
			err = errors.New("Sandbox resources unavailable; explicitly prepare or import resources")
		}
		return v, err
	})
}
func (c Catalog) prepare(ctx context.Context, store *resource.Store, id string, load func(string) (resource.Verified, error)) (sandbox.PreparedResources, error) {
	if err := c.Validate(); err != nil {
		return sandbox.PreparedResources{}, err
	}
	if id == "" {
		id = c.DefaultBundle()
	}
	bundle, ok := c.Bundles[id]
	if !ok {
		return sandbox.PreparedResources{}, errors.New("unknown resource bundle")
	}
	if bundle.Architecture != runtime.GOARCH {
		return sandbox.PreparedResources{}, errors.New("Sandbox image does not match this Runtime architecture")
	}
	result := sandbox.PreparedResources{BundleID: id, ImageReference: bundle.ImageReference, ManifestDigest: bundle.ManifestDigest, Architecture: bundle.Architecture}
	for _, item := range []struct {
		id   string
		dest *string
	}{{bundle.Image, &result.Image}} {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		v, err := load(item.id)
		if err != nil {
			return result, err
		}
		*item.dest = v.Path()
	}
	return result, nil
}
