// Package plugin loads the common installed package format. Capability consumers
// decide where and how to execute contributions; discovery starts no processes.
package plugin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/core"
)

type Package struct {
	Root     string
	Manifest core.PluginManifest
}

func Load(directory string) (Package, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return Package{}, err
	}
	p := Package{Root: root}
	data, err := p.ReadFile("plugin.json", 65536)
	if err != nil {
		return p, err
	}
	if err = core.DecodeJSON(data, &p.Manifest); err != nil {
		return p, err
	}
	return p, p.Manifest.Validate()
}

func Discover(directory string) ([]Package, error) {
	packages := []Package{}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return packages, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("plugin package links are forbidden")
		}
		if !entry.IsDir() {
			continue
		}
		p, err := Load(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", entry.Name(), err)
		}
		if seen[p.Manifest.ID] {
			return nil, errors.New("duplicate plugin ID")
		}
		seen[p.Manifest.ID] = true
		packages = append(packages, p)
	}
	return packages, nil
}

// Path confines all capability assets to regular package paths without symlinks.
func (p Package) Path(name string) (string, error) {
	if !core.SafeRelativePath(name) {
		return "", errors.New("invalid plugin package path")
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		return "", err
	}
	absolute := filepath.Join(root, filepath.FromSlash(name))
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	if canonical != absolute {
		return "", errors.New("plugin package paths must not use symlinks")
	}
	return absolute, nil
}
func (p Package) ReadFile(name string, limit int64) ([]byte, error) {
	path, err := p.Path(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid plugin package file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("plugin file exceeds limit")
	}
	return data, nil
}
func (p Package) Executable(name string) (string, error) {
	path, err := p.Path(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("plugin executable unavailable")
	}
	return path, nil
}

// ToolBundle projects only the selected tool capability and its text files.
// Only explicitly declared tool files enter transferable Task bundles.
func (p Package) ToolBundle() (core.PluginBundle, error) {
	b := core.PluginBundle{}
	if err := p.Manifest.Validate(); err != nil {
		return b, err
	}
	tools := p.Manifest.Contributes.Tools
	if tools == nil {
		return b, errors.New("plugin provides no tool capability")
	}
	b.Manifest = core.PluginManifest{SchemaVersion: p.Manifest.SchemaVersion, ID: p.Manifest.ID, Version: p.Manifest.Version, Contributes: core.PluginContributions{Tools: tools}}
	b.Files = map[string]string{}
	for _, name := range tools.Files {
		data, err := p.ReadFile(name, 512*1024)
		if err != nil {
			return b, err
		}
		if !utf8.Valid(data) {
			return b, errors.New("tool files must be UTF-8 text")
		}
		b.Files[name] = string(data)
	}
	var err error
	b.Digest, err = b.Hash()
	if err != nil {
		return b, err
	}
	return b, b.Validate()
}

// BackgroundIdentity pins the locally installed capability and its declared files.
func (p Package) BackgroundIdentity() (core.PluginIdentity, error) {
	identity := core.PluginIdentity{ID: p.Manifest.ID, Version: p.Manifest.Version}
	if p.Manifest.Contributes.Background == nil {
		return identity, errors.New("background capability unavailable")
	}
	files := map[string]string{}
	size := 0
	for _, name := range p.Manifest.Contributes.Background.Files {
		data, err := p.ReadFile(name, 512*1024)
		if err != nil {
			return identity, err
		}
		size += len(data)
		if size > 512*1024 {
			return identity, errors.New("background package too large")
		}
		files[name] = string(data)
	}
	var err error
	identity.Digest, err = core.HashJSON([]any{p.Manifest, files})
	return identity, err
}

// ModelProviderIdentity pins the Control-local definitions and adapter files.
// It does not authorize execution or add these assets to a Task bundle.
func (p Package) ModelProviderIdentity() (core.PluginIdentity, error) {
	identity := core.PluginIdentity{ID: p.Manifest.ID, Version: p.Manifest.Version}
	if err := p.Manifest.Validate(); err != nil {
		return identity, err
	}
	c := p.Manifest.Contributes.ModelProviders
	if c == nil {
		return identity, errors.New("model provider capability unavailable")
	}
	files := map[string]string{}
	size := 0
	if c.Adapter != nil {
		for _, name := range c.Adapter.Files {
			data, err := p.ReadFile(name, 512*1024)
			if err != nil {
				return identity, err
			}
			size += len(data)
			if size > 512*1024 || !utf8.Valid(data) {
				return identity, errors.New("invalid model provider adapter files")
			}
			files[name] = string(data)
		}
	}
	var err error
	identity.Digest, err = core.HashJSON([]any{p.Manifest, files})
	return identity, err
}

// MCPIdentity pins a local MCP contribution and all declared source files.
func (p Package) MCPIdentity() (core.PluginIdentity, error) {
	identity := core.PluginIdentity{ID: p.Manifest.ID, Version: p.Manifest.Version}
	c := p.Manifest.Contributes.MCP
	if c == nil {
		return identity, errors.New("MCP capability unavailable")
	}
	files := map[string]string{}
	size := 0
	for _, name := range c.Files {
		raw, err := p.ReadFile(name, 512*1024)
		if err != nil {
			return identity, err
		}
		size += len(raw)
		if size > 512*1024 || !utf8.Valid(raw) {
			return identity, errors.New("invalid MCP package files")
		}
		files[name] = string(raw)
	}
	var err error
	identity.Digest, err = core.HashJSON([]any{p.Manifest, files})
	return identity, err
}
