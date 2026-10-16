package control

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostAssistantWorkspaceAndInstructionDiscoveryAreOwnerScoped(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	a, err := s.ExecutionWorkspace("first")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ExecutionWorkspace("second")
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, filepath.Join(a, "AGENTS.md"), "FIRST_PRIVATE")
	writeTaskFixture(t, filepath.Join(b, "AGENTS.md"), "SECOND_PRIVATE")
	first, err := s.prepareExecution(ctx, "first", core.HostTarget("local"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.prepareExecution(ctx, "second", core.HostTarget("local"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || first.Workspace != a || second.Workspace != b || strings.Contains(first.Instructions, "SECOND_PRIVATE") || !strings.Contains(first.Instructions, "FIRST_PRIVATE") || strings.Contains(second.Instructions, "FIRST_PRIVATE") {
		t.Fatal(first, second)
	}
	mail, err := s.prepareExecutionFor(ctx, "mail-execution", "first", core.HostTarget("local"), false, "")
	if err != nil || mail.Owner != "mail-execution" || mail.Workspace != a {
		t.Fatal(mail, err)
	}
	worker, err := s.prepareExecution(ctx, "worker", core.HostTarget("local"), true, "")
	if err != nil || worker.Workspace != filepath.Join(s.options.Workspace, "tasks", "worker") {
		t.Fatal(worker, err)
	}
}
