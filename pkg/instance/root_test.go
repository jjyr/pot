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

func TestReleasedIdentityRemainsUnchanged(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "identity.json")
	const original = "{\"format\":\"pot-go\",\"version\":1,\"runtime_id\":\"released-node\"}\n"
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if root.Identity.RuntimeID != "released-node" {
		t.Fatal("changed node identity")
	}
	contents, err := os.ReadFile(file)
	if err != nil || string(contents) != original {
		t.Fatal("rewrote released identity", err)
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

func TestRootInitializesAroundPreinstalledExtensions(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skills", "plugins"} {
		if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "mcp.json"), []byte(`{"version":1,"servers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	const instructions = "Existing global instructions"
	if err := os.WriteFile(filepath.Join(path, "AGENTS.md"), []byte(instructions), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := os.Stat(filepath.Join(path, "mcp.json")); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(path, "AGENTS.md")); err != nil || string(raw) != instructions {
		t.Fatal("initialization changed global instructions", string(raw), err)
	}
}
