package pwapush

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jjyr/pot/internal/browsernotifications"
	"github.com/jjyr/pot/internal/systemnotifications"
)

var ErrSubscriptionExpired = errors.New("push subscription expired")
var ErrInvalidSubscription = errors.New("invalid push subscription")

// Transport is the encrypted Web Push boundary, also used by isolated tests.
type Transport interface {
	Send(context.Context, Subscription, []byte) Result
}
type Service struct {
	store     *browsernotifications.Store
	events    systemnotifications.Authority
	sender    Transport
	publicKey string
}

func NewService(store *browsernotifications.Store, events systemnotifications.Authority, origin string, transport Transport) (*Service, error) {
	var secret []byte
	err := store.Transaction(context.Background(), func(tx *sql.Tx) error {
		err := tx.QueryRow("SELECT value FROM notification_secrets WHERE id='vapid-p256-v1'").Scan(&secret)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		secret, err = GenerateKey()
		if err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO notification_secrets(id,value) VALUES('vapid-p256-v1',?)", secret)
		return err
	})
	if err != nil {
		return nil, err
	}
	sender, err := New(secret, origin)
	if err != nil {
		return nil, err
	}
	var delivery Transport = sender
	if transport != nil {
		delivery = transport
	}
	return &Service{store: store, events: events, sender: delivery, publicKey: sender.PublicKey}, nil
}
func (s *Service) PublicKey() string { return s.publicKey }
func (s *Service) Register(ctx context.Context, d browsernotifications.Device, sub Subscription) error {
	if err := Validate(sub); err != nil {
		return ErrInvalidSubscription
	}
	if sub.ExpirationTime != nil && *sub.ExpirationTime <= time.Now().UnixMilli() {
		return ErrInvalidSubscription
	}
	body, _ := json.Marshal(sub)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(body))
	return s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var previous, hash string
		var invalid bool
		err := tx.QueryRowContext(ctx, "SELECT body,fingerprint,invalidated FROM notification_push_subscriptions WHERE device=?", d.ID).Scan(&previous, &hash, &invalid)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if invalid && hash == fingerprint {
			return ErrSubscriptionExpired
		}
		if err = s.store.RegisterTx(ctx, tx, d, previous != string(body)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,revision=revision+1 WHERE id<>? AND id IN (SELECT device FROM notification_push_subscriptions WHERE body=?)", d.ID, string(body)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE notification_push_subscriptions SET body='' WHERE device<>? AND body=?", d.ID, string(body)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO notification_push_subscriptions(device,body,fingerprint,invalidated) VALUES(?,?,?,0) ON CONFLICT(device) DO UPDATE SET body=excluded.body,fingerprint=excluded.fingerprint,invalidated=0`, d.ID, string(body), fingerprint)
		return err
	})
}
func (s *Service) Check(ctx context.Context, id, binding string) error {
	var invalid bool
	err := s.store.DB.QueryRowContext(ctx, "SELECT p.invalidated FROM notification_push_subscriptions p JOIN notification_devices d ON d.id=p.device WHERE d.id=? AND d.binding=? AND d.channel='push'", id, binding).Scan(&invalid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if invalid {
		return ErrSubscriptionExpired
	}
	return nil
}

// ClearRevoked removes inert subscription secrets, retaining invalid fingerprints
// so a reload cannot silently reactivate a provider-rejected subscription.
func (s *Service) ClearRevoked(ctx context.Context) error {
	return s.store.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE notification_push_subscriptions SET body='' WHERE device IN (SELECT id FROM notification_devices WHERE enabled=0)")
		return err
	})
}
func payload(d browsernotifications.Device, e systemnotifications.Event) ([]byte, error) {
	title, body := e.Text(d.Locale)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(map[string]any{"version": 1, "event": e.Target(), "device": d.ID, "title": title, "body": body})
	return encoded.Bytes(), err
}
func (s *Service) deliver(ctx context.Context, d browsernotifications.Device, out browsernotifications.Delivery) {
	var body string
	var sub Subscription
	// Read the subscription and current reservation authority in one SQLite
	// snapshot. An old reservation must never borrow a replacement's endpoint.
	// The query finishes before HTTP; no writer or authority lock spans Send.
	err := s.store.DB.QueryRowContext(ctx, `SELECT p.body FROM notification_push_subscriptions p
 JOIN notification_devices d ON d.id=p.device JOIN notification_deliveries q ON q.device=d.id
 WHERE d.id=? AND d.binding=? AND d.revision=? AND d.control_id=? AND d.enabled=1
 AND d.permission='granted' AND d.channel='push' AND d.expires_ms>? AND p.invalidated=0
 AND q.event=? AND q.revision=d.revision AND q.channel='push' AND q.state='sending' AND q.attempts=?`,
		d.ID, d.Binding, d.Revision, out.Event.Control, time.Now().UnixMilli(), out.Event.ID, out.Attempt).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		// Only retire this exact unsent reservation, even if the device has since
		// changed. Finish intentionally requires current authority for HTTP results.
		_, _ = s.store.DB.ExecContext(ctx, "UPDATE notification_deliveries SET state='expired',updated_ms=? WHERE event=? AND device=? AND revision=? AND attempts=? AND state='sending'", time.Now().UnixMilli(), out.Event.ID, d.ID, d.Revision, out.Attempt)
		return
	}
	state := "failed"
	result := Result{}
	if !s.events.Current(out.Event) {
		state = "expired"
	} else if err == nil && json.Unmarshal([]byte(body), &sub) == nil {
		if encoded, err := payload(d, out.Event); err == nil {
			result = s.sender.Send(ctx, sub, encoded)
			if result.Unknown {
				state = "unknown"
			} else if result.Status >= 200 && result.Status < 300 {
				state = "accepted"
			}
		}
	}
	next := time.Now()
	if state == "failed" && result.RetryAfter > 0 && out.Attempt < 3 {
		state = "queued"
		next = next.Add(min(result.RetryAfter, time.Minute))
	}
	if err = s.store.Finish(ctx, d.ID, d.Binding, out.Event.ID, out.Attempt, state, next); err != nil {
		return
	}
	if result.Invalid {
		_ = s.store.Transaction(ctx, func(tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, "UPDATE notification_devices SET enabled=0,revision=revision+1 WHERE id=? AND binding=? AND revision=?", d.ID, d.Binding, d.Revision)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				return nil
			}
			_, err = tx.ExecContext(ctx, "UPDATE notification_push_subscriptions SET body='',invalidated=1 WHERE device=?", d.ID)
			return err
		})
	}
}

// Run consumes the selected Push receipts, owning provider retries and invalidation.
// The business source neither starts this service nor knows that Web Push exists.
func (s *Service) Run(ctx context.Context) {
	maintenance := time.NewTicker(time.Hour)
	defer maintenance.Stop()
	for ctx.Err() == nil {
		s.store.ExpireSends(ctx)
		for {
			d, out, err := s.store.Reserve(ctx, "", "", "push", 5*time.Minute, false)
			if err != nil {
				break
			}
			if out.Event.ID != "" {
				s.deliver(ctx, d, out)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		case <-maintenance.C:
			s.store.Maintain(ctx)
			_ = s.ClearRevoked(ctx)
		}
	}
}
