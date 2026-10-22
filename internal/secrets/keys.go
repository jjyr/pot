package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/privatefs"
	"io"
	"os"
	"path/filepath"
)

// FileKey accepts an explicit owner-only raw key file. Creation is opt-in and is
// used only for the platform default, never an explicit POT_SECRET_KEY_FILE.
type FileKey struct {
	Path   string
	Create bool
}

func (k FileKey) Key(ctx context.Context, allowCreate bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := readFile(k.Path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !k.Create || !allowCreate {
		return nil, errors.New("secret master key is missing; restore the original key")
	}
	parent := filepath.Dir(k.Path)
	if err := privatefs.Mkdir(parent); err != nil {
		return nil, err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || privatefs.Check(parent) != nil {
		return nil, errors.New("secret key directory must be owner-only")
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	defer clear(key)
	temporary, err := os.CreateTemp(parent, ".master-key-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err = temporary.Write(key); err != nil {
		return nil, err
	}
	if err = temporary.Sync(); err != nil {
		return nil, err
	}
	if err = temporary.Close(); err != nil {
		return nil, err
	}
	// Hard-link publication is atomic and cannot replace a concurrent winner.
	if err = os.Link(temporary.Name(), k.Path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err = privatefs.SyncDir(parent); err != nil {
		return nil, err
	}
	return readFile(k.Path)
}
func readFile(path string) ([]byte, error) {
	file, err := privatefs.Open(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("secret key file must be regular")
	}
	if err = privatefs.CheckFile(file); err != nil {
		return nil, err
	}
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil {
		clear(key)
		return nil, err
	}
	if len(key) != 32 {
		clear(key)
		return nil, errors.New("secret key file must contain exactly 32 raw bytes")
	}
	return key, nil
}

type SystemKey struct{ Account string }

func (k SystemKey) Key(ctx context.Context, allowCreate bool) ([]byte, error) {
	if path, ok := os.LookupEnv("POT_SECRET_KEY_FILE"); ok {
		return FileKey{Path: path}.Key(ctx, false)
	}
	if k.Account == "" {
		return nil, fmt.Errorf("Control secret key account is required")
	}
	return platformKey(ctx, k.Account, allowCreate)
}
