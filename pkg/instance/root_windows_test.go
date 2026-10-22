package instance

import (
	"path/filepath"
	"testing"
)

func TestWindowsRootLockAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private root")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := r.Identity
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("duplicate Runtime acquired root")
	}
	r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Identity != id {
		t.Fatal("Runtime identity changed on restart")
	}
	if err = WriteJSON(filepath.Join(path, "state.json"), map[string]int{"generation": 1}); err != nil {
		t.Fatal(err)
	}
	if err = WriteJSON(filepath.Join(path, "state.json"), map[string]int{"generation": 2}); err != nil {
		t.Fatal(err)
	}
}
