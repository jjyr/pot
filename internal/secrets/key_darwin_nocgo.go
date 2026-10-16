//go:build darwin && !cgo

package secrets

import (
	"context"
	"errors"
)

func platformKey(context.Context, string, bool) ([]byte, error) {
	return nil, errors.New("macOS Keychain requires CGO; no key storage fallback is permitted")
}
