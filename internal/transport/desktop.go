package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jjyr/pot/internal/desktopview"
	"io/fs"
	"net/http"
	"strings"
)

// DesktopHandler is mounted only behind the Runtime's protected owner API. The
// independent browser server replaces the trusted login ID before forwarding.
func DesktopHandler(ctx context.Context, manager desktopview.Manager, assets fs.FS) (*desktopview.Service, error) {
	return desktopview.New(desktopview.Options{Manager: manager, Assets: assets, Authorize: func(r *http.Request) (desktopview.Identity, error) {
		credential := r.Header.Get("Authorization")
		if !strings.HasPrefix(credential, "Bearer ") {
			return desktopview.Identity{}, errors.New("protected owner authentication required")
		}
		sum := sha256.Sum256([]byte(credential))
		owner := "operator-" + hex.EncodeToString(sum[:])
		if browser := r.Header.Get("X-Pot-Browser-ID"); browser != "" {
			if len(browser) != 64 {
				return desktopview.Identity{}, errors.New("invalid trusted browser identity")
			}
			if _, err := hex.DecodeString(browser); err != nil {
				return desktopview.Identity{}, err
			}
			owner = "browser-" + browser
		}
		return desktopview.Identity{Owner: owner, Valid: func() bool { return ctx.Err() == nil }}, nil
	}, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }})
}
func desktopPath(path string) bool {
	return path == "/api/v1/desktop/sessions" || strings.HasPrefix(path, "/desktop/") || strings.HasPrefix(path, "/desktop-assets/")
}
