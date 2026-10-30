package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	pluginpkg "github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/protocol"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type taskResource struct {
	Config        core.ResolvedSpec `json:"config"`
	ReservedNames []string          `json:"reserved_names"`
	Roots         []core.SkillRoot  `json:"roots"`
}

func (s *Service) selectTask(ctx context.Context, owner string, r protocol.CreateTask) (tasks.Selection, error) {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return tasks.Selection{}, err
	}
	var spec core.ResolvedSpec
	forced, err := s.experimentTaskConfig(ctx, owner, r.RequestID)
	if err != nil {
		return tasks.Selection{}, err
	}
	if forced != nil {
		spec = *forced
	} else if r.Profile != nil || r.Model != nil {
		thread, err := c.Thread(ctx)
		if err != nil || thread == nil {
			return tasks.Selection{}, errors.New("assistant model is not configured")
		}
		spec = thread.Config
		profileID := spec.SourceProfileID
		if r.Profile != nil {
			profileID = *r.Profile
		}
		var chosen *core.Profile
		for _, profile := range s.Profiles.savedSnapshot().Profiles {
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
	} else {
		state, err := c.Configuration(ctx)
		if err != nil {
			return tasks.Selection{}, err
		}
		if state.Config == nil {
			return tasks.Selection{}, errors.New("assistant model is not configured")
		}
		spec = *state.Config
		if len(state.Intent) > 0 && string(state.Intent) != "null" {
			var selection AssistantSelection
			if err := core.DecodeJSON(state.Intent, &selection); err != nil {
				return tasks.Selection{}, err
			}
			if selection.TaskDefault != nil {
				spec, err = s.resolveTaskDefault(ctx, *selection.TaskDefault)
				if err != nil {
					return tasks.Selection{}, err
				}
			}
		}
	}
	if err := s.Profiles.sourceAvailable(ctx, spec.Provider); err != nil {
		return tasks.Selection{}, err
	}
	settings, err := s.Profiles.defaultAgentSettings()
	if err != nil {
		return tasks.Selection{}, err
	}
	spec.Params.Agent.Plugins = settings.Plugins
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
func (s *Service) currentTaskResource(ctx context.Context, l tasks.Launch) (taskResource, error) {
	body, _ := json.Marshal(l.Config)
	var spec core.ResolvedSpec
	if err := core.DecodeJSON(body, &spec); err != nil {
		return taskResource{}, err
	}
	var profile *core.Profile
	for _, candidate := range s.Profiles.savedSnapshot().Profiles {
		if candidate.ID == l.Config.SourceProfileID {
			copy := candidate
			profile = &copy
			break
		}
	}
	if profile == nil {
		return taskResource{}, errors.New("Task source Provider is unavailable for local resource preparation")
	}
	if err := s.Profiles.sourceAvailable(ctx, spec.Provider); err != nil {
		return taskResource{}, err
	}
	settings, err := s.Profiles.defaultAgentSettings()
	if err != nil {
		return taskResource{}, err
	}
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	for _, name := range l.Plugins {
		plugin, exists := settings.Plugins[name]
		if !exists || !plugin.Enabled {
			return taskResource{}, fmt.Errorf("selected plugin %s is unavailable or disabled", name)
		}
		if strings.HasPrefix(name, "mcp-") {
			plugin.Source = nil
			spec.Params.Agent.Plugins[name] = plugin
			continue
		}
		directory := filepath.Join(s.options.PluginRoot, name)
		if plugin.Source != nil {
			directory = *plugin.Source
		}
		bundle, err := pluginpkg.LoadPlugin(directory)
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
	// Freeze selected node-wide Skills. Worker instructions and project Skills
	// are discovered in the execution workspace.
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
		enabled, err := s.enabled(ctx, "skills", extensionID(skill.Location))
		if err != nil {
			return taskResource{}, err
		}
		if !enabled {
			return taskResource{}, fmt.Errorf("selected skill %s is disabled", skill.Name)
		}
		selectedPaths[filepath.Dir(skill.Location)] = true
	}
	resources := taskResource{Config: spec, ReservedNames: reserved, Roots: []core.SkillRoot{}}
	for _, root := range roots {
		names := []string{}
		for selected := range selectedPaths {
			if filepath.Dir(selected) == root.Path {
				names = append(names, filepath.Base(selected))
			}
		}
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		frozen, err := snapshotSkills(ctx, root, names, filepath.Join(s.options.Root, "cache", "resources", "skills"))
		if err != nil {
			return taskResource{}, err
		}
		resources.Roots = append(resources.Roots, frozen)
	}
	return resources, nil
}

// snapshotSkills freezes only the selected local directories. File contents stay
// as files, with executable modes intact; there is no resource wire format.
func snapshotSkills(ctx context.Context, root core.SkillRoot, names []string, cache string) (core.SkillRoot, error) {
	if err := os.MkdirAll(cache, 0700); err != nil {
		return core.SkillRoot{}, err
	}
	stage, err := os.MkdirTemp(cache, ".prepare-")
	if err != nil {
		return core.SkillRoot{}, err
	}
	defer os.RemoveAll(stage)
	evidence := map[string]string{}
	total := 0
	for _, name := range names {
		source := filepath.Join(root.Path, name)
		err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, e := filepath.Rel(root.Path, path)
			if e != nil {
				return e
			}
			if strings.Count(filepath.ToSlash(rel), "/") > 32 {
				return errors.New("skill resource depth limit exceeded")
			}
			if strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("skill resource links forbidden")
			}
			if entry.IsDir() {
				return os.MkdirAll(filepath.Join(stage, rel), 0700)
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return errors.New("skill resource must be regular")
			}
			if info.Size() > 2*1024*1024 || len(evidence) >= 2048 {
				return errors.New("task skill resource limit exceeded")
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			total += len(data)
			if total > 16*1024*1024 {
				return errors.New("task skill resource limit exceeded")
			}
			hash, e := core.HashJSON([]any{data, uint32(info.Mode().Perm())})
			if e != nil {
				return e
			}
			evidence[filepath.ToSlash(rel)] = hash
			dest := filepath.Join(stage, rel)
			if err := os.WriteFile(dest, data, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(dest, info.Mode().Perm())
		})
		if err != nil {
			return core.SkillRoot{}, err
		}
	}
	digest, err := core.HashJSON(evidence)
	if err != nil {
		return core.SkillRoot{}, err
	}
	dest := filepath.Join(cache, digest)
	if err = os.MkdirAll(cache, 0700); err != nil {
		return core.SkillRoot{}, err
	}
	if _, err = os.Stat(dest); os.IsNotExist(err) {
		if err = os.Rename(stage, dest); err != nil {
			return core.SkillRoot{}, err
		}
	} else if err != nil {
		return core.SkillRoot{}, err
	}
	return core.SkillRoot{Path: dest, Location: dest, Builtin: root.Builtin}, nil
}

