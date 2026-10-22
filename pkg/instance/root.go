// Package instance establishes exclusive ownership of a fresh Go data root.
// It never imports or removes the previous release's data.
package instance

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/privatefs"
	"os"
	"path/filepath"
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
	if filepath.Dir(abs) == abs {
		return nil, errors.New("invalid data root")
	}
	if err = privatefs.Mkdir(abs); err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || privatefs.Check(abs) != nil {
		return nil, errors.New("Go data root must be a private directory")
	}
	// Resolve parent aliases such as macOS /tmp before deriving machine ownership
	// and mount identities. The root itself must still be a real directory above.
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	lock, err := privatefs.Lock(filepath.Join(abs, "runtime.lock"), false, true)
	if err != nil {
		return nil, errors.New("data root already belongs to a running Runtime or lock is not private: " + err.Error())
	}
	fail := func(err error) (*Root, error) { lock.Close(); return nil, err }
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
	file, err := privatefs.CreateTemp(dir, ".pot-state-")
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
	if err = privatefs.Replace(name, path); err != nil {
		return err
	}
	return privatefs.SyncDir(dir)
}
