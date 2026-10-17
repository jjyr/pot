package providerplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxresources"
)

type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	HostOS        []string `json:"host_os"`
	Executable    string   `json:"executable"`
	Args          []string `json:"args,omitempty"`
	Catalog       string   `json:"catalog,omitempty"`
	DefaultShared bool     `json:"default_shared"`
}
type Registry struct {
	Providers       []sandbox.Provider
	Catalog         sandboxresources.Catalog
	DefaultProvider string
}

// Discover loads only owner-installed local packages. It starts provider hosts
// for a bounded handshake, but never prepares images, engines or guests.
func Discover(ctx context.Context, directory string, config Config) (registry Registry, result error) {
	registry.Catalog = sandboxresources.Empty()
	registry.Providers = []sandbox.Provider{}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return registry, err
	}
	directory = absolute
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	defer func() {
		if result != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, p := range registry.Providers {
				result = errors.Join(result, p.Shutdown(shutdown))
			}
		}
	}()
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 {
				return registry, errors.New("provider package links are forbidden")
			}
			continue
		}
		packageRoot := filepath.Join(directory, entry.Name())
		data, err := packageFile(packageRoot, "provider.json", 65536)
		if err != nil {
			return registry, err
		}
		var m Manifest
		if err = core.DecodeJSON(data, &m); err != nil {
			return registry, err
		}
		if m.SchemaVersion != Version || !core.Identifier(m.ID) || !core.Identifier(m.Version) || len(m.HostOS) == 0 || len(m.Args) > 16 || !core.SafeRelativePath(m.Executable) {
			return registry, errors.New("invalid provider manifest")
		}
		if !slices.Contains(m.HostOS, runtime.GOOS) {
			continue
		}
		if seen[m.ID] {
			return registry, errors.New("duplicate provider plugin ID")
		}
		seen[m.ID] = true
		if m.Catalog != "" {
			data, err = packageFile(packageRoot, m.Catalog, 2*1024*1024)
			if err != nil {
				return registry, err
			}
			var catalog sandboxresources.Catalog
			if err = core.DecodeJSON(data, &catalog); err != nil {
				return registry, err
			}
			if err = registry.Catalog.Merge(catalog, m.ID); err != nil {
				return registry, err
			}
		}
		executable, err := packagePath(packageRoot, m.Executable)
		if err != nil {
			return registry, err
		}
		stat, err := os.Lstat(executable)
		if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0111 == 0 {
			return registry, fmt.Errorf("provider %s executable is missing or not executable", m.ID)
		}
		client, err := start(ctx, executable, m.Args, config)
		if err != nil {
			return registry, fmt.Errorf("provider %s: %w", m.ID, err)
		}
		registry.Providers = append(registry.Providers, client)
		if client.Descriptor().ID != m.ID || client.Descriptor().HostOS != runtime.GOOS {
			return registry, errors.New("provider handshake identity differs from manifest")
		}
		if m.DefaultShared {
			if registry.DefaultProvider != "" || !client.Descriptor().Capabilities.SharedExecution {
				return registry, errors.New("ambiguous or unsupported default shared provider")
			}
			registry.DefaultProvider = m.ID
		}
	}
	return registry, nil
}
func packagePath(root, name string) (string, error) {
	if !core.SafeRelativePath(name) {
		return "", errors.New("invalid provider package path")
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(canonicalRoot, filepath.FromSlash(name))
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if canonical != absolute {
		return "", errors.New("provider package paths must not use symlinks")
	}
	return path, nil
}
func packageFile(root, name string, limit int64) ([]byte, error) {
	path, err := packagePath(root, name)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid provider package file")
	}
	return os.ReadFile(path)
}
