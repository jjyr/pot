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
)

func TestSocketSessionRevocation(t *testing.T) {
	for _, mode := range []string{"logout", "replace", "expire"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("{}"))
			}))
			defer upstream.Close()

			cfg := NewConfig()
			s, err := New(Options{
				Origin:   "http://localhost",
				Config:   cfg,
				Upstream: Upstream{URL: upstream.URL, Credential: "private", Client: upstream.Client()},
				Assets:   fstest.MapFS{},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Host = "localhost"
				s.ServeHTTP(w, r)
			}))
			defer ts.Close()

			expires := time.Now().Add(time.Hour).Unix()
			if mode == "expire" {
				expires = time.Now().Add(2 * time.Second).Unix()
			}
			cookie := &http.Cookie{Name: "pot_session", Value: s.cookie(cfg, expires, "nonce")}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			opts := &websocket.DialOptions{
				Subprotocols: []string{"pot.v1"},
				HTTPHeader: http.Header{
					"Origin": {"http://localhost"},
					"Cookie": {cookie.String()},
				},
			}
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/ws", opts)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()

			switch mode {
			case "logout":
				rr := httptest.NewRecorder()
				request := httptest.NewRequest("POST", "http://localhost/access/logout", nil)
				request.AddCookie(cookie)
				s.ServeHTTP(rr, request)
				if rr.Code != http.StatusOK {
					t.Fatalf("logout status = %d, want %d", rr.Code, http.StatusOK)
				}
			case "replace":
				next := NewConfig()
				next.Generation = cfg.Generation + 1
				if err := s.ReplaceAccess(next); err != nil {
					t.Fatal(err)
				}
			case "expire":
			}

			_, _, err = conn.Read(ctx)
			if err == nil {
				t.Fatal("expected connection to close, got nil error")
			}
			if ctx.Err() != nil {
				t.Fatalf("connection closure timed out: %v", ctx.Err())
			}
		})
	}
}
