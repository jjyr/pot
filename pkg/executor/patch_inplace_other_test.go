//go:build !linux

package executor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNonLinuxPatchKeepsOriginalStrategy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	write := []string{path, dir, path + "-missing"}
	scope, filtered := patchScope(dir, write)
	if scope != nil || !reflect.DeepEqual(filtered, write) {
		t.Fatal("non-Linux launch changed write grants", scope, filtered)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// The original atomic writer replaces this inode instead of rejecting its
	// second hard link, even if private input supplies a Linux write scope.
	alias := filepath.Join(dir, "alias")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	var input, output bytes.Buffer
	err := json.NewEncoder(&input).Encode(patchWorkerRequest{
		Workspace:  dir,
		Patch:      "*** Begin Patch\n*** Update File: file\n@@\n-old\n+new\n*** End Patch",
		WriteScope: &patchWriteScope{Files: []string{path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ServePatchWorker(&input, &output); err != nil {
		t.Fatal(err)
	}
	var result struct{ Report, Error string }
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Error != "" {
		t.Fatal("non-Linux worker selected the Linux writer", result, err)
	}
	for name, want := range map[string]string{path: "new\n", alias: "old\n"} {
		if data, err := os.ReadFile(name); err != nil || string(data) != want {
			t.Fatal("original atomic behavior changed", name, string(data), err)
		}
	}
}
