//go:build darwin

// Package desktop is the only Go layer that depends on Wails. Headless CLI and
// executor packages must never import it or require desktop frameworks.
package desktop

import (
	"context"
	_ "embed"

	"github.com/jjyr/pot/internal/preview"
	webassets "github.com/jjyr/pot/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Pot brand artwork is embedded independently of external runtime resources.
// The canonical vector source is resources/app/PotIcon.swift.
//
//go:embed assets/Pot.png
var appIcon []byte

//go:embed assets/PotMenuBar.png
var trayIcon []byte

// PreviewService binds only safe fixture operations. Service.Close/Subscribe are
// not exported to JavaScript, and no production operator or execution API exists.
type PreviewService struct {
	source *preview.Service
	main   *application.WebviewWindow
	tray   *application.WebviewWindow
}

func (s *PreviewService) Snapshot() preview.Snapshot { return s.source.Snapshot() }

func (s *PreviewService) Send(request preview.SendRequest) (preview.Receipt, error) {
	return s.source.Send(request)
}

func (s *PreviewService) Cancel(request preview.CancelRequest) error {
	return s.source.Cancel(request)
}

func (s *PreviewService) OpenMain() {
	s.tray.Hide()
	s.main.Show()
	s.main.Focus()
}

func RunPreview() error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	source := preview.New(ctx)
	defer source.Close()
	service := &PreviewService{source: source}
	app := application.New(application.Options{
		Name: "Pot Go Preview", Description: "Disposable Go/Wails UI migration preview", Icon: appIcon,
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(webassets.FS())},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		OnShutdown: func() {
			stop()
			source.Close()
		},
	})
	main := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "pot-main", Title: "Pot", Width: 1100, Height: 760, MinWidth: 390, MinHeight: 480,
		URL: "/?platform=desktop&mode=preview",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInsetUnified, InvisibleTitleBarHeight: 44,
		},
	})
	main.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		main.Hide()
		event.Cancel()
	})
	tray := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "pot-tray", Title: "Pot", Width: 380, Height: 560,
		URL: "/?platform=desktop&surface=tray&mode=preview", Frameless: true,
		AlwaysOnTop: true, Hidden: true, DisableResize: true, HideOnEscape: true, HideOnFocusLost: true,
	})
	tray.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		tray.Hide()
		event.Cancel()
	})
	service.main, service.tray = main, tray
	status := app.SystemTray.New()
	status.SetTemplateIcon(trayIcon)
	status.SetTooltip("Pot — UI preview")
	status.AttachWindow(tray).WindowOffset(5)
	menu := app.NewMenu()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	app.Menu.Set(menu)
	trayMenu := app.NewMenu()
	trayMenu.Add("Open Pot").OnClick(func(*application.Context) { service.OpenMain() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit Pot Preview").OnClick(func(*application.Context) { app.Quit() })
	status.SetMenu(trayMenu)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			updates, remove := source.Subscribe()
			defer remove()
			for {
				select {
				case <-ctx.Done():
					return
				case snapshot := <-updates:
					app.Event.Emit("preview:snapshot", snapshot)
				}
			}
		}()
	})
	return app.Run()
}
