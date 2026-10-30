package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func invokeSocket(t *testing.T, s *Server, method, path string, body []byte, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	t.Helper()

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = s.origin.Host
		s.ServeHTTP(w, r)
	}))
	defer testServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if origin == "" {
		origin = s.origin.String()
	}

	header := http.Header{}
	header.Set("Origin", origin)
	if cookie != nil {
		header.Set("Cookie", cookie.String())
	}

	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(testServer.URL, "http")+"/api/v1/ws", &websocket.DialOptions{
		Subprotocols: []string{"pot.v1"},
		HTTPHeader:   header,
	})

	recorder := httptest.NewRecorder()

	if err != nil {
		if resp == nil {
			t.Fatal(err)
		}
		recorder.WriteHeader(resp.StatusCode)
		io.Copy(recorder, resp.Body)
		resp.Body.Close()
		return recorder
	}
	defer conn.CloseNow()

	req := map[string]any{
		"type":   "request",
		"id":     "one",
		"method": method,
		"path":   path,
	}
	if len(body) > 0 {
		req["body"] = json.RawMessage(body)
	}

	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatal(err)
	}

	var msg struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Status int             `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatal(err)
	}

	if msg.Type != "response" || msg.ID != "one" {
		t.Fatalf("unexpected message: type=%q id=%q", msg.Type, msg.ID)
	}

	recorder.WriteHeader(msg.Status)
	if msg.Status >= 200 && msg.Status < 300 {
		recorder.Write(msg.Result)
	} else {
		recorder.Write(msg.Error)
	}

	return recorder
}
