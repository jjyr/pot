//go:build linux

package executor

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func inplaceFixture(t *testing.T) (string, *patchOriginal) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("original contents\n"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0750); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return path, &patchOriginal{file: info, parent: parent, data: []byte("original contents\n")}
}

func TestPatchInPlacePreservesIdentityAndMode(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "short": "short\n", "long": strings.Repeat("long\n", 100)} {
		t.Run(name, func(t *testing.T) {
			path, original := inplaceFixture(t)
			if err := writePatchInPlace(path, []byte(content), original); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || !os.SameFile(info, original.file) || info.Mode().Perm() != 0750 {
				t.Fatal("identity or mode changed", info, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != content {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestPatchInPlaceConcurrentWriters(t *testing.T) {
	path, original := inplaceFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			results <- writePatchInPlace(path, []byte("replacement\n"), original)
		})
	}
	close(start)
	workers.Wait()
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatal("expected one commit and one conflict", first, second)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement\n" {
		t.Fatal(string(data), err)
	}
}

func TestPatchInPlaceRejectsChangedTargets(t *testing.T) {
	for _, change := range []string{"contents", "file", "parent", "symlink", "hardlink", "mode", "locked", "missing", "directory"} {
		t.Run(change, func(t *testing.T) {
			path, original := inplaceFixture(t)
			other := filepath.Join(t.TempDir(), "other")
			if err := os.WriteFile(other, original.data, 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch change {
			case "contents":
				err = os.WriteFile(path, []byte("concurrent edit\n"), 0750)
			case "mode":
				err = os.Chmod(path, 0600)
			case "file":
				err = os.Rename(other, path)
			case "parent":
				err = os.Rename(filepath.Dir(path), filepath.Dir(path)+"-old")
				if err == nil {
					err = os.Mkdir(filepath.Dir(path), 0700)
				}
				if err == nil {
					err = os.WriteFile(path, original.data, 0750)
				}
			case "symlink", "missing", "directory":
				err = os.Remove(path)
				if err == nil && change == "symlink" {
					err = os.Symlink(other, path)
				}
				if err == nil && change == "directory" {
					err = os.Mkdir(path, 0700)
				}
			case "hardlink":
				err = os.Link(path, other+"-link")
			case "locked":
				var file *os.File
				file, err = os.OpenFile(path, os.O_RDWR, 0)
				if err == nil {
					t.Cleanup(func() { _ = file.Close() })
					err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if err := writePatchInPlace(path, []byte("replacement\n"), original); err == nil {
				t.Fatal("accepted changed target")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("rejected patch changed contents")
			}
			if change == "symlink" {
				data, err := os.ReadFile(other)
				if err != nil || string(data) != string(original.data) {
					t.Fatal("symlink target changed", err)
				}
			}
		})
	}
}

type failingPatchFile struct {
	*os.File
	operation string
	failed    bool
	repeated  bool
}

func (f *failingPatchFile) fail(operation string) bool {
	if f.operation != operation || (f.failed && !f.repeated) {
		return false
	}
	f.failed = true
	return true
}

func (f *failingPatchFile) WriteAt(data []byte, at int64) (int, error) {
	if f.fail("write") {
		n, _ := f.File.WriteAt(data[:len(data)/2], at)
		return n, io.ErrShortWrite
	}
	return f.File.WriteAt(data, at)
}

func (f *failingPatchFile) Truncate(size int64) error {
	if f.fail("truncate") {
		return errors.New("injected truncate error")
	}
	return f.File.Truncate(size)
}

func (f *failingPatchFile) Sync() error {
	if f.fail("sync") {
		return errors.New("injected sync error")
	}
	return f.File.Sync()
}

func TestPatchInPlaceRestoresOnIOFailure(t *testing.T) {
	for _, operation := range []string{"write", "truncate", "sync"} {
		for name, content := range map[string]string{"short": "short", "long": strings.Repeat("long", 30)} {
			t.Run(operation+"/"+name, func(t *testing.T) {
				path, original := inplaceFixture(t)
				file, err := os.OpenFile(path, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				err = replacePatchContents(&failingPatchFile{File: file, operation: operation}, []byte(content), original.data)
				if err == nil || !strings.Contains(err.Error(), "original contents restored") {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != string(original.data) {
					t.Fatal("failed patch damaged original", string(data), err)
				}
			})
		}
	}
}

func TestPatchInPlaceReportsRestorationFailure(t *testing.T) {
	path, original := inplaceFixture(t)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	err = replacePatchContents(&failingPatchFile{File: file, operation: "sync", repeated: true}, []byte("new"), original.data)
	if err == nil || !strings.Contains(err.Error(), "restoration failed; file may be partially modified") {
		t.Fatal(err)
	}
}

func TestPatchAtomicErrorsDoNotSwitchToInPlace(t *testing.T) {
	for _, failure := range []error{unix.EBUSY, unix.EACCES, unix.EROFS} {
		t.Run(failure.Error(), func(t *testing.T) {
			path, original := inplaceFixture(t)
			err := replacePatchAtomically(path, []byte("replacement\n"), 0750, func(from, to string) error {
				return &os.LinkError{Op: "rename", Old: from, New: to, Err: failure}
			}, false)
			if !errors.Is(err, failure) {
				t.Fatal("atomic failure not preserved", err)
			}
			info, err := os.Stat(path)
			if err != nil || !os.SameFile(info, original.file) {
				t.Fatal("failed atomic patch changed identity", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != string(original.data) {
				t.Fatal("failed atomic patch changed contents", string(data), err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatal("temporary file left behind", entries, err)
			}
		})
	}
}
