package browsernotifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/systemnotifications"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeviceReceiptsReadingAndActivationBaselines(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	events, err := systemnotifications.OpenHub(path)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	device := func(id, route string) Device {
		d := Device{ID: strings.Repeat(id, 24), Binding: "session:" + id, Control: "node", Channel: route, Enabled: true, Permission: "granted", Locale: "en", Expires: time.Now().Add(time.Hour).UnixMilli()}
		if err := store.Register(ctx, d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	a, b := device("a", "local"), device("b", "push")
	lease := func(d Device, seq uint64, focused bool) {
		t.Helper()
		if err := store.Lease(ctx, d.ID, d.Binding, "tab", systemnotifications.View{Sequence: seq, Space: "node", Owner: "alpha", Route: "chat", Focused: focused}); err != nil {
			t.Fatal(err)
		}
	}
	lease(a, 2, false)
	lease(a, 1, true) // a late lease cannot restore stale foreground state
	state := func(e, d, want string) {
		t.Helper()
		var got string
		if err := store.DB.QueryRow("SELECT state FROM notification_deliveries WHERE event=? AND device=?", e, d).Scan(&got); err != nil || got != want {
			t.Fatal(e, d, got, want, err)
		}
	}
	publish := func(id string) systemnotifications.Record {
		t.Helper()
		e := systemnotifications.Event{ID: id, Control: "node", Space: "node", Owner: "alpha", Kind: "task-complete", Task: "task"}
		body, _ := json.Marshal(e)
		if _, err := store.DB.Exec("INSERT INTO notification_events(id,body,created_ms) VALUES(?,?,?)", e.ID, string(body), time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		r := systemnotifications.Record{Event: e, Created: time.Now()}
		store.DB.QueryRow("SELECT sequence FROM notification_events WHERE id=?", id).Scan(&r.Sequence)
		return r
	}
	enqueue := func(r systemnotifications.Record) {
		t.Helper()
		if err := store.Enqueue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	r := publish("first")
	enqueue(r)
	enqueue(r)
	_, local, err := store.Reserve(ctx, a.ID, a.Binding, "local", 25*time.Second, true)
	if err != nil || local.Event.ID != r.Event.ID {
		t.Fatal(local, err)
	}
	if _, _, err = store.Reserve(ctx, a.ID, a.Binding, "local", 25*time.Second, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("double reservation", err)
	}
	if err = store.Finish(ctx, a.ID, a.Binding, r.Event.ID, local.Attempt, "accepted", time.Now()); err != nil {
		t.Fatal(err)
	}
	state(r.Event.ID, a.ID, "accepted")
	state(r.Event.ID, b.ID, "queued")
	_, remote, err := store.Reserve(ctx, b.ID, b.Binding, "push", 5*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	store.Finish(ctx, b.ID, b.Binding, r.Event.ID, remote.Attempt, "unknown", time.Now())
	b.Channel = "local"
	if err = store.Register(ctx, b); err != nil {
		t.Fatal(err)
	}
	enqueue(r)
	state(r.Event.ID, b.ID, "unknown")
	c := device("c", "local")
	enqueue(r)
	var count int
	store.DB.QueryRow("SELECT count(*) FROM notification_deliveries WHERE device=?", c.ID).Scan(&count)
	if count != 0 {
		t.Fatal("new installation replayed old event")
	}
	for i, test := range []struct {
		view  systemnotifications.View
		state string
	}{
		{systemnotifications.View{Space: "node", Owner: "alpha", Route: "chat", Focused: true}, "suppressed"},
		{systemnotifications.View{Space: "node", Owner: "other", Route: "chat", Focused: true}, "queued"},
		{systemnotifications.View{Space: "node", Owner: "alpha", Task: "task", Focused: true}, "suppressed"},
		{systemnotifications.View{Space: "node", Owner: "alpha", Task: "other-task", Focused: true}, "queued"},
	} {
		view := test.view
		view.Sequence = uint64(i + 3)
		if err := store.Lease(ctx, a.ID, a.Binding, "tab", view); err != nil {
			t.Fatal(err)
		}
		record := publish(fmt.Sprint("reading-", i))
		enqueue(record)
		state(record.Event.ID, a.ID, test.state)
	}

	store.DB.Exec("UPDATE notification_views SET expires_ms=0")
	next := publish("expired")
	enqueue(next)
	state(next.Event.ID, a.ID, "expired")
	// Current work survives a sibling receiver, but not a reconnect without one.
	lease(a, 7, false)
	next = publish("live")
	enqueue(next)
	l := Local{Store: store, Events: events}
	store.receivers[a.ID] = map[string]int{"one": 1}
	l.Baseline(ctx, a.ID, a.Binding, "two")
	state(next.Event.ID, a.ID, "queued")
	l.Baseline(ctx, a.ID, a.Binding, "one")
	state(next.Event.ID, a.ID, "expired")
	store.Remove(ctx, b.ID, a.Binding)
	store.DB.QueryRow("SELECT count(*) FROM notification_devices WHERE id=?", b.ID).Scan(&count)
	if count != 1 {
		t.Fatal("removed another session")
	}
	if err = store.Reconcile(ctx, func(binding string) bool { return binding == a.Binding }); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	store.DB.QueryRow("SELECT enabled FROM notification_devices WHERE id=?", b.ID).Scan(&enabled)
	if enabled {
		t.Fatal("revoked binding survived restart reconciliation")
	}
}
