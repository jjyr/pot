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
	result := core.ExecutionContext{Owner: owner, Data: s.options.Root, Role: core.Main, MainPrompt: assistant.MainPrompt}
	if worker {
		result.Role = core.Worker
		result.MainPrompt = ""
	}
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
	return result, nil
}
