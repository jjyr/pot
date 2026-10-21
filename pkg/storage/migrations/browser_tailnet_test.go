package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetireBrowserTailnetPreservesCredentialsAndInactiveKeys(t *testing.T) {
	for _, settings := range []string{`{"connected":true,"enabled":true}`, `{"connected":false,"enabled":false}`, `{"connected":true,"enabled":false}`, `{"connected":false,"enabled":true}`} {
		root := t.TempDir()
		path := filepath.Join(root, "access-settings.json")
		if err := os.WriteFile(path, []byte(settings), 0600); err != nil {
			t.Fatal(err)
		}
		preserved := []string{filepath.Join(root, "browser-access.json"), filepath.Join(root, "tailscale", "tailscaled.state")}
		for _, file := range preserved {
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("preserved private state"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for attempt := 0; attempt < 2; attempt++ {
			if err := RetireBrowserTailnet(root); err != nil {
				t.Fatal(attempt, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("obsolete configuration survived", err)
			}
			for _, file := range preserved {
				data, err := os.ReadFile(file)
				if err != nil || string(data) != "preserved private state" {
					t.Fatal("unrelated state modified", err)
				}
			}
		}
	}
}

func TestRetireBrowserTailnetFailsWithoutRemovingUnrecognizedData(t *testing.T) {
	for _, raw := range []string{`invalid`, `null`, `{"unknown":true}`, `{"connected":"yes"}`, `{"enabled":null}`} {
		root := t.TempDir()
		path := filepath.Join(root, "access-settings.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := RetireBrowserTailnet(root); err == nil {
			t.Fatal("unrecognized data removed")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != raw {
			t.Fatal("failed migration modified input", err)
		}
		if err = os.WriteFile(path, []byte(`{"connected":true,"enabled":true}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err = RetireBrowserTailnet(root); err != nil {
			t.Fatal("repaired configuration could not be retried", err)
		}
	}
}

func TestRetireBrowserTailnetRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "private.json")
	const raw = `{"connected":true,"enabled":true}`
	if err := os.WriteFile(target, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "access-settings.json")); err != nil {
		t.Fatal(err)
	}
	if err := RetireBrowserTailnet(root); err == nil {
		t.Fatal("retired settings symlink accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != raw {
		t.Fatal("symlink target modified", err)
	}
}
