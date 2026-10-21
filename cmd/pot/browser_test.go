package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAccessCommandRejectsEmbeddedNetworkActions(t *testing.T) {
	for _, action := range []string{"connect", "disconnect"} {
		var output bytes.Buffer
		if err := accessCommand(context.Background(), []string{"-browser-root", t.TempDir(), action}, &output); err == nil {
			t.Fatal("retired network action accepted", action)
		}
	}
}

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
