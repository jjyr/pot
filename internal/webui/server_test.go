package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestAuthenticationAllowlistCredentialSeparationAndRotation(t *testing.T) {
	requests := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Get("Authorization") + "|" + r.Header.Get("Cookie")
		if r.URL.Path == "/api/v1/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("event: snapshot\ndata: {}\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	config := NewConfig()
	server, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: upstream.URL, Credential: "control-secret", Client: upstream.Client()}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	invoke := func(method, path, authorization, cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, nil)
		r.Header.Set("Authorization", authorization)
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	if w := invoke("GET", "/api/v1/runtime", "Bearer "+config.Token, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := invoke("POST", "/access/token", "Bearer control-secret", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := invoke("POST", "/access/token", "Bearer "+config.Token, "", "http://localhost")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal(cookie)
	}
	if w = invoke("GET", "/api/v1/runtime", "Bearer browser-secret", cookie.String(), ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if actual := <-requests; actual != "Bearer control-secret|" {
		t.Fatal("credential leaked or confused:", actual)
	}
	if w = invoke("GET", "/api/v1/operator/private", "", cookie.String(), ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		if w = invoke(method, "/api/v1/sandbox-resources", "", cookie.String(), "http://localhost"); w.Code != 404 {
			t.Fatalf("browser resource provisioning allowed: %s %d", method, w.Code)
		}
	}
	for _, endpoint := range []struct{ method, path string }{
		{"POST", "/api/v1/desktop/sessions"},
		{"GET", "/desktop/retired/rfb"},
		{"GET", "/desktop-assets/rfb.js"},
		{"POST", "/api/v1/machines"},
	} {
		if denied := invoke(endpoint.method, endpoint.path, "", cookie.String(), "http://localhost"); denied.Code != 404 {
			t.Fatal("retired management route published", endpoint, denied.Code)
		}
	}
	if w = invoke("POST", "/api/v1/messages", "", cookie.String(), "https://attacker.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "http://localhost/api/v1/events", nil).WithContext(ctx)
	r.Header.Set("Cookie", cookie.String())
	done := make(chan struct{})
	go func() { server.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("stream not connected")
	}
	next := NewConfig()
	next.Generation = config.Generation + 1
	if err = server.ReplaceAccess(next); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rotation did not stop stream")
	}
	if w = invoke("GET", "/api/v1/runtime", "", cookie.String(), ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestBrowserRateLimitAndOrigin(t *testing.T) {
	config := NewConfig()
	s, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: "http://runtime", Credential: "control", Client: &http.Client{}}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "http://localhost/access/token", nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		expected := 401
		if i == 10 {
			expected = 429
		}
		if w.Code != expected {
			t.Fatal(i, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://evil/api/v1/runtime", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	data, _ := io.ReadAll(w.Result().Body)
	if w.Code != 200 || !strings.Contains(string(data), "UI") {
		t.Fatal(w.Code, string(data))
	}
}
