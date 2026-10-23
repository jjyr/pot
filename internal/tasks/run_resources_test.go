package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalPreparedResourcesSurviveResumeAndRestartWithoutReplacement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	j, err := OpenJournal(path, "runtime", "boot")
	if err != nil {
		t.Fatal(err)
	}
	launch := launchFixture(t)
	if _, err = j.Accept(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.PinResources(ctx, "runtime", "task", "run", json.RawMessage(`{"value":"early"}`)); err == nil {
		t.Fatal("resources fixed before execution admission")
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	original, digest, err := j.PinResources(ctx, "runtime", "task", "run", json.RawMessage(`{"value":"current"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = j.Wait(ctx, "runtime", "task", "run", "allowance"); err != nil {
		t.Fatal(err)
	}
	if err = j.Resume(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, "runtime", "task", "run"); err != nil {
		t.Fatal(err)
	}
	reused, same, err := j.PinResources(ctx, "runtime", "task", "run", json.RawMessage(`{"value":"later"}`))
	if err != nil || same != digest || string(original) != string(reused) {
		t.Fatal("allowance resume resampled resources", err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, "runtime", "nextboot")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	reused, same, err = j.PreparedResources(ctx, "runtime", "task", "run")
	if err != nil || same != digest || string(original) != string(reused) {
		t.Fatal("restart lost original resource evidence", err)
	}
	if _, _, err = j.PinResources(ctx, "runtime", "task", "run", json.RawMessage(`{}`)); err == nil {
		t.Fatal("fenced original accepted replacement")
	}
	if err = os.WriteFile(filepath.Join(j.resources.Root, "objects", digest, "resources.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.PreparedResources(ctx, "runtime", "task", "run"); err == nil {
		t.Fatal("corrupted original resource evidence accepted")
	}
}
