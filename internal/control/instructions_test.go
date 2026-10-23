package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostInstructionPriorityIgnoresSharedRulesAndKeepsActualSkillPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	cwd := filepath.Join(repo, "child")
	os.MkdirAll(home, 0700)
	os.MkdirAll(cwd, 0700)
	os.Mkdir(filepath.Join(repo, ".git"), 0700)
	os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("Global rule"), 0600)
	os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("Root rule"), 0600)
	os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("Child rule"), 0600)
	var source InstructionSources
	text, roots, err := source.Prepare(home, cwd, "Assistant rule")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Global rule") || !strings.Contains(text, "Assistant rule") {
		t.Fatal("assistant rules must replace the shared instruction source", text)
	}
	resolved, _ := filepath.EvalSymlinks(cwd)
	if strings.Index(text, "Root rule") > strings.Index(text, "Child rule") || roots[0].Path != filepath.Join(resolved, ".agents/skills") || roots[0].Location != roots[0].Path {
		t.Fatal(text, roots)
	}
	os.Remove(filepath.Join(home, "AGENTS.md"))
	os.Mkdir(filepath.Join(home, "AGENTS.md"), 0700)
	text, _, err = source.Prepare(home, cwd, "")
	if err != nil || strings.Contains(text, "Global rule") {
		t.Fatal(text, err)
	}
}
