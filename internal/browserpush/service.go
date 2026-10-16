// Package browserpush persists browser installations and relays finalized
// assistant notifications using server-held VAPID/Control credentials.
package browserpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jjyr/pot/internal/storage"
)

type Keys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     Keys   `json:"keys"`
}
type Notification struct {
	AssistantID     string
	Sequence        int64
	MessageID, Text string
	CreatedAtMS     int64
	Final           bool
}
type Feed struct {
	Cursors  map[string]int64
	Messages []Notification
}

// Source uses the configured Control identity and authenticated server-held
// credential. It must never fall back to local messages when Control is offline.
type Source interface {
	Identity() string
	Feed(context.Context, map[string]int64, bool) (Feed, error)
}
type Sender interface {
	PublicKey() string
	Send(context.Context, Subscription, []byte) (int, error)
}
type Identity struct {
	Owner string
	Valid func() bool
}
type Options struct {
	Root            string
	Source          Source
	Sender          Sender
	CredentialEpoch func() string
	Authorize       func(*http.Request) (Identity, error)
	CheckOrigin     func(*http.Request) bool
	SecureCookies   bool
}
type Service struct {
	mu         sync.Mutex
	dispatch   sync.Mutex
	db         *sql.DB
	options    Options
	foreground map[string]time.Time
	disabled   map[string]bool
	closed     bool
}
type record struct {
	ID, Owner, Credential, ControlID, Revision string
	Subscription                               Subscription
	Locale                                     string
	PrivatePreview                             bool
	Cursors                                    map[string]int64
	Failures                                   uint32
	RetryAfterMS                               int64
}

const installationCookie = "__Host-pot-installation"

