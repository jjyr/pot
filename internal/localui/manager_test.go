package localui

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativeScopeCannotForwardToRemoteOwner(t *testing.T) {
	var operations atomic.Int64
	local := Functions{Request: func(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		switch path {
		case "/api/v1/runtime":
			return json.RawMessage(`{"runtime_id":"node","boot_id":"boot"}`), nil
		case "/api/v1/assistants":
			return json.RawMessage(`[]`), nil
		case "/api/v1/agent-links":
			return json.RawMessage(`{"items":[]}`), nil
		}
		operations.Add(1)
		if strings.Contains(path, "space_id=") {
			t.Error("UI grouping leaked into business route")
		}
		return json.RawMessage(`{"accepted":true}`), nil
	}, Events: func(ctx context.Context, path string, receive func(Event) error) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	m, err := Open(context.Background(), Options{Local: local, LocalID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if len(m.Snapshot().Items) != 1 || m.Snapshot().Items[0].RuntimeID != "node" {
		t.Fatal(m.Snapshot())
	}
	if _, err = m.Call(context.Background(), "foreign", "POST", "/api/v1/tasks", nil); err == nil {
		t.Fatal("foreign owner route accepted")
	}
	if operations.Load() != 0 {
		t.Fatal("foreign owner request used local backend")
	}
	if _, err = m.Call(context.Background(), "node", "POST", "/api/v1/tasks?space_id=node", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if operations.Load() != 1 {
		t.Fatal(operations.Load())
	}
}
func TestChangedEventRejectsUnchangedAndRetainsScopeReset(t *testing.T) {
	event := Event{Data: json.RawMessage(`{"versions":{"tasks":1},"boot_id":"boot"}`)}
	_, versions, dirty := changedEvent(event, nil)
	if !dirty {
		t.Fatal("initial baseline discarded")
	}
	_, _, dirty = changedEvent(event, versions)
	if dirty {
		t.Fatal("unchanged event refetched")
	}
	_, _, dirty = changedEvent(Event{Data: json.RawMessage(`{"versions":{"tasks":1},"boot_id":"new"}`)}, versions)
	if !dirty {
		t.Fatal("restart baseline discarded")
	}
}
