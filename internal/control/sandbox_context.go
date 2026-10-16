package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/internal/agent"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/sandbox"
)

func (s *InstructionSources) globalText(agentHome string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(agentHome, "AGENTS.md"))
	if err == nil {
		s.global = strings.ToValidUTF8(string(data), "�")
		s.globalError = ""
	} else if os.IsNotExist(err) {
		s.global = ""
		s.globalError = ""
	} else {
		s.globalError = err.Error()
	}
	return s.global
}
func (s *InstructionSources) Diagnostic() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.globalError
}
func instructionSection(name, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return fmt.Sprintf("<%s>\n%s\n</%s>", name, text, name)
}

// prepareExecution discovers instructions in the execution namespace. Sandbox
// absolute symlinks are never interpreted against the Control Host filesystem.
func (s *Service) prepareExecution(ctx context.Context, owner string, target core.ExecutionTarget, worker bool, rules string) (core.ExecutionContext, error) {
	result := core.ExecutionContext{Owner: owner, Data: s.options.Root, Role: core.Main, MainPrompt: assistant.MainPrompt}
	if worker {
		result.Role = core.Worker
		result.MainPrompt = ""
	}
	var roots []core.SkillRoot
	if target.Target.Kind == "host" {
		if !target.IsLocalTo(s.options.RuntimeID) {
			return result, errors.New("remote instructions require destination preparation")
		}
		cwd := s.options.Workspace
		if worker {
			cwd = filepath.Join(cwd, "tasks", owner)
			if err := os.MkdirAll(cwd, 0700); err != nil {
				return result, err
			}
		}
		instructions, project, err := s.instructions.prepare(s.options.AgentHome, cwd, rules, !worker)
		if err != nil {
			return result, err
		}
		result.Instructions = instructions
		result.Workspace = cwd
		roots = project
	} else {
		if s.options.Sandboxes == nil {
			return result, errors.New("Sandbox workspace unavailable")
		}
		w, err := s.options.Sandboxes.WorkspacePaths(ctx, target, owner, worker)
		if err != nil {
			return result, err
		}
		result.Workspace = w.GuestCWD
		global := ""
		if !worker {
			global = s.instructions.globalText(s.options.AgentHome)
		} else {
			rules = ""
		}
		parts := []string{instructionSection("global_instructions", global), instructionSection("assistant_instructions", rules)}
		filesystem, err := w.OpenInstructions()
		if err != nil && !os.IsNotExist(err) {
			return result, err
		}
		if filesystem != nil {
			defer filesystem.Close()
			repository := w.GuestRoot
			for current := w.GuestCWD; ; current = path.Dir(current) {
				if _, err := filesystem.Stat(path.Join(current, ".git")); err == nil {
					repository = current
					break
				} else if !os.IsNotExist(err) && !errors.Is(err, sandbox.ErrOutsideWorkspace) {
					return result, err
				}
				if current == w.GuestRoot {
					break
				}
			}
			directories := []string{}
			for current := w.GuestCWD; ; current = path.Dir(current) {
				directories = append(directories, current)
				if current == repository {
					break
				}
			}
			for i := len(directories) - 1; i >= 0; i-- {
				directory := directories[i]
				text, err := filesystem.ReadFile(path.Join(directory, "AGENTS.md"))
				if err == nil {
					parts = append(parts, instructionSection("project_instructions", fmt.Sprintf("Directory: %s\n%s", directory, strings.ToValidUTF8(string(text), "�"))))
				} else if errors.Is(err, sandbox.ErrOutsideWorkspace) {
					parts = append(parts, instructionSection("instruction_diagnostic", directory+"/AGENTS.md: outside workspace; omitted"))
				} else if !os.IsNotExist(err) {
					return result, err
				}
			}
			// Deeper project roots take priority, independently of instruction ordering.
			for _, directory := range directories {
				visible := path.Join(directory, ".agents/skills")
				source, err := filesystem.SourcePath(visible)
				if os.IsNotExist(err) || errors.Is(err, sandbox.ErrOutsideWorkspace) {
					continue
				}
				if err != nil {
					return result, err
				}
				roots = append(roots, core.SkillRoot{Path: source, Location: visible})
			}
		}
		parts = append(parts, instructionSection("instruction_discovery", "Project instructions apply from the nearest repository root through the execution directory. Inspect descendant AGENTS.md before deeper edits. Sources outside the shared workspace are omitted. Explicit user instructions take precedence; instructions do not grant tools or permissions."))
		result.Instructions = strings.Join(parts, "\n\n")
		if !worker {
			roots = append(roots, core.SkillRoot{Path: filepath.Join(s.options.AgentHome, "skills"), Location: path.Join(w.GuestCWD, ".pot/agent/skills")})
		}
	}
	if !worker && s.options.BuiltinSkills != "" {
		location := s.options.BuiltinSkills
		if target.Target.Kind == "sandbox" {
			location = path.Join(result.Workspace, ".pot/agent/skills")
		}
		builtin := core.SkillRoot{Path: s.options.BuiltinSkills, Location: location, Builtin: true}
		catalog, err := agent.DiscoverSkills(core.SkillResources{Roots: []core.SkillRoot{builtin}})
		if err != nil {
			return result, err
		}
		for _, skill := range catalog.Skills {
			result.SkillResources.ReservedNames = append(result.SkillResources.ReservedNames, skill.Name)
		}
		roots = append(roots, builtin)
	}
	result.SkillResources.Roots = roots
	return result, nil
}
