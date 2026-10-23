package assistant

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"path/filepath"
	"testing"
)

func TestCreationBindsAllInputsAndDoesNotResolveRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.sqlite")
	r, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	resolve := func() (*core.ResolvedSpec, error) { count++; return nil, nil }
	first, err := r.CreateConfigured(ctx, "create", "Name", "profile", "rules", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Rename(ctx, first.ID, "Renamed", first.Revision); err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	duplicate, err := r.CreateConfigured(ctx, "create", "Name", "profile", "rules", resolve)
	if err != nil || duplicate.ID != first.ID || duplicate.Name != "Renamed" || count != 1 {
		t.Fatal(duplicate, count, err)
	}
	for _, pair := range [][2]string{{"other", "rules"}, {"profile", "changed"}} {
		if _, err = r.CreateConfigured(ctx, "create", "Name", pair[0], pair[1], resolve); err == nil {
			t.Fatal("conflicting creation accepted")
		}
	}
	_, rules, err := r.Initial(ctx, first.ID)
	if err != nil || rules != "rules" {
		t.Fatal(rules, err)
	}
}

func TestDeletedAssistantSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	r, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Create(ctx, "original", "Original")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Delete(ctx, a.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	definitions, err := r.Definitions(ctx)
	if err != nil || len(definitions) != 0 {
		t.Fatal(definitions, err)
	}
	if _, err = r.Rename(ctx, a.ID, "Renamed", a.Revision); err == nil {
		t.Fatal("renamed deleted assistant")
	}
	if _, err = r.Create(ctx, "original", "Original"); err == nil {
		t.Fatal("replayed deleted creation")
	}
	b, err := r.Create(ctx, "new", "New")
	if err != nil || b.ID == a.ID {
		t.Fatal(b, err)
	}
}
