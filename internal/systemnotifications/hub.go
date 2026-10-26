package systemnotifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
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
	sources     map[string]string
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
	return ok && (e.Source == "" || h.sources[e.Source] == e.SourceBoot) && h.control != "" && e.Control == h.control && e.Space == h.control && o.Boot == e.Boot
}
func (h *Hub) signalLocked() {
	for sub := range h.subscribers {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
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

// Accept converts an already committed business fact, without transport policy.
func (h *Hub) Accept(ctx context.Context, fact protocol.LifecycleEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	owner, ok := h.owners[fact.Owner]
	if !ok || fact.Suppressed || fact.ID == "" || (fact.Source != "" && (fact.SourceBoot == "" || h.sources[fact.Source] != fact.SourceBoot)) {
		return nil
	}
	kind := ""
	switch fact.Kind {
	case "reply_completed":
		kind = "reply"
	case "task_completed":
		kind = "task-complete"
	case "task_interrupted":
		kind = "task-failed"
	case "approval_requested":
		kind = "approval"
	default:
		return nil // failed Tasks first enter assistant recovery
	}
	e := event(owner, kind, fact.ID, fact.Task, fact.Name, fact.Preview)
	e.Source, e.SourceBoot = fact.Source, fact.SourceBoot
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = h.publish(ctx, tx, e); err != nil {
		return err
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
