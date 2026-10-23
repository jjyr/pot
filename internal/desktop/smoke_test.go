//go:build darwin || (linux && desktop)

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSmokeRefusesImplicitOrExistingDataRoot(t *testing.T) {
	if err := RunSmoke(""); err == nil {
		t.Fatal("smoke must not select the product's default root")
	}
	root := t.TempDir()
	path := filepath.Join(root, "existing-state")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RunSmoke(root); err == nil {
		t.Fatal("smoke must refuse nonempty data roots")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "preserve" {
		t.Fatal("existing state changed", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("smoke initialized files in a nonempty data root", entries, err)
	}
}
