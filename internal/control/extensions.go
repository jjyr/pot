package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/storage"
	"github.com/jjyr/pot/plugins"
)

type Extension struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Source         string   `json:"source"`
	Location       string   `json:"location"`
	Version        string   `json:"version,omitempty"`
	Icon           string   `json:"icon,omitempty"`
	Enabled        bool     `json:"enabled"`
	Revision       int64    `json:"revision"`
	Origin         string   `json:"origin,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	Requirements   []string `json:"requirements,omitempty"`
	Background     bool     `json:"background,omitempty"`
	ModelProviders bool     `json:"model_providers,omitempty"`
	Manual         bool     `json:"manual,omitempty"`
}
type ExtensionCommand struct {
	Action   string `json:"action"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Digest   string `json:"digest"`
}

func extensionID(location string) string { value, _ := core.HashJSON(location); return value }
func extensionEnabled(ctx context.Context, db *sql.DB, kind, id string) (bool, error) {
	if db == nil {
		return true, nil
	}
	var enabled bool
	err := db.QueryRowContext(ctx, "SELECT enabled FROM extension_settings WHERE kind=? AND id=?", kind, id).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return enabled, err
}
func (s *Service) enabled(ctx context.Context, kind, id string) (bool, error) {
	return extensionEnabled(ctx, s.integrationDB, kind, id)
}
func discoverPlugins(roots ...string) ([]plugin.Package, error) {
	result := []plugin.Package{}
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		packages, err := plugin.Discover(root)
		if err != nil {
			return nil, err
		}
		for _, p := range packages {
			if seen[p.Manifest.ID] {
				return nil, fmt.Errorf("duplicate installed plugin %s", p.Manifest.ID)
			}
			seen[p.Manifest.ID] = true
			result = append(result, p)
		}
	}
	if !seen["standard-model-providers"] {
		var manifest core.PluginManifest
		if err := core.DecodeJSON(pluginassets.StandardModelProviders, &manifest); err != nil {
			return nil, err
		}
		if err := manifest.Validate(); err != nil {
			return nil, err
		}
		result = append(result, plugin.Package{Root: "builtin:standard-model-providers", Manifest: manifest})
	}
	return result, nil
}
func (s *Service) installedPlugins() ([]plugin.Package, error) {
	return discoverPlugins(s.options.PluginRoot, filepath.Join(s.options.AgentHome, "plugins"))
}
func (s *Service) extensionCatalog(ctx context.Context, owner string) ([]Extension, []string, error) {
	entries := []Extension{}
	diagnostics := []string{}
	packages, err := s.installedPlugins()
	if err != nil {
		return nil, nil, err
	}
	for _, p := range packages {
		source := "builtin"
		if filepath.Dir(p.Root) == filepath.Join(s.options.AgentHome, "plugins") {
			source = "user"
		}
		item := Extension{ID: p.Manifest.ID, Kind: "plugins", Name: p.Manifest.ID, Source: source, Location: p.Root, Version: p.Manifest.Version, Enabled: true}
		item.Icon, _ = p.Icon()
		if tools := p.Manifest.Contributes.Tools; tools != nil {
			item.Requirements = tools.Requirements
			for _, raw := range tools.Tools {
				var tool struct{ Name, Description string }
				if json.Unmarshal(raw, &tool) == nil {
					item.Tools = append(item.Tools, tool.Name)
					if item.Description == "" {
						item.Description = tool.Description
					}
				}
			}
		}
		if p.Manifest.Contributes.MCP != nil {
			item.Tools = append(item.Tools, "mcp_"+strings.ReplaceAll(p.Manifest.ID, "-", "_"))
		}
		item.Background = p.Manifest.Contributes.Background != nil
		if p.Manifest.Contributes.ModelProviders != nil {
			item.ModelProviders = true
		}
		entries = append(entries, item)
	}
	// Main's actual workspace supplies project context. Global management works
	// even before any assistant exists and never creates a conversation or turn.
	roots := []core.SkillRoot{}
	if core.Identifier(owner) {
		cwd, err := s.ExecutionWorkspace(owner)
		if err != nil {
			return nil, nil, err
		}
		_, project, err := s.instructions.prepare(s.options.AgentHome, cwd, "", true)
		if err != nil {
			return nil, nil, err
		}
		roots = project
	} else {
		roots = append(roots, core.SkillRoot{Path: filepath.Join(s.options.AgentHome, "skills"), Location: filepath.Join(s.options.AgentHome, "skills")})
	}
	if s.options.BuiltinSkills != "" {
		roots = append(roots, core.SkillRoot{Path: s.options.BuiltinSkills, Location: s.options.BuiltinSkills, Builtin: true})
	}
	reserved := []string{}
	if s.options.BuiltinSkills != "" {
		catalog, err := agent.DiscoverSkills(core.SkillResources{Roots: []core.SkillRoot{{Path: s.options.BuiltinSkills, Location: s.options.BuiltinSkills, Builtin: true}}})
		if err != nil {
			return nil, nil, err
		}
		for _, skill := range catalog.Skills {
			reserved = append(reserved, skill.Name)
		}
	}
	catalog, err := agent.DiscoverSkills(core.SkillResources{Roots: roots, ReservedNames: reserved})
	if err != nil {
		return nil, nil, err
	}
	diagnostics = append(diagnostics, catalog.Diagnostics...)
	for _, skill := range catalog.Skills {
		source := "project"
		if strings.HasPrefix(skill.Location, filepath.Join(s.options.AgentHome, "skills")+string(filepath.Separator)) {
			source = "user"
		}
		if s.options.BuiltinSkills != "" && strings.HasPrefix(skill.Location, s.options.BuiltinSkills+string(filepath.Separator)) {
			source = "builtin"
		}
		entries = append(entries, Extension{ID: extensionID(skill.Location), Kind: "skills", Name: skill.Name, Description: skill.Description, Source: source, Location: skill.Location, Enabled: true, Manual: skill.DisableModelInvocation})
	}
	for index := range entries {
		item := &entries[index]
		err := s.integrationDB.QueryRowContext(ctx, "SELECT enabled,revision,source FROM extension_settings WHERE kind=? AND id=?", item.Kind, item.ID).Scan(&item.Enabled, &item.Revision, &item.Origin)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
	}
	return entries, diagnostics, nil
}
func (s *Service) Extensions(ctx context.Context, owner string) (any, error) {
	entries, diagnostics, err := s.extensionCatalog(ctx, owner)
	if err != nil {
		return nil, err
	}
	servers, err := s.mcpServers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		servers[i].SecretRef = ""
	}
	return map[string]any{"items": entries, "mcp": servers, "diagnostics": diagnostics}, nil
}
func (s *Service) ExtensionContent(ctx context.Context, owner, id string) (any, error) {
	entries, _, err := s.extensionCatalog(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, item := range entries {
		if item.Kind == "skills" && item.ID == id {
			data, err := readRegular(item.Location, 1024*1024)
			if err != nil {
				return nil, err
			}
			return map[string]any{"content": string(data), "digest": extensionID(string(data))}, nil
		}
	}
	return nil, errors.New("extension unavailable")
}
func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid extension file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit+1))
}
func (s *Service) ManageExtension(ctx context.Context, owner string, c ExtensionCommand) (any, error) {
	if c.Kind != "plugins" && c.Kind != "skills" {
		return nil, errors.New("invalid extension kind")
	}
	var result any
	err := s.Machines.OwnerAction(func() error {
		s.extensionsMu.Lock()
		defer s.extensionsMu.Unlock()
		entries, _, err := s.extensionCatalog(ctx, owner)
		if err != nil {
			return err
		}
		if c.Action == "install" || c.Action == "create" {
			result, err = s.installExtension(ctx, c, entries)
			return err
		}
		var item *Extension
		for _, entry := range entries {
			if entry.Kind == c.Kind && entry.ID == c.ID {
				copy := entry
				item = &copy
				break
			}
		}
		if item == nil {
			return errors.New("extension unavailable")
		}
		if c.Revision != item.Revision {
			return errors.New("extension configuration changed")
		}
		switch c.Action {
		case "toggle":
			_, err = s.integrationDB.ExecContext(ctx, "INSERT INTO extension_settings(kind,id,enabled,revision,source) VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision", item.Kind, item.ID, c.Enabled, item.Revision+1, item.Origin)
		case "save":
			if item.Kind != "skills" || item.Source == "builtin" {
				return errors.New("extension is read only")
			}
			if !utf8.ValidString(c.Content) || len(c.Content) > 1024*1024 {
				return errors.New("invalid skill content")
			}
			previous, readErr := readRegular(item.Location, 1024*1024)
			if readErr != nil {
				return readErr
			}
			if c.Digest == "" || extensionID(string(previous)) != c.Digest {
				return errors.New("skill content changed")
			}
			// Validate the new frontmatter in isolation before replacing the existing file.
			stage, stageErr := os.MkdirTemp("", "pot-skill-check-")
			if stageErr != nil {
				return stageErr
			}
			defer os.RemoveAll(stage)
			if err = os.Mkdir(filepath.Join(stage, item.Name), 0700); err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(stage, item.Name, "SKILL.md"), []byte(c.Content), 0600); err != nil {
				return err
			}
			parsed, parseErr := agent.DiscoverSkills(core.SkillResources{Roots: []core.SkillRoot{{Path: stage, Location: stage}}})
			if parseErr != nil || len(parsed.Skills) != 1 || parsed.Skills[0].Name != item.Name {
				return errors.New("invalid skill metadata or renamed skill")
			}
			temp, tempErr := os.CreateTemp(filepath.Dir(item.Location), ".pot-skill-")
			if tempErr != nil {
				return tempErr
			}
			name := temp.Name()
			defer os.Remove(name)
			_, err = temp.WriteString(c.Content)
			err = errors.Join(err, temp.Close())
			if err == nil {
				err = os.Rename(name, item.Location)
			}
			if err == nil {
				_, err = s.integrationDB.ExecContext(ctx, "INSERT INTO extension_settings(kind,id,enabled,revision,source) VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET revision=excluded.revision", item.Kind, item.ID, item.Enabled, item.Revision+1, item.Origin)
				if err != nil {
					rollback, rollbackErr := os.CreateTemp(filepath.Dir(item.Location), ".pot-skill-rollback-")
					if rollbackErr == nil {
						_, rollbackErr = rollback.Write(previous)
						rollbackErr = errors.Join(rollbackErr, rollback.Close())
						if rollbackErr == nil {
							rollbackErr = os.Rename(rollback.Name(), item.Location)
						}
						_ = os.Remove(rollback.Name())
					}
					err = errors.Join(err, rollbackErr)
				}
			}
		case "remove":
			if item.Source != "user" {
				return errors.New("only user installations can be removed here")
			}
			path := item.Location
			if item.Kind == "skills" {
				path = filepath.Dir(path)
			}
			// Move to a private trash rather than deleting an extension irreversibly.
			trash := filepath.Join(s.options.AgentHome, "extension-trash")
			if err = os.MkdirAll(trash, 0700); err != nil {
				return err
			}
			dest := filepath.Join(trash, fmt.Sprintf("%s-%d", item.ID, storage.Now()))
			err = os.Rename(path, dest)
			if err == nil {
				_, err = s.integrationDB.ExecContext(ctx, "DELETE FROM extension_settings WHERE kind=? AND id=?", item.Kind, item.ID)
				if err != nil {
					err = errors.Join(err, os.Rename(dest, path))
				}
			}
		case "update":
			if item.Source != "user" || item.Origin == "" {
				return errors.New("installation source unavailable")
			}
			c.Source = item.Origin
			c.Name = item.Name
			result, err = s.installExtension(ctx, c, entries)
		default:
			return errors.New("unknown extension action")
		}
		if result == nil {
			result = map[string]bool{"saved": err == nil}
		}
		return err
	})
	if err == nil {
		s.Changes.Notify("extensions")
		s.Changes.Notify("skills")
		s.Changes.Notify("assistants")
	}
	return result, err
}
func (s *Service) installExtension(ctx context.Context, c ExtensionCommand, entries []Extension) (any, error) {
	parent := filepath.Join(s.options.AgentHome, c.Kind)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	name := c.Name
	source := c.Source
	if c.Action == "create" {
		if c.Kind != "skills" || !core.Identifier(name) || strings.Contains(name, "/") {
			return nil, errors.New("invalid skill name")
		}
		path := filepath.Join(stage, name)
		if err = os.Mkdir(path, 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(c.Content), 0600); err != nil {
			return nil, err
		}
	} else {
		if c.Kind == "skills" && strings.HasPrefix(source, "https://github.com/") {
			downloaded, cleanup, err := downloadSkill(ctx, source)
			if err != nil {
				return nil, err
			}
			defer cleanup()
			source = downloaded
		}
		if !filepath.IsAbs(source) {
			return nil, errors.New("installation requires an absolute local directory")
		}
		if c.Kind == "plugins" {
			p, err := plugin.Load(source)
			if err != nil {
				return nil, err
			}
			name = p.Manifest.ID
			target := filepath.Join(stage, name)
			if err = os.Mkdir(target, 0700); err != nil {
				return nil, err
			}
			files := []string{"plugin.json"}
			if p.Manifest.Contributes.Tools != nil {
				if _, err = p.ToolBundle(); err != nil {
					return nil, err
				}
				files = append(files, p.Manifest.Contributes.Tools.Files...)
			}
			if p.Manifest.Contributes.MCP != nil {
				if _, err = p.MCPIdentity(); err != nil {
					return nil, err
				}
				files = append(files, p.Manifest.Contributes.MCP.Files...)
			}
			if p.Manifest.Contributes.Background != nil {
				if _, err = p.BackgroundIdentity(); err != nil {
					return nil, err
				}
				files = append(files, p.Manifest.Contributes.Background.Files...)
			}
			if p.Manifest.Contributes.ModelProviders != nil {
				if _, err = p.ModelProviderIdentity(); err != nil {
					return nil, err
				}
				if adapter := p.Manifest.Contributes.ModelProviders.Adapter; adapter != nil {
					files = append(files, adapter.Files...)
				}
			}
			if name, _, iconErr := p.IconAsset(); iconErr == nil {
				files = append(files, name)
			}
			for _, name := range []string{"icon-NOTICE.txt", "icon-LICENSE.txt"} {
				if _, noticeErr := p.ReadFile(name, 64*1024); noticeErr == nil {
					files = append(files, name)
				}
			}
			for _, file := range files {
				data, err := p.ReadFile(file, 512*1024)
				if err != nil {
					return nil, err
				}
				dest := filepath.Join(target, file)
				if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
					return nil, err
				}
				if err = os.WriteFile(dest, data, 0600); err != nil {
					return nil, err
				}
			}
		} else {
			name = filepath.Base(source)
			if !core.Identifier(name) {
				return nil, errors.New("invalid skill directory")
			}
			if err = copySkillTree(source, filepath.Join(stage, name)); err != nil {
				return nil, err
			}
		}
	}
	if c.Action == "update" && c.Name != name {
		return nil, errors.New("extension identity changed at source")
	}
	if c.Kind == "plugins" {
		candidate, err := plugin.Load(filepath.Join(stage, name))
		if err != nil {
			return nil, err
		}
		servers, err := s.mcpServers(ctx)
		if err != nil {
			return nil, err
		}
		for _, toolName := range pluginModelToolNames(candidate) {
			for _, other := range entries {
				if other.Kind == "plugins" && other.ID != name && slices.Contains(other.Tools, toolName) {
					return nil, errors.New("plugin tool name already registered")
				}
			}
			for _, server := range servers {
				if toolName == "mcp_"+strings.ReplaceAll(server.ID, "-", "_") {
					return nil, errors.New("plugin tool name conflicts with MCP server")
				}
			}
		}
	}
	if strings.HasPrefix(name, "mcp-") {
		return nil, errors.New("reserved extension identity")
	}
	if c.Kind == "skills" {
		catalog, err := agent.DiscoverSkills(core.SkillResources{Roots: []core.SkillRoot{{Path: stage, Location: stage}}})
		if err != nil || len(catalog.Skills) != 1 || catalog.Skills[0].Name != name {
			return nil, errors.New("invalid skill bundle")
		}
	}
	for _, item := range entries {
		if item.Kind == c.Kind && item.Name == name && (c.Action != "update" || item.Source != "user") {
			return nil, errors.New("extension name already installed or reserved")
		}
	}
	dest := filepath.Join(parent, name)
	backup := ""
	if _, err = os.Lstat(dest); err == nil {
		if c.Action != "update" {
			return nil, errors.New("extension exists")
		}
		backup = stage + "-previous"
		if err = os.Rename(dest, backup); err != nil {
			return nil, err
		}
	}
	if err = os.Rename(filepath.Join(stage, name), dest); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return nil, err
	}
	id := name
	if c.Kind == "skills" {
		id = extensionID(filepath.Join(dest, "SKILL.md"))
	}
	revision := int64(1)
	enabled := true
	for _, item := range entries {
		if item.ID == id && item.Kind == c.Kind {
			revision = item.Revision + 1
			enabled = item.Enabled
		}
	}
	_, err = s.integrationDB.ExecContext(ctx, "INSERT INTO extension_settings(kind,id,enabled,revision,source) VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET revision=excluded.revision,source=excluded.source", c.Kind, id, enabled, revision, c.Source)
	if err != nil {
		_ = os.RemoveAll(dest)
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return nil, err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return map[string]any{"id": id, "saved": true}, nil
}
func copySkillTree(source, dest string) error {
	total, count := int64(0), 0
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("skill links forbidden")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if slices.Contains(strings.Split(rel, string(filepath.Separator)), ".git") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(dest, rel), 0700)
		}
		if !entry.Type().IsRegular() {
			return errors.New("skill special files forbidden")
		}
		count++
		if count > 2048 {
			return errors.New("skill file limit exceeded")
		}
		data, err := readRegular(path, 2*1024*1024)
		if err != nil {
			return err
		}
		total += int64(len(data))
		if total > 16*1024*1024 {
			return errors.New("skill size limit exceeded")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if info.Mode()&0111 != 0 {
			mode = 0700
		}
		return os.WriteFile(filepath.Join(dest, rel), data, mode)
	})
}