func Open(options Options) (*Service, error) {
	if !filepath.IsAbs(options.Root) || options.CredentialEpoch == nil || options.Authorize == nil || options.CheckOrigin == nil {
		return nil, errors.New("push requires private root and explicit browser authentication")
	}
	db, err := storage.OpenSQLite(filepath.Join(options.Root, "browser-push.sqlite"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS browser_push_installations(id TEXT PRIMARY KEY,body TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return &Service{db: db, options: options, foreground: map[string]time.Time{}, disabled: map[string]bool{}}, nil
}
func Validate(subscription Subscription) error {
	u, err := url.Parse(subscription.Endpoint)
	if err != nil || len(subscription.Endpoint) > 2048 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return errors.New("invalid push endpoint")
	}
	switch u.Hostname() {
	case "fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com":
	default:
		return errors.New("unsupported push provider")
	}
	key, err := base64.RawURLEncoding.DecodeString(subscription.Keys.P256dh)
	if err != nil || len(key) != 65 || key[0] != 4 {
		return errors.New("invalid browser public key")
	}
	if _, err := ecdh.P256().NewPublicKey(key); err != nil {
		return errors.New("invalid browser curve point")
	}
	auth, err := base64.RawURLEncoding.DecodeString(subscription.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return errors.New("invalid browser auth secret")
	}
	return nil
}
func (s *Service) Available() bool {
	return s.options.SecureCookies && s.options.Source != nil && s.options.Sender != nil && s.options.Source.Identity() != ""
}
func (s *Service) PublicKey() string {
	if !s.Available() {
		return ""
	}
	return s.options.Sender.PublicKey()
}
func (s *Service) load(ctx context.Context, id string) (record, error) {
	var r record
	var data []byte
	if err := s.db.QueryRowContext(ctx, "SELECT body FROM browser_push_installations WHERE id=?", id).Scan(&data); err != nil {
		return r, err
	}
	err := json.Unmarshal(data, &r)
	return r, err
}
func (s *Service) save(ctx context.Context, r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO browser_push_installations VALUES (?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", r.ID, data)
	return err
}
func (s *Service) all(ctx context.Context) ([]record, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM browser_push_installations ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []record
	for rows.Next() {
		var data []byte
		var r record
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type State struct {
	Enabled        bool   `json:"enabled"`
	PrivatePreview bool   `json:"private_preview"`
	Locale         string `json:"locale"`
	Failures       uint32 `json:"failures"`
}

func (s *Service) State(ctx context.Context, owner, id string) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.load(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Owner != owner {
		return nil, nil
	}
	return &State{Enabled: s.Available() && !s.disabled[id] && r.ControlID == s.options.Source.Identity() && r.Credential == s.options.CredentialEpoch(), PrivatePreview: r.PrivatePreview, Locale: r.Locale, Failures: r.Failures}, nil
}
func (s *Service) Remove(ctx context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.load(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Owner != owner {
		return errors.New("installation belongs to another browser login")
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM browser_push_installations WHERE id=?", id)
	if err == nil {
		delete(s.foreground, id)
		delete(s.disabled, id)
	}
	return err
}
func (s *Service) RevokeOwner(ctx context.Context, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.all(ctx)
	if err != nil {
		return err
	}
	return storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		for _, r := range records {
			if r.Owner == owner {
				if _, err := tx.ExecContext(ctx, "DELETE FROM browser_push_installations WHERE id=?", r.ID); err != nil {
					return err
				}
				delete(s.foreground, r.ID)
			}
		}
		return nil
	})
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.dispatch.Lock()
	defer s.dispatch.Unlock()
	return s.db.Close()
}
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.Dispatch(ctx); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}
func normalizedLocale(locale string) string {
	if locale == "zh-CN" {
		return locale
	}
	return "en"
}

func Payload(private bool, locale string, n Notification) []byte {
	preview := n.Text
	if len(preview) > 2000 {
		preview = preview[:2000]
		for !utf8.ValidString(preview) {
			preview = preview[:len(preview)-1]
		}
	}
	if private {
		preview = "New message from your assistant"
		if locale == "zh-CN" {
			preview = "助手发来新消息"
		}
	}
	for {
		data, _ := json.Marshal(map[string]any{"title": "Pot", "body": preview, "message_id": n.MessageID, "conversation": n.AssistantID, "url": "/?page=chat&assistant=" + url.QueryEscape(n.AssistantID) + "&message=" + url.QueryEscape(n.MessageID), "tag": n.AssistantID + ":" + n.MessageID})
		if len(data) <= 3000 || preview == "" {
			return data
		}
		for range 50 {
			if preview == "" {
				break
			}
			_, size := utf8.DecodeLastRuneInString(preview)
			preview = preview[:len(preview)-size]
		}
	}
}
func (s *Service) Dispatch(ctx context.Context) error {
	s.dispatch.Lock()
	defer s.dispatch.Unlock()
	s.mu.Lock()
	if s.closed || !s.Available() {
		s.mu.Unlock()
		return nil
	}
	records, err := s.all(ctx)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, snapshot := range records {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if snapshot.Failures >= 7 || snapshot.RetryAfterMS > time.Now().UnixMilli() || snapshot.ControlID != s.options.Source.Identity() || snapshot.Credential != s.options.CredentialEpoch() {
			continue
		}
		feedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		feed, err := s.options.Source.Feed(feedCtx, snapshot.Cursors, false)
		cancel()
		if err != nil {
			continue
		}
		for _, n := range feed.Messages {
			if !n.Final || n.Sequence <= 0 || n.AssistantID == "" || n.MessageID == "" || len(n.AssistantID) > 256 || len(n.MessageID) > 256 {
				continue
			}
			s.mu.Lock()
			current, loadErr := s.load(ctx, snapshot.ID)
			eligible := loadErr == nil && !s.closed && !s.disabled[snapshot.ID] && current.Revision == snapshot.Revision && current.Cursors[n.AssistantID] < n.Sequence && current.Credential == s.options.CredentialEpoch() && current.ControlID == s.options.Source.Identity()
			visible := time.Now().Before(s.foreground[snapshot.ID])
			s.mu.Unlock()
			if !eligible {
				break
			}
			code := http.StatusNoContent
			var sendErr error
			if !visible && n.CreatedAtMS >= time.Now().Add(-24*time.Hour).UnixMilli() {
				sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				code, sendErr = s.options.Sender.Send(sendCtx, current.Subscription, Payload(current.PrivatePreview, current.Locale, n))
				cancel()
			}
			s.mu.Lock()
			latest, loadErr := s.load(context.WithoutCancel(ctx), snapshot.ID)
			if loadErr != nil || latest.Revision != snapshot.Revision || s.closed || latest.Credential != s.options.CredentialEpoch() || latest.ControlID != s.options.Source.Identity() {
				s.mu.Unlock()
				break
			}
			removed := false
			switch {
			case sendErr == nil && code >= 200 && code < 300:
				if latest.Cursors == nil {
					latest.Cursors = map[string]int64{}
				}
				latest.Cursors[n.AssistantID] = max(latest.Cursors[n.AssistantID], n.Sequence)
				latest.Failures = 0
				latest.RetryAfterMS = 0
			case sendErr == nil && (code == 404 || code == 410):
				_, err = s.db.ExecContext(context.WithoutCancel(ctx), "DELETE FROM browser_push_installations WHERE id=?", latest.ID)
				removed = true
			default:
				latest.Failures++
				latest.RetryAfterMS = time.Now().UnixMilli() + 1000*(1<<min(latest.Failures, 10))
			}
			if !removed {
				err = s.save(context.WithoutCancel(ctx), latest)
			}
			if err != nil {
				s.disabled[snapshot.ID] = true
			}
			s.mu.Unlock()
			if removed || err != nil || sendErr != nil || code < 200 || code >= 300 {
				break
			}
		}
	}
	return nil
}

func (s *Service) subscribe(ctx context.Context, identity Identity, id string, subscription Subscription, locale string, private bool) (string, error) {
	if !s.Available() {
		return "", errors.New("push unavailable")
	}
	if err := Validate(subscription); err != nil {
		return "", err
	}
	credential, control := s.options.CredentialEpoch(), s.options.Source.Identity()
	feedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	feed, err := s.options.Source.Feed(feedCtx, nil, true)
	cancel()
	if err != nil {
		return "", errors.New("Control notification source unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !identity.Valid() || credential != s.options.CredentialEpoch() || control != s.options.Source.Identity() {
		return "", errors.New("browser credential or Control changed")
	}
	previous, lookup := s.load(ctx, id)
	existingOwned := lookup == nil && previous.Owner == identity.Owner
	if !existingOwned {
		id = rand.Text()
	}
	all, err := s.all(ctx)
	if err != nil {
		return "", err
	}
	if len(all) >= 128 && !existingOwned {
		return "", errors.New("push installation limit")
	}
	cursors := feed.Cursors
	if lookup == nil && previous.Owner == identity.Owner && previous.Credential == credential && previous.ControlID == control && previous.Subscription.Endpoint == subscription.Endpoint {
		cursors = previous.Cursors
	}
	r := record{ID: id, Owner: identity.Owner, Credential: credential, ControlID: control, Revision: rand.Text(), Subscription: subscription, Locale: normalizedLocale(locale), PrivatePreview: private, Cursors: cursors}
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		for _, old := range all {
			if old.Subscription.Endpoint == subscription.Endpoint {
				if _, err := tx.ExecContext(ctx, "DELETE FROM browser_push_installations WHERE id=?", old.ID); err != nil {
					return err
				}
			}
		}
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO browser_push_installations VALUES (?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", r.ID, data)
		return err
	})
	if err == nil {
		delete(s.disabled, id)
	}
	return id, err
}
