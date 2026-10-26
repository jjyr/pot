package systemnotifications

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	h, err := OpenHub(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h.control = "node"
	h.owners = map[string]Owner{"alpha": {ID: "alpha", Name: "Alpha", Control: "node", Space: "node"}}
	t.Cleanup(func() { h.Close() })
	return h
}
func TestIndependentSubscriptionsReadDurableEventsWithoutHistory(t *testing.T) {
	h := testHub(t)
	ctx := context.Background()
	a, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	e := event(Owner{ID: "alpha", Control: "node", Space: "node", Name: "Alpha"}, "reply", "new", "", "", strings.Repeat("😀", 130))
	if err = h.Accept(ctx, protocol.LifecycleEvent{ID: "new", Kind: "reply_completed", Owner: e.Owner, Preview: e.Preview}); err != nil {
		t.Fatal(err)
	}
	if err = h.Accept(ctx, protocol.LifecycleEvent{ID: "new", Kind: "reply_completed", Owner: e.Owner, Preview: e.Preview}); err != nil {
		t.Fatal(err)
	}
	first, err := a.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.Next(ctx)
	if err != nil || first.Sequence != again.Sequence {
		t.Fatal("unacknowledged event was lost", err)
	}
	a.Ack(first)
	second, err := b.Next(ctx)
	if err != nil || second.Event.ID != first.Event.ID {
		t.Fatal("one consumer swallowed another's event", err)
	}
	b.Ack(second)
	c, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err = c.Next(bounded); err == nil {
		t.Fatal("new consumer replayed history")
	}
	_, body := e.Text("zh-CN")
	if len([]rune(body)) != 120 || !strings.HasSuffix(body, "…") {
		t.Fatal(body)
	}
}

func TestCommittedFactSelection(t *testing.T) {
	h := testHub(t)
	for i, tt := range []struct {
		kind   string
		silent bool
		want   int
	}{
		{"task_completed", false, 1}, {"task_interrupted", false, 1}, {"task_failed", false, 0},
		{"task_cancelled", false, 0}, {"task_completed", true, 0}, {"approval_requested", false, 1},
		{"approval_resolved", false, 0}, {"reply_completed", false, 1},
	} {
		var before, after int
		h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&before)
		if err := h.Accept(context.Background(), protocol.LifecycleEvent{ID: fmt.Sprint(i), Kind: tt.kind, Owner: "alpha", Task: "exact-task", Suppressed: tt.silent}); err != nil {
			t.Fatal(err)
		}
		h.db.QueryRow("SELECT count(*) FROM notification_events").Scan(&after)
		if after-before != tt.want {
			t.Fatal(tt, after-before)
		}
	}
}
