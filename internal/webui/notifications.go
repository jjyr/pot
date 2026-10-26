package webui

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/browsernotifications"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/pwapush"
	"github.com/jjyr/pot/internal/systemnotifications"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type notificationService struct {
	events  *systemnotifications.Hub
	devices *browsernotifications.Store
	local   *browsernotifications.Local
	push    *pwapush.Service
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func startNotifications(root string, upstream Upstream, origin string, config Config, transport pwapush.Transport) (*notificationService, error) {
	path := filepath.Join(root, "control.sqlite")
	events, err := systemnotifications.OpenHub(path)
	if err != nil {
		return nil, err
	}
	devices, err := browsernotifications.Open(path)
	if err != nil {
		events.Close()
		return nil, err
	}
	cleanup := func() { devices.Close(); events.Close() }
	if err = devices.Reconcile(context.Background(), func(binding string) bool {
		generation, session, ok := strings.Cut(binding, ":")
		return ok && !config.Disabled && generation == strconv.FormatUint(config.Generation, 10) && config.Revoked[session] == 0
	}); err != nil {
		cleanup()
		return nil, err
	}
	push, err := pwapush.NewService(devices, events, origin, transport)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err = push.ClearRevoked(context.Background()); err != nil {
		cleanup()
		return nil, err
	}
	browserSub, err := events.Subscribe()
	if err != nil {
		cleanup()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &notificationService{events: events, devices: devices, local: &browsernotifications.Local{Store: devices, Events: events}, push: push, cancel: cancel}
	caller := localui.NewHTTPClient(upstream.URL, upstream.Client, upstream.Credential, upstream.ControlID)
	n.wg.Add(3)
	go func() { defer n.wg.Done(); events.Consume(ctx, caller) }()
	go func() { defer n.wg.Done(); devices.Run(ctx, browserSub) }()
	go func() { defer n.wg.Done(); push.Run(ctx) }()
	return n, nil
}
func (n *notificationService) close() {
	n.cancel()
	n.wg.Wait()
	_ = n.devices.Close()
	_ = n.events.Close()
}
func (n *notificationService) revoke(ctx context.Context, binding string) error {
	if err := n.devices.Revoke(ctx, binding); err != nil {
		return err
	}
	return n.push.ClearRevoked(ctx)
}
func notificationBinding(r *http.Request, generation uint64) string {
	return strconv.FormatUint(generation, 10) + ":" + sessionOwner(r)
}
func (s *Server) notificationRoute(w http.ResponseWriter, r *http.Request, c Config) {
	if s.notifications == nil {
		failure(w, 503, "notification service unavailable")
		return
	}
	events := s.notifications.events
	registry := s.notifications.devices
	local := s.notifications.local
	if r.Method == "GET" && r.URL.Path == "/api/v1/notifications/session" {
		if events.Control() == "" {
			failure(w, 503, "notification source unavailable")
			return
		}
		reply(w, 200, map[string]string{"binding": notificationBinding(r, c.Generation), "control": events.Control()})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/v1/notifications/key" {
		reply(w, 200, map[string]string{"public_key": s.notifications.push.PublicKey()})
		return
	}
	if r.Method != "POST" {
		failure(w, 405, "method not allowed")
		return
	}
	binding := notificationBinding(r, c.Generation)
	var input struct {
		Device       string                   `json:"device"`
		Session      string                   `json:"session,omitempty"`
		Control      string                   `json:"control,omitempty"`
		Space        string                   `json:"space,omitempty"`
		Channel      string                   `json:"channel,omitempty"`
		Enabled      bool                     `json:"enabled,omitempty"`
		Permission   string                   `json:"permission,omitempty"`
		Locale       string                   `json:"locale,omitempty"`
		Subscription *pwapush.Subscription    `json:"subscription,omitempty"`
		View         systemnotifications.View `json:"view,omitempty"`
		Event        string                   `json:"event,omitempty"`
		Attempt      int                      `json:"attempt,omitempty"`
		State        string                   `json:"state,omitempty"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16385))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.Device) < 20 || len(input.Device) > 128 {
		failure(w, 400, "invalid notification request")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		failure(w, 400, "invalid notification request")
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/v1/notifications/device":
		if !events.ValidateScope(input.Control, input.Space, nil) {
			failure(w, 409, "notification scope changed")
			return
		}

		if input.Channel == "push" {
			if input.Subscription == nil {
				failure(w, 400, "invalid push subscription")
				return
			}
		} else if input.Subscription != nil {
			failure(w, 400, "local notification device cannot register Push")
			return
		}
		cookie, _ := r.Cookie("pot_session")
		parts := strings.Split(cookie.Value, ".")
		expires, _ := strconv.ParseInt(parts[1], 10, 64)
		// Body reads and validation above cannot hold the authority lock. Once
		// they finish, revalidate and commit in one sequence with revocation.
		s.mu.Lock()
		if !s.authenticatedLocked(r, c) {
			s.mu.Unlock()
			failure(w, 401, "browser authentication required")
			return
		}
		device := browsernotifications.Device{ID: input.Device, Binding: binding, Control: input.Control, Channel: input.Channel, Enabled: input.Enabled, Permission: input.Permission, Locale: input.Locale, Expires: expires * 1000}
		if input.Channel == "push" {
			err = s.notifications.push.Register(r.Context(), device, *input.Subscription)
		} else {
			err = registry.Register(r.Context(), device)
		}
		s.mu.Unlock()
	case "/api/v1/notifications/view":
		if input.View.Owner != "" && !events.ValidateScope(input.Control, input.View.Space, []string{input.View.Owner}) {
			failure(w, 409, "notification view scope changed")
			return
		}
		err = s.notifications.push.Check(r.Context(), input.Device, binding)
		if err == nil {
			err = registry.Lease(r.Context(), input.Device, binding, input.Session, input.View)
		}
	case "/api/v1/notifications/next":
		stream, finish, streamErr := s.stream(r, c.Generation)
		if streamErr != nil {
			err = streamErr
			break
		}
		defer finish()
		delivery, e := local.Next(stream, input.Device, binding, input.Session)
		if e != nil {
			err = e
			break
		}
		w.Header().Set("Content-Type", "application/json")
		if encodeErr := json.NewEncoder(w).Encode(map[string]any{"delivery": delivery}); encodeErr != nil && delivery != nil {
			receiptContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = local.Complete(receiptContext, input.Device, binding, delivery.Event.ID, delivery.Attempt, "unknown")
		}
		return
	case "/api/v1/notifications/receipt":
		err = local.Complete(r.Context(), input.Device, binding, input.Event, input.Attempt, input.State)
	case "/api/v1/notifications/baseline":
		err = local.Baseline(r.Context(), input.Device, binding, input.Session)
	case "/api/v1/notifications/remove":
		err = registry.Remove(r.Context(), input.Device, binding)
	default:
		failure(w, 404, "notification route unavailable")
		return
	}
	if err != nil {
		if errors.Is(err, pwapush.ErrInvalidSubscription) {
			failure(w, 400, "invalid push subscription")
			return
		}
		if errors.Is(err, pwapush.ErrSubscriptionExpired) {
			failure(w, 410, "notification subscription expired")
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		failure(w, 409, "notification request rejected")
		return
	}
	reply(w, 200, map[string]bool{"accepted": true})
}
