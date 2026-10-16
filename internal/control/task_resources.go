package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/tasks"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type taskResource struct {
	Config core.ResolvedSpec `json:"config"`
	Roots  []taskSkillRoot   `json:"roots"`
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
	// Only immutable Control global skills cross the execution boundary. Worker
	// instructions and project skills are discovered at the fixed destination.
	roots := []core.SkillRoot{{Path: filepath.Join(s.options.AgentHome, "skills")}}
	if s.options.BuiltinSkills != "" {
		roots = append(roots, core.SkillRoot{Path: s.options.BuiltinSkills, Builtin: true})
	}
	enabled := map[string]core.PluginConfig{}
	for name, p := range spec.Params.Agent.Plugins {
		if p.Enabled {
			enabled[name] = p
		}
	}
	spec.Params.Agent.Plugins = enabled
	if err = spec.Rehash(); err != nil {
		return tasks.Selection{}, err
	}
	resources := taskResource{Config: spec, Roots: []taskSkillRoot{}}
	total := 0
	for _, root := range roots {
		tree := taskSkillRoot{Builtin: root.Builtin, Files: map[string]string{}}
		info, err := os.Lstat(root.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return tasks.Selection{}, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return tasks.Selection{}, errors.New("skill source must be a directory without links")
		}
		fs, err := os.OpenRoot(root.Path)
		if err != nil {
			return tasks.Selection{}, err
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
			return tasks.Selection{}, err
		}
		resources.Roots = append(resources.Roots, tree)
	}
	revision, err := core.HashJSON(resources)
	if err != nil {
		return tasks.Selection{}, err
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		return tasks.Selection{}, err
	}
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO task_resource_snapshots(revision,body)VALUES(?,?)ON CONFLICT(revision)DO NOTHING", revision, string(raw)); err != nil {
		return tasks.Selection{}, err
	}
	plugins := []string{}
	digests := map[string]string{}
	for name, p := range spec.Params.Agent.Plugins {
		if p.Enabled {
			if p.Bundle == nil {
				return tasks.Selection{}, fmt.Errorf("plugin %s snapshot missing", name)
			}
			plugins = append(plugins, name)
			digests[name] = p.Bundle.Digest
		}
	}
	sort.Strings(plugins)
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	if err = spec.Rehash(); err != nil {
		return tasks.Selection{}, err
	}
	return tasks.Selection{Config: spec, Plugins: plugins, ResourceRevision: revision, PluginDigests: digests}, nil
}
func (s *Service) taskResource(ctx context.Context, revision string) (taskResource, error) {
	var raw []byte
	if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT body FROM task_resource_snapshots WHERE revision=?", revision).Scan(&raw); err != nil {
		return taskResource{}, err
	}
	var r taskResource
	if err := core.DecodeJSON(raw, &r); err != nil {
		return r, err
	}
	actual, err := core.HashJSON(r)
	if err != nil || actual != revision {
		return r, errors.New("task resource revision mismatch")
	}
	return r, nil
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
