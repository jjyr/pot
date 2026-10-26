package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/internal/secrets"
)

func TestLocalMessageFilesShareOrdinaryFilesAcrossPolicies(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "local-files", Name: "Files"})
	other, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "other-files", Name: "Other"})
	root, _ := s.ExecutionWorkspace(a.ID)
	outside := filepath.Join(t.TempDir(), "source.go")
	writeTaskFixture(t, outside, "package example\n// synthetic source\n")
	writeTaskFixture(t, filepath.Join(root, "inside.txt"), "inside")
	foreign := filepath.Join(s.Registry.Directory(other.ID), ".ordinary-config")
	writeTaskFixture(t, foreign, "ordinary sibling file")
	if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"ask", "allow"} {
		if err := s.Permissions.Configure(s.options.RuntimeID, MachinePolicy{ExecutionAccess: policy}); err != nil {
			t.Fatal(err)
		}
		before, _, _ := s.Permissions.Policy(s.options.RuntimeID)
		s.bindMessageFiles(a.ID, "", "run", policy, fmt.Sprintf("[outside](%s:2)\n\n[inside](inside.txt)\n\n[alias](alias)\n\n[sibling](%s)", outside, foreign), root)
		for _, ref := range awaitFiles(t, s, a.ID, policy) {
			if ref.State != "ready" {
				t.Fatalf("policy %s: %+v", policy, ref)
			}
		}
		after, _, _ := s.Permissions.Policy(s.options.RuntimeID)
		if before != after {
			t.Fatal("sharing changed command policy")
		}
	}
	source := awaitFiles(t, s, a.ID, "ask")[0].Source
	source.Node = "another-node"
	if err := s.Resources.BindMessage(ctx, a.ID, "main", "remote-absolute", "[source]("+outside+")", source); err != nil {
		t.Fatal(err)
	}
	if got := awaitFiles(t, s, a.ID, "remote-absolute")[0].State; got != "source_offline" {
		t.Fatal("remote execution was mapped to the local path", got)
	}

}

func TestLocalMessageFileRejectsMutationDuringRead(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.txt")
	writeTaskFixture(t, path, "original")
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, _ := root.Stat("source.txt")
	revision, _ := taskFileRevision(info)
	data, err := readBoundRoot(t.Context(), root, "source.txt", 1024, revision, func(os.FileInfo) error {
		// The descriptor is open and its initial revision has been captured;
		// mutate before its bytes are read to exercise the post-read fence.
		return os.WriteFile(path, []byte("mutated during read"), 0600)
	})
	if !errors.Is(err, errFileChanged) || len(data) != 0 {
		t.Fatal("unstable file bytes escaped the reader", err)
	}
}

func TestMessageAuthenticationSystemKeyUsesExactConfiguredIdentity(t *testing.T) {
	key := filepath.Join(t.TempDir(), "nonstandard-key-name")
	writeTaskFixture(t, key, "synthetic key")
	t.Setenv("POT_SECRET_KEY_FILE", key)
	s := &Service{options: Options{Root: t.TempDir(), Keys: secrets.SystemKey{Account: "test"}}}
	info, _ := os.Stat(key)
	if err := s.messageAuthenticationGuard()(key, info); !errors.Is(err, errFileForbidden) {
		t.Fatal("configured SystemKey was not excluded", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := filepath.Join(s.options.Root, "machines.sqlite") + suffix
		writeTaskFixture(t, path, "synthetic node-key container")
		info, _ := os.Stat(path)
		if err := s.messageAuthenticationGuard()(path, info); !errors.Is(err, errFileForbidden) {
			t.Fatal("node-key container was not excluded", suffix, err)
		}
	}
}

func TestLocalMessageFilesAuthenticationAliasesAndDependencies(t *testing.T) {
	s, _ := configurationService(t)
	ctx := context.Background()
	a, _ := s.CreateAssistant(ctx, CreateAssistant{RequestID: "auth-files", Name: "Files"})
	root, _ := s.ExecutionWorkspace(a.ID)
	// All authentication bytes in this test are synthetic. Never inspect host
	// credentials to exercise a classification rule.
	key := filepath.Join(t.TempDir(), "configured-key")
	writeTaskFixture(t, key, "synthetic authentication material")
	s.options.Keys = secrets.FileKey{Path: key}
	endpoint := filepath.Join(s.options.Root, "runtime.json")
	writeTaskFixture(t, endpoint, `{"token":"synthetic-only"}`)
	if err := os.Symlink(key, filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(key), filepath.Join(root, "directory-alias")); err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, filepath.Join(root, "auth.html"), `<script src="symlink"></script>`)
	writeTaskFixture(t, filepath.Join(root, "ordinary.key"), "ordinary documentation, not a managed key")
	s.bindMessageFiles(a.ID, "", "run", "auth", fmt.Sprintf("[key](%s)\n\n[endpoint](%s)\n\n[symlink](symlink)\n\n[hardlink](hardlink)\n\n[parent](directory-alias/configured-key)\n\n[html](auth.html)\n\n[ordinary](ordinary.key)\n\n[identity](%s)", key, endpoint, filepath.Join(s.options.Root, "machines.sqlite")), root)
	for i, ref := range awaitFiles(t, s, a.ID, "auth") {
		want := "forbidden"
		if i == 6 {
			want = "ready"
		}
		if ref.State != want || (want == "forbidden" && ref.Resource != nil) {
			t.Fatalf("reference %d: %+v", i, ref)
		}
	}
	// Check the descriptor itself when a safe filename is replaced with a link
	// to a known credential between classification and opening.
	info, _ := os.Stat(key)
	revision, _ := taskFileRevision(info)
	disk, err := os.OpenRoot(filepath.Dir(key))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	guard := s.messageAuthenticationGuard()
	data, err := readBoundRoot(ctx, disk, filepath.Base(key), 1024, revision, func(info os.FileInfo) error {
		return guard("innocent-alias", info)
	})
	if !errors.Is(err, errFileForbidden) || len(data) != 0 {
		t.Fatal("credential descriptor read", err)
	}
	if err := os.Rename(key, key+"-old"); err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, key, "replacement synthetic key")
	if err := guard(key+"-old", info); !errors.Is(err, errFileForbidden) {
		t.Fatal("credential replacement freed its prior identity", err)
	}
}

func TestLocalMessageFilesRejectSpecialAndOversizeExternalFiles(t *testing.T) {
	s, _ := configurationService(t)
	a, _ := s.CreateAssistant(t.Context(), CreateAssistant{RequestID: "special-files", Name: "Files"})
	root, _ := s.ExecutionWorkspace(a.ID)
	directory := t.TempDir()
	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(directory, "large")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(resources.FileLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s.bindMessageFiles(a.ID, "", "run", "special", fmt.Sprintf("[fifo](%s)\n\n[directory](%s)\n\n[large](%s)", fifo, directory, large), root)
	for i, ref := range awaitFiles(t, s, a.ID, "special") {
		want := "forbidden"
		if i == 2 {
			want = "too_large"
		}
		if ref.State != want {
			t.Fatalf("reference %d: %+v", i, ref)
		}
	}
}
