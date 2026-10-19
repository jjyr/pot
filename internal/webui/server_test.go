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
	for _, path := range []string{"/access/push", "/access/push/remove", "/access/push/heartbeat", "/access/push/preferences", "/api/v1/assistant-notifications"} {
		if w := invoke("POST", path, "", cookie.String(), "http://localhost"); w.Code != 404 {
			t.Fatalf("retired push route %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := invoke("GET", "/access/status", "", cookie.String(), ""); w.Code != 200 || w.Body.String() != "{\"authenticated\":true,\"scope\":\"control\"}\n" {
		t.Fatalf("browser access status: %d %s", w.Code, w.Body.String())
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
	policy := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "frame-src 'self' blob:;") {
		t.Fatal("browser must permit owner-bound PDF Blob previews", policy)
	}
	if !strings.Contains(policy, "connect-src 'self';") || !strings.Contains(policy, "frame-ancestors 'none';") {
		t.Fatal("attachment previews must retain browser connection and framing restrictions", policy)
	}
}

func TestBrowserReadPagesReachControl(t *testing.T) {
	requests := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer control-secret" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect upstream credentials")
		}
		requests <- r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[],"offset":20,"next_offset":null}`))
	}))
	defer upstream.Close()
	config := NewConfig()
	server, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: upstream.URL, Credential: "control-secret", Client: upstream.Client()}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	login := httptest.NewRequest("POST", "http://localhost/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatal("login failed", response.Code)
	}
	cookie := response.Result().Cookies()[0]
	for _, kind := range []string{"models", "requests", "tools", "compactions", "send-receipt"} {
		t.Run(kind, func(t *testing.T) {
			path := "/api/v1/usage/" + kind + "?scope=main&timezone=Asia%2FShanghai&days=30&offset=20"
			if kind == "send-receipt" {
				path = "/api/v1/messages/receipt?space_id=remote&assistant_id=alice&conversation_id=main&request_id=send&user_message_id=original"
			}
			r := httptest.NewRequest("GET", "http://localhost"+path, nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Body.String() != `{"items":[],"offset":20,"next_offset":null}` {
				t.Fatalf("read page not proxied: status=%d body=%s", w.Code, w.Body.String())
			}
			if received := <-requests; received != path {
				t.Fatal("read query changed", received)
			}
			for _, method := range []string{"POST", "PUT", "DELETE"} {
				r = httptest.NewRequest(method, "http://localhost"+path, nil)
				r.AddCookie(cookie)
				w = httptest.NewRecorder()
				server.ServeHTTP(w, r)
				if w.Code != http.StatusNotFound {
					t.Fatal("read page allowed mutation", method, w.Code)
				}
			}
		})
	}
	for _, path := range []string{"/api/v1/usage/private", "/api/v1/usage/models/private", "/api/v1/usage/models/"} {
		if Allowed("GET", path) {
			t.Fatal("unknown usage route published", path)
		}
	}
}

func TestAttachmentPublicationAllowlist(t *testing.T) {
	for _, route := range []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/api/v1/attachments", true},
		{"GET", "/api/v1/attachments/file-id", true},
		{"POST", "/api/v1/turn/retry", true},
		{"POST", "/api/v1/turn/continue", true},
		{"GET", "/api/v1/attachments", false},
		{"DELETE", "/api/v1/attachments/file-id", false},
		{"GET", "/api/v1/attachments/../../private", false},
		{"GET", "/api/v1/attachments/file-id/extra", false},
	} {
		if got := Allowed(route.method, route.path); got != route.allowed {
			t.Errorf("%s %s: got %t", route.method, route.path, got)
		}
	}
}
