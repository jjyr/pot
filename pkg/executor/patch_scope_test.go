package executor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPatchScopeSelectsBeforeWriting(t *testing.T) {
	workspace := t.TempDir()
	path, original := inplaceFixture(t)
	outside := filepath.Dir(path)
	content := "replacement\n"
	for _, test := range []struct {
		name    string
		write   []string
		inPlace bool
	}{
		{name: "no grant"},
		{name: "exact file", write: []string{path}, inPlace: true},
		{name: "sibling file", write: []string{filepath.Join(outside, "sibling")}},
		{name: "nonexistent file", write: []string{path + "-missing"}},
		{name: "directory", write: []string{outside}},
		{name: "file then directory", write: []string{path, outside}},
		{name: "directory then file", write: []string{outside, path}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope, filtered := patchScope(workspace, test.write)
			edit := patchEdit{path: path, content: &content, original: original}
			if got := scope.inPlace(edit); got != test.inPlace {
				t.Fatalf("in-place=%v, want %v; scope=%+v", got, test.inPlace, scope)
			}
			if len(test.write) == 2 && !reflect.DeepEqual(filtered, []string{outside}) {
				t.Fatalf("redundant file mount retained: %v", filtered)
			}
			for _, edit := range []patchEdit{
				{path: path, content: &content},  // create
				{path: path, original: original}, // delete
				{path: path, content: &content, original: original, destination: path + "-moved"},
			} {
				if scope.inPlace(edit) {
					t.Fatal("directory mutation selected in-place")
				}
			}
		})
	}
	// The implicit workspace directory also wins over an exact file grant.
	scope, filtered := patchScope(outside, []string{path})
	if scope.inPlace(patchEdit{path: path, content: &content, original: original}) || len(filtered) != 0 {
		t.Fatal("workspace file did not retain atomic replacement", scope, filtered)
	}
	// Prefixes cannot turn a sibling directory into a writable ancestor.
	if patchWithin(outside+"-sibling/file", outside) {
		t.Fatal("directory prefix granted a sibling")
	}
}

func TestPatchScopeResolvesAncestorAliases(t *testing.T) {
	path, original := inplaceFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(path), alias); err != nil {
		t.Fatal(err)
	}
	scope, _ := patchScope(t.TempDir(), []string{path})
	content := "replacement\n"
	if !scope.inPlace(patchEdit{path: filepath.Join(alias, filepath.Base(path)), content: &content, original: original}) {
		t.Fatal("ancestor alias lost the exact file scope")
	}
}

func TestPatchStrategyPreservesSelectedSemantics(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(map[bool]string{false: "host atomic", true: "exact file in-place"}[exact], func(t *testing.T) {
			path, original := inplaceFixture(t)
			var scope patchWriteScope
			if exact {
				scope, _ = patchScope(t.TempDir(), []string{path})
			}
			patch := "*** Begin Patch\n*** Update File: " + path + "\n@@\n-original contents\n+replacement\n*** End Patch"
			if _, err := applyPatch(filepath.Dir(path), patch, scope); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil || os.SameFile(original.file, after) != exact {
				t.Fatal("selected strategy changed", after, err)
			}
		})
	}
}
