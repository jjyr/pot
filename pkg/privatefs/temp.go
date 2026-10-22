package privatefs

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// CreateTemp creates a new private file even when the parent has a broad ACL.
// Windows mode bits alone cannot provide the Unix CreateTemp privacy guarantee.
func CreateTemp(dir, prefix string) (*os.File, error) {
	if filepath.Base(prefix) != prefix {
		return nil, errors.New("invalid private temporary file prefix")
	}
	for range 100 {
		name := filepath.Join(dir, prefix+rand.Text())
		file, err := Open(name, os.O_CREATE|os.O_EXCL|os.O_RDWR)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return file, err
	}
	return nil, errors.New("private temporary file name exhausted")
}
