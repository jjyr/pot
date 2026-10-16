package webui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessStoreRotationCannotBeOverwrittenByStaleLogout(t *testing.T) {
	store := AccessStore{Path: filepath.Join(t.TempDir(), "access.json")}
	original, err := store.Load(true)
	if err != nil {
		t.Fatal(err)
	}
	original.Revoked = map[string]int64{"logged-out": 922337203685477000}
	if err = store.SaveSessions(original); err != nil {
		t.Fatal(err)
	}
	// Merge independent revocations without a read/write lost update.
	other := original
	other.Revoked = map[string]int64{"other": 922337203685477000}
	if err = store.SaveSessions(other); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.Load(false)
	if len(loaded.Revoked) != 2 {
		t.Fatal("revocation lost")
	}
	rotated, err := store.Change("regenerate-token")
	if err != nil || rotated.Token == original.Token || rotated.Generation != original.Generation+1 {
		t.Fatal(rotated.Generation, err)
	}
	if err = store.SaveSessions(original); err == nil {
		t.Fatal("stale logout replaced newer credentials")
	}
	disabled, err := store.Change("disable")
	if err != nil || !disabled.Disabled {
		t.Fatal(err)
	}
	enabled, err := store.Change("enable")
	if err != nil || enabled.Disabled || enabled.Generation != disabled.Generation+1 {
		t.Fatal(err)
	}
	if err = os.Chmod(store.Path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(false); err == nil {
		t.Fatal("public credentials accepted")
	}
}
