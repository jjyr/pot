package localui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestStreamManyMultiplexesAndUnsubscribes(t *testing.T) {
	var connections atomic.Int32
	done := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"pot.v1"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		write := func(value any) bool {
			raw, _ := json.Marshal(value)
			return conn.Write(r.Context(), websocket.MessageText, raw) == nil
		}
		ids := []string{}
		for len(ids) < 2 {
			_, raw, e := conn.Read(r.Context())
			if e != nil {
				return
			}
			var f struct{ Type, ID string }
			json.Unmarshal(raw, &f)
			ids = append(ids, f.ID)
		}
		// ACKs and events for different subscriptions may interleave.
		for i := 1; i >= 0; i-- {
			if !write(map[string]any{"type": "response", "id": ids[i], "status": 200}) {
				return
			}
			if !write(map[string]any{"type": "event", "id": ids[i], "event": "state", "data": map[string]int{"n": i}}) {
				return
			}
		}
		var unsubscribed []string
		for len(unsubscribed) < 2 {
			_, raw, e := conn.Read(r.Context())
			if e != nil {
				break
			}
			var f struct{ Type, ID string }
			json.Unmarshal(raw, &f)
			if f.Type == "unsubscribe" {
				unsubscribed = append(unsubscribed, f.ID)
			}
		}
		done <- unsubscribed
	}))
	defer server.Close()
	stop := errors.New("client policy complete")
	paths := []string{}
	err := NewHTTPClient(server.URL, server.Client(), "private", "").StreamMany(t.Context(), []string{"/api/v1/a", "/api/v1/b"}, func(path string, event Event) error {
		paths = append(paths, path)
		if len(paths) == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || connections.Load() != 1 || len(paths) != 2 || paths[0] != "/api/v1/b" || paths[1] != "/api/v1/a" {
		t.Fatal(paths, err, connections.Load())
	}
	select {
	case ids := <-done:
		if len(ids) != 2 {
			t.Fatal("missing unsubscribe", ids)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unsubscribe not received")
	}
}
