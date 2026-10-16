//go:build linux

package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func platformKey(ctx context.Context, _ string, allowCreate bool) ([]byte, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return nil, errors.New("HOME unavailable; configure POT_SECRET_KEY_FILE")
	}
	return (FileKey{Path: filepath.Join(home, ".pot/secrets/master.key"), Create: true}).Key(ctx, allowCreate)
}
