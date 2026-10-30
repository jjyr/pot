package preview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jjyr/pot/internal/localui"
)

func TestSocketSnapshotSubscribeAndDisconnect(t *testing.T) {
	s := fixture(t)
	server := httptest.NewServer(Handler(s, fstest.MapFS{}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, server.URL+"/api/preview/ws", &websocket.DialOptions{
		Subprotocols: []string{"pot.v1"},
		HTTPHeader:   http.Header{"Origin": []string{server.URL}},
	})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()

	var frame struct {
		Type, ID, Event string
		Status          int
		Result, Data    json.RawMessage
	}

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type":   "request",
		"id":     "one",
		"method": "GET",
		"path":   "/api/preview/snapshot",
	}); err != nil {
		t.Fatalf("write snapshot request: %v", err)
	}

	if err := wsjson.Read(ctx, conn, &frame); err != nil {
		t.Fatalf("read snapshot response: %v", err)
	}
	if frame.Type != "response" || frame.ID != "one" || frame.Status != 200 {
		t.Fatalf("unexpected snapshot frame: type=%q id=%q status=%d", frame.Type, frame.ID, frame.Status)
	}

	var snap Snapshot
	if err := json.Unmarshal(frame.Result, &snap); err != nil {
		t.Fatalf("decode snapshot result: %v", err)
	}
	if snap.ConversationID != ConversationID {
		t.Fatalf("unexpected snapshot conversation id: %q", snap.ConversationID)
	}
	if snap.Cursor != "0" {
		t.Fatalf("unexpected snapshot cursor: %q", snap.Cursor)
	}

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "subscribe",
		"id":   "sub",
		"path": "/api/preview/events",
	}); err != nil {
		t.Fatalf("write subscribe request: %v", err)
	}

	if err := wsjson.Read(ctx, conn, &frame); err != nil {
		t.Fatalf("read subscribe ack: %v", err)
	}
	if frame.Type != "response" || frame.ID != "sub" || frame.Status != 200 {
		t.Fatalf("unexpected subscribe ack: type=%q id=%q status=%d", frame.Type, frame.ID, frame.Status)
	}

	if err := wsjson.Read(ctx, conn, &frame); err != nil {
		t.Fatalf("read initial snapshot event: %v", err)
	}
	if frame.Type != "event" || frame.Event != "snapshot" || len(frame.Data) == 0 {
		t.Fatalf("unexpected initial event: type=%q event=%q data=%s", frame.Type, frame.Event, frame.Data)
	}

	var eventSnap Snapshot
	if err := json.Unmarshal(frame.Data, &eventSnap); err != nil {
		t.Fatalf("decode initial event data: %v", err)
	}
	if eventSnap.ConversationID != ConversationID {
		t.Fatalf("unexpected event conversation id: %q", eventSnap.ConversationID)
	}
	if eventSnap.Cursor != "0" {
		t.Fatalf("unexpected event cursor: %q", eventSnap.Cursor)
	}

	conn.CloseNow()
	waitForSubscribers(t, s, 0)
}

func TestSocketCallerRejectsBadMessages(t *testing.T) {
	s := fixture(t)
	caller := SocketCaller(s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	largeBody := json.RawMessage(`{"payload":"` + strings.Repeat("a", 65537) + `"}`)

	tests := []struct {
		name string
		body json.RawMessage
		want int
	}{
		{name: "unknown fields", body: json.RawMessage(`{"unknown":true}`), want: 400},
		{name: "multiple bodies", body: json.RawMessage(`{}{}`), want: 400},
		{name: "invalid conversation", body: json.RawMessage(`{"conversation_id":""}`), want: 400},
		{name: "too large", body: largeBody, want: 413},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := caller.Call(ctx, "POST", "/api/preview/messages", tc.body)
			if err == nil {
				t.Fatal("expected error")
			}

			var se *localui.SocketError
			if !errors.As(err, &se) {
				t.Fatalf("expected *localui.SocketError, got %T: %v", err, err)
			}
			if se.Status != tc.want {
				t.Fatalf("unexpected status %d, want %d", se.Status, tc.want)
			}
		})
	}
}

func waitForSubscribers(t *testing.T, s *Service, max int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for {
		if subscriberCount(s) <= max {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscribers did not drop to <= %d", max)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func subscriberCount(s *Service) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribers)
}
