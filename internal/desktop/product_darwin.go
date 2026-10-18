//go:build darwin

package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/attention"
	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/productinfo"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/spaces"
	"github.com/jjyr/pot/internal/systemnotifications"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	assets "github.com/jjyr/pot/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// Service is the desktop adapter to the same application contract used by CLI
// and browser. Exported bindings contain no lifecycle or credential access.
type Service struct {
	ctx    context.Context
	app    *application.App
	api    *transport.Application
	spaces *spaces.Manager

	main, tray                *application.WebviewWindow
	mu                        sync.Mutex
	subscriptions             map[string]context.CancelFunc
	reads                     *attention.Store
	notificationStore         *systemnotifications.Store
	notifier                  *notifications.NotificationService
	notificationAuthorization *systemnotifications.Authorization
	notificationViews         map[string]NotificationView
	pendingNotification       map[string]interface{}
	attentionWake             chan struct{}
	reduceMotion, motionKnown bool
}

func (s *Service) Call(ctx context.Context, method, path, body string) (string, error) {
	value, err := s.call(ctx, method, path, body)
	if err == nil {
		return value, nil
	}
	api := protocol.APIError{Code: "request_failed", Message: err.Error()}
	_ = errors.As(err, &api)
	status := 409
	if api.Code == "not_found" {
		status = 404
	}
	encoded, marshalErr := json.Marshal(map[string]any{"error": api, "status": status})
	return string(encoded), marshalErr
}
func (s *Service) OpenMain() { s.tray.Hide(); s.main.Show(); s.main.Focus() }
func (s *Service) Subscribe(space, owner string) (string, error) {
	if _, err := s.spaces.Space(space); err != nil {
		return "", err
	}
	if !core.Identifier(owner) {
		return "", errors.New("invalid assistant identity")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	token := rand.Text()
	s.mu.Lock()
	if len(s.subscriptions) >= 128 {
		s.mu.Unlock()
		cancel()
		return "", errors.New("too many subscriptions")
	}
	s.subscriptions[token] = cancel
	s.mu.Unlock()
	go func() { defer s.Unsubscribe(token); s.streamSpace(ctx, space, owner) }()

	return token, nil
}
func (s *Service) Unsubscribe(token string) {
	s.mu.Lock()
	cancel := s.subscriptions[token]
	delete(s.subscriptions, token)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func Run(root string) error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	runtime, err := product.Open(ctx, product.Options{Root: root, Control: control.Options{Embedded: true}})
	if err != nil {
		return err
	}
	defer runtime.Close()
	api := &transport.Application{RuntimeResources: runtime.ResourceCall, Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID, Name: "Pot"}
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
	service := &Service{notificationStore: notificationStore, notifier: notifier, notificationViews: map[string]NotificationView{}, ctx: ctx, api: api, subscriptions: map[string]context.CancelFunc{}, reads: reads, attentionWake: make(chan struct{}, 1)}
	service.notificationAuthorization = &systemnotifications.Authorization{
		Store: notificationStore, Status: notificationAuthorization,
		Request: func() error { _, err := notifier.RequestNotificationAuthorization(); return err },
	}
	localClient := socket.Endpoint.Client()
	localClient.Timeout = 0
	manager, err := spaces.Open(ctx, spaces.Options{Path: filepath.Join(runtime.Root.Path, "app-spaces.json"), Local: spaces.NewLocalHTTPClient(localClient, socket.Endpoint.Token, api.RuntimeID), LocalID: api.RuntimeID, LocalName: "This Mac", OnStreamReset: func(space string) {
		service.mu.Lock()
		app := service.app
		service.mu.Unlock()
		if app != nil {
			app.Event.Emit("pot:changed", map[string]any{"space_id": space, "reset": true})
		}
	}, OnChange: func(space string, topics json.RawMessage) {
		service.wakeAttention()
		service.mu.Lock()
		app := service.app
		service.mu.Unlock()
		if app != nil {
			var value any
			if json.Unmarshal(topics, &value) == nil {
				app.Event.Emit("pot:changed", map[string]any{"space_id": space, "topics": value})
			}
		}
	}})
	if err != nil {
		return err
	}
	defer manager.Close()
	service.spaces = manager
	files := application.AssetFileServerFS(assets.FS())

	app := application.New(application.Options{Name: "Pot", Description: "Pot " + buildinfo.Version, Icon: appIcon, Services: []application.Service{application.NewService(service), application.NewService(notifier)}, Assets: application.AssetOptions{Handler: files}, Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false}, OnShutdown: func() {
		stop()
		service.mu.Lock()
		for _, cancel := range service.subscriptions {
			cancel()
		}
		service.mu.Unlock()
		_ = manager.Close()
		_ = runtime.Close()
	}})
	service.mu.Lock()
	service.app = app
	service.mu.Unlock()
	notifier.OnNotificationResponse(service.notificationResponse)
	main := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-main", Title: "Pot", Width: 1100, Height: 760, MinWidth: 390, MinHeight: 480, URL: "/?platform=desktop", Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInsetUnified, InvisibleTitleBarHeight: 44}})
	main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { main.Hide(); e.Cancel() })
	tray := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-tray", Title: "Pot", Width: 380, Height: 560, URL: "/?platform=desktop&surface=tray", Frameless: true, AlwaysOnTop: true, Hidden: true, DisableResize: true, HideOnEscape: true, HideOnFocusLost: true})
	tray.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { tray.Hide(); e.Cancel() })
	service.main, service.tray = main, tray
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
	menu := app.NewMenu()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.WindowMenu)
	app.Menu.Set(menu)
	trayMenu := app.NewMenu()
	trayMenu.Add("Open Pot").OnClick(func(*application.Context) { service.OpenMain() })
	trayMenu.AddSeparator()
	trayMenu.Add("Quit Pot").OnClick(func(*application.Context) { app.Quit() })
	status.SetMenu(trayMenu)
	app.Event.OnApplicationEvent(events.Common.ThemeChanged, func(*application.ApplicationEvent) { service.wakeAttention() })
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go service.runAttention(ctx, status)
		go func() {
			sub := manager.Changes.Subscribe()
			defer sub.Close()
			for {
				topics, err := sub.Next(ctx)
				if err != nil {
					return
				}
				service.wakeAttention()
				catalog := manager.Snapshot()
				service.app.Event.Emit("pot:changed", map[string]any{"space_id": "", "topics": topics})
				for _, space := range catalog.Items {
					service.app.Event.Emit("pot:changed", map[string]any{"space_id": space.ID, "topics": topics})
				}
			}
		}()

	})
	return app.Run()
}
