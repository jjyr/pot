// Package browsernotifications owns browser installation intent, reading leases
// and per-installation receipts. It is not the business event producer. Native
// desktop notifications do not use this browser protocol.
package browsernotifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/systemnotifications"
	"github.com/jjyr/pot/pkg/storage"
)

type Device struct {
	ID, Binding, Control, Channel, Permission, Locale string
	Enabled                                           bool
	Expires, Revision                                 int64
}
type Delivery struct {
	Event   systemnotifications.Event `json:"event"`
	Attempt int                       `json:"attempt"`
}
type Store struct {
	DB        *sql.DB
	mu        sync.Mutex
	wake      chan struct{}
	receivers map[string]map[string]int
}

func Open(path string) (*Store, error) {
	db, err := storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("UPDATE notification_deliveries SET state='unknown' WHERE state='sending'"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, wake: make(chan struct{}, 1), receivers: map[string]map[string]int{}}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Store) Transaction(ctx context.Context, apply func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = apply(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		s.Signal()
	}
	return err
}
func (s *Store) Register(ctx context.Context, d Device) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error { return s.RegisterTx(ctx, tx, d, false) })
}

// RegisterTx shares the writer transaction with a transport's subscription change.
func (s *Store) RegisterTx(ctx context.Context, tx *sql.Tx, d Device, changed bool) error {
	if d.Control == "" || len(d.Control) > 256 || len(d.ID) < 20 || len(d.ID) > 128 || len(d.Binding) > 256 || d.Binding == "" || (d.Channel != "local" && d.Channel != "push") {
		return errors.New("invalid browser notification device")
	}
	if d.Permission != "granted" && d.Permission != "default" && d.Permission != "denied" {
		return errors.New("invalid notification permission")
	}
	if d.Locale != "zh-CN" {
		d.Locale = "en"
	}
	var old Device
	var after int64
	err := tx.QueryRowContext(ctx, "SELECT binding,control_id,channel,enabled,permission,revision,after_sequence FROM notification_devices WHERE id=?", d.ID).Scan(&old.Binding, &old.Control, &old.Channel, &old.Enabled, &old.Permission, &old.Revision, &after)
	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return err
	}
	if !fresh && old.Binding != d.Binding {
		return errors.New("notification device belongs to another session")
	}
	changed = changed || fresh || old.Control != d.Control || old.Channel != d.Channel || old.Enabled != d.Enabled || old.Permission != d.Permission
	revision := old.Revision
	if changed {
		revision++
		if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0) FROM notification_events").Scan(&after); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET state='expired' WHERE device=? AND state='queued'", d.ID); err != nil {
			return err
		}
	}
	if fresh {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_devices WHERE expires_ms>?", time.Now().UnixMilli()).Scan(&count); err != nil {
			return err
		}
		if count >= 256 {
			return errors.New("too many notification installations")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_devices(id,binding,control_id,channel,enabled,permission,locale,expires_ms,revision,after_sequence) VALUES(?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET control_id=excluded.control_id,channel=excluded.channel,enabled=excluded.enabled,permission=excluded.permission,locale=excluded.locale,expires_ms=excluded.expires_ms,revision=excluded.revision,after_sequence=excluded.after_sequence`, d.ID, d.Binding, d.Control, d.Channel, d.Enabled, d.Permission, d.Locale, d.Expires, revision, after)
	return err
}
func (s *Store) Revoke(ctx context.Context, binding string) error {
	query := "UPDATE notification_devices SET enabled=0,revision=revision+1"
	args := []any{}
	if binding != "" {
		query += " WHERE binding=?"
		args = append(args, binding)
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, query, args...); return err })
}
func (s *Store) Remove(ctx context.Context, id, binding string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM notification_devices WHERE id=? AND binding=?", id, binding)
		return err
	})
}
func (s *Store) Reconcile(ctx context.Context, valid func(string) bool) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT DISTINCT binding FROM notification_devices WHERE enabled=1")
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
			if _, err = tx.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,revision=revision+1 WHERE binding=?", binding); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) Lease(ctx context.Context, id, binding, session string, view systemnotifications.View) error {
	if session == "" || len(session) > 128 || view.Sequence == 0 || view.Sequence > 9007199254740991 {
		return errors.New("invalid notification session sequence")
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_devices WHERE id=? AND binding=?", id, binding).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("notification installation unavailable")
		}
		body, _ := json.Marshal(view)
		_, err := tx.ExecContext(ctx, `INSERT INTO notification_views(device,session,body,sequence,expires_ms) VALUES(?,?,?,?,?) ON CONFLICT(device,session) DO UPDATE SET body=excluded.body,sequence=excluded.sequence,expires_ms=excluded.expires_ms WHERE excluded.sequence>notification_views.sequence`, id, session, string(body), view.Sequence, time.Now().Add(25*time.Second).UnixMilli())
		return err
	})
}
func reading(ctx context.Context, tx *sql.Tx, id string, event systemnotifications.Event) (bool, bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT body FROM notification_views WHERE device=? AND expires_ms>?", id, time.Now().UnixMilli())
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	viewing, active := false, false
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return false, false, err
		}
		var view systemnotifications.View
		if json.Unmarshal([]byte(body), &view) == nil {
			active = true
			viewing = viewing || view.Viewing(event)
		}
	}
	return viewing, active, rows.Err()
}

// Run selects each installation's single channel and queues its receipt once.
// Failed queue writes retain this subscription's record for retry.
func (s *Store) Run(ctx context.Context, sub *systemnotifications.Subscription) {
	defer sub.Close()
	for ctx.Err() == nil {
		record, err := sub.Next(ctx)
		if err == nil {
			err = s.Enqueue(ctx, record)
		}
		if err == nil {
			sub.Ack(record)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (s *Store) Enqueue(ctx context.Context, r systemnotifications.Record) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id,channel,revision FROM notification_devices WHERE enabled=1 AND permission='granted' AND expires_ms>? AND control_id=? AND after_sequence<?", time.Now().UnixMilli(), r.Event.Control, r.Sequence)
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
			viewing, active, err := reading(ctx, tx, d.id, r.Event)
			if err != nil {
				return err
			}
			state := "queued"
			if viewing {
				state = "suppressed"
			} else if d.channel == "local" && !active {
				state = "expired"
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO notification_deliveries(event,device,channel,revision,state,next_ms,updated_ms) VALUES(?,?,?,?,?,?,?)", r.Event.ID, d.id, d.channel, d.revision, state, r.Created.UnixMilli(), time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// Reserve records uncertainty before giving an effect to a transport. Age and
// presence requirements are supplied by that transport, not by the event source.
func (s *Store) Reserve(ctx context.Context, id, binding, route string, maxAge time.Duration, requireLive bool) (Device, Delivery, error) {
	var d Device
	var out Delivery
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		query := `SELECT d.id,d.binding,d.control_id,d.channel,d.enabled,d.permission,d.locale,d.expires_ms,d.revision,e.body,q.attempts,e.created_ms FROM notification_deliveries q JOIN notification_devices d ON d.id=q.device JOIN notification_events e ON e.id=q.event WHERE q.state='queued' AND q.channel=? AND q.next_ms<=? AND d.enabled=1 AND d.permission='granted' AND d.expires_ms>? AND q.revision=d.revision`
		now := time.Now()
		args := []any{route, now.UnixMilli(), now.UnixMilli()}
		if id != "" {
			query += " AND d.id=? AND d.binding=?"
			args = append(args, id, binding)
		}
		query += " ORDER BY e.sequence LIMIT 1"
		var body string
		var created int64
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&d.ID, &d.Binding, &d.Control, &d.Channel, &d.Enabled, &d.Permission, &d.Locale, &d.Expires, &d.Revision, &body, &out.Attempt, &created); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(body), &out.Event); err != nil {
			return err
		}
		viewing, active, err := reading(ctx, tx, d.ID, out.Event)
		if err != nil {
			return err
		}
		state := "sending"
		if now.Sub(time.UnixMilli(created)) > maxAge || requireLive && !active {
			state = "expired"
		} else if viewing {
			state = "suppressed"
		}
		out.Attempt++
		_, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET state=?,attempts=?,updated_ms=? WHERE event=? AND device=?", state, out.Attempt, now.UnixMilli(), out.Event.ID, d.ID)
		if state != "sending" {
			out = Delivery{}
		}
		return err
	})
	return d, out, err
}
func (s *Store) Finish(ctx context.Context, id, binding, event string, attempt int, state string, next time.Time) error {
	if state != "accepted" && state != "failed" && state != "unknown" && state != "suppressed" && state != "expired" && state != "queued" {
		return errors.New("invalid notification delivery result")
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET state=?,next_ms=?,updated_ms=? WHERE event=? AND device=? AND attempts=? AND state='sending' AND EXISTS(SELECT 1 FROM notification_devices d WHERE d.id=? AND d.binding=? AND d.revision=notification_deliveries.revision)`, state, next.UnixMilli(), time.Now().UnixMilli(), event, id, attempt, id, binding)
		return err
	})
}
func (s *Store) Maintain(ctx context.Context) {
	_ = s.Transaction(ctx, func(tx *sql.Tx) error {
		for _, q := range []struct {
			query  string
			before int64
		}{
			{"UPDATE notification_devices SET enabled=0,revision=revision+1 WHERE enabled=1 AND expires_ms<?", time.Now().UnixMilli()},
			{"DELETE FROM notification_devices WHERE expires_ms<?", time.Now().Add(-7 * 24 * time.Hour).UnixMilli()},
			{"DELETE FROM notification_views WHERE expires_ms<?", time.Now().UnixMilli()},
		} {
			if _, err := tx.ExecContext(ctx, q.query, q.before); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ExpireSends(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, "UPDATE notification_deliveries SET state='unknown' WHERE state='sending' AND updated_ms<?", time.Now().Add(-time.Minute).UnixMilli())
}
