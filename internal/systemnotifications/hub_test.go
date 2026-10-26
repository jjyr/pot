package systemnotifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/pwapush"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	h, err := OpenHub(filepath.Join(t.TempDir(), "notifications.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h.control = "node"
	h.owners = map[string]Owner{"alpha": {ID: "alpha"}}
	t.Cleanup(func() { h.Close() })
	return h
}
func device(t *testing.T, h *Hub, id, channel string) Device {
	t.Helper()
	d := Device{ID: strings.Repeat(id, 24), Binding: "session:" + id, Control: "node", Channel: channel, Enabled: true, Permission: "granted", Locale: "en", Expires: time.Now().Add(time.Hour).UnixMilli()}
	if err := h.Register(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}
func requireState(t *testing.T, h *Hub, event, device, want string) {
	t.Helper()
	var state string
	if err := h.db.QueryRow("SELECT state FROM notification_deliveries WHERE event=? AND device=?", event, device).Scan(&state); err != nil || state != want {
		t.Fatalf("event %s device %s: %s (%v), want %s", event, device, state, err, want)
	}
}
func TestDevicesHaveOneChannelAndIndependentReceipts(t *testing.T) {
	ctx := context.Background()
	h := testHub(t)
	a := device(t, h, "a", "local")
	b := device(t, h, "b", "push")
	owner := Owner{ID: "alpha", Space: "node", Control: "node", Name: "Alpha"}
	e := event(owner, "reply", "reply", "", "", " 你好\n"+strings.Repeat("😀", 130))
	if err := testLease(h, ctx, a.ID, a.Binding, "tab-a", View{Space: "node", Owner: "other", Focused: true, Route: "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := h.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	_, first, err := h.reserve(ctx, a.ID, a.Binding, "local")
	if err != nil || first.Event.ID != e.ID {
		t.Fatal(first, err)
	}
	if _, _, err = h.reserve(ctx, a.ID, a.Binding, "local"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("second tab reserved same event", err)
	}
	if err = h.Complete(ctx, a.ID, a.Binding, e.ID, first.Attempt, Result{State: "accepted"}); err != nil {
		t.Fatal(err)
	}
	requireState(t, h, e.ID, a.ID, "accepted")
	requireState(t, h, e.ID, b.ID, "queued")
	_, p, err := h.reserve(ctx, "", "", "push")
	if err != nil || p.Event.ID != e.ID {
		t.Fatal(p, err)
	}
	if err = h.Complete(ctx, b.ID, b.Binding, e.ID, p.Attempt, Result{State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	// Replacing Push with local cannot replay an uncertain send of the same event.
	b.Channel = "local"
	if err = h.Register(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = h.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	requireState(t, h, e.ID, b.ID, "unknown")
	_, body := e.Text("zh-CN")
	if len([]rune(body)) != 120 || len(body) > 512 || !strings.HasSuffix(body, "…") {
		t.Fatal(body)
	}
	title, body := event(owner, "approval", "approval", "task", "Build", "").Text("zh-CN")
	if title != "Pot · Build" || body != "需要你的审批，请在 Pot 中查看" {
		t.Fatal(title, body)
	}
}
func TestForegroundLeasesBaselinesAndChannelLifetime(t *testing.T) {
	h := testHub(t)
	ctx := context.Background()
	a := device(t, h, "a", "local")
	b := device(t, h, "b", "push")
	owner := Owner{ID: "alpha", Space: "node", Control: "node"}
	for _, d := range []Device{a, b} {
		if err := testLease(h, ctx, d.ID, d.Binding, "view", View{Space: "node", Owner: "alpha", Focused: true, Route: "chat"}); err != nil {
			t.Fatal(err)
		}
	}
	first := event(owner, "approval", "first", "task", "", "")
	h.Publish(ctx, first)
	for _, d := range []Device{a, b} {
		requireState(t, h, first.ID, d.ID, "suppressed")
	}
	// Leases expire; they cannot suppress forever after a page is suspended.
	h.db.Exec("UPDATE notification_views SET expires_ms=0")
	second := event(owner, "task-complete", "second", "task", "", "")
	h.Publish(ctx, second)
	requireState(t, h, second.ID, a.ID, "expired")
	requireState(t, h, second.ID, b.ID, "queued")
	// A newly focused exact Task uses the same hub predicate on every channel.
	testLease(h, ctx, b.ID, b.Binding, "view", View{Space: "node", Owner: "alpha", Task: "task", Focused: true})
	_, out, err := h.reserve(ctx, "", "", "push")
	if err != nil || out.Event.ID != "" {
		t.Fatal(out, err)
	}
	requireState(t, h, second.ID, b.ID, "suppressed")
	testLease(h, ctx, a.ID, a.Binding, "one", View{})
	third := event(owner, "reply", "third", "", "", "")
	h.Publish(ctx, third)
	// Another active receiver keeps live work; a reconnect without siblings discards it.
	h.receivers[a.ID] = map[string]int{"one": 1}
	h.Baseline(ctx, a.ID, a.Binding, "two")
	requireState(t, h, third.ID, a.ID, "queued")
	h.Baseline(ctx, a.ID, a.Binding, "one")
	requireState(t, h, third.ID, a.ID, "expired")
	if err = h.Remove(ctx, b.ID, a.Binding); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	h.db.QueryRow("SELECT enabled FROM notification_devices WHERE id=?", b.ID).Scan(&enabled)
	if !enabled {
		t.Fatal("foreign device removed")
	}
	if err = h.Remove(ctx, b.ID, b.Binding); err != nil {
		t.Fatal(err)
	}
	if err = testLease(h, ctx, b.ID, b.Binding, "view", View{}); err == nil {
		t.Fatal("deleted installation kept authority")
	}
}
func TestRetriesInvalidSubscriptionsAndLateResults(t *testing.T) {
	h := testHub(t)
	ctx := context.Background()
	d := device(t, h, "d", "push")
	e := event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "new", "", "", "")
	h.Publish(ctx, e)
	for attempt := 1; attempt <= 3; attempt++ {
		_, delivery, err := h.reserve(ctx, "", "", "push")
		if err != nil || delivery.Attempt != attempt {
			t.Fatal(delivery, err)
		}
		if err = h.Complete(ctx, d.ID, d.Binding, e.ID, attempt, Result{State: "failed", RetryAfter: time.Nanosecond}); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, h, e.ID, d.ID, "failed")
	next := event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "replacement", "", "", "")
	h.Publish(ctx, next)
	_, delivery, err := h.reserve(ctx, "", "", "push")
	if err != nil {
		t.Fatal(err)
	}
	d.Subscription = "replacement"
	h.Register(ctx, d)
	// A delayed 410 for the old subscription must not remove its replacement.
	h.Complete(ctx, d.ID, d.Binding, next.ID, delivery.Attempt, Result{State: "failed", Invalid: true})
	var enabled bool
	h.db.QueryRow("SELECT enabled FROM notification_devices WHERE id=?", d.ID).Scan(&enabled)
	if !enabled {
		t.Fatal("late result disabled new subscription")
	}
	final := event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "final", "", "", "")
	h.Publish(ctx, final)
	_, delivery, err = h.reserve(ctx, "", "", "push")
	if err != nil {
		t.Fatal(err)
	}
	h.Complete(ctx, d.ID, d.Binding, final.ID, delivery.Attempt, Result{State: "failed", Invalid: true})
	var sub string
	h.db.QueryRow("SELECT enabled,subscription FROM notification_devices WHERE id=?", d.ID).Scan(&enabled, &sub)
	if enabled || sub != "" {
		t.Fatal("410 did not clear subscription")
	}
	if err = h.Register(ctx, d); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatal("invalid subscription was silently reactivated", err)
	}
}
func TestRestartRetainsKeysCursorAndUncertainResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	ctx := context.Background()
	h, err := OpenHub(path)
	if err != nil {
		t.Fatal(err)
	}
	h.control = "node"
	h.owners = map[string]Owner{"alpha": {ID: "alpha"}}
	key, err := h.Secret("vapid", pwapush.GenerateKey)
	if err != nil {
		t.Fatal(err)
	}
	d := device(t, h, "d", "push")
	cursor := uint64(99)
	h.commitSource(ctx, "alpha", sourceState{Boot: "boot", Reply: "reply", Count: 7, Cursor: &cursor}, nil)
	e := event(Owner{ID: "alpha", Control: "node", Space: "node"}, "reply", "r", "", "", "")
	h.Publish(ctx, e)
	h.reserve(ctx, "", "", "push")
	h.Close()
	h, err = OpenHub(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	again, err := h.Secret("vapid", func() ([]byte, error) { t.Fatal("regenerated VAPID"); return nil, nil })
	if err != nil || string(key) != string(again) {
		t.Fatal(err)
	}
	requireState(t, h, e.ID, d.ID, "unknown")
	var got uint64
	if err = h.db.QueryRow("SELECT task_cursor FROM notification_sources WHERE owner='alpha'").Scan(&got); err != nil || got != 99 {
		t.Fatal(got, err)
	}
	if err = h.Reconcile(ctx, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	h.db.QueryRow("SELECT enabled FROM notification_devices WHERE id=?", d.ID).Scan(&enabled)
	if enabled {
		t.Fatal("revoked authority revived on restart")
	}
}
func TestCommittedSourcesIndependentReadersRecoveryAndBootFences(t *testing.T) {
	h := testHub(t)
	ctx := context.Background()
	d := device(t, h, "d", "push")
	boot := "one"
	reply := "old"
	count := 1
	sequence := uint64(8)
	approval := "old"
	failTasks := false
	failApprovals := false
	lateBoot := false
	source := localui.Functions{Request: func(_ context.Context, _ string, path string, _ json.RawMessage) (json.RawMessage, error) {
		var value any
		switch {
		case path == "/api/v1/runtime":
			value = map[string]any{"runtime_id": "node", "boot_id": boot}
		case path == "/api/v1/assistants":
			value = []any{map[string]any{"id": "alpha", "name": "Alpha", "reply_id": reply, "reply_count": count, "preview": " 完成\n检查 ", "capabilities": map[string]any{"version": 1, "operations": []string{"task.outcomes", "approvals.manage"}}}}
		case path == "/api/v1/agent-links":
			value = map[string]any{"items": []any{}}
		case strings.HasPrefix(path, "/api/v1/tasks/outcomes"):
			if lateBoot {
				boot = "late-boot"
			}
			if failTasks {
				return nil, errors.New("task source unavailable")
			}
			items := []any{}
			if strings.Contains(path, "after=") && sequence > 8 {
				for _, phase := range []string{"failed", "cancelled", "complete", "interrupted"} {
					items = append(items, map[string]any{"id": phase, "title": "Build", "phase": phase, "sequence": sequence})
				}
			}
			value = map[string]any{"cursor": sequence, "items": items}
		case strings.HasPrefix(path, "/api/v1/permissions"):
			if failApprovals {
				return nil, errors.New("approval source unavailable")
			}
			value = map[string]any{"pending": []any{map[string]any{"id": approval, "task_title": "Build", "request": map[string]any{"owner": "alpha", "task": "task", "args": "secret-tool-argument"}}}}
		default:
			return nil, errors.New("unexpected source path: " + path)
		}
		body, err := json.Marshal(value)
		return body, err
	}}
	states := map[string]sourceState{}
	refresh := func(reset bool) {
		t.Helper()
		if err := h.readSources(ctx, source, states, map[string]bool{"task-outcomes": true, "permissions": true}, reset); err != nil {
			t.Fatal(err)
		}
	}
	refresh(true)
	var n int
	h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n)
	if n != 0 {
		t.Fatal("initial history notified", n)
	}
	sequence = 9
	approval = "new"
	failTasks = true
	reply = "new"
	count = 2
	refresh(false)
	if states["alpha"].Cursor == nil || *states["alpha"].Cursor != 8 || !states["alpha"].Approvals["new"] {
		t.Fatal("readers not independent", states)
	}
	failTasks = false
	failApprovals = true
	refresh(false)
	if *states["alpha"].Cursor != 9 {
		t.Fatal("Task reader blocked by approvals")
	}
	rows, err := h.db.Query("SELECT body FROM notification_events ORDER BY sequence")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		if strings.Contains(raw, "secret-tool-argument") {
			t.Fatal("tool arguments leaked")
		}
		var e Event
		json.Unmarshal([]byte(raw), &e)
		kinds = append(kinds, e.Kind)
		require := e.ID
		_ = require
	}
	rows.Close()
	if strings.Join(kinds, ",") != "reply,approval,task-complete,task-failed" {
		t.Fatal(kinds)
	}
	requireState(t, h, event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "new", "", "", "").ID, d.ID, "queued")
	failApprovals = false
	boot = "two"
	reply = "boot-late"
	count = 20
	sequence = 30
	approval = "boot-approval"
	refresh(false)
	h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n)
	if n != 4 {
		t.Fatal("boot replacement replayed history", n)
	}
	if _, delivery, err := h.reserve(ctx, "", "", "push"); err != nil || delivery.Event.ID != "" {
		t.Fatal("old-boot event delivered", delivery, err)
	}
	requireState(t, h, event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "new", "", "", "").ID, d.ID, "expired")
	reply = "reconnect-late"
	count = 21
	sequence = 31
	refresh(true)
	h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n)
	if n != 4 {
		t.Fatal("reconnect replayed history", n)
	}
	lateBoot = true
	sequence = 32
	if err := h.readSources(ctx, source, states, map[string]bool{"task-outcomes": true}, false); !errors.Is(err, ErrSourceChanged) {
		t.Fatal("late boot result was not fenced", err)
	}
	h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n)
	if n != 4 {
		t.Fatal("late read notified", n)
	}
}

