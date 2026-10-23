package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPatchPreflightMoveModeAndEscape(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside := t.TempDir()
	if _, err := ApplyPatch(root, "*** Begin Patch\n*** Add File: a.txt\n+one\n+two\n*** End Patch"); err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(root, "a.txt"), 0750)
	if _, err := ApplyPatch(root, "*** Begin Patch\n*** Update File: a.txt\n*** Move to: dir/b.txt\n@@\n one\n-two\n+三\n*** End of File\n*** End Patch"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "dir/b.txt"))
	if err != nil || string(data) != "one\n三\n" {
		t.Fatalf("%q %v", data, err)
	}
	info, _ := os.Stat(filepath.Join(root, "dir/b.txt"))
	if info.Mode().Perm() != 0750 {
		t.Fatal("lost executable mode")
	}
	if _, err := ApplyPatch(root, "*** Begin Patch\n*** Add File: should-not-exist\n+new\n*** Update File: dir/b.txt\n@@\n-missing\n+replacement\n*** End Patch"); err == nil {
		t.Fatal("accepted bad context")
	}
	if _, err := os.Stat(filepath.Join(root, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("mutated before validation")
	}
	// ApplyPatch itself runs under the caller-selected filesystem boundary.
	external := filepath.Join(outside, "external.txt")
	if _, err := ApplyPatch(root, "*** Begin Patch\n*** Add File: "+external+"\n+allowed\n*** End Patch"); err != nil {
		t.Fatal("host patch rejected an external path", err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "allowed\n" {
		t.Fatal(string(data), err)
	}
	link := filepath.Join(root, "link")
	os.Symlink(external, link)
	if _, err := ApplyPatch(root, "*** Begin Patch\n*** Delete File: "+link+"\n*** End Patch"); err == nil {
		t.Fatal("patched symlink itself")
	}
	if _, err := ApplyPatch(root, "*** Begin Patch\n*** Delete File: dir/b.txt\n*** End Patch"); err != nil {
		t.Fatal(err)
	}
}

func TestHostPatchRetainsFileOnlyPermissionFallback(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if _, err := ApplyPatch(dir, "*** Begin Patch\n*** Update File: file\n@@\n-old\n+new\n*** End Patch"); err != nil {
		t.Fatal("host lost its original file-only fallback", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("fallback replaced the inode", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new\n" {
		t.Fatal("fallback did not update contents", string(data), err)
	}
}
