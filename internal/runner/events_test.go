package runner

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"testing"
)

func TestRunnerTranscriptIsOrderedPagedAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runner.sqlite")
	journal, err := Open(path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	journal.Accept(ctx, launch)
	if _, err = journal.Claim(ctx, "control", "task", "run"); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 3; i++ {
		event := core.AgentEvent{Generation: 1, TurnID: "run", Seq: i, Update: core.Update{Type: "text_delta", Text: "part"}}
		if err = journal.AppendEvent(ctx, "control", "task", "run", event); err != nil {
			t.Fatal(err)
		}
	}
	if err = journal.AppendEvent(ctx, "control", "task", "run", core.AgentEvent{Generation: 2, TurnID: "run", Seq: 4}); err == nil {
		t.Fatal("stale generation entered transcript")
	}
	first, err := journal.Events(ctx, "control", "task", "run", 0, 2)
	if err != nil || len(first.Events) != 2 || first.Cursor != 2 || !first.HasMore {
		t.Fatal(first, err)
	}
	journal.Close()
	journal, err = Open(path, "runtime", "next-boot")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	last, err := journal.Events(ctx, "control", "task", "run", first.Cursor, 2)
	if err != nil || len(last.Events) != 1 || last.Cursor != 3 || last.HasMore {
		t.Fatal(last, err)
	}
}
