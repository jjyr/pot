package localui

import (
	"context"
	"encoding/json"
	"net/url"
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
		u, err := url.ParseRequestURI(path)
		if err != nil || u.Query().Get("expected_control_id") != "node" || u.Query().Get("configuration_control_id") != "node" || u.Query().Get("configuration_boot_id") != "boot" {
			t.Error("native scope stripped configuration or outbox preconditions", path, err)
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
	if _, err = m.Call(context.Background(), "node", "POST", "/api/v1/tasks?space_id=node&expected_control_id=node&configuration_control_id=node&configuration_boot_id=boot", json.RawMessage(`{}`)); err != nil {
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

func TestChangedEventPreservesProductionSSEBoot(t *testing.T) {
	first, versions, dirty := changedEvent(Event{ID: "boot-a:1", Data: json.RawMessage(`{"tasks":1}`)}, nil)
	if !dirty || string(versions["boot_id"]) != `"boot-a"` || !json.Valid(first.Data) {
		t.Fatalf("lost initial SSE identity: %s %+v", first.Data, versions)
	}
	_, _, dirty = changedEvent(Event{ID: "boot-a:2", Data: json.RawMessage(`{"tasks":1}`)}, versions)
	if dirty {
		t.Fatal("SSE sequence without a version change invalidated state")
	}
	changed, _, dirty := changedEvent(Event{ID: "boot-b:1", Data: json.RawMessage(`{"tasks":1}`)}, versions)
	var value map[string]json.RawMessage
	_ = json.Unmarshal(changed.Data, &value)
	if !dirty || string(value["boot_id"]) != `"boot-b"` || string(value["tasks"]) != "1" {
		t.Fatalf("boot change did not invalidate the complete baseline: %s", changed.Data)
	}
	for _, body := range []string{`null`, `{"versions":null}`} {
		_, kept, dirty := changedEvent(Event{ID: "boot-c:1", Data: json.RawMessage(body)}, versions)
		if !dirty || string(kept["boot_id"]) != `"boot-a"` {
			t.Fatalf("invalid version map established a boot: %s", body)
		}
	}
}
