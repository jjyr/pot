//go:build darwin || windows || (linux && desktop)

package desktop

import (
	"errors"
	"github.com/jjyr/pot/internal/localui"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"time"
)

type NotificationView struct {
	Space   string `json:"space"`
	Owner   string `json:"owner"`
	Task    string `json:"task"`
	Route   string `json:"route"`
	Focused bool   `json:"focused"`
}
type SystemNotification struct {
	ID      string `json:"id"`
	Space   string `json:"space"`
	Control string `json:"control"`
	Owner   string `json:"owner"`
	Task    string `json:"task"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

func (s *Service) SystemNotificationStatus() map[string]any {
	return map[string]any{"enabled": s.notificationStore.Enabled(), "permission": notificationAuthorization()}
}
func (s *Service) RequestSystemNotificationPermission() (map[string]any, error) {
	if err := s.notificationAuthorization.Authorize(); err != nil {
		return nil, err
	}
	return s.SystemNotificationStatus(), nil
}

// NotificationMainReady is called after the main UI has loaded its directory
// and painted. Tray initialization cannot consume or start the automatic attempt.
func (s *Service) NotificationMainReady(surface string) (map[string]any, error) {
	if s.smoke && surface == "main" {
		s.mu.Lock()
		s.smokeReady = true
		s.mu.Unlock()
		// Let Wails deliver this binding's response before destroying its WebView.
		time.AfterFunc(250*time.Millisecond, s.app.Quit)
		return s.SystemNotificationStatus(), nil
	}
	if surface != "main" {
		return s.SystemNotificationStatus(), nil
	}
	if !s.main.IsVisible() {
		status := s.SystemNotificationStatus()
		status["deferred"] = true
		return status, nil
	}
	err := s.notificationAuthorization.MainReady(surface)
	s.app.Event.Emit("pot:notification-preference")
	return s.SystemNotificationStatus(), err
}
func (s *Service) SetSystemNotificationsEnabled(enabled bool) error {
	if err := s.notificationStore.SetEnabled(enabled); err != nil {
		return err
	}
	s.app.Event.Emit("pot:notification-preference")
	return nil
}
func (s *Service) SetNotificationView(surface string, view NotificationView) error {
	if surface != "main" && surface != "tray" {
		return errors.New("invalid notification surface")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notificationViews[surface] = view
	return nil
}
func (s *Service) NotifySystem(event SystemNotification) error {
	_, validOwner := localui.AttentionID(event.Owner)
	if event.ID == "" || len(event.ID) > 2048 || !validOwner || len(event.Title) > 512 || len(event.Body) > 512 {
		return errors.New("invalid system notification")
	}
	space, err := s.spaces.Space(event.Space)
	if err != nil {
		return err
	}
	if space.ControlID != event.Control {
		return errors.New("notification Control identity changed")
	}
	claimed, err := s.notificationStore.Claim(event.ID)
	if err != nil || !claimed {
		return err
	}
	if !s.notificationStore.Enabled() {
		return nil
	}
	s.mu.Lock()
	views := make(map[string]NotificationView, len(s.notificationViews))
	for surface, view := range s.notificationViews {
		views[surface] = view
	}
	s.mu.Unlock()
	viewing := false
	for surface, view := range views {
		window := s.main
		if surface == "tray" {
			window = s.tray
		}
		if view.Focused && window.IsVisible() && window.IsFocused() && view.Space == event.Space && view.Owner == event.Owner && ((view.Task != "" && view.Task == event.Task) || (view.Task == "" && view.Route == "chat")) {
			viewing = true
			break
		}
	}
	if viewing || notificationAuthorization() != "granted" {
		return nil
	}
	return s.notifier.SendNotification(notifications.NotificationOptions{ID: event.ID, Title: event.Title, Body: event.Body,
		Data: map[string]interface{}{"space": event.Space, "control": event.Control, "owner": event.Owner, "task": event.Task, "kind": event.Kind, "name": event.Name}})
}

// TakeSystemNotification lets the main WebView recover a click received before
// its event listener or Space catalogue was ready, including a cold App launch.
func (s *Service) TakeSystemNotification() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.pendingNotification
	s.pendingNotification = nil
	return value
}
func (s *Service) notificationResponse(result notifications.NotificationResult) {
	if result.Error != nil {
		return
	}
	if result.Response.ActionIdentifier != "com.apple.UNNotificationDefaultActionIdentifier" && result.Response.ActionIdentifier != notifications.DefaultActionIdentifier {
		return
	}
	data := result.Response.UserInfo
	// The main UI validates the current Control/Space directory before routing.
	// Forward stale destinations too so it can show an explicit missing-target
	// error rather than activate an unrelated conversation.

	s.mu.Lock()
	s.pendingNotification = data
	s.mu.Unlock()
	s.app.Show()
	s.OpenMain()
	s.app.Event.Emit("pot:notification-click")
}
