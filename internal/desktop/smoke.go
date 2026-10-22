//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type smokeKey struct{}

// RunSmoke exercises the production Runtime, assets, native WebView and bridge.
// It accepts only an empty test root and suppresses model metadata refreshes.
func RunSmoke(root string) error {
	if root == "" {
		return errors.New("smoke test requires an explicit empty root")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		return errors.New("smoke test requires an existing empty root")
	}
	return runProduct(context.WithValue(context.Background(), smokeKey{}, true), root)
}

func (s *Service) runSmoke(app *application.App) error {
	timer := time.AfterFunc(45*time.Second, app.Quit)
	defer timer.Stop()
	if err := app.Run(); err != nil {
		return err
	}
	s.mu.Lock()
	ready := s.smokeReady
	s.mu.Unlock()
	if !ready {
		return errors.New("native main UI did not reach its ready bridge callback")
	}
	fmt.Println("POT_DESKTOP_SMOKE_OK")
	return nil
}
