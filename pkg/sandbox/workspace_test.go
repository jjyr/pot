package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionsResolveGuestSymlinksWithoutHostEscape(t *testing.T) {
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, "project/sub"), 0700); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("rules\n", 20000)
	if err := os.WriteFile(filepath.Join(host, "project/AGENTS.md"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"relative": "../AGENTS.md", "absolute": "/data/workspace/project/AGENTS.md",
		"host": filepath.Join(host, "project/AGENTS.md"), "escape": "../../../../etc/passwd", "loop": "loop",
	} {
		if err := os.Symlink(target, filepath.Join(host, "project/sub", name)); err != nil {
			t.Fatal(err)
		}
	}
	view, err := (WorkspacePaths{HostRoot: host, GuestRoot: "/data/workspace"}).OpenInstructions()
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, name := range []string{"relative", "absolute"} {
		data, err := view.ReadFile("/data/workspace/project/sub/" + name)
		if err != nil || string(data) != text {
			t.Fatalf("%s: len=%d err=%v", name, len(data), err)
		}
	}
	for _, name := range []string{"host", "escape"} {
		if _, err := view.ReadFile("/data/workspace/project/sub/" + name); !errors.Is(err, ErrOutsideWorkspace) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := view.ReadFile("/data/workspace/project/sub/loop"); err == nil {
		t.Fatal("symlink loop accepted")
	}
	if _, err := view.Stat("/etc/passwd"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatal(err)
	}
}

func TestHostMappingsRejectAmbiguousMounts(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMappings([]HostMapping{{Source: source, Destination: "/mnt/host/source", ReadOnly: true}}); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"/mnt/host", "/mnt/host/source/../escape", "/data/workspace", "/mnt/host/x:y"} {
		if err := ValidateMappings([]HostMapping{{Source: source, Destination: dest}}); err == nil {
			t.Fatalf("accepted %s", dest)
		}
	}
	if err := ValidateMappings([]HostMapping{{Source: source, Destination: "/mnt/host/a"}, {Source: source, Destination: "/mnt/host/a/b"}}); err == nil {
		t.Fatal("overlap accepted")
	}
}
