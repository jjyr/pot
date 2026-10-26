package pwapush

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/browsernotifications"
	"github.com/jjyr/pot/internal/systemnotifications"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type currentEvents struct{}

func (currentEvents) Current(systemnotifications.Event) bool { return true }

type testDelivery func(context.Context, Subscription, []byte) Result

func (f testDelivery) Send(c context.Context, s Subscription, p []byte) Result { return f(c, s, p) }
func TestPushServiceRetriesUncertaintyReplacementAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	store, err := browsernotifications.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	outcome := Result{Status: 503, RetryAfter: time.Nanosecond}
	calls := 0
	sender := testDelivery(func(_ context.Context, _ Subscription, body []byte) Result {
		calls++
		if strings.Contains(string(body), "endpoint") || strings.Contains(string(body), "p256dh") || strings.Contains(string(body), "auth") {
			t.Error("subscription secret in payload")
		}
		return outcome
	})
	service, err := NewService(store, currentEvents{}, "https://pot.tailnet.test", sender)
	if err != nil {
		t.Fatal(err)
	}
	key := service.PublicKey()
	d := browsernotifications.Device{ID: strings.Repeat("d", 24), Control: "node", Binding: "session", Channel: "push", Enabled: true, Permission: "granted", Expires: time.Now().Add(time.Hour).UnixMilli()}
	sub, _ := subscription(t)
	if err = service.Register(ctx, d, sub); err != nil {
		t.Fatal(err)
	}
	enqueue := func(id string) {
		t.Helper()
		e := systemnotifications.Event{ID: id, Control: "node", Space: "node", Owner: "alpha", Kind: "reply", Name: "Alpha", BodyKey: "notifications.replyComplete"}
		body, _ := json.Marshal(e)
		result, err := store.DB.Exec("INSERT INTO notification_events(id,body,created_ms) VALUES(?,?,?)", id, string(body), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		seq, _ := result.LastInsertId()
		if err = store.Enqueue(ctx, systemnotifications.Record{Sequence: seq, Event: e, Created: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	reserve := func() (browsernotifications.Device, browsernotifications.Delivery) {
		t.Helper()
		target, out, err := store.Reserve(ctx, "", "", "push", 5*time.Minute, false)
		if err != nil {
			t.Fatal(err)
		}
		return target, out
	}
	state := func(id, want string) {
		t.Helper()
		var got string
		if err := store.DB.QueryRow("SELECT state FROM notification_deliveries WHERE event=? AND device=?", id, d.ID).Scan(&got); err != nil || got != want {
			t.Fatal(id, got, want, err)
		}
	}
	enqueue("retry")
	for i := 1; i <= 3; i++ {
		target, out := reserve()
		if out.Attempt != i {
			t.Fatal(out)
		}
		service.deliver(ctx, target, out)
	}
	state("retry", "failed")
	if calls != 3 {
		t.Fatal(calls)
	}
	enqueue("uncertain")
	outcome = Result{Unknown: true}
	target, out := reserve()
	service.deliver(ctx, target, out)
	state("uncertain", "unknown")
	if _, _, err = store.Reserve(ctx, "", "", "push", 5*time.Minute, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("uncertain send retried", err)
	}
	enqueue("interrupted")
	reserve()
	store.Close()
	store, err = browsernotifications.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err = NewService(store, currentEvents{}, "https://pot.tailnet.test", sender)
	if err != nil || service.PublicKey() != key {
		t.Fatal("VAPID changed on restart", err)
	}
	state("interrupted", "unknown")
	enqueue("late-rejection")
	target, out = reserve()
	replacement := sub
	replacement.Endpoint += "/replacement"
	service.sender = testDelivery(func(context.Context, Subscription, []byte) Result {
		if err := service.Register(ctx, d, replacement); err != nil {
			t.Fatal(err)
		}
		return Result{Status: 410, Invalid: true}
	})
	service.deliver(ctx, target, out)
	var enabled bool
	store.DB.QueryRow("SELECT enabled FROM notification_devices WHERE id=?", d.ID).Scan(&enabled)
	if !enabled {
		t.Fatal("late rejection disabled replacement")
	}
	enqueue("gone")
	service.sender = testDelivery(func(context.Context, Subscription, []byte) Result { return Result{Status: 410, Invalid: true} })
	target, out = reserve()
	service.deliver(ctx, target, out)
	var body string
	store.DB.QueryRow("SELECT enabled FROM notification_devices WHERE id=?", d.ID).Scan(&enabled)
	store.DB.QueryRow("SELECT body FROM notification_push_subscriptions WHERE device=?", d.ID).Scan(&body)
	if enabled || body != "" {
		t.Fatal("410 retained active subscription")
	}
	if err = service.Register(ctx, d, replacement); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatal("invalid subscription reactivated", err)
	}
}

func TestPushPayloadBoundsAndNavigationWhitelist(t *testing.T) {
	control := strings.Repeat("n", 128)
	owner := "agent-link:" + strings.Repeat("l", 128) + ":" + strings.Repeat("a", 128)
	id, _ := json.Marshal([]string{control, owner, "reply", strings.Repeat("r", 256)})
	event := systemnotifications.Event{ID: string(id), Boot: "private-boot", Space: control, Control: control, Owner: owner, Kind: "reply", Name: strings.Repeat("😀", 60), Preview: strings.Repeat("😀", 119) + "…", BodyKey: "notifications.replyComplete"}
	payload, err := payload(browsernotifications.Device{ID: strings.Repeat("d", 128), Locale: "zh-CN"}, event)
	if err != nil || len(payload) > 3072 {
		t.Fatal("maximum legal target exceeded encrypted record budget", len(payload), err)
	}
	var message map[string]json.RawMessage
	if err = json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	var target map[string]any
	json.Unmarshal(message["event"], &target)
	if len(target) != 7 || target["owner"] != owner || target["preview"] != nil || target["boot"] != nil || target["body_key"] != nil {
		t.Fatal("push did not use the navigation whitelist")
	}
}
