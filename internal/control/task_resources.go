package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/agent"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/resource"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/tasks"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type taskResource struct {
	Config        core.ResolvedSpec `json:"config"`
	ReservedNames []string          `json:"reserved_names"`
	Roots         []taskSkillRoot   `json:"roots"`
}
type taskSkillRoot struct {
	Builtin bool              `json:"builtin"`
	Files   map[string]string `json:"files"`
}

func (s *Service) selectTask(ctx context.Context, owner string, r protocol.CreateTask) (tasks.Selection, error) {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return tasks.Selection{}, err
	}
	thread, err := c.Thread(ctx)
	if err != nil || thread == nil {
		return tasks.Selection{}, errors.New("assistant model is not configured")
	}
	spec := thread.Config
	if r.Profile != nil || r.Model != nil {
		profileID := spec.SourceProfileID
		if r.Profile != nil {
			profileID = *r.Profile
		}
		var chosen *core.Profile
		for _, profile := range s.Profiles.Snapshot().Profiles {
			if profile.ID == profileID {
				copy := profile
				chosen = &copy
				break
			}
		}
		if chosen == nil {
			return tasks.Selection{}, errors.New("Task Provider is unavailable")
		}
		model := chosen.Model
		if r.Profile == nil {
			model = spec.Model
		}
		if r.Model != nil {
			model = strings.TrimSpace(*r.Model)
		}
		spec, err = s.Profiles.ResolveAssistant(AssistantSelection{ProfileID: chosen.ID, ProfileRevision: chosen.Revision, Model: model})
		if err != nil {
			return tasks.Selection{}, err
		}
	}
	plugins := []string{}
	for name, plugin := range spec.Params.Agent.Plugins {
		if plugin.Enabled {
			plugins = append(plugins, name)
		}
	}
	sort.Strings(plugins)
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	if err = spec.Rehash(); err != nil {
		return tasks.Selection{}, err
	}
	return tasks.Selection{Config: spec, Plugins: plugins}, nil
}
func (s *Service) currentTaskResource(ctx context.Context, l runner.Launch) (taskResource, error) {
	body, _ := json.Marshal(l.Config)
	var spec core.ResolvedSpec
	if err := core.DecodeJSON(body, &spec); err != nil {
		return taskResource{}, err
	}
	var profile *core.Profile
	for _, candidate := range s.Profiles.Snapshot().Profiles {
		if candidate.ID == l.Config.SourceProfileID {
			copy := candidate
			profile = &copy
			break
		}
	}
	if profile == nil {
		return taskResource{}, errors.New("Task source Provider is unavailable for resource synchronization")
	}
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	for _, name := range l.Plugins {
		plugin, exists := profile.Params.Agent.Plugins[name]
		if !exists || !plugin.Enabled {
			return taskResource{}, fmt.Errorf("selected plugin %s is unavailable or disabled", name)
		}
		directory := filepath.Join(s.options.PluginRoot, name)
		if plugin.Source != nil {
			directory = *plugin.Source
		}
		bundle, err := resource.LoadPlugin(directory)
		if err != nil {
			return taskResource{}, err
		}
		if bundle.Manifest.ID != name {
			return taskResource{}, errors.New("selected plugin identity changed")
		}
		plugin.Bundle = &bundle
		plugin.Source = nil
		spec.Params.Agent.Plugins[name] = plugin
	}
	if err := spec.Rehash(); err != nil {
		return taskResource{}, err
	}
	// Only currently selected Control global skills cross the execution boundary. Worker
	// instructions and project skills are discovered at the fixed destination.
	roots := []core.SkillRoot{{Path: filepath.Join(s.options.AgentHome, "skills")}}
	if s.options.BuiltinSkills != "" {
		roots = append(roots, core.SkillRoot{Path: s.options.BuiltinSkills, Builtin: true})
	}
	reserved := []string{}
	for i := range roots {
		roots[i].Location = roots[i].Path
		if roots[i].Builtin {
			catalog, err := agent.DiscoverSkills(core.SkillResources{Roots: []core.SkillRoot{roots[i]}})
			if err != nil {
				return taskResource{}, err
			}
			for _, skill := range catalog.Skills {
				reserved = append(reserved, skill.Name)
			}
		}
	}
	selection := []string{}
	if l.Skills != nil {
		selection = append(selection, (*l.Skills)...)
	}
	catalog, err := agent.DiscoverSkills(core.SkillResources{Roots: roots, ReservedNames: reserved, Selection: &selection})
	if err != nil {
		return taskResource{}, err
	}
	selectedPaths := map[string]bool{}
	for _, skill := range catalog.Skills {
		selectedPaths[filepath.Dir(skill.Location)] = true
	}
	resources := taskResource{Config: spec, ReservedNames: reserved, Roots: []taskSkillRoot{}}
	total := 0
	for _, root := range roots {
		tree := taskSkillRoot{Builtin: root.Builtin, Files: map[string]string{}}
		info, err := os.Lstat(root.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return taskResource{}, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return taskResource{}, errors.New("skill source must be a directory without links")
		}
		fs, err := os.OpenRoot(root.Path)
		if err != nil {
			return taskResource{}, err
		}
		var walk func(string, int) error
		walk = func(dir string, depth int) error {
			if depth > 32 {
				return errors.New("skill depth exceeds limit")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			f, err := fs.Open(dir)
			if err != nil {
				return err
			}
			children, err := f.ReadDir(-1)
			f.Close()
			if err != nil {
				return err
			}
			for _, child := range children {
				candidate := filepath.Join(root.Path, dir, child.Name())
				included := false
				for selected := range selectedPaths {
					if candidate == selected || strings.HasPrefix(selected, candidate+string(filepath.Separator)) || strings.HasPrefix(candidate, selected+string(filepath.Separator)) {
						included = true
						break
					}
				}
				if !included {
					continue
				}
				if strings.HasPrefix(child.Name(), ".") {
					continue
				}
				path := filepath.Join(dir, child.Name())
				if child.Type()&os.ModeSymlink != 0 {
					return errors.New("skill resource links forbidden")
				}
				if child.IsDir() {
					if err := walk(path, depth+1); err != nil {
						return err
					}
					continue
				}
				if !child.Type().IsRegular() {
					return errors.New("skill resource must be regular")
				}
				f, err := fs.Open(path)
				if err != nil {
					return err
				}
				data, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
				f.Close()
				if err != nil {
					return err
				}
				total += len(data)
				if len(data) > 2*1024*1024 || total > 16*1024*1024 || len(tree.Files) >= 2048 {
					return errors.New("task skill resource limit exceeded")
				}
				tree.Files[filepath.ToSlash(path)] = base64.RawStdEncoding.EncodeToString(data)
			}
			return nil
		}
		err = walk(".", 0)
		fs.Close()
		if err != nil {
			return taskResource{}, err
		}
		if len(tree.Files) > 0 {
			resources.Roots = append(resources.Roots, tree)
		}
	}
	return resources, nil
}

func (s *Service) installTaskSkills(ctx context.Context, r taskResource, owner, revision string, target core.ExecutionTarget) ([]core.SkillRoot, error) {
	roots := []core.SkillRoot{}
	hostRoot := s.options.Workspace
	host := filepath.Join(hostRoot, "tasks", owner)
	guest := host
	if target.Target.Kind == "sandbox" {
		if s.options.Sandboxes == nil {
			return nil, errors.New("Sandbox manager unavailable")
		}
		w, err := s.options.Sandboxes.WorkspacePaths(ctx, target, owner, true)
		if err != nil {
			return nil, err
		}
		hostRoot, host, guest = w.HostRoot, w.HostCWD, w.GuestCWD
	}
	anchor, err := os.OpenRoot(hostRoot)
	if err != nil {
		return nil, err
	}
	defer anchor.Close()
	relative := filepath.Join("tasks", owner, ".pot/control-resources", revision)
	if err = anchor.RemoveAll(relative); err != nil {
		return nil, err
	}
	for i, tree := range r.Roots {
		directory := filepath.Join(host, ".pot/control-resources", revision, fmt.Sprint(i))
		relativeRoot := filepath.Join(relative, fmt.Sprint(i))
		if err := anchor.MkdirAll(relativeRoot, 0700); err != nil {
			return nil, err
		}
		fs, err := anchor.OpenRoot(relativeRoot)
		if err != nil {
			return nil, err
		}
		for name, encoded := range tree.Files {
			if !core.SafeRelativePath(name) {
				fs.Close()
				return nil, errors.New("invalid immutable skill path")
			}
			data, err := base64.RawStdEncoding.DecodeString(encoded)
			if err != nil {
				fs.Close()
				return nil, err
			}
			parent := filepath.Dir(name)
			if err = fs.MkdirAll(parent, 0700); err != nil {
				fs.Close()
				return nil, err
			}
			f, err := fs.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
			if err != nil {
				fs.Close()
				return nil, err
			}
			_, err = f.Write(data)
			closeErr := f.Close()
			if err = errors.Join(err, closeErr); err != nil {
				fs.Close()
				return nil, err
			}
		}
		fs.Close()
		roots = append(roots, core.SkillRoot{Path: directory, Location: filepath.ToSlash(filepath.Join(guest, ".pot/control-resources", revision, fmt.Sprint(i))), Builtin: tree.Builtin})
	}
	return roots, ctx.Err()
}

func validateTaskResource(l runner.Launch, r taskResource) error {
	if err := r.Config.Validate(); err != nil {
		return err
	}
	check := r.Config
	check.Params.Agent.Plugins = map[string]core.PluginConfig{}
	if err := check.Rehash(); err != nil {
		return err
	}
	if check.SpecHash != l.Config.SpecHash {
		return errors.New("resource response changed fixed model configuration")
	}
	if len(r.Config.Params.Agent.Plugins) != len(l.Plugins) {
		return errors.New("resource response changed selected plugin set")
	}
	for _, name := range l.Plugins {
		plugin, ok := r.Config.Params.Agent.Plugins[name]
		if !ok || !plugin.Enabled || plugin.Bundle == nil || plugin.Bundle.Manifest.ID != name {
			return errors.New("selected plugin resource missing")
		}
		if err := plugin.Bundle.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) readPreparedTaskResources(ctx context.Context, l runner.Launch) (taskResource, string, error) {
	raw, revision, err := s.RunnerJournal.PreparedResources(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return taskResource{}, "", err
	}
	if raw == nil {
		return taskResource{}, "", errors.New("original Runner resources not prepared")
	}
	var resources taskResource
	if err = core.DecodeJSON(raw, &resources); err != nil {
		return resources, "", err
	}
	return resources, revision, validateTaskResource(l, resources)
}
func (s *Service) prepareTaskResources(ctx context.Context, l runner.Launch, fetch func() (taskResource, error)) (taskResource, string, error) {
	raw, _, err := s.RunnerJournal.PreparedResources(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return taskResource{}, "", err
	}
	if raw == nil {
		resources, err := fetch()
		if err != nil {
			return taskResource{}, "", err
		}
		if err = validateTaskResource(l, resources); err != nil {
			return taskResource{}, "", err
		}
		raw, err = json.Marshal(resources)
		if err != nil {
			return taskResource{}, "", err
		}
		if _, _, err = s.RunnerJournal.PinResources(ctx, l.ControlID, l.TaskID, l.RunID, raw); err != nil {
			return taskResource{}, "", err
		}
	}
	return s.readPreparedTaskResources(ctx, l)
}
