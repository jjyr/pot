package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserRootNeverSilentlyChangesControl(t *testing.T) {
	root := t.TempDir()
	if err := pinBrowserControl(root, "control-one"); err != nil {
		t.Fatal(err)
	}
	if err := pinBrowserControl(root, "control-one"); err != nil {
		t.Fatal(err)
	}
	if err := pinBrowserControl(root, "control-two"); err == nil {
		t.Fatal("browser silently rebound to another Control")
	}
	if err := os.Chmod(filepath.Join(root, "browser-control.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := pinBrowserControl(root, "control-one"); err == nil {
		t.Fatal("public Control binding accepted")
	}
}
