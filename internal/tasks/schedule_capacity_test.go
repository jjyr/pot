package tasks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestScheduledLaunchCapacitySurvivesJournalRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := OpenJournal(path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	launch := launchFixture(t)
	launch.Text = "a" + strings.Repeat("\x00", protocol.MaxScheduledTaskTextBytes-1)
	if _, err := j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, "runtime", "nextboot")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := j.Inspect(ctx, "runtime", "task", "run")
	if err != nil || restored == nil || restored.Launch.Text != launch.Text {
		t.Fatalf("journal lost scheduled input: %v", err)
	}
	launch.Text += "a"
	if err := launch.Validate("runtime"); err == nil {
		t.Fatal("launch accepted input beyond its byte limit")
	}
}
