//go:build darwin

package desktop

import (
	"context"
	"errors"
	"fmt"

	"github.com/jjyr/pot/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ConfirmSandboxResources keeps the existing macOS owner confirmation native.
// Displayed identity and size come from the pinned request, never model text.
// Returning true alone authorizes no transfer; the current owner action must
// still submit the same request through the ordinary resource command port.
func (s *Service) ConfirmSandboxResources(ctx context.Context, id, locale string) (bool, error) {
	if !core.Identifier(id) || s.api.Resources == nil {
		return false, errors.New("resource request unavailable")
	}
	var window *application.WebviewWindow
	if s.main.IsVisible() && s.main.IsFocused() {
		window = s.main
	} else if s.tray.IsVisible() && s.tray.IsFocused() {
		window = s.tray
	} else {
		return false, errors.New("visible focused owner window required")
	}
	status, err := s.api.Resources.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range status.Requests {
		if r.ID != id {
			continue
		}
		if r.State != "awaiting_consent" {
			return false, errors.New("resource request no longer awaits confirmation")
		}
		var missing uint64
		for _, b := range status.Bundles {
			if b.ID == r.BundleID {
				missing = b.MissingBytes
			}
		}
		title, download, cancel, size := "Prepare Sandbox resources", "Download and continue", "Cancel", "Download size"
		if locale == "zh-CN" {
			title, download, cancel, size = "准备 Sandbox 资源", "下载并继续", "取消", "待下载大小"
		}
		answer := make(chan bool, 1)
		dialog := s.app.Dialog.Question().SetTitle(title).SetMessage(fmt.Sprintf("%s · %s\n%s · %.1f MiB", r.SandboxID, r.BundleID, size, float64(missing)/(1024*1024))).SetIcon(appIcon).AttachToWindow(window)
		dialog.AddButton(download).SetAsDefault().OnClick(func() {
			select {
			case answer <- true:
			default:
			}
		})
		dialog.AddButton(cancel).SetAsCancel().OnClick(func() {
			select {
			case answer <- false:
			default:
			}
		})
		dialog.Show()
		select {
		case value := <-answer:
			return value, nil
		case <-ctx.Done():
			return false, ctx.Err()
		case <-s.ctx.Done():
			return false, s.ctx.Err()
		}
	}
	return false, errors.New("resource request not found")
}
