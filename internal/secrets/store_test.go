package secrets

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEncryptedRestartImmutableAADAndWrongKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	key, _ := NewMemoryKey(bytes.Repeat([]byte{7}, 32))
	store, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("never-write-this-credential-plaintext")
	if err = store.Put(ctx, "provider/v1", secret); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "provider/v1", secret); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "provider/v1", []byte("replacement")); err == nil {
		t.Fatal("immutable reference replaced")
	}
	store.Close()
	file, err := os.ReadFile(path)
	if err != nil || bytes.Contains(file, secret) {
		t.Fatal("plaintext persisted", err)
	}
	store, err = Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value, err := store.Get(ctx, "provider/v1")
	if err != nil || !bytes.Equal(value, secret) {
		t.Fatal(err)
	}
	wrong, _ := NewMemoryKey(bytes.Repeat([]byte{8}, 32))
	other, err := Open(path, wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Get(ctx, "provider/v1"); err == nil {
		t.Fatal("wrong key accepted")
	}
	other.Close()
	if _, err = store.db.Exec("UPDATE secrets SET id='provider/v2'"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(ctx, "provider/v2"); err == nil {
		t.Fatal("reference AAD tampering accepted")
	}
}
func TestFreshNonceAndLostKeyNeverRegenerated(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	keyPath := filepath.Join(root, "secrets", "master.key")
	store, err := Open(filepath.Join(root, "control.sqlite"), FileKey{Path: keyPath, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Get(ctx, "missing"); err == nil {
		t.Fatal("missing secret succeeded")
	}
	if _, err = os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("read generated key", err)
	}
	for _, id := range []string{"one", "two"} {
		if err = store.Put(ctx, id, []byte("same")); err != nil {
			t.Fatal(err)
		}
	}
	var different bool
	if err = store.db.QueryRow("SELECT (SELECT nonce FROM secrets WHERE id='one') != (SELECT nonce FROM secrets WHERE id='two')").Scan(&different); err != nil || !different {
		t.Fatal("nonce reused", err)
	}
	if err = os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(ctx, "three", []byte("lost")); err == nil {
		t.Fatal("missing master key regenerated")
	}
	if _, err = os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("lost key recreated", err)
	}
}
func TestConcurrentKeyPublicationAndPermissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keys", "master.key")
	source := FileKey{Path: path, Create: true}
	var wg sync.WaitGroup
	keys := make(chan []byte, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := source.Key(ctx, true)
			if err != nil {
				failures <- err
			} else {
				keys <- key
			}
		}()
	}
	wg.Wait()
	close(keys)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var first []byte
	for key := range keys {
		if first == nil {
			first = key
		} else if !bytes.Equal(first, key) {
			t.Fatal("initializers published different keys")
		}
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{path, 0600}, {filepath.Dir(path), 0700}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatal(item, info, err)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Key(ctx, true); err == nil {
		t.Fatal("public key file accepted")
	}
	os.Chmod(path, 0600)
	os.WriteFile(path, []byte("short"), 0600)
	if _, err := source.Key(ctx, true); err == nil {
		t.Fatal("invalid key replaced")
	}
}
func TestExplicitFileAndUnavailableSourceHaveNoFallback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "absent")
	t.Setenv("POT_SECRET_KEY_FILE", path)
	if _, err := (SystemKey{Account: "fixture-only"}).Key(ctx, true); err == nil {
		t.Fatal("explicit missing key generated")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{9}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := (SystemKey{Account: "fixture-only"}).Key(ctx, true)
	if err != nil || len(key) != 32 {
		t.Fatal(err)
	}
}
