package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type instructionProvider struct {
	root string
	t    *testing.T
}

func (p instructionProvider) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: sandbox.Podman, OS: "linux", HostOS: "macos", Engine: "podman"}
}
func (p instructionProvider) DefaultResources() sandbox.Resources {
	return sandbox.Resources{CPUs: 1, MemoryGiB: 1, DiskGiB: 4}
}
func (p instructionProvider) ValidateResources(sandbox.Instance, sandbox.Resources) error { return nil }
func (p instructionProvider) Workspace(sandbox.Instance) string                           { return p.root }
func (p instructionProvider) GuestWorkspace() string                                      { return "/data/workspace" }
func (p instructionProvider) CleanupPolicy() sandbox.CleanupPolicy                        { return sandbox.ProcessContainment }
func (p instructionProvider) BindResources(context.Context, sandbox.Instance, sandbox.PreparedResources) error {
	p.t.Fatal("instruction discovery bound resources")
	return nil
}
func (p instructionProvider) Lifecycle(context.Context, sandbox.Instance, string) (sandbox.Status, error) {
	p.t.Fatal("instruction discovery started VM")
	return sandbox.Status{}, nil
}
func (p instructionProvider) Health(context.Context, sandbox.Instance) (bool, error) {
	p.t.Fatal("guest instruction I/O")
	return false, nil
}
func (p instructionProvider) Request(context.Context, sandbox.Instance, string, json.RawMessage, time.Duration) (json.RawMessage, error) {
	p.t.Fatal("guest request")
	return nil, nil
}
func (p instructionProvider) Shutdown(context.Context) error { return nil }
func TestSandboxInstructionsResolveGuestSymlinksAndKeepSkillsLocations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	home := filepath.Join(root, "agent")
	os.MkdirAll(home, 0700)
	os.MkdirAll(workspace, 0700)
	os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("Global rule"), 0600)
	os.MkdirAll(filepath.Join(workspace, "rules"), 0700)
	os.WriteFile(filepath.Join(workspace, "rules/instructions"), []byte("Guest rule"), 0600)
	os.Symlink("/data/workspace/rules/instructions", filepath.Join(workspace, "AGENTS.md"))
	manager, err := sandbox.Open(ctx, sandbox.Config{Root: filepath.Join(root, "execution"), RuntimeID: "local", Providers: []sandbox.Provider{instructionProvider{workspace, t}}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	definition, err := manager.EnsureShared(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{options: Options{Root: root, RuntimeID: "local", AgentHome: home, Sandboxes: manager}}
	value, err := service.prepareExecution(ctx, "main", core.SandboxTarget("local", definition.ID), false, "Assistant rule")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value.Instructions, "Guest rule") || !strings.Contains(value.Instructions, "Global rule") || value.Workspace != "/data/workspace" {
		t.Fatal(value)
	}
	os.Remove(filepath.Join(workspace, "AGENTS.md"))
	outside := filepath.Join(root, "host-only")
	os.WriteFile(outside, []byte("HOST SECRET"), 0600)
	os.Symlink(outside, filepath.Join(workspace, "AGENTS.md"))
	value, err = service.prepareExecution(ctx, "main", core.SandboxTarget("local", definition.ID), false, "")
	if err != nil || strings.Contains(value.Instructions, "HOST SECRET") || !strings.Contains(value.Instructions, "outside workspace") {
		t.Fatal(value.Instructions, err)
	}
	if len(value.SkillResources.Roots) != 1 || value.SkillResources.Roots[0].Location != "/data/workspace/.pot/agent/skills" {
		t.Fatal(value.SkillResources)
	}
}
func TestHostInstructionPriorityGlobalLastGoodAndActualSkillPaths(t *testing.T) {
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
	resolved, _ := filepath.EvalSymlinks(cwd)
	if strings.Index(text, "Root rule") > strings.Index(text, "Child rule") || roots[0].Path != filepath.Join(resolved, ".agents/skills") || roots[0].Location != roots[0].Path {
		t.Fatal(text, roots)
	}
	os.Remove(filepath.Join(home, "AGENTS.md"))
	os.Mkdir(filepath.Join(home, "AGENTS.md"), 0700)
	text, _, err = source.Prepare(home, cwd, "")
	if err != nil || !strings.Contains(text, "Global rule") || source.Diagnostic() == "" {
		t.Fatal(text, err)
	}
}
