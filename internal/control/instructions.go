package control

import (
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type InstructionSources struct {
	mu          sync.Mutex
	global      string
	globalError string
}

// Prepare reloads mutable Main instructions for each run. The global last-good
// cache is process-local; project read errors fail preparation rather than reuse
// stale text. User input is never modified to carry these sections.
func (s *InstructionSources) Prepare(agentHome, workspace, assistantRules string) (string, []core.SkillRoot, error) {
	cwd, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", nil, err
	}
	if !filepath.IsAbs(cwd) {
		return "", nil, errors.New("execution workspace must be absolute")
	}
	root := cwd
	for current := cwd; ; current = filepath.Dir(current) {
		_, err := os.Stat(filepath.Join(current, ".git"))
		if err == nil {
			root = current
			break
		}
		if !os.IsNotExist(err) {
			return "", nil, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	parts := []string{}
	global := s.globalText(agentHome)
	add := func(name, text string) {
		if strings.TrimSpace(text) != "" {
			parts = append(parts, fmt.Sprintf("<%s>\n%s\n</%s>", name, text, name))
		}
	}
	add("global_instructions", global)
	add("assistant_instructions", assistantRules)
	roots := []core.SkillRoot{{Path: filepath.Join(agentHome, "skills"), Location: filepath.Join(agentHome, "skills")}}
	paths := []string{}
	for current := cwd; ; current = filepath.Dir(current) {
		paths = append(paths, current)
		if current == root {
			break
		}
	}
	for i := len(paths) - 1; i >= 0; i-- {
		directory := paths[i]
		data, err := os.ReadFile(filepath.Join(directory, "AGENTS.md"))
		if err != nil && !os.IsNotExist(err) {
			return "", nil, err
		}
		if err == nil {
			add("project_instructions", fmt.Sprintf("Directory: %s\n%s", directory, strings.ToValidUTF8(string(data), "�")))
		}
		roots = append([]core.SkillRoot{{Path: filepath.Join(directory, ".agents", "skills"), Location: filepath.Join(directory, ".agents", "skills")}}, roots...)
	}
	add("instruction_discovery", "Project instructions apply from the nearest repository root through the execution directory. Before editing deeper paths, inspect applicable descendant AGENTS.md files. Explicit user instructions take precedence; project instructions do not grant tools or permissions.")
	return strings.Join(parts, "\n\n"), roots, nil
}
