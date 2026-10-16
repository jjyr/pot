//go:build darwin

package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/transport"
	assets "github.com/jjyr/pot/web"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Service is the desktop adapter to the same application contract used by CLI
// and browser. Exported bindings contain no lifecycle or credential access.
type Service struct {
	ctx           context.Context
	app           *application.App
	api           *transport.Application
	main, tray    *application.WebviewWindow
	mu            sync.Mutex
	subscriptions map[string]context.CancelFunc
}

func (s *Service) Call(ctx context.Context, method, path, body string) (string, error) {
	value, err := s.api.JSON(ctx, method, path, body)
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
	if space != "" && space != "local" {
		return "", errors.New("Space unavailable")
	}
	if !core.Identifier(owner) {
		return "", errors.New("invalid assistant identity")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	c, err := s.api.Control.Conversation(ctx, owner)
	if err != nil {
		cancel()
		return "", err
	}
	updates, remove, err := c.Subscribe(ctx)
	if err != nil {
		cancel()
		return "", err
	}
	token := rand.Text()
	s.mu.Lock()
	if len(s.subscriptions) >= 128 {
		s.mu.Unlock()
		cancel()
		remove()
		return "", errors.New("too many subscriptions")
	}
	s.subscriptions[token] = cancel
	s.mu.Unlock()
	go func() {
		defer remove()
		defer s.Unsubscribe(token)
		for {
			select {
			case <-ctx.Done():
				return
			case snapshot, ok := <-updates:
				if !ok {
					return
				}
				s.app.Event.Emit("pot:snapshot", map[string]any{"space_id": space, "owner": owner, "snapshot": snapshot})
			}
		}
	}()
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
	api := &transport.Application{Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID, Name: "Pot"}
	socket, err := transport.StartSocket(ctx, runtime.Root.Path, api)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		socket.Close(ctx)
	}()
	service := &Service{ctx: ctx, api: api, subscriptions: map[string]context.CancelFunc{}}
	app := application.New(application.Options{Name: "Pot Go", Description: "Pot", Icon: appIcon, Services: []application.Service{application.NewService(service)}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets.FS())}, Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false}, OnShutdown: func() {
		stop()
		service.mu.Lock()
		for _, cancel := range service.subscriptions {
			cancel()
		}
		service.mu.Unlock()
		_ = runtime.Close()
	}})
	service.app = app
	main := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-main", Title: "Pot", Width: 1100, Height: 760, MinWidth: 390, MinHeight: 480, URL: "/?platform=desktop", Mac: application.MacWindow{TitleBar: application.MacTitleBarHiddenInsetUnified, InvisibleTitleBarHeight: 44}})
	main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { main.Hide(); e.Cancel() })
	tray := app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "pot-tray", Title: "Pot", Width: 380, Height: 560, URL: "/?platform=desktop&surface=tray", Frameless: true, AlwaysOnTop: true, Hidden: true, DisableResize: true, HideOnEscape: true, HideOnFocusLost: true})
	tray.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { tray.Hide(); e.Cancel() })
	service.main, service.tray = main, tray
	status := app.SystemTray.New()
	status.SetTemplateIcon(trayIcon)
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
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		forward := func(sub *core.Subscription) {
			go func() {
				defer sub.Close()
				for {
					topics, err := sub.Next(ctx)
					if err != nil {
						return
					}
					app.Event.Emit("pot:changed", map[string]any{"space_id": "local", "topics": topics})
				}
			}()
		}
		forward(runtime.Control.Changes.Subscribe())
		forward(runtime.Control.Permissions.Changes.Subscribe())
	})
	return app.Run()
}
