package control

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/secrets"
)

func taskResourceService(t *testing.T) *Service {
	t.Helper()
	manager, _ := execution.New("local", "boot", execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func writeTaskFixture(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestWorkerResourcesKeepDestinationInstructionsAndSkills(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	s.Registry.Create(ctx, "create", "Pot")
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: "https://example.test", Protocol: "openai_chat", APIKey: "fixture-only", Agent: &settings, MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, filepath.Join(s.options.AgentHome, "AGENTS.md"), "MAIN_GLOBAL_PRIVATE")
	writeTaskFixture(t, filepath.Join(s.options.AgentHome, "skills", "global", "SKILL.md"), "---\nname: global\ndescription: Global skill\n---\nGlobal instructions")
	writeTaskFixture(t, filepath.Join(s.options.Workspace, ".agents", "skills", "source", "SKILL.md"), "SOURCE_PROJECT_SKILL")
	selected, err := s.selectTask(ctx, "main", protocol.CreateTask{})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := s.taskResource(ctx, selected.ResourceRevision)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range resources.Roots {
		for _, data := range root.Files {
			decoded, _ := base64.RawStdEncoding.DecodeString(data)
			if strings.Contains(string(decoded), "SOURCE_PROJECT_SKILL") || strings.Contains(string(decoded), "MAIN_GLOBAL_PRIVATE") {
				t.Fatal("Main/source project context crossed resource boundary")
			}
		}
	}
	cwd := filepath.Join(s.options.Workspace, "tasks", "worker")
	writeTaskFixture(t, filepath.Join(cwd, ".git"), "")
	writeTaskFixture(t, filepath.Join(cwd, "AGENTS.md"), "DESTINATION_PROJECT")
	writeTaskFixture(t, filepath.Join(cwd, ".agents", "skills", "destination", "SKILL.md"), "---\nname: destination\ndescription: Destination skill\n---\nDestination")
	prepared, err := s.prepareTask(ctx, runner.Run{Launch: runner.Launch{ControlID: "local", RuntimeID: "local", AssistantID: "main", TaskID: "worker", RunID: "run", Generation: 1, ExecutionTarget: core.HostTarget("local"), ResourceRevision: selected.ResourceRevision, Config: selected.Config}})
	if err != nil {
		t.Fatal(err)
	}
	execution := prepared.Context
	if !strings.Contains(execution.Instructions, "DESTINATION_PROJECT") || strings.Contains(execution.Instructions, "MAIN_GLOBAL_PRIVATE") {
		t.Fatalf("Worker instructions: %s", execution.Instructions)
	}
	canonicalCWD, _ := filepath.EvalSymlinks(cwd)
	if len(execution.SkillResources.Roots) < 2 || execution.SkillResources.Roots[0].Path != filepath.Join(canonicalCWD, ".agents", "skills") {
		t.Fatalf("destination skill precedence lost: %+v", execution.SkillResources.Roots)
	}
}
