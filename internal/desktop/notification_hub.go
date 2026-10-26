//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/systemnotifications"
)

// The resident native host consumes the same backend event/dispatcher contract
// as browser installations. Main/tray pages only report permission and reading
// intent; their lifetime cannot produce a second business-event observer.
func (s *Service) startNotificationHub(ctx context.Context, root, node string, source localui.Caller) (func(), error) {
	hub, err := systemnotifications.OpenHub(filepath.Join(root, "control.sqlite"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	hub.Viewing = func(event systemnotifications.Event) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for surface, view := range s.notificationViews {
			window := s.main
			if surface == "tray" {
				window = s.tray
			}
			current := systemnotifications.View{Space: view.Space, Owner: view.Owner, Task: view.Task, Route: view.Route, Focused: view.Focused && window != nil && window.IsVisible() && window.IsFocused()}
			if current.Viewing(event) {
				return true
			}
		}
		return false
	}
	id := "native-" + node
	binding := "native:" + node
	session := rand.Text()
	var leaseSequence uint64
	update := func() {
		leaseSequence++
		s.mu.Lock()
		locale := s.notificationLocale
		views := map[string]NotificationView{}
		for k, v := range s.notificationViews {
			views[k] = v
		}
		s.mu.Unlock()
		if locale == "" {
			locale = "en"
		}
		_ = hub.Register(ctx, systemnotifications.Device{ID: id, Binding: binding, Control: node, Channel: "local", Enabled: s.notificationStore.Enabled(), Permission: notificationAuthorization(), Locale: locale, Expires: time.Now().Add(time.Minute).UnixMilli()})
		// The native host verifies actual Wails window state on every renewal.
		for surface, view := range views {
			window := s.main
			if surface == "tray" {
				window = s.tray
			}
			focused := view.Focused && window != nil && window.IsVisible() && window.IsFocused()
			_ = hub.Lease(ctx, id, binding, session+"/"+surface, systemnotifications.View{Sequence: leaseSequence, Space: view.Space, Owner: view.Owner, Task: view.Task, Route: view.Route, Focused: focused})
		}
		// Retain a delivery lease while all native windows are hidden or initializing.
		_ = hub.Lease(ctx, id, binding, session+"/host", systemnotifications.View{Sequence: leaseSequence})
	}
	wg.Add(4)
	go func() { defer wg.Done(); hub.Observe(ctx, source) }()
	go func() { defer wg.Done(); hub.Dispatch(ctx) }()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			update()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.notificationWake:
			}
		}
	}()
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			delivery, err := hub.Next(ctx, id, binding, session)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			if delivery == nil {
				continue
			}
			event := delivery.Event
			s.mu.Lock()
			locale := s.notificationLocale
			s.mu.Unlock()
			title, body := event.Text(locale)
			state := "accepted"
			if !s.notificationStore.Enabled() || notificationAuthorization() != "granted" {
				state = "suppressed"
			}
			if state == "accepted" {
				if err = s.sendSystemNotification(SystemNotification{ID: event.ID, Space: event.Space, Control: event.Control, Owner: event.Owner, Task: event.Task, Kind: event.Kind, Name: event.Name, Title: title, Body: body}); err != nil {
					state = "failed"
				}
			}
			// Native SendNotification errors do not prove the OS rejected an effect;
			// preserve uncertainty, with no blind resend from a second window.
			if state == "failed" {
				state = "unknown"
			}
			_ = hub.Complete(ctx, id, binding, event.ID, delivery.Attempt, systemnotifications.Result{State: state})
		}
	}()
	return func() { cancel(); wg.Wait(); _ = hub.Close() }, nil
}
func (s *Service) wakeNotifications() {
	select {
	case s.notificationWake <- struct{}{}:
	default:
	}
}
