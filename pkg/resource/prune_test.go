package resource

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPruneDoesNotFollowCacheSymlink(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "cache"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	r := Resource{ReleaseTag: "sandbox-go-v1", Filename: "blob.bin", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 1, UnpackedBytes: 1, Format: Raw}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(s.Root(), r.SHA256)); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("prune followed symlink", err)
	}
}
