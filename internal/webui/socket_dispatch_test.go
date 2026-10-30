package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestSocketDispatchRestrictions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer private" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer private")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer upstream.Close()

	cfg := NewConfig()
	s, err := New(Options{
		Origin: "http://localhost",
		Config: cfg,
		Upstream: Upstream{
			URL:        upstream.URL,
			Credential: "private",
			Client:     upstream.Client(),
		},
		Assets: fstest.MapFS{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	cookie := &http.Cookie{
		Name:  "pot_session",
		Value: s.cookie(cfg, time.Now().Add(time.Hour).Unix(), "nonce"),
	}

	cases := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{"GET", "/api/v1/runtime?locale=zh%2DCN", "", 200},
		{"POST", "/api/v1/assistants/agent-link%3Apeer%3Aagent", "{}", 200},
		{"GET", "/api/v1/operator/private", "", 404},
		{"GET", "/api/v1/lifecycle", "", 404},
		{"GET", "/api/v1/ws", "", 404},
		{"POST", protocol.AssistantMailAPI, `{"operation":"send"}`, 403},
	}

	for _, tc := range cases {
		rec := invokeSocket(t, s, tc.method, tc.path, []byte(tc.body), cookie, "http://localhost")
		if rec.Code != tc.status {
			t.Fatalf("%s %s: status = %d, want %d (body %q)", tc.method, tc.path, rec.Code, tc.status, rec.Body.String())
		}
	}

	// Missing cookie should be rejected for a normal protected route.
	rec := invokeSocket(t, s, "GET", "/api/v1/runtime", nil, nil, "http://localhost")
	if rec.Code != 401 {
		t.Fatalf("missing cookie: status = %d, want 401", rec.Code)
	}

	// Origin handshake restrictions.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "localhost"
		s.ServeHTTP(w, r)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws"

	for _, origin := range []string{"", "https://evil.example"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			Subprotocols: []string{"pot.v1"},
			HTTPHeader: http.Header{
				"Origin": []string{origin},
				"Cookie": []string{cookie.Name + "=" + cookie.Value},
			},
		})
		cancel()

		if err == nil {
			if conn != nil {
				conn.CloseNow()
			}
			t.Fatalf("origin %q: expected dial error, got success", origin)
		}
		if resp == nil {
			t.Fatalf("origin %q: expected response, got nil", origin)
		}
		if resp.StatusCode != 403 {
			t.Fatalf("origin %q: status = %d, want 403", origin, resp.StatusCode)
		}
		if resp.Body != nil {
			resp.Body.Close()
		}
	}
}
