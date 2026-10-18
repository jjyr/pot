package systemnotifications

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDevicePreferenceAndClaimsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Enabled() {
		t.Fatal("new clients must default on")
	}
	if err = store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Claim("control-a/owner/reply/1"); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Enabled() {
		t.Fatal("disabled preference lost")
	}
	if ok, err := store.Claim("control-a/owner/reply/1"); ok || err != nil {
		t.Fatalf("replayed event: %v %v", ok, err)
	}
	if ok, err := store.Claim("control-b/owner/reply/1"); !ok || err != nil {
		t.Fatal("Control identities must remain independent")
	}
	if err = store.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil || !store.Enabled() {
		t.Fatal("enabled preference lost", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("notification state must be private", err)
	}
}
func TestWindowsClaimOneEventConcurrently(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			ok, err := store.Claim("same-event")
			if err != nil {
				t.Error(err)
			}
			if ok {
				count.Add(1)
			}
		})
	}
	workers.Wait()
	if count.Load() != 1 {
		t.Fatalf("got %d claims", count.Load())
	}
}
func TestFailedPreferenceSaveDoesNotChangeMemory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "missing", "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetEnabled(false); err == nil {
		t.Fatal("expected persistence error")
	}
	if !store.Enabled() {
		t.Fatal("failed save changed active preference")
	}
	if ok, err := store.Claim("event"); ok || err == nil {
		t.Fatal("failed persistence must not claim an event")
	}
}
