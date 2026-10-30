//go:build darwin || (linux && desktop)

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

// PreviewService binds only native presentation and a transport bootstrap. No
// fixture, production operator, or execution API is exported to JavaScript.
type PreviewService struct {
	main      *application.WebviewWindow
	tray      *application.WebviewWindow
	transport transportBootstrap
}

func (s *PreviewService) Transport() transportBootstrap { return s.transport }

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
	service := &PreviewService{}
	desktopTransport, err := startDesktopSocket(ctx, "/api/preview/ws", preview.SocketCaller(source), "")
	if err != nil {
		return err
	}
	defer desktopTransport.Close()
	service.transport = desktopTransport.bootstrap
	app := application.New(application.Options{
		Name: "Pot", Description: "Pot", Icon: appIcon,
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.AssetFileServerFS(webassets.FS())},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		OnShutdown: func() {
			_ = desktopTransport.Close()
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
		closeMain(app, main, event)
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
	configureMenu(app)
	trayMenu := app.NewMenu()
	trayMenu.Add("Open Pot").OnClick(func(*application.Context) { service.OpenMain() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit Pot Preview").OnClick(func(*application.Context) { app.Quit() })
	status.SetMenu(trayMenu)
	return app.Run()
}
