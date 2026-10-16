package instance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootExclusiveAndStableIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity := r.Identity
	if _, err = Open(path); err == nil {
		t.Fatal("opened active root")
	}
	r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Identity != identity {
		t.Fatal("replaced identity")
	}
}
func TestOldDataPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old")
	os.Mkdir(path, 0700)
	old := filepath.Join(path, "control.sqlite")
	os.WriteFile(old, []byte("old data"), 0600)
	if _, err := Open(path); err == nil {
		t.Fatal("adopted unrecognized data")
	}
	body, _ := os.ReadFile(old)
	if string(body) != "old data" {
		t.Fatal("changed old data")
	}
}

func TestRootCanonicalizesParentAliasesAndRejectsSymlinkRoot(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "parent-alias")
	if err = os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	r, err := Open(filepath.Join(alias, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Path != filepath.Join(parent, "data") {
		t.Fatalf("noncanonical ownership path: %s", r.Path)
	}
	if _, err = Open(r.Path); err == nil {
		t.Fatal("canonical path bypassed the active root lock")
	}
	leaf := filepath.Join(t.TempDir(), "root-alias")
	if err = os.Symlink(r.Path, leaf); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(leaf); err == nil {
		t.Fatal("accepted a symlink as the data root")
	}
}
