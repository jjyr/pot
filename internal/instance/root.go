// Package instance establishes exclusive ownership of a fresh Go data root.
// It never imports or removes the previous release's data.
package instance

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"os"
	"path/filepath"
	"syscall"
)

type Identity struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	RuntimeID string `json:"runtime_id"`
}
type Root struct {
	Path     string
	Identity Identity
	lock     *os.File
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pot-go"), nil
}
func Open(path string) (*Root, error) {
	if path == "" {
		return nil, errors.New("Go data root must be explicit")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if abs == "/" {
		return nil, errors.New("invalid data root")
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("Go data root must be a private directory")
	}
	fd, err := syscall.Open(filepath.Join(abs, "runtime.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "runtime.lock")
	fail := func(err error) (*Root, error) { lock.Close(); return nil, err }
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(errors.New("data root already belongs to a running Runtime"))
	}
	file := filepath.Join(abs, "identity.json")
	body, err := os.ReadFile(file)
	var identity Identity
	if os.IsNotExist(err) {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return fail(err)
		}
		for _, entry := range entries {
			if entry.Name() != "runtime.lock" {
				return fail(errors.New("unrecognized data root; choose an empty directory for the Go release"))
			}
		}
		identity = Identity{"pot-go", 1, rand.Text()}
		if err = WriteJSON(file, identity); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	} else if err = core.DecodeJSON(body, &identity); err != nil {
		return fail(err)
	}
	if identity.Format != "pot-go" || identity.Version != 1 || !core.Identifier(identity.RuntimeID) {
		return fail(errors.New("unsupported Go data-root identity"))
	}
	return &Root{abs, identity, lock}, nil
}
func (r *Root) Close() error {
	if r.lock == nil {
		return nil
	}
	err := r.lock.Close()
	r.lock = nil
	return err
}

// WriteJSON atomically publishes private state, then syncs its parent directory.
func WriteJSON(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".pot-state-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(body, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
