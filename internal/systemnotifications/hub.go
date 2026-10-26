package systemnotifications

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/storage"
)

// Result distinguishes acceptance from display. An uncertain network result is
// terminal, preventing a retry or alternate channel from producing a duplicate.
type Result struct {
	State      string
	RetryAfter time.Duration
	Invalid    bool
}
type Device struct {
	ID           string `json:"id"`
	Control      string `json:"control"`
	Channel      string `json:"channel"`
	Enabled      bool   `json:"enabled"`
	Permission   string `json:"permission"`
	Locale       string `json:"locale"`
	Subscription string `json:"subscription,omitempty"`
	Binding      string `json:"-"`
	Expires      int64  `json:"-"`
	Revision     int64  `json:"-"`
}

var ErrSubscriptionExpired = errors.New("push subscription expired")

type Sender func(context.Context, Device, Event) Result
type Hub struct {
	db        *sql.DB
	mu        sync.Mutex
	wake      chan struct{}
	owners    map[string]Owner
	control   string
	Push      Sender
	Viewing   func(Event) bool
	receivers map[string]map[string]int
}

func OpenHub(path string) (*Hub, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	// Interrupted sends may already have been accepted. Never blindly replay them.
	if _, err = db.Exec("UPDATE notification_deliveries SET state='unknown' WHERE state='sending'"); err != nil {
		db.Close()
		return nil, err
	}
	return &Hub{db: db, wake: make(chan struct{}, 1), owners: map[string]Owner{}, receivers: map[string]map[string]int{}}, nil
}
func (h *Hub) Close() error { return h.db.Close() }
func (h *Hub) Signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *Hub) Secret(id string, create func() ([]byte, error)) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var value []byte
	err := h.db.QueryRow("SELECT value FROM notification_secrets WHERE id=?", id).Scan(&value)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	value, err = create()
	if err != nil {
		return nil, err
	}
	_, err = h.db.Exec("INSERT INTO notification_secrets(id,value) VALUES(?,?)", id, value)
	return value, err
}
func (h *Hub) ValidateScope(control, space string, owners []string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.control == "" || control != h.control || space != h.control {
		return false
	}
	for _, id := range owners {
		if _, ok := h.owners[id]; !ok {
			return false
		}
	}
	return true
}
func (h *Hub) Register(ctx context.Context, d Device) error {
	if d.Control == "" || len(d.Control) > 256 || len(d.ID) < 20 || len(d.ID) > 128 || len(d.Binding) > 256 || d.Binding == "" || (d.Channel != "local" && d.Channel != "push") || len(d.Subscription) > 8192 {
		return errors.New("invalid notification device")
	}
	if d.Permission != "granted" && d.Permission != "default" && d.Permission != "denied" {
		return errors.New("invalid notification permission")
	}
	if d.Locale != "zh-CN" {
		d.Locale = "en"
	}
	if d.Channel == "local" {
		d.Subscription = ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous Device
	var previousHash string
	var invalidated bool
	err = tx.QueryRow("SELECT binding,control_id,channel,subscription,enabled,permission,revision,subscription_hash,invalidated FROM notification_devices WHERE id=?", d.ID).Scan(&previous.Binding, &previous.Control, &previous.Channel, &previous.Subscription, &previous.Enabled, &previous.Permission, &previous.Revision, &previousHash, &invalidated)
	changed := errors.Is(err, sql.ErrNoRows)
	if err != nil && !changed {
		return err
	}
	if !changed && previous.Binding != d.Binding {
		return errors.New("notification device belongs to another session")
	}
	fingerprint := ""
	if d.Subscription != "" {
		fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(d.Subscription)))
	}
	if d.Channel == "push" && invalidated && fingerprint == previousHash {
		return ErrSubscriptionExpired
	}
	changed = changed || previous.Control != d.Control || previous.Channel != d.Channel || previous.Subscription != d.Subscription || previous.Enabled != d.Enabled || previous.Permission != d.Permission
	if changed {
		if _, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET state='expired' WHERE device=? AND state='queued'", d.ID); err != nil {
			return err
		}
	}
	// One subscription endpoint represents one installation. A reauthenticated
	// installation replaces its old binding rather than retaining another sender.
	if d.Channel == "push" && d.Subscription != "" {
		if _, err = tx.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,subscription='',revision=revision+1 WHERE id<>? AND subscription=?", d.ID, d.Subscription); err != nil {
			return err
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_devices WHERE expires_ms>?", time.Now().UnixMilli()).Scan(&count); err != nil {
		return err
	}
	if previous.Revision == 0 && count >= 256 {
		return errors.New("too many notification installations")
	}
	revision := previous.Revision
	if changed {
		revision++
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_devices(id,binding,control_id,channel,enabled,permission,locale,subscription,subscription_hash,invalidated,expires_ms,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET control_id=excluded.control_id,channel=excluded.channel,enabled=excluded.enabled,permission=excluded.permission,locale=excluded.locale,subscription=excluded.subscription,subscription_hash=excluded.subscription_hash,invalidated=0,expires_ms=excluded.expires_ms,revision=excluded.revision`, d.ID, d.Binding, d.Control, d.Channel, d.Enabled, d.Permission, d.Locale, d.Subscription, fingerprint, false, d.Expires, revision)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (h *Hub) Revoke(ctx context.Context, binding string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.db.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,subscription='',revision=revision+1 WHERE binding=?", binding)
	return err
}
func (h *Hub) Remove(ctx context.Context, id, binding string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.db.ExecContext(ctx, "DELETE FROM notification_devices WHERE id=? AND binding=?", id, binding)
	return err
}
func (h *Hub) Lease(ctx context.Context, id, binding, session string, view View) error {
	if session == "" || len(session) > 128 || view.Sequence == 0 || view.Sequence > 9007199254740991 {
		return errors.New("invalid notification session sequence")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var invalidated bool
	if err := h.db.QueryRowContext(ctx, "SELECT invalidated FROM notification_devices WHERE id=? AND binding=?", id, binding).Scan(&invalidated); err != nil {
		return err
	}
	if invalidated {
		return ErrSubscriptionExpired
	}
	body, _ := json.Marshal(view)
	_, err := h.db.ExecContext(ctx, `INSERT INTO notification_views(device,session,body,sequence,expires_ms) VALUES(?,?,?,?,?)
 ON CONFLICT(device,session) DO UPDATE SET body=excluded.body,sequence=excluded.sequence,expires_ms=excluded.expires_ms WHERE excluded.sequence>notification_views.sequence`, id, session, string(body), view.Sequence, time.Now().Add(25*time.Second).UnixMilli())
	return err
}
func suppressed(ctx context.Context, tx *sql.Tx, id string, event Event, now int64) (bool, bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM notification_views WHERE device=? AND expires_ms>?", id, now)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	active := false
	viewing := false
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return false, false, err
		}
		var view View
		if json.Unmarshal([]byte(body), &view) == nil {
			active = true
			viewing = viewing || view.Viewing(event)
		}
	}
	return viewing, active, rows.Err()
}
func (h *Hub) commitSource(ctx context.Context, owner string, next sourceState, events []Event, reader ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	approvals := ""
	if next.Approvals != nil {
		body, _ := json.Marshal(next.Approvals)
		approvals = string(body)
	}
	part := "all"
	if len(reader) > 0 {
		part = reader[0]
	}
	switch part {
	case "reply":
		_, err = tx.ExecContext(ctx, `INSERT INTO notification_sources(owner,boot,reply_id,reply_count,task_cursor,approvals) VALUES(?,?,?,?,NULL,NULL)
 ON CONFLICT(owner) DO UPDATE SET boot=excluded.boot,reply_id=excluded.reply_id,reply_count=excluded.reply_count`, owner, next.Boot, next.Reply, next.Count)
	case "task", "approval":
		var result sql.Result
		if part == "task" {
			result, err = tx.ExecContext(ctx, "UPDATE notification_sources SET task_cursor=? WHERE owner=? AND boot=?", next.Cursor, owner, next.Boot)
		} else {
			result, err = tx.ExecContext(ctx, "UPDATE notification_sources SET approvals=? WHERE owner=? AND boot=?", approvals, owner, next.Boot)
		}
		if err == nil {
			rows, _ := result.RowsAffected()
			if rows != 1 {
				return ErrSourceChanged
			}
		}
	default:
		_, err = tx.ExecContext(ctx, `INSERT INTO notification_sources(owner,boot,reply_id,reply_count,task_cursor,approvals) VALUES(?,?,?,?,?,?)
 ON CONFLICT(owner) DO UPDATE SET boot=excluded.boot,reply_id=excluded.reply_id,reply_count=excluded.reply_count,task_cursor=excluded.task_cursor,approvals=excluded.approvals`, owner, next.Boot, next.Reply, next.Count, next.Cursor, approvals)
	}
	if err != nil {
		return err
	}
	for _, event := range events {
		if err = h.publish(ctx, tx, event); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err == nil {
		h.Signal()
	}
	return err
}
func (h *Hub) publish(ctx context.Context, tx *sql.Tx, event Event) error {
	now := time.Now().UnixMilli()
	body, _ := json.Marshal(event)
	inserted, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO notification_events(id,body,created_ms) VALUES(?,?,?)", event.ID, string(body), now)
	if err != nil {
		return err
	}
	n, _ := inserted.RowsAffected()
	if n == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,channel,revision FROM notification_devices WHERE enabled=1 AND permission='granted' AND expires_ms>? AND control_id=?", now, event.Control)
	if err != nil {
		return err
	}
	type target struct {
		id, channel string
		revision    int64
	}
	var targets []target
	for rows.Next() {
		var d target
		if err = rows.Scan(&d.id, &d.channel, &d.revision); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range targets {
		viewing, active, err := suppressed(ctx, tx, d.id, event, now)
		if err != nil {
			return err
		}
		state := "queued"
		if viewing {
			state = "suppressed"
		} else if d.channel == "local" && !active {
			state = "expired"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO notification_deliveries(event,device,channel,revision,state,next_ms,updated_ms) VALUES(?,?,?,?,?,?,?)`, event.ID, d.id, d.channel, d.revision, state, now, now); err != nil {
			return err
		}
	}
	return nil
}

