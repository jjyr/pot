package control

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
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
	pluginRoot := t.TempDir()
	s.options.PluginRoot = pluginRoot
	s.Profiles.pluginRoot = pluginRoot
	pluginDir := filepath.Join(pluginRoot, "qualifier")
	writeTaskFixture(t, filepath.Join(pluginDir, "plugin.json"), `{"schema_version":2,"id":"qualifier","version":"1","contributes":{"tools":{"runtime":"node","execution":"host_or_sandbox","entrypoint":"plugin.mjs","files":["plugin.mjs"],"requirements":[],"tools":[{"name":"fixture_echo","description":"fixture","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}]},"sandbox_providers":[{"id":"fixture.provider","host_os":["darwin","linux"],"executable":"bin/provider","default_shared":false}],"resources":[{"provider":"fixture.provider","catalog":"catalog.json"}]}}`)
	writeTaskFixture(t, filepath.Join(pluginDir, "plugin.mjs"), "OLD_PLUGIN")
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: "https://example.test", Protocol: "openai_chat", APIKey: "fixture-only", MaxOutputTokens: 100})
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
	omitted, err := s.selectTask(ctx, "main", protocol.CreateTask{})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.currentTaskResource(ctx, runner.Launch{Config: omitted.Config, Plugins: omitted.Plugins})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Roots) != 0 {
		t.Fatal("omitted selection inherited global skills")
	}
	if _, err = s.currentTaskResource(ctx, runner.Launch{Config: omitted.Config, Plugins: omitted.Plugins, Skills: &[]string{"source"}}); err == nil {
		t.Fatal("source project skill satisfied selected global bundle")
	}
	selected, err := s.selectTask(ctx, "main", protocol.CreateTask{Skills: &[]string{"global"}})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := s.currentTaskResource(ctx, runner.Launch{Config: selected.Config, Plugins: selected.Plugins, Skills: &[]string{"global"}})
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
	launch := runner.Launch{ControlID: "local", RuntimeID: "local", AssistantID: "main", TaskID: "worker", RunID: "run", Generation: 1, ExecutionTarget: core.HostTarget("local"), Config: selected.Config, Plugins: selected.Plugins, Skills: &[]string{"global"}, Text: "work"}
	run, err := s.RunnerJournal.Accept(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunnerJournal.Claim(ctx, "local", "worker", "run"); err != nil {
		t.Fatal(err)
	}
	// Queued tasks synchronize current global content immediately before Prepare.
	writeTaskFixture(t, filepath.Join(pluginDir, "plugin.mjs"), "QUEUED_PLUGIN_UPDATE")
	globalPath := filepath.Join(s.options.AgentHome, "skills", "global", "SKILL.md")
	writeTaskFixture(t, globalPath, "---\nname: global\ndescription: Global skill\n---\nQUEUED_UPDATE")
	prepared, err := s.prepareTask(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	pinned, digest, err := s.readPreparedTaskResources(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	contains := func(r taskResource, text string) bool {
		for _, root := range r.Roots {
			for _, data := range root.Files {
				decoded, _ := base64.RawStdEncoding.DecodeString(data)
				if strings.Contains(string(decoded), text) {
					return true
				}
			}
		}
		return false
	}
	if pinned.Config.Params.Agent.Plugins["qualifier"].Bundle.Files["plugin.mjs"] != "QUEUED_PLUGIN_UPDATE" || pinned.Config.Params.Agent.Plugins["qualifier"].Source != nil {
		t.Fatal("plugin current source not pinned as destination bundle")
	}
	b := pinned.Config.Params.Agent.Plugins["qualifier"].Bundle
	if len(b.Files) != 1 || len(b.Manifest.Contributes.SandboxProviders) != 0 || len(b.Manifest.Contributes.Resources) != 0 {
		t.Fatal("Task synchronized local plugin capabilities", b)
	}
	if !contains(pinned, "QUEUED_UPDATE") {
		t.Fatal("queued Task did not use current global resources")
	}
	writeTaskFixture(t, globalPath, "---\nname: global\ndescription: Global skill\n---\nINFLIGHT_UPDATE")
	reused, sameDigest, err := s.prepareTaskResources(ctx, launch, func() (taskResource, error) { t.Fatal("in-flight resources fetched again"); return taskResource{}, nil })
	if err != nil || sameDigest != digest || !contains(reused, "QUEUED_UPDATE") || contains(reused, "INFLIGHT_UPDATE") {
		t.Fatal("original run resource evidence changed", err)
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
