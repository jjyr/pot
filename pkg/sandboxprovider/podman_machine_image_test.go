package sandboxprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestPinnedMachineImageCacheRevalidatesBeforeUse(t *testing.T) {
	data := []byte("reviewed compressed machine disk")
	digest := sha256.Sum256(data)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write(data)
	}))
	defer server.Close()
	source := machineImageArtifact{URL: server.URL, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		path, err := fetchPodmanMachineImage(context.Background(), root, source, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Fatalf("image=%q error=%v", got, err)
		}
		if round == 1 {
			// Same length corruption must be repaired, not trusted by file size.
			if err = os.WriteFile(path, make([]byte, len(data)), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("expected initial and corruption-repair transfers, got %d", calls.Load())
	}
}

func TestPinnedMachineImageRefusesCorruptionAndSymlinks(t *testing.T) {
	digest := sha256.Sum256([]byte("good"))
	source := machineImageArtifact{SHA256: hex.EncodeToString(digest[:]), Size: 4}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("evil")) }))
	defer server.Close()
	source.URL = server.URL
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fetchPodmanMachineImage(context.Background(), root, source, server.Client()); err == nil {
		t.Fatal("corrupt disk accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed transfer left partial files: %v %v", entries, err)
	}
	outside := filepath.Join(t.TempDir(), "disk")
	if err = os.WriteFile(outside, []byte("good"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, "machine-os-"+source.SHA256+".raw.zst")); err != nil {
		t.Fatal(err)
	}
	if _, err = fetchPodmanMachineImage(context.Background(), root, source, server.Client()); err == nil {
		t.Fatal("symlink cache accepted")
	}
}
