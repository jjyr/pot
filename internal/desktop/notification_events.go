//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/systemnotifications"
	"path/filepath"
	"sync"
	"time"
)

// Native delivery is an independent event consumer. Existing native preference,
// authorization, durable claims and Wails sending remain device-local; no browser
// registration, reading lease, Push subscription or HTTP receipt is involved.
func (s *Service) startNotificationEvents(ctx context.Context, root string, source localui.Caller) (func(), error) {
	events, err := systemnotifications.OpenHub(filepath.Join(root, "control.sqlite"))
	if err != nil {
		return nil, err
	}
	sub, err := events.Subscribe()
	if err != nil {
		events.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); events.Consume(ctx, source) }()
	go func() {
		defer wg.Done()
		defer sub.Close()
		for ctx.Err() == nil {
			record, err := sub.Next(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			event := record.Event
			if !s.notificationStore.Enabled() || notificationAuthorization() != "granted" || s.viewingNotification(event) || time.Since(record.Created) > 25*time.Second {
				sub.Ack(record)
				continue
			}
			claimed, err := s.notificationStore.Claim(event.ID)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			if claimed {
				s.mu.Lock()
				locale := s.notificationLocale
				s.mu.Unlock()
				title, body := event.Text(locale)
				// The existing native send cannot prove that an OS error rejected the
				// effect. Its durable claim retains uncertainty rather than blindly resend.
				_ = s.sendSystemNotification(SystemNotification{ID: event.ID, Space: event.Space, Control: event.Control, Owner: event.Owner, Task: event.Task, Kind: event.Kind, Name: event.Name, Title: title, Body: body})
			}
			sub.Ack(record)
		}
	}()
	return func() { cancel(); wg.Wait(); _ = events.Close() }, nil
}
func (s *Service) viewingNotification(event systemnotifications.Event) bool {
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
