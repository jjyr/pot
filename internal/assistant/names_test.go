package assistant

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestKitchenNamePoolAndRandomSelection(t *testing.T) {
	want := []string{"Cup", "Mug", "Bowl", "Pan", "Wok", "Spoon", "Fork", "Tray", "Whisk", "Kettle", "Lid", "Jar", "Jug", "Plate", "Dish", "Glass"}
	if !slices.Equal(kitchenNames[:], want) {
		t.Fatalf("kitchen identity pool changed: %v", kitchenNames)
	}
	for index, expected := range want {
		got := pickAssistantName(nil, func(size int) int {
			if size != len(want) {
				t.Fatalf("random choice excludes unused names: %d", size)
			}
			return index
		})
		if got != expected {
			t.Fatalf("choice %d: %q, want %q", index, got, expected)
		}
	}
}

func TestKitchenNamesPreferUnusedThenSmallestSuffix(t *testing.T) {
	first := func(int) int { return 0 }
	used := []string{" cuP ", "Cup 2", "Custom name"}
	if name := pickAssistantName(used, first); name != "Mug" {
		t.Fatal("did not skip manually named/case-folded duplicate", name)
	}
	used = append(used, kitchenNames[1:]...)
	used = append(used, "Cup 4")
	if name := pickAssistantName(used, first); name != "Cup 3" {
		t.Fatal("did not choose smallest free suffix", name)
	}
	for index, base := range kitchenNames {
		if name := pickAssistantName(kitchenNames[:], func(int) int { return index }); name != base+" 2" {
			t.Fatal("suffix did not retain randomly chosen base", name)
		}
	}
	// A renamed/deleted base becomes available before any numbered variant.
	used = slices.DeleteFunc(slices.Clone(used), func(name string) bool { return name == "Bowl" })
	if name := pickAssistantName(used, first); name != "Bowl" {
		t.Fatal("unused base did not take priority", name)
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
	for index, d := range definitions {
		if seen[d.Name] {
			t.Fatal("concurrent duplicate", d.Name)
		}
		seen[d.Name] = true
		if index < len(kitchenNames) && !slices.Contains(kitchenNames[:], d.Name) {
			t.Fatal("numbered before base names exhausted", d.Name)
		}
		if index >= len(kitchenNames) && !strings.Contains(d.Name, " ") {
			t.Fatal("missing suffix", d.Name)
		}
	}
	original := definitions[0]
	renamed, err := first.Rename(ctx, original.ID, "My kitchen helper", original.Revision)
	if err != nil {
		t.Fatal(err)
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
	next, err := r.Create(ctx, "freed-base", "")
	if err != nil || next.Name != original.Name {
		t.Fatal("did not reuse free base name", next, err)
	}
	other, err := OpenRegistry(filepath.Join(t.TempDir(), "other.sqlite"), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	independent, err := other.Create(ctx, "independent", "")
	if err != nil || !slices.Contains(kitchenNames[:], independent.Name) {
		t.Fatal("another Space inherited occupied names", independent, err)
	}
}