func TestLinkedSourceIdentityAndRevocationFence(t *testing.T) {
	h := testHub(t)
	ctx := context.Background()
	d := device(t, h, "l", "push")
	reply := "old"
	count := 4
	cursor := 20
	approval := "old"
	status := "connected"
	revokeLate := false
	owner := "agent-link:link:codex"
	source := localui.Functions{Request: func(_ context.Context, _ string, path string, _ json.RawMessage) (json.RawMessage, error) {
		var out any
		switch {
		case path == "/api/v1/runtime":
			out = map[string]string{"runtime_id": "node", "boot_id": "boot"}
		case path == "/api/v1/assistants":
			out = []any{}
		case path == "/api/v1/agent-links":
			out = map[string]any{"items": []any{map[string]any{"id": "link", "status": status, "assistants": []any{map[string]any{"address": map[string]string{"node_id": "peer", "kind": "assistant", "agent_id": "codex"}, "name": "Codex", "reply_id": reply, "reply_count": count, "preview": "Linked completed reply", "capabilities": map[string]any{"version": 1, "operations": []string{"chat.read", "task.outcomes", "approvals.manage"}}}}}}}
		case strings.HasPrefix(path, "/api/v1/tasks/outcomes"):
			if !strings.Contains(path, "assistant_id=agent-link%3Alink%3Acodex") {
				t.Fatal("linked owner lost", path)
			}
			if revokeLate {
				status = "revoked"
			}
			items := []any{}
			if strings.Contains(path, "after=") && cursor > 20 {
				items = append(items, map[string]any{"id": "linked-task", "phase": "complete", "title": "Build", "sequence": cursor})
			}
			out = map[string]any{"cursor": cursor, "items": items}
		case strings.HasPrefix(path, "/api/v1/permissions"):
			out = map[string]any{"pending": []any{map[string]any{"id": approval, "task_title": "Build", "request": map[string]any{"owner": owner, "task": "linked-task", "args": "never-send-this"}}}}
		default:
			return nil, errors.New("unexpected linked read")
		}
		data, err := json.Marshal(out)
		return data, err
	}}
	states := map[string]sourceState{}
	if err := h.readSources(ctx, source, states, map[string]bool{}, true); err != nil {
		t.Fatal(err)
	}
	reply = "new"
	count++
	cursor++
	approval = "new"
	if err := h.readSources(ctx, source, states, map[string]bool{"agent-links": true}, false); err != nil {
		t.Fatal(err)
	}
	var n int
	h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n)
	if n != 3 {
		t.Fatal(n)
	}
	rows, err := h.db.Query("SELECT body FROM notification_events")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var body string
		rows.Scan(&body)
		var event Event
		json.Unmarshal([]byte(body), &event)
		if event.Owner != owner || event.Space != "node" || event.Control != "node" || strings.Contains(body, "never-send-this") {
			t.Fatal(event)
		}
	}
	rows.Close()
	cursor++
	revokeLate = true
	if err = h.readSources(ctx, source, states, map[string]bool{"task-outcomes": true}, false); !errors.Is(err, ErrSourceChanged) {
		t.Fatal("late linked Task read retained authority", err)
	}
	h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&n)
	if n != 3 {
		t.Fatal("revoked peer produced a notification", n)
	}
	revokeLate = false
	if err = h.readSources(ctx, source, states, map[string]bool{"agent-links": true}, false); err != nil {
		t.Fatal(err)
	}
	if h.ValidateScope("node", "node", []string{owner}) {
		t.Fatal("retained linked identity restored authority")
	}
	_, delivery, err := h.reserve(ctx, "", "", "push")
	if err != nil || delivery.Event.ID != "" {
		t.Fatal("queued revoked owner was delivered", delivery, err)
	}
	_ = d
}

