package tasks

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"testing"
)

func TestTranscriptOutputDoesNotInvalidateRunLifecycle(t *testing.T) {
	ctx := t.Context()
	journal, err := openJournal(t, filepath.Join(t.TempDir(), "tasks.sqlite"), "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.db.Close()
	launch := launchFixture(t)
	if _, err = journal.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err = journal.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	sub := journal.Subscribe()
	defer sub.Close()
	previous, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"text_delta", "tool_output", "tool_state", "turn_finished", "future_state_event"} {
		event := core.AgentEvent{Generation: 1, TurnID: "run", Seq: uint64(i + 1), Update: core.Update{Type: kind, Text: "synthetic", Phase: "waiting_approval"}}
		if err = journal.AppendEvent(ctx, "runtime", "task", "run", event); err != nil {
			t.Fatal(err)
		}
		next, err := sub.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "text_delta" || kind == "tool_output" {
			if next["runs"] != previous["runs"] || next["runner"] <= previous["runner"] {
				t.Fatalf("output changed lifecycle: %v -> %v", previous, next)
			}
		} else if next["runs"] <= previous["runs"] {
			t.Fatalf("state event %q lost lifecycle invalidation", kind)
		}
		previous = next
	}
	page, err := journal.Events(ctx, "runtime", "task", "run", 0, 100)
	if err != nil || page.Cursor != 5 || len(page.Events) != 5 {
		t.Fatalf("event persistence changed: %+v %v", page, err)
	}
}

func TestRunnerTranscriptIsOrderedPagedAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	journal, err := openJournal(t, path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	journal.Accept(ctx, launch)
	if _, err = journal.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 3; i++ {
		event := core.AgentEvent{Generation: 1, TurnID: "run", Seq: i, Update: core.Update{Type: "text_delta", Text: "part"}}
		if err = journal.AppendEvent(ctx, "runtime", "task", "run", event); err != nil {
			t.Fatal(err)
		}
	}
	if err = journal.AppendEvent(ctx, "runtime", "task", "run", core.AgentEvent{Generation: 2, TurnID: "run", Seq: 4}); err == nil {
		t.Fatal("stale generation entered transcript")
	}
	first, err := journal.Events(ctx, "runtime", "task", "run", 0, 2)
	if err != nil || len(first.Events) != 2 || first.Cursor != 2 || !first.HasMore {
		t.Fatal(first, err)
	}
	journal.db.Close()
	journal, err = openJournal(t, path, "runtime", "next-boot")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.db.Close()
	last, err := journal.Events(ctx, "runtime", "task", "run", first.Cursor, 2)
	if err != nil || len(last.Events) != 1 || last.Cursor != 3 || last.HasMore {
		t.Fatal(last, err)
	}
}
