package browsernotifications

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/internal/systemnotifications"
	"time"
)

// Local consumes notifications for browser constructors. Its receiver lifetime
// and receipts are browser concerns; native desktop does not register here.
type Local struct {
	Store  *Store
	Events systemnotifications.Authority
}

func (l *Local) Complete(ctx context.Context, id, binding, event string, attempt int, state string) error {
	if state != "accepted" && state != "failed" && state != "unknown" && state != "suppressed" {
		return errors.New("invalid local notification result")
	}
	next := time.Now()
	if state == "failed" && attempt < 3 {
		state = "queued"
		next = next.Add(5 * time.Second)
	}
	return l.Store.Finish(ctx, id, binding, event, attempt, state, next)
}
func (l *Local) Baseline(ctx context.Context, id, binding, session string) error {
	s := l.Store
	s.mu.Lock()
	defer s.mu.Unlock()
	for active, count := range s.receivers[id] {
		if active != session && count > 0 {
			return nil
		}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE notification_deliveries SET state='expired' WHERE device=? AND channel='local' AND state='queued' AND EXISTS(SELECT 1 FROM notification_devices WHERE id=? AND binding=?)`, id, id, binding)
	return err
}
func (l *Local) Next(ctx context.Context, id, binding, session string) (*Delivery, error) {
	if session == "" || len(session) > 128 {
		return nil, errors.New("invalid notification receiver session")
	}
	s := l.Store
	s.mu.Lock()
	var count int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM notification_devices WHERE id=? AND binding=? AND channel='local' AND enabled=1 AND permission='granted' AND expires_ms>?", id, binding, time.Now().UnixMilli()).Scan(&count)
	if err != nil || count != 1 {
		s.mu.Unlock()
		return nil, errors.New("notification receiver unavailable")
	}
	if s.receivers[id] == nil {
		s.receivers[id] = map[string]int{}
	}
	s.receivers[id][session]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.receivers[id][session]--
		if s.receivers[id][session] <= 0 {
			delete(s.receivers[id], session)
		}
		if len(s.receivers[id]) == 0 {
			delete(s.receivers, id)
		}
	}()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		d, out, err := s.Reserve(ctx, id, binding, "local", 25*time.Second, true)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if out.Event.ID != "" {
			if l.Events.Current(out.Event) {
				return &out, nil
			}
			if err = s.Finish(ctx, d.ID, d.Binding, out.Event.ID, out.Attempt, "expired", time.Now()); err != nil {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, nil
		case <-s.wake:
		case <-time.After(time.Second):
		}
	}
}
