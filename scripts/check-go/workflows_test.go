package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflowContract struct {
	On map[string]struct {
		Branches []string
		Inputs   map[string]struct{ Required bool }
	}
	Concurrency struct {
		Group  string
		Cancel string `yaml:"cancel-in-progress"`
	}
	Jobs map[string]struct {
		Name     string
		If       string
		Uses     string
		Needs    yaml.Node
		RunsOn   string `yaml:"runs-on"`
		With     map[string]string
		Strategy struct {
			Matrix struct {
				OS   []string
				Plan []struct{ Mode, Shard string }
			}
		}
		Steps []struct{ Run string }
	}
}

func readWorkflow(t *testing.T, name string) workflowContract {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/" + name + ".yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowContract
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

func TestProductWorkflowRouting(t *testing.T) {
	tests := readWorkflow(t, "tests")
	ci := readWorkflow(t, "ci")
	for _, event := range []string{"pull_request", "push", "workflow_dispatch", "workflow_call"} {
		if _, ok := tests.On[event]; !ok {
			t.Fatalf("missing test entrypoint %s", event)
		}
	}
	if !reflect.DeepEqual(tests.On["push"].Branches, []string{"main"}) || !tests.On["workflow_call"].Inputs["ref"].Required {
		t.Fatal("automatic main or exact-source caller contract changed")
	}
	light, full := tests.Jobs["lightweight"], tests.Jobs["product"]
	if light.If != "github.event_name != 'workflow_dispatch' && inputs.ref == ''" || full.If != "github.event_name == 'workflow_dispatch' || inputs.ref != ''" {
		t.Fatal("PR/main must use automatic checks; manual and resolved-source calls must be full independently of publication")
	}
	if light.RunsOn != "ubuntu-24.04" || light.Name != "Lightweight checks (Linux)" || len(light.Strategy.Matrix.OS) != 0 {
		t.Fatal("automatic checks must stay on one explicitly named Linux runner")
	}
	if !strings.HasPrefix(full.Name, "Full product / ") || !reflect.DeepEqual(full.Strategy.Matrix.OS, []string{"ubuntu-24.04", "macos-26"}) {
		t.Fatal("full checks must retain both product platforms")
	}
	expected := []struct{ Mode, Shard string }{{"normal", "1/1"}, {"race", "1/4"}, {"race", "2/4"}, {"race", "3/4"}, {"race", "4/4"}}
	if !reflect.DeepEqual(full.Strategy.Matrix.Plan, expected) {
		t.Fatal("full normal/race coverage changed")
	}
	hasRun := func(steps []struct{ Run string }, command string) bool {
		for _, step := range steps {
			if step.Run == command {
				return true
			}
		}
		return false
	}
	if !hasRun(light.Steps, "mise run ci:light") || !hasRun(full.Steps, "mise run ci:normal") || !hasRun(full.Steps, "mise exec -- python scripts/test_go.py race --shard ${{ matrix.plan.shard }} --count=1") {
		t.Fatal("lightweight or complete product entrypoint changed")
	}
	sandbox, ok := tests.Jobs["sandbox"]
	if !ok {
		t.Fatal("sandbox job missing")
	}
	if sandbox.If != full.If || sandbox.RunsOn != "${{ matrix.os }}" {
		t.Fatal("sandbox must run independently for every full validation")
	}
	if !reflect.DeepEqual(sandbox.Strategy.Matrix.OS, []string{"ubuntu-24.04", "macos-26"}) || !hasRun(sandbox.Steps, "mise run test:sandbox") {
		t.Fatal("independent sandbox checks must retain real qualification on both platforms")
	}
	lifecycle, ok := tests.Jobs["macos-lifecycle"]
	if !ok || lifecycle.If != light.If || lifecycle.RunsOn != "macos-26" {
		t.Fatal("PR/main must retain targeted macOS lifecycle checks")
	}
	caller := ci.Jobs["product"]
	if caller.Uses != "./.github/workflows/tests.yml" || caller.With["ref"] != "${{ needs.source.outputs.sha }}" || caller.If != "" {
		t.Fatal("release and nonpublishing candidates must unconditionally call full checks at the resolved SHA")
	}
	var gated func(string, int) bool
	gated = func(name string, depth int) bool {
		if name == "product" {
			return true
		}
		if depth > len(ci.Jobs) {
			return false
		}
		needs := ci.Jobs[name].Needs
		parents := needs.Content
		if needs.Kind == yaml.ScalarNode {
			parents = []*yaml.Node{&needs}
		}
		for _, parent := range parents {
			if gated(parent.Value, depth+1) {
				return true
			}
		}
		return false
	}
	for _, channel := range []string{"build-checks", "server", "macos", "desktop", "docker-smoke", "container", "distribution"} {
		if !gated(channel, 0) {
			t.Fatalf("distribution job %s no longer requires full product checks", channel)
		}
	}
	if !strings.Contains(tests.Concurrency.Group, "'full' || 'light'") || tests.Concurrency.Cancel != "${{ github.event_name != 'workflow_dispatch' && inputs.ref == '' }}" {
		t.Fatal("automatic runs must not cancel manual or release checks")
	}
	mise, err := os.ReadFile("../../mise.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{
		`depends = ["test:go:ci", "check:ci", "test:contracts", "test:release"]`,
		`depends = ["lint", "test:i18n", "test:core", "test:ui", "test:packaging"]`,
	} {
		if !strings.Contains(string(mise), contract) {
			t.Fatalf("lost CI task boundary: %s", contract)
		}
	}
}