var testLeaseSequence atomic.Uint64

func testLease(h *Hub, ctx context.Context, id, binding, session string, view View) error {
	view.Sequence = testLeaseSequence.Add(1)
	return h.Lease(ctx, id, binding, session, view)
}

func TestLateReadingLeaseCannotRestoreOldForegroundState(t *testing.T) {
	h := testHub(t)
	ctx := context.Background()
	d := device(t, h, "s", "push")
	newer := View{Sequence: 2, Space: "node", Owner: "other", Route: "chat", Focused: true}
	if err := h.Lease(ctx, d.ID, d.Binding, "page", newer); err != nil {
		t.Fatal(err)
	}
	older := View{Sequence: 1, Space: "node", Owner: "alpha", Route: "chat", Focused: true}
	if err := h.Lease(ctx, d.ID, d.Binding, "page", older); err != nil {
		t.Fatal(err)
	}
	e := event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "after-navigation", "", "", "")
	if err := h.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	requireState(t, h, e.ID, d.ID, "queued")
	// A crashed page's lease must not make a fresh page replay queued events.
	local := device(t, h, "c", "local")
	testLease(h, ctx, local.ID, local.Binding, "crashed-page", View{})
	next := event(Owner{ID: "alpha", Space: "node", Control: "node"}, "reply", "while-page-closed", "", "", "")
	h.Publish(ctx, next)
	if err := h.Baseline(ctx, local.ID, local.Binding, "new-page"); err != nil {
		t.Fatal(err)
	}
	requireState(t, h, next.ID, local.ID, "expired")
}
