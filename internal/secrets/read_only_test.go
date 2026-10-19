package secrets

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlySecretsRejectMutationsAndMissingKeyCreation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path, keyPath := filepath.Join(root, "control.sqlite"), filepath.Join(root, "master.key")
	store, err := Open(path, FileKey{Path: keyPath, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "owner", []byte("existing-token")); err != nil {
		t.Fatal(err)
	}
	store.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err = OpenReadOnly(path, FileKey{Path: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value, err := store.Get(ctx, "owner")
	if err != nil || string(value) != "existing-token" {
		t.Fatal("could not read existing credential", err)
	}
	clear(value)
	if err = store.Put(ctx, "another", []byte("new-token")); err == nil {
		t.Fatal("read-only secrets permitted Put")
	}
	if err = store.Delete(ctx, "owner"); err == nil {
		t.Fatal("read-only secrets permitted Delete")
	}
	store.Close()
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only secrets changed the database", err)
	}
	if err = os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	store, err = OpenReadOnly(path, FileKey{Path: keyPath, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Get(ctx, "owner"); err == nil {
		t.Fatal("read-only secret read regenerated a missing key")
	}
	if _, err = os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("read-only secret read wrote a key", err)
	}
	// Even Put on an empty read-only store cannot provision a master key before
	// SQLite rejects its write.
	emptyPath := filepath.Join(root, "empty.sqlite")
	empty, err := Open(emptyPath, FileKey{Path: keyPath, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	empty.Close()
	empty, err = OpenReadOnly(emptyPath, FileKey{Path: keyPath, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if err = empty.Put(ctx, "another", []byte("new-token")); err == nil {
		t.Fatal("empty read-only store permitted Put")
	}
	if _, err = os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("read-only Put provisioned a master key", err)
	}
}
