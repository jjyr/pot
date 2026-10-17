package productinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadUsesFixedBoundedPlainTextNotices(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"LICENSE.txt", "GoDependencyLicenses.txt", "FrontendFontLicense.txt", "FrontendIconLicense.txt", "ModelDirectoryLicense.txt", "node/LICENSE"} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte("<script>plain text notice</script>"), 0600)
	}
	os.WriteFile(filepath.Join(root, "not-a-notice"), []byte("must not be read"), 0600)
	value, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if !value.NoticesAvailable || len(value.Licenses) != 6 || !strings.Contains(value.Licenses[0].Text, "<script>") {
		t.Fatalf("notice projection %+v", value)
	}
	os.Remove(filepath.Join(root, "GoDependencyLicenses.txt"))
	value, err = Read(root)
	if err != nil || value.NoticesAvailable {
		t.Fatal("missing notices presented as complete")
	}
	os.Symlink(filepath.Join(root, "not-a-notice"), filepath.Join(root, "GoDependencyLicenses.txt"))
	if _, err = Read(root); err == nil {
		t.Fatal("notice symlink accepted")
	}
	os.Remove(filepath.Join(root, "GoDependencyLicenses.txt"))
	f, err := os.Create(filepath.Join(root, "GoDependencyLicenses.txt"))
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(8*1024*1024 + 1)
	f.Close()
	if _, err = Read(root); err == nil {
		t.Fatal("unbounded notice accepted")
	}
}
