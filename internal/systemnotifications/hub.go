package systemnotifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/storage"
	"sync"
	"time"
)

// Hub produces committed business events. Subscribers have independent cursors;
// wakeups only prompt reading the durable event log and never carry the event.
// Delivery mechanisms, permissions and installation state belong to consumers.
type Hub struct {
	db          *sql.DB
	mu          sync.Mutex
	owners      map[string]Owner
	control     string
	subscribers map[*Subscription]struct{}
}

func OpenHub(path string) (*Hub, error) {
	db, err := storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("DELETE FROM notification_events WHERE created_ms<?", time.Now().Add(-7*24*time.Hour).UnixMilli()); err != nil {
		db.Close()
		return nil, err
	}
	return &Hub{db: db, owners: map[string]Owner{}, subscribers: map[*Subscription]struct{}{}}, nil
}
func (h *Hub) Close() error         { return h.db.Close() }
func (h *Hub) Control() string      { h.mu.Lock(); defer h.mu.Unlock(); return h.control }
func (h *Hub) Current(e Event) bool { h.mu.Lock(); defer h.mu.Unlock(); return h.current(e) }
func (h *Hub) current(e Event) bool {
	o, ok := h.owners[e.Owner]
	return ok && h.control != "" && e.Control == h.control && e.Space == h.control && o.Boot == e.Boot
}
func (h *Hub) signalLocked() {
	for sub := range h.subscribers {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}
func (h *Hub) resetSource(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.control = ""
	h.owners = map[string]Owner{}
	h.signalLocked()
	return nil
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
		h.signalLocked()
	}
	return err
}

func (h *Hub) publish(ctx context.Context, tx *sql.Tx, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO notification_events(id,body,created_ms) VALUES(?,?,?)", event.ID, string(body), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM notification_events WHERE created_ms<?", time.Now().Add(-7*24*time.Hour).UnixMilli())
	return err
}

type Record struct {
	Sequence int64
	Event    Event
	Created  time.Time
}
type Subscription struct {
	hub    *Hub
	wake   chan struct{}
	cursor int64
}

// Subscribe establishes a fresh baseline, without replaying old notifications.
// Unacknowledged records remain in the log during a running consumer's retries.
func (h *Hub) Subscribe() (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := &Subscription{hub: h, wake: make(chan struct{}, 1)}
	if err := h.db.QueryRow("SELECT coalesce(max(sequence),0) FROM notification_events").Scan(&sub.cursor); err != nil {
		return nil, err
	}
	h.subscribers[sub] = struct{}{}
	return sub, nil
}
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	delete(s.hub.subscribers, s)
}
func (s *Subscription) Ack(record Record) { s.cursor = record.Sequence }
func (s *Subscription) Next(ctx context.Context) (Record, error) {
	for {
		var r Record
		var body string
		var created int64
		err := s.hub.db.QueryRowContext(ctx, "SELECT sequence,body,created_ms FROM notification_events WHERE sequence>? ORDER BY sequence LIMIT 1", s.cursor).Scan(&r.Sequence, &body, &created)
		if err == nil {
			if err = json.Unmarshal([]byte(body), &r.Event); err != nil {
				return r, err
			}
			r.Created = time.UnixMilli(created)
			if s.hub.Control() != "" {
				if s.hub.Current(r.Event) {
					return r, nil
				}
				s.Ack(r)
				continue
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return r, err
		}
		select {
		case <-ctx.Done():
			return Record{}, ctx.Err()
		case <-s.wake:
		case <-time.After(time.Second):
		}
	}
}
