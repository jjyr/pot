package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/pwapush"
	"github.com/jjyr/pot/internal/systemnotifications"
)

type notificationService struct {
	hub    *systemnotifications.Hub
	push   *pwapush.Sender
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func startNotifications(root string, upstream Upstream, origin string, config Config) (*notificationService, error) {
	hub, err := systemnotifications.OpenHub(filepath.Join(root, "control.sqlite"))
	if err != nil {
		return nil, err
	}
	if err = hub.Reconcile(context.Background(), func(binding string) bool {
		generation, session, ok := strings.Cut(binding, ":")
		return ok && !config.Disabled && generation == strconv.FormatUint(config.Generation, 10) && config.Revoked[session] == 0
	}); err != nil {
		hub.Close()
		return nil, err
	}
	secret, err := hub.Secret("vapid-p256-v1", pwapush.GenerateKey)
	if err != nil {
		hub.Close()
		return nil, err
	}
	push, err := pwapush.New(secret, origin)
	if err != nil {
		hub.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &notificationService{hub: hub, push: push, cancel: cancel}
	hub.Push = func(ctx context.Context, d systemnotifications.Device, event systemnotifications.Event) systemnotifications.Result {
		var sub pwapush.Subscription
		if json.Unmarshal([]byte(d.Subscription), &sub) != nil {
			return systemnotifications.Result{State: "failed", Invalid: true}
		}
		payload, err := pushPayload(d, event)
		if err != nil {
			return systemnotifications.Result{State: "failed"}
		}
		sent := push.Send(ctx, sub, payload)
		state := "failed"
		if sent.Unknown {
			state = "unknown"
		} else if sent.Status >= 200 && sent.Status < 300 {
			state = "accepted"
		}
		return systemnotifications.Result{State: state, RetryAfter: sent.RetryAfter, Invalid: sent.Invalid}
	}
	caller := localui.NewHTTPClient(upstream.URL, upstream.Client, upstream.Credential, upstream.ControlID)
	n.wg.Add(2)
	go func() { defer n.wg.Done(); hub.Observe(ctx, caller) }()
	go func() { defer n.wg.Done(); hub.Dispatch(ctx) }()
	return n, nil
}
func (n *notificationService) close() { n.cancel(); n.wg.Wait(); _ = n.hub.Close() }
func notificationBinding(r *http.Request, generation uint64) string {
	return strconv.FormatUint(generation, 10) + ":" + sessionOwner(r)
}
func (s *Server) notificationRoute(w http.ResponseWriter, r *http.Request, c Config) {
	if s.notifications == nil {
		failure(w, 503, "notification service unavailable")
		return
	}
	hub := s.notifications.hub
	if r.Method == "GET" && r.URL.Path == "/api/v1/notifications/session" {
		if hub.Control() == "" {
			failure(w, 503, "notification source unavailable")
			return
		}
		reply(w, 200, map[string]string{"binding": notificationBinding(r, c.Generation), "control": hub.Control()})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/v1/notifications/key" {
		reply(w, 200, map[string]string{"public_key": s.notifications.push.PublicKey})
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
		if !hub.ValidateScope(input.Control, input.Space, nil) {
			failure(w, 409, "notification scope changed")
			return
		}
		subscription := ""
		if input.Channel == "push" {
			if input.Subscription == nil || pwapush.Validate(*input.Subscription) != nil {
				failure(w, 400, "invalid push subscription")
				return
			}
			if input.Subscription.ExpirationTime != nil && *input.Subscription.ExpirationTime <= time.Now().UnixMilli() {
				failure(w, 400, "expired push subscription")
				return
			}
			raw, _ := json.Marshal(input.Subscription)
			subscription = string(raw)
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
		err = hub.Register(r.Context(), systemnotifications.Device{ID: input.Device, Binding: binding, Control: input.Control, Channel: input.Channel, Enabled: input.Enabled, Permission: input.Permission, Locale: input.Locale, Subscription: subscription, Expires: expires * 1000})
		s.mu.Unlock()
	case "/api/v1/notifications/view":
		if input.View.Owner != "" && !hub.ValidateScope(input.Control, input.View.Space, []string{input.View.Owner}) {
			failure(w, 409, "notification view scope changed")
			return
		}
		err = hub.Lease(r.Context(), input.Device, binding, input.Session, input.View)
	case "/api/v1/notifications/next":
		stream, finish, streamErr := s.stream(r, c.Generation)
		if streamErr != nil {
			err = streamErr
			break
		}
		defer finish()
		delivery, e := hub.Next(stream, input.Device, binding, input.Session)
		if e != nil {
			err = e
			break
		}
		w.Header().Set("Content-Type", "application/json")
		if encodeErr := json.NewEncoder(w).Encode(map[string]any{"delivery": delivery}); encodeErr != nil && delivery != nil {
			receiptContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = hub.Complete(receiptContext, input.Device, binding, delivery.Event.ID, delivery.Attempt, systemnotifications.Result{State: "unknown"})
		}
		return
	case "/api/v1/notifications/receipt":
		// A definite constructor failure can be retried on this channel. No result
		// ever registers or falls back to Push.
		retry := time.Duration(0)
		if input.State == "failed" {
			retry = 5 * time.Second
		}
		err = hub.Complete(r.Context(), input.Device, binding, input.Event, input.Attempt, systemnotifications.Result{State: input.State, RetryAfter: retry})
	case "/api/v1/notifications/baseline":
		err = hub.Baseline(r.Context(), input.Device, binding, input.Session)
	case "/api/v1/notifications/remove":
		err = hub.Remove(r.Context(), input.Device, binding)
	default:
		failure(w, 404, "notification route unavailable")
		return
	}
	if err != nil {
		if errors.Is(err, systemnotifications.ErrSubscriptionExpired) {
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

func pushPayload(device systemnotifications.Device, event systemnotifications.Event) ([]byte, error) {
	title, body := event.Text(device.Locale)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(map[string]any{"version": 1, "event": event.Target(), "device": device.ID, "title": title, "body": body})
	return encoded.Bytes(), err
}
