package systemnotifications

import (
	"context"
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
	h.owners = map[string]Owner{"alpha": {ID: "alpha"}}
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
	if err = h.commitSource(ctx, e.Owner, sourceState{}, []Event{e}); err != nil {
		t.Fatal(err)
	}
	if err = h.commitSource(ctx, e.Owner, sourceState{}, []Event{e}); err != nil {
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