func validateTaskResource(l tasks.Launch, r taskResource) error {
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
func (s *Service) readPreparedTaskResources(ctx context.Context, l tasks.Launch) (taskResource, string, error) {
	raw, revision, err := s.RunJournal.PreparedResources(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return taskResource{}, "", err
	}
	if raw == nil {
		return taskResource{}, "", errors.New("original task resources not prepared")
	}
	var resources taskResource
	if err = core.DecodeJSON(raw, &resources); err != nil {
		return resources, "", err
	}
	return resources, revision, validateTaskResource(l, resources)
}
func (s *Service) prepareTaskResources(ctx context.Context, l tasks.Launch) (taskResource, string, error) {
	raw, _, err := s.RunJournal.PreparedResources(ctx, l.ControlID, l.TaskID, l.RunID)
	if err != nil {
		return taskResource{}, "", err
	}
	if raw == nil {
		resources, err := s.currentTaskResource(ctx, l)
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
		if err = s.Profiles.sourceAvailable(ctx, l.Config.Provider); err != nil {
			return taskResource{}, "", err
		}
		if _, _, err = s.RunJournal.PinResources(ctx, l.ControlID, l.TaskID, l.RunID, raw); err != nil {
			return taskResource{}, "", err
		}
	}
	return s.readPreparedTaskResources(ctx, l)
}