// Publish is used by committed source adapters and isolated contract tests.
func (h *Hub) Publish(ctx context.Context, event Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = h.publish(ctx, tx, event); err != nil {
		return err
	}
	err = tx.Commit()
	if err == nil {
		h.Signal()
	}
	return err
}

// RevokeAll stops every subscription when the operator replaces access authority.
func (h *Hub) RevokeAll(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.db.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,subscription='',revision=revision+1")
	return err
}
func (h *Hub) Baseline(ctx context.Context, id, binding, session string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// A crashed page's unexpired reading lease is not evidence of an active
	// sibling receiver. Only a current delivery request can retain live work.
	for active, count := range h.receivers[id] {
		if active != session && count > 0 {
			return nil
		}
	}
	_, err := h.db.ExecContext(ctx, `UPDATE notification_deliveries SET state='expired' WHERE device=? AND channel='local' AND state='queued'
 AND EXISTS(SELECT 1 FROM notification_devices WHERE id=? AND binding=?)`, id, id, binding)
	return err
}

func (h *Hub) Control() string { h.mu.Lock(); defer h.mu.Unlock(); return h.control }

// Reconcile removes authority that expired or was revoked while this host was
// stopped. It runs before starting the dispatcher, including a disabled server.
func (h *Hub) Reconcile(ctx context.Context, valid func(string) bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rows, err := h.db.QueryContext(ctx, "SELECT DISTINCT binding FROM notification_devices WHERE enabled=1")
	if err != nil {
		return err
	}
	var invalid []string
	for rows.Next() {
		var binding string
		if err = rows.Scan(&binding); err != nil {
			rows.Close()
			return err
		}
		if !valid(binding) {
			invalid = append(invalid, binding)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, binding := range invalid {
		if _, err = h.db.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,subscription='',revision=revision+1 WHERE binding=?", binding); err != nil {
			return err
		}
	}
	return nil
}

func (h *Hub) resetSource(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.control = ""
	h.owners = map[string]Owner{}
	_, err := h.db.ExecContext(ctx, "UPDATE notification_deliveries SET state='expired' WHERE state='queued'")
	return err
}
