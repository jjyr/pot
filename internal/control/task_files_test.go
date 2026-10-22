package control

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskChunksBoundMemoryPreserveRevisionAndConfineLinks(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	root := filepath.Join(s.options.Workspace, "tasks", "worker")
	name := filepath.Join(root, "large.bin")
	writeTaskFixture(t, name, "")
	bindTestWorkspace(t, s, "worker", root)
	file, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(32 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt([]byte("tail"), 32*1024*1024-4); err != nil {
		t.Fatal(err)
	}
	file.Close()
	first, err := s.localTaskFileChunk(ctx, "worker", "large.bin", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(first.Data)
	if err != nil || uint64(len(data)) != TaskFileChunkSize || first.Size != 32*1024*1024 {
		t.Fatalf("chunk: %+v %v", first, err)
	}
	last, err := s.localTaskFileChunk(ctx, "worker", "large.bin", 32*1024*1024-4, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = base64.StdEncoding.DecodeString(last.Data)
	if string(data) != "tail" {
		t.Fatal("wrong random access chunk")
	}
	// Same length mutation still invalidates the accepted revision.
	file, _ = os.OpenFile(name, os.O_RDWR, 0600)
	file.WriteAt([]byte("edit"), 0)
	file.Close()
	if _, err = s.localTaskFileChunk(ctx, "worker", "large.bin", TaskFileChunkSize, first.Revision); err == nil {
		t.Fatal("changed file accepted")
	}
	outside := filepath.Join(t.TempDir(), "private")
	writeTaskFixture(t, outside, "secret")
	if err = os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"escape", "../../control.sqlite"} {
		if _, err = s.localTaskFileChunk(ctx, "worker", path, 0, ""); err == nil {
			t.Fatal("escaped original workspace")
		}
	}
}
