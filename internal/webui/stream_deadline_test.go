package webui

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"testing/fstest"
)

// TestBrowserBusinessHTTPRetired verifies that the browser-facing business
// endpoints formerly served by the SSE/HTTP proxy have been retired. Every
// request must return 404 Not Found and must never reach the upstream, which
// is guarded by an atomic counter.
func TestBrowserBusinessHTTPRetired(t *testing.T) {
	var upstreamCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := NewConfig()
	server, err := New(Options{
		Origin: "http://localhost",
		Config: cfg,
		Upstream: Upstream{
			URL:        upstream.URL,
			Credential: "control",
			Client:     upstream.Client(),
		},
		Assets: fstest.MapFS{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	cookie := &http.Cookie{
		Name:  "pot_session",
		Value: server.cookie(cfg, time.Now().Add(time.Hour).Unix(), "test"),
	}

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/events"},
		{http.MethodGet, "/api/v1/changes"},
		{http.MethodGet, "/api/v1/lifecycle"},
		{http.MethodGet, "/api/v1/runtime"},
		{http.MethodGet, "/api/v1/notifications/session"},
		{http.MethodPost, "/api/v1/messages"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, "http://localhost"+ep.path, nil)
		req.AddCookie(cookie)
		req.Header.Set("Origin", "http://localhost")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: got status %d, want %d", ep.method, ep.path, rec.Code, http.StatusNotFound)
		}
	}
	if upstreamCount.Load() != 0 {
		t.Fatal("rejected request reached upstream")
	}
}
