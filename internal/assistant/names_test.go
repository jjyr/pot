package assistant

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func TestAssistantSlugPool(t *testing.T) {
	if len(slugWords) < 1024 {
		t.Fatalf("too few combinations: %d words", len(slugWords))
	}
	seen := map[string]bool{}
	wordPattern := regexp.MustCompile(`^[a-z]{2,12}$`)
	for _, word := range slugWords {
		if !wordPattern.MatchString(word) || seen[word] {
			t.Fatalf("invalid or duplicate slug word: %q", word)
		}
		seen[word] = true
	}
}

func TestExistingAssistantIdentitiesSurviveNewNaming(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	r, err := OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	// The persisted ID/name fields keep their existing string contract. No
	// schema conversion or rewriting of historical references is needed.
	for _, item := range [][3]string{{"main", "Cup", "original-main"}, {"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "Renamed helper", "original-other"}} {
		if _, err := r.db.Exec("INSERT INTO assistant_definitions(id,request_id) VALUES(?,?)", item[0], item[2]); err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Exec("INSERT INTO assistant_file_export VALUES(?,?,?,'cup',?,'')", item[0], item[1], "", item[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.ExportAssistantFiles(r.db, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	before, err := r.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = OpenRegistry(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	created, err := r.Create(ctx, "new", "")
	if err != nil || created.Name != slugDisplayName(created.ID) || !strings.Contains(created.ID, "-") {
		t.Fatal(created, err)
	}
	after, err := r.Definitions(ctx)
	if err != nil || len(after) != 3 || !slices.Equal(before, after[:2]) {
		t.Fatal("new naming changed an existing identity", after, err)
	}
}

func TestAssistantSlugAllocatesOnlyFreePairs(t *testing.T) {
	words := []string{"amber", "otter", "quiet"}
	// Names are compared case-insensitively; duplicate reservations count once.
	used := []string{" AMBER-OTTER ", "amber-otter", "quiet-amber", "legacy-ID", "main"}
	want := []string{"amber-amber", "amber-quiet", "otter-amber", "otter-otter", "otter-quiet", "quiet-otter", "quiet-quiet"}
	for index, expected := range want {
		got, err := pickAssistantSlug(words, used, func(size int) int {
			if size != len(want) {
				t.Fatalf("wrong available count: %d", size)
			}
			return index
		})
		if err != nil || got != expected {
			t.Fatalf("choice %d: %q, %v; want %q", index, got, err, expected)
		}
	}
	used = append(used, want...)
	if _, err := pickAssistantSlug(words, used, func(int) int { t.Fatal("exhausted pool attempted a random pick"); return 0 }); err == nil {
		t.Fatal("exhausted pool accepted")
	}
}

func TestRandomCreationConcurrentHandlesRetriesAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	open := func() *Registry {
		r, err := OpenRegistry(path, "main")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first, second := open(), open()
	defer first.Close()
	defer second.Close()
	var wg sync.WaitGroup
	for index := range 32 {
		wg.Go(func() {
			r := []*Registry{first, second}[index%2]
			for range 2 {
				if _, err := r.CreateWithIntent(ctx, fmt.Sprintf("create-%d", index), "", "", "", nil, "Hi, I'm {name}.", false, nil); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	definitions, err := first.Definitions(ctx)
	if err != nil || len(definitions) != 32 {
		t.Fatalf("duplicate/missing creation: %d %v", len(definitions), err)
	}
	seen := map[string]bool{}
	for _, d := range definitions {
		parts := strings.Split(d.ID, "-")
		if d.Name != slugDisplayName(d.ID) || !core.Identifier(d.ID) || len(parts) != 2 || !slices.Contains(slugWords, parts[0]) || !slices.Contains(slugWords, parts[1]) {
			t.Fatal("creation did not share a two-word ID and initial name", d)
		}
		if seen[d.ID] {
			t.Fatal("concurrent duplicate", d.ID)
		}
		seen[d.ID] = true
	}
	original := definitions[0]
	renamed, err := first.Rename(ctx, original.ID, "My helper", original.Revision)
	if err != nil || renamed.ID != original.ID {
		t.Fatal("rename changed ID", renamed, err)
	}
	first.Close()
	second.Close()
	r := open()
	defer r.Close()
	retry, err := r.CreateWithIntent(ctx, original.RequestID, "", "", "", nil, "Hi, I'm {name}.", false, nil)
	if err != nil || retry != renamed {
		t.Fatal("retry changed identity after rename/restart", retry, err)
	}
	welcome, err := r.Welcome(ctx, original.ID)
	if err != nil || welcome != "Hi, I'm "+original.Name+"." {
		t.Fatal("welcome was regenerated on retry", welcome, err)
	}
	if err := r.Delete(ctx, original.ID, renamed.Revision); err != nil {
		t.Fatal(err)
	}
	next, err := r.Create(ctx, "next", "")
	if err != nil || seen[next.ID] || next.Name != slugDisplayName(next.ID) {
		t.Fatal("new creation reused historical ID", next, err)
	}
}
