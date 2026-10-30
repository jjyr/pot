//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/attention"
	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/productinfo"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/internal/systemnotifications"
	"github.com/jjyr/pot/internal/transport"
	assets "github.com/jjyr/pot/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// Desktop service: native UI shell drives the shared browser business WebSocket adapter.
// Exports scoped ephemeral Transport bootstrap and native window/notification/download/attention integrations.
type Service struct {
	app    *application.App
	spaces *localui.Manager

	main, tray                *application.WebviewWindow
	mu                        sync.Mutex
	transport                 transportBootstrap
	reads                     *attention.Store
	notificationStore         *systemnotifications.Store
	notifier                  *notifications.NotificationService
	notificationAuthorization *systemnotifications.Authorization
	notificationViews         map[string]NotificationView
	notificationLocale        string

	pendingNotification       map[string]interface{}
	attentionWake             chan struct{}
	reduceMotion, motionKnown bool
	smoke                     bool
	smokeReady                bool
}

func (s *Service) Transport() transportBootstrap { return s.transport }
func (s *Service) OpenMain()                     { s.tray.Hide(); s.main.Show(); s.main.Focus() }
func Run(root string) error {
	ctx := context.Background()
	host, err := selfupdate.OpenHost()
	if err != nil {
		return err
	}
	if host != nil {
		return host.Run(ctx, func(ctx context.Context) error { return runProduct(ctx, root) })
	}
	return runProduct(ctx, root)
}
func runProduct(parent context.Context, root string) (result error) {
	ctx, stop := context.WithCancel(parent)
	defer stop()
	smoke, _ := ctx.Value(smokeKey{}).(bool)
	runtime, err := product.Open(ctx, product.Options{Root: root, DisableMetadataRefresh: smoke, Control: control.Options{Embedded: true}})
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, runtime.Close()) }()
	api := &transport.Application{RuntimeResources: runtime.ResourceCall, Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID, Name: "Pot"}
	api.Updates = runtime.Updates.Call
	api.Maintenance = runtime.Maintaining
	api.About = func(context.Context) (any, error) { return productinfo.Read(runtime.AssetDirectory) }
	socket, err := transport.StartSocket(ctx, runtime.Root.Path, api)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		socket.Close(ctx)
	}()
	reads, err := attention.Open(filepath.Join(runtime.Root.Path, "assistant-reads.json"))
	if err != nil {
		return err
	}
	notificationStore, err := systemnotifications.Open(filepath.Join(runtime.Root.Path, "system-notifications.json"))
	if err != nil {
		return err
	}
	notifier := notifications.New()
	service := &Service{notificationStore: notificationStore, notifier: notifier, notificationViews: map[string]NotificationView{}, reads: reads, attentionWake: make(chan struct{}, 1)}

	service.smoke = smoke
	service.notificationAuthorization = &systemnotifications.Authorization{
		Store: notificationStore, Status: notificationAuthorization,
		Request: func() error { _, err := notifier.RequestNotificationAuthorization(); return err },
	}
	localClient := socket.Endpoint.Client()
	localClient.Timeout = 0
	manager, err := localui.Open(ctx, localui.Options{Local: localui.NewLocalHTTPClient(localClient, socket.Endpoint.Token, api.RuntimeID), LocalID: api.RuntimeID, LocalName: localDeviceName(), OnChange: func(space string, topics json.RawMessage) {
		service.wakeAttention()
	}})
	if err != nil {
		return err
	}
	defer manager.Close()
	service.spaces = manager
	desktopTransport, err := startDesktopSocket(ctx, "/api/v1/ws", desktopCaller(service), api.RuntimeID)
	if err != nil {
		return err
	}
	defer desktopTransport.Close()
	service.transport = desktopTransport.bootstrap
	files := service.resourceAssets(application.AssetFileServerFS(assets.FS()))

	var cleanupOnce sync.Once
	var cleanupResult error
	cleanup := func() error {
		cleanupOnce.Do(func() {
			stop()
			cleanupResult = errors.Join(desktopTransport.Close(), manager.Close(), runtime.Close())
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cleanupResult = errors.Join(cleanupResult, socket.Close(closeCtx))
		})
		return cleanupResult
	}
	app := application.New(application.Options{Name: "Pot", Description: "Pot " + buildinfo.Version, Icon: appIcon, Services: []application.Service{application.NewService(service), application.NewService(notifier)}, Assets: application.AssetOptions{Handler: files}, Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false}, OnShutdown: func() { _ = cleanup() }})
	if host := selfupdate.FromContext(ctx); host != nil {
		host.AppLifecycle(cleanup, app.Quit)
	}
	service.mu.Lock()
	service.app = app
	service.mu.Unlock()
	notifier.OnNotificationResponse(service.notificationResponse)
	main := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-main", Title: "Pot", Width: 1100, Height: 760, MinWidth: 390, MinHeight: 480, URL: "/?platform=desktop", Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInsetUnified, InvisibleTitleBarHeight: 44}})
	main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { closeMain(app, main, e) })
	tray := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-tray", Title: "Pot", Width: 380, Height: 560, URL: "/?platform=desktop&surface=tray", Frameless: true, AlwaysOnTop: true, Hidden: true, DisableResize: true, HideOnEscape: true, HideOnFocusLost: true})
	tray.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { tray.Hide(); e.Cancel() })
	service.main, service.tray = main, tray
	stopNotifications, err := service.startNotificationEvents(ctx, runtime.Root.Path, localui.NewLocalHTTPClient(localClient, socket.Endpoint.Token, api.RuntimeID))
	if err != nil {
		return err
	}
	defer stopNotifications()
	// A Dock reopen belongs to the main window; Wails' default handler reveals
	// every hidden window, including the compact tray and isolated viewer.
	app.Event.RegisterApplicationEventHook(events.Mac.ApplicationShouldHandleReopen, func(e *application.ApplicationEvent) {
		service.OpenMain()
		e.Cancel()
	})
	status := app.SystemTray.New()
	status.SetIcon(trayIcon)
	status.SetTooltip("Pot")
	status.AttachWindow(tray).WindowOffset(5)
	configureMenu(app)
	trayMenu := app.NewMenu()
	trayMenu.Add("Open Pot").OnClick(func(*application.Context) { service.OpenMain() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit Pot").OnClick(func(*application.Context) { app.Quit() })
	status.SetMenu(trayMenu)
	app.Event.OnApplicationEvent(events.Common.ThemeChanged, func(*application.ApplicationEvent) { service.wakeAttention() })
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if host := selfupdate.FromContext(ctx); host != nil {
			host.Ready()
		}
		go service.runAttention(ctx, status)

	})
	if selfupdate.FromContext(ctx) == nil {
		go func() { <-ctx.Done(); app.Quit() }()
	}
	if smoke {
		return service.runSmoke(app)
	}
	return app.Run()
}
