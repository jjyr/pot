package browserpush

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func pushReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func pushFail(w http.ResponseWriter, status int, code string) {
	pushReply(w, status, map[string]any{"error": code})
}
func decodeBody(r *http.Request, out any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return io.ErrShortBuffer
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, err := s.options.Authorize(r)
	if err != nil || identity.Owner == "" || identity.Valid == nil || !identity.Valid() {
		pushFail(w, 401, "login_required")
		return
	}
	if !s.options.CheckOrigin(r) {
		pushFail(w, 403, "origin_not_allowed")
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		pushFail(w, 405, "push_method")
		return
	}
	cookie, _ := r.Cookie(installationCookie)
	id := ""
	if cookie != nil {
		id = cookie.Value
	}
	switch r.URL.Path {
	case "/access/push":
		var args struct {
			Subscription   Subscription `json:"subscription"`
			Locale         string       `json:"locale"`
			PrivatePreview bool         `json:"private_preview"`
		}
		if decodeBody(r, &args) != nil || Validate(args.Subscription) != nil {
			pushFail(w, 400, "invalid_subscription")
			return
		}
		id, err := s.subscribe(r.Context(), identity, id, args.Subscription, args.Locale, args.PrivatePreview)
		if err != nil {
			pushFail(w, 503, "push_unavailable")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: installationCookie, Value: id, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 25920000})
		pushReply(w, 200, map[string]bool{"enabled": true})
	case "/access/push/remove":
		if err := s.Remove(r.Context(), identity.Owner, id); err != nil {
			pushFail(w, 500, "subscription_save_failed")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: installationCookie, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		pushReply(w, 200, map[string]bool{"enabled": false})
	case "/access/push/heartbeat":
		var args struct {
			Visible bool `json:"visible"`
		}
		if decodeBody(r, &args) != nil {
			pushFail(w, 400, "push_heartbeat")
			return
		}
		s.mu.Lock()
		record, err := s.load(r.Context(), id)
		if err == nil && record.Owner == identity.Owner && record.Credential == s.options.CredentialEpoch() {
			if args.Visible {
				s.foreground[id] = time.Now().Add(25 * time.Second)
			} else {
				delete(s.foreground, id)
			}
		}
		s.mu.Unlock()
		pushReply(w, 200, map[string]bool{"ok": true})
	case "/access/push/preferences":
		var args struct {
			Locale         string `json:"locale"`
			PrivatePreview *bool  `json:"private_preview"`
		}
		if decodeBody(r, &args) != nil {
			pushFail(w, 400, "push_preferences")
			return
		}
		s.mu.Lock()
		record, err := s.load(r.Context(), id)
		if err == nil && record.Owner == identity.Owner && record.Credential == s.options.CredentialEpoch() {
			record.Locale = normalizedLocale(args.Locale)
			if args.PrivatePreview != nil {
				record.PrivatePreview = *args.PrivatePreview
			}
			err = s.save(r.Context(), record)
		} else if err == nil {
			err = errors.New("installation binding mismatch")
		}
		s.mu.Unlock()
		if err != nil {
			pushFail(w, 400, "push_installation")
			return
		}
		pushReply(w, 200, map[string]bool{"ok": true})
	default:
		pushFail(w, 404, "push_route")
	}
}
