package systemnotifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Delivery struct {
	Event   Event `json:"event"`
	Attempt int   `json:"attempt"`
}

// reserve atomically chooses a single channel and records an uncertain send
// before handing bytes to any transport. It never consumes another device.
func (h *Hub) reserve(ctx context.Context, id, binding, channel string) (Device, Delivery, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var device Device
	if h.control == "" {
		return device, Delivery{}, sql.ErrNoRows
	}
	var result Delivery
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return device, result, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	query := `SELECT d.id,d.binding,d.control_id,d.channel,d.enabled,d.permission,d.locale,d.subscription,d.expires_ms,d.revision,e.body,q.attempts,e.created_ms
 FROM notification_deliveries q JOIN notification_devices d ON d.id=q.device JOIN notification_events e ON e.id=q.event
 WHERE q.state='queued' AND q.channel=? AND q.next_ms<=? AND d.enabled=1 AND d.permission='granted' AND d.expires_ms>? AND q.revision=d.revision`
	args := []any{channel, now, now}
	if id != "" {
		query += " AND d.id=? AND d.binding=?"
		args = append(args, id, binding)
	}
	query += " ORDER BY e.sequence LIMIT 1"
	var body string
	var created int64
	err = tx.QueryRowContext(ctx, query, args...).Scan(&device.ID, &device.Binding, &device.Control, &device.Channel, &device.Enabled, &device.Permission, &device.Locale, &device.Subscription, &device.Expires, &device.Revision, &body, &result.Attempt, &created)
	if err != nil {
		return device, result, err
	}
	if err = json.Unmarshal([]byte(body), &result.Event); err != nil {
		return device, result, err
	}
	viewing, active, err := suppressed(ctx, tx, device.ID, result.Event, now)
	if err != nil {
		return device, result, err
	}
	if h.Viewing != nil {
		viewing = h.Viewing(result.Event)
	}
	state := "sending"
	current, known := h.owners[result.Event.Owner]
	stale := !known || device.Control != result.Event.Control || result.Event.Control != h.control || result.Event.Space != h.control || current.Boot != result.Event.Boot
	if stale || now-created > int64((5*time.Minute)/time.Millisecond) || channel == "local" && (!active || now-created > 25000) {
		state = "expired"
	} else if viewing {
		state = "suppressed"
	}
	result.Attempt++
	_, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET state=?,attempts=?,updated_ms=? WHERE event=? AND device=?", state, result.Attempt, now, result.Event.ID, device.ID)
	if err != nil {
		return device, result, err
	}
	if err = tx.Commit(); err != nil {
		return device, result, err
	}
	if state != "sending" {
		return device, Delivery{}, nil
	}
	return device, result, nil
}
func (h *Hub) Complete(ctx context.Context, id, binding, event string, attempt int, result Result) error {
	if result.State != "accepted" && result.State != "failed" && result.State != "unknown" && result.State != "suppressed" {
		return errors.New("invalid notification delivery result")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := result.State
	next := time.Now()
	if state == "failed" && result.RetryAfter > 0 && attempt < 3 {
		state = "queued"
		delay := min(result.RetryAfter, time.Minute)
		next = next.Add(delay)
	}
	updated, err := h.db.ExecContext(ctx, `UPDATE notification_deliveries SET state=?,next_ms=?,updated_ms=? WHERE event=? AND device=? AND attempts=? AND state='sending'
 AND EXISTS(SELECT 1 FROM notification_devices d WHERE d.id=? AND d.binding=? AND d.revision=notification_deliveries.revision)`, state, next.UnixMilli(), time.Now().UnixMilli(), event, id, attempt, id, binding)
	affected := int64(0)
	if err == nil {
		affected, _ = updated.RowsAffected()
	}
	if err == nil && result.Invalid && affected == 1 {
		_, err = h.db.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,subscription='',invalidated=1,revision=revision+1 WHERE id=? AND binding=?", id, binding)
	}
	h.Signal()
	return err
}
func (h *Hub) Next(ctx context.Context, id, binding, session string) (*Delivery, error) {
	if session == "" || len(session) > 128 {
		return nil, errors.New("invalid notification receiver session")
	}
	h.mu.Lock()
	var count int
	err := h.db.QueryRowContext(ctx, "SELECT count(*) FROM notification_devices WHERE id=? AND binding=? AND channel='local' AND enabled=1 AND permission='granted' AND expires_ms>?", id, binding, time.Now().UnixMilli()).Scan(&count)
	if err != nil || count != 1 {
		h.mu.Unlock()
		return nil, errors.New("notification receiver unavailable")
	}
	if h.receivers[id] == nil {
		h.receivers[id] = map[string]int{}
	}
	h.receivers[id][session]++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.receivers[id][session]--
		if h.receivers[id][session] <= 0 {
			delete(h.receivers[id], session)
		}
		if len(h.receivers[id]) == 0 {
			delete(h.receivers, id)
		}
	}()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		_, delivery, err := h.reserve(ctx, id, binding, "local")
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if delivery.Event.ID != "" {
			return &delivery, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, nil
		case <-h.wake:
		case <-time.After(time.Second):
		}
	}
}
func (h *Hub) Dispatch(ctx context.Context) {
	housekeeping := time.NewTicker(time.Hour)
	defer housekeeping.Stop()
	for ctx.Err() == nil {
		h.mu.Lock()
		_, _ = h.db.ExecContext(ctx, "UPDATE notification_deliveries SET state='unknown' WHERE state='sending' AND updated_ms<?", time.Now().Add(-time.Minute).UnixMilli())
		h.mu.Unlock()
		if h.Push != nil {
			for {
				device, delivery, err := h.reserve(ctx, "", "", "push")
				if err != nil {
					break
				}
				if delivery.Event.ID == "" {
					continue
				}
				result := h.Push(ctx, device, delivery.Event)
				_ = h.Complete(ctx, device.ID, device.Binding, delivery.Event.ID, delivery.Attempt, result)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
		case <-time.After(time.Second):
		case <-housekeeping.C:
			h.mu.Lock()
			_, _ = h.db.Exec("DELETE FROM notification_events WHERE created_ms<?", time.Now().Add(-7*24*time.Hour).UnixMilli())
			_, _ = h.db.Exec("UPDATE notification_devices SET enabled=0,subscription='',revision=revision+1 WHERE enabled=1 AND expires_ms<?", time.Now().UnixMilli())
			_, _ = h.db.Exec("DELETE FROM notification_devices WHERE expires_ms<?", time.Now().Add(-7*24*time.Hour).UnixMilli())
			_, _ = h.db.Exec("DELETE FROM notification_views WHERE expires_ms<?", time.Now().UnixMilli())
			h.mu.Unlock()
		}
	}
}
