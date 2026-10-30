package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
)

func instructionSection(name, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return fmt.Sprintf("<%s>\n%s\n</%s>", name, text, name)
}

// prepareExecution discovers instructions and Skills in the destination Runtime.
func (s *Service) prepareExecution(ctx context.Context, owner string, target core.ExecutionTarget, worker bool, rules string) (core.ExecutionContext, error) {
	return s.prepareExecutionFor(ctx, owner, owner, target, worker, rules)
}

// ExecutionWorkspace is the conversational assistant's persistent local directory.
// It creates no executor and grants no execution authority.
func (s *Service) ExecutionWorkspace(owner string) (string, error) {
	if !core.Identifier(owner) {
		return "", errors.New("invalid assistant workspace identity")
	}
	directory := filepath.Join(s.options.Workspace, "assistants", owner)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	return directory, nil
}
func (s *Service) prepareExecutionFor(ctx context.Context, owner, workspaceOwner string, target core.ExecutionTarget, worker bool, rules string) (core.ExecutionContext, error) {
	result := core.ExecutionContext{Owner: owner, Data: s.options.Root, Role: core.Main}
	if worker {
		result.Role = core.Worker
	}
	result.RolePrompt = assistant.PromptForRole(result.Role)
	var roots []core.SkillRoot

	if !target.IsLocalTo(s.options.RuntimeID) {
		return result, errors.New("remote instructions require destination preparation")
	}
	cwd := s.options.Workspace
	if !worker {
		var err error
		cwd, err = s.ExecutionWorkspace(workspaceOwner)
		if err != nil {
			return result, err
		}
	}
	if worker {
		var err error
		cwd, err = s.taskWorkspacePath(ctx, owner)
		if err != nil {
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

	if !worker && s.options.BuiltinSkills != "" {
		location := s.options.BuiltinSkills

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
	if !worker {
		catalog, err := agent.DiscoverSkills(result.SkillResources)
		if err != nil {
			return result, err
		}
		selection := []string{}
		for _, skill := range catalog.Skills {
			enabled, err := s.enabled(ctx, "skills", extensionID(skill.Location))
			if err != nil {
				return result, err
			}
			if enabled {
				selection = append(selection, skill.Name)
			}
		}
		result.SkillResources.Selection = &selection
	}
	return result, nil
}
