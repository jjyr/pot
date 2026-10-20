package serverconfig

import (
	"github.com/jjyr/pot/pkg/instance"
	"os"
	"path/filepath"
	"testing"
)

func TestServerSettingsRemainPrivateAndRequireAbsoluteDirectories(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	initialized, err := instance.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	initialized.Close()
	settings := Settings{Version: 1, Workspace: "/tmp/workspace", Resources: "/tmp/resources"}
	if err := Save(root, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded != settings {
		t.Fatal(loaded, err)
	}
	info, _ := os.Stat(filepath.Join(root, "server.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("settings not private")
	}
	settings.Workspace = "relative"
	if err = Save(root, settings); err == nil {
		t.Fatal("relative directory accepted")
	}
	os.Chmod(filepath.Join(root, "server.json"), 0644)
	if _, err = Load(root); err == nil {
		t.Fatal("public settings accepted")
	}
}
