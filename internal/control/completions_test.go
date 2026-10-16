package control

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
)

func TestCompletionsUseOwnerResourcesAndBoundMetadataWithoutExecution(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	if _, err := s.Registry.Create(ctx, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	if err := s.Machines.SetLabels(ctx, "local", []string{"desktop", "task-runner"}); err != nil {
		t.Fatal(err)
	}
	// Resolving metadata must never invoke tool execution or require a Provider.
	resolutions := 0
	s.options.ResolveSandbox = func(context.Context) (core.ExecutionTarget, error) {
		resolutions++
		return core.HostTarget("local"), nil
	}
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		writeTaskFixture(t, filepath.Join(s.options.AgentHome, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: Fixture skill\n---\nInstructions")
	}
	lookup := func(query string) []protocol.Completion {
		t.Helper()
		result, err := s.Completions(ctx, "main", query)
		if err != nil {
			t.Fatal(err)
		}
		return result["items"].([]protocol.Completion)
	}
	if items := lookup("/machine:"); len(items) != 1 || items[0].Token != "/machine:local" {
		t.Fatal(items)
	}
	if items := lookup("/tag:desk"); len(items) != 1 || items[0].Token != "/tag:desktop" {
		t.Fatal(items)
	}
	if items := lookup("/skill:"); len(items) != 50 || items[0].Token != "/skill:skill-00" || items[49].Token != "/skill:skill-49" {
		t.Fatal(items)
	}
	if items := lookup("SKILL-59"); len(items) != 1 || items[0].Token != "/skill:skill-59" {
		t.Fatal(items)
	}
	before := resolutions
	if _, err := s.Completions(ctx, "unknown", ""); err == nil {
		t.Fatal("unknown owner accepted")
	}
	if _, err := s.Completions(ctx, "main", "bad\nquery"); err == nil {
		t.Fatal("multiline query accepted")
	}
	if resolutions != before {
		t.Fatal("invalid request resolved resources")
	}
}
