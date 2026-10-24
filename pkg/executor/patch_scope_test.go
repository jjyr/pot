//go:build linux

package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPatchSelectsEachFileBeforeMutation(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale second file", true: "mixed grants"}[valid], func(t *testing.T) {
			exact, original := inplaceFixture(t)
			atomic, atomicOriginal := inplaceFixture(t)
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(filepath.Dir(exact), alias); err != nil {
				t.Fatal(err)
			}
			scope, _ := patchScope(t.TempDir(), []string{exact, filepath.Dir(atomic)})
			old := "original contents"
			if !valid {
				old = "stale contents"
			}
			patch := "*** Begin Patch\n*** Update File: " + filepath.Join(alias, filepath.Base(exact)) + "\n@@\n-original contents\n+replacement\n" +
				"*** Update File: " + atomic + "\n@@\n-" + old + "\n+replacement\n*** End Patch"
			_, err := applyPatch(filepath.Dir(exact), patch, scope)
			if (err == nil) != valid {
				t.Fatal("unexpected preflight result", err)
			}
			for path, before := range map[string]*patchOriginal{exact: original, atomic: atomicOriginal} {
				want := "original contents\n"
				if valid {
					want = "replacement\n"
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Fatal("unexpected contents", path, string(data), err)
				}
				after, err := os.Stat(path)
				if err != nil || os.SameFile(before.file, after) != (!valid || path == exact) {
					t.Fatal("unexpected write strategy", path, err)
				}
			}
		})
	}
}
