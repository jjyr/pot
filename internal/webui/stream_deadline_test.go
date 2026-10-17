package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(value time.Time) error {
	w.deadlines = append(w.deadlines, value)
	return nil
}

func TestProxyClearsStreamWriteDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: snapshot\ndata: {}\n\n"))
	}))
	defer upstream.Close()
	config := NewConfig()
	server, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: upstream.URL, Credential: "control-secret", Client: upstream.Client()}, Assets: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	r := httptest.NewRequest("GET", "http://localhost/api/v1/events", nil)
	r.AddCookie(&http.Cookie{Name: "pot_session", Value: server.cookie(config, time.Now().Add(time.Hour).Unix(), "test")})
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	server.ServeHTTP(w, r)
	if w.Code != 200 || len(w.deadlines) != 2 || w.deadlines[0].IsZero() || !w.deadlines[1].IsZero() {
		t.Fatal("proxy deadline must bound the write without timing out idle HTTP/2 streams", w.Code, w.deadlines)
	}
}
