package control

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
)

func TestRealMainSkillSandboxResources(t *testing.T) {
	t.Parallel()
	if os.Getenv("POT_SANDBOX_INTEGRATION") != "1" {
		t.Skip("requires real sandbox qualification")
	}
	s := taskResourceService(t)
	var err error
	s.options.Node, err = exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	s.options.SandboxDirectory = os.Getenv("POT_SANDBOX_SDK")
	if s.options.SandboxDirectory == "" {
		s.options.SandboxDirectory, err = filepath.Abs("../../sandbox")
		if err != nil {
			t.Fatal(err)
		}
	}
	s.options.BuiltinSkills = filepath.Join(s.options.Root, "bundle", "skills")
	for _, skill := range []struct{ root, name string }{{filepath.Join(s.options.AgentHome, "skills"), "installed"}, {s.options.BuiltinSkills, "builtin"}} {
		writeTaskFixture(t, filepath.Join(skill.root, skill.name, "SKILL.md"), "---\nname: "+skill.name+"\ndescription: Test skill\n---\nSKILL_READ_OK\n")
		writeTaskFixture(t, filepath.Join(skill.root, skill.name, "scripts", "probe.sh"), "printf SUPPORT_READ_OK")
	}
	secret := filepath.Join(s.options.Root, "private-state")
	writeTaskFixture(t, secret, "PRIVATE_STATE")
	link := filepath.Join(s.options.AgentHome, "skills", "installed", "escape")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	execution, err := s.prepareExecution(t.Context(), "main", core.RuntimeTarget("local"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := agent.DiscoverSkills(execution.SkillResources)
	if err != nil || len(catalog.Skills) != 2 {
		t.Fatal(catalog, err)
	}
	engine, err := s.host("main", 1)
	if err != nil {
		t.Fatal(err)
	}
	policy, _, err := s.Permissions.Policy("local")
	if err != nil || policy.ExecutionAccess != "ask" {
		t.Fatal(policy, err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	shell := core.DefaultShellSettings()
	shell.ShellSnapshot, shell.AllowLoginShell = false, false
	for _, skill := range catalog.Skills {
		command := "cat " + quote(skill.Location) + "; /bin/sh " + quote(filepath.Join(filepath.Dir(skill.Location), "scripts", "probe.sh"))
		result, err := engine.Execute(t.Context(), executor.Request{ID: skill.Name, Mode: "sandbox", Revision: policy.Revision, CWD: execution.Workspace, Policy: shell, Operation: "exec_command", Args: map[string]any{"cmd": command, "login": false}}, nil)
		if err != nil || result["exit_code"] != 0 || result["execution_mode"] != "sandbox" || !strings.Contains(result["stdout"].(string), "SKILL_READ_OK") || !strings.Contains(result["stdout"].(string), "SUPPORT_READ_OK") {
			t.Fatalf("advertised skill unavailable: %v %v", result, err)
		}
		command = "if cat " + quote(secret) + "; then exit 21; fi; if cat " + quote(link) + "; then exit 22; fi; if printf changed > " + quote(skill.Location) + "; then exit 23; fi; printf BOUNDARY_OK"
		result, err = engine.Execute(t.Context(), executor.Request{ID: skill.Name + "-boundary", Mode: "sandbox", Revision: policy.Revision, CWD: execution.Workspace, Policy: shell, Operation: "exec_command", Args: map[string]any{"cmd": command, "login": false}}, nil)
		if err != nil || result["exit_code"] != 0 || result["stdout"] != "BOUNDARY_OK" {
			t.Fatalf("skill grant exceeded read boundary: %v %v", result, err)
		}
		data, err := os.ReadFile(skill.Location)
		if err != nil || !strings.Contains(string(data), "SKILL_READ_OK") {
			t.Fatal("skill was modified", err)
		}
	}
}
