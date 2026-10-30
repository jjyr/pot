package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPotMCPOAuthCallbackNavigationKeepsAPIsFenced(t *testing.T) {
	s, err := New(Options{Origin: "https://pot.example", Config: NewConfig(), Upstream: Upstream{URL: "http://runtime", Credential: "control", Client: &http.Client{}}, Assets: fstest.MapFS{"mcp-pot-callback.html": &fstest.MapFile{Data: []byte("callback")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, test := range []struct {
		method, path, mode, dest, origin, host string
		status                                 int
	}{
		{"GET", "/mcp-pot-callback.html?state=opaque&code=opaque", "navigate", "document", "", "pot.example", 200},
		{"GET", "/mcp-pot-callback.html", "cors", "empty", "", "pot.example", 403},
		{"POST", "/mcp-pot-callback.html", "navigate", "document", "", "pot.example", 403},
		{"GET", "/mcp-pot-callback.html", "navigate", "document", "https://foreign.example", "pot.example", 403},
		{"GET", "/mcp-pot-callback.html", "navigate", "document", "", "foreign.example", 403},
		{"GET", "/api/v1/agent-links/mcp", "navigate", "document", "", "pot.example", 403},
		{"POST", "/api/v1/agent-links/mcp", "navigate", "document", "", "pot.example", 403},
		{"GET", "/access/status", "navigate", "document", "", "pot.example", 403},
	} {
		r := httptest.NewRequest(test.method, "https://pot.example"+test.path, nil)
		r.Host = test.host
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Mode", test.mode)
		r.Header.Set("Sec-Fetch-Dest", test.dest)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%+v: %d %s", test, w.Code, w.Body.String())
		}
		if test.status == 200 && (w.Body.String() != "callback" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Content-Security-Policy") == "") {
			t.Fatal("callback content or headers", w.Body.String(), w.Header())
		}
	}
	// Loading the callback does not establish an owner session.
	r := httptest.NewRequest("POST", "https://pot.example/api/v1/agent-links/mcp", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("callback bypassed API authentication", w.Code)
	}
}

func TestAuthenticationAllowlistCredentialSeparationAndRotation(t *testing.T) {
	requests := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Get("Authorization") + "|" + r.Header.Get("Cookie")
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
		if strings.HasPrefix(path, "/api/") {
			var c *http.Cookie
			if cookie != "" {
				r := httptest.NewRequest(method, "http://localhost"+path, nil)
				r.Header.Set("Cookie", cookie)
				c, _ = r.Cookie("pot_session")
			}
			return invokeSocket(t, server, method, path, nil, c, origin)
		}
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
		{"GET", "/api/v1/machines"},
		{"POST", "/api/v1/machines"},
	} {
		if denied := invoke(endpoint.method, endpoint.path, "", cookie.String(), "http://localhost"); denied.Code != 404 {
			t.Fatal("retired management route published", endpoint, denied.Code)
		}
	}
	if w = invoke("POST", "/api/v1/messages", "", cookie.String(), "https://attacker.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	next := NewConfig()
	next.Generation = config.Generation + 1
	if err = server.ReplaceAccess(next); err != nil {
		t.Fatal(err)
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
				path = "/api/v1/messages/receipt?space_id=remote&assistant_id=alice&conversation_id=main&request_id=send&user_message_id=original&expected_control_id=owning-control"
			}
			w := invokeSocket(t, server, "GET", path, nil, cookie, "")
			if w.Code != http.StatusOK || w.Body.String() != `{"items":[],"offset":20,"next_offset":null}` {
				t.Fatalf("read page not proxied: status=%d body=%s", w.Code, w.Body.String())
			}
			if received := <-requests; func() bool {
				want, err := url.ParseRequestURI(path)
				if err != nil {
					t.Fatal(err)
				}
				got, err := url.ParseRequestURI(received)
				if err != nil {
					t.Fatal(err)
				}
				return got.Path == want.Path && got.Query().Encode() == want.Query().Encode()
			}() == false {
				t.Fatal("read query changed", received)
			}
			for _, method := range []string{"POST", "PUT", "DELETE"} {
				expectedStatus := http.StatusMethodNotAllowed
				if method == "POST" {
					expectedStatus = http.StatusNotFound
				}
				w = invokeSocket(t, server, method, path, nil, cookie, "")
				if w.Code != expectedStatus {
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
		{"POST", "/api/v1/assistants/main/delete", true},
		{"GET", "/api/v1/assistants/main/delete", false},
		{"POST", "/api/v1/assistants/main/delete/extra", false},
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

func TestAgentLinkPublicationAllowlist(t *testing.T) {
	for _, route := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/v1/assistants/profile", true},
		{"GET", "/api/v1/assistants/default-avatar", true},
		{"GET", "/api/v1/assistants/profile/extra", false},
		{"GET", "/api/v1/assistants/default-avatar/extra", false},
		{"POST", "/api/v1/assistants/agent-link:peer:codex", true},
		{"POST", "/api/v1/assistants/agent-link:peer:codex/delete", true},
		{"POST", "/api/v1/assistants/agent-link:peer:codex/delete/extra", false},
		{"POST", "/api/v1/assistants/agent-link:peer:codex/extra", false},
		{"POST", "/api/v1/assistants/agent-link:peer:", false},
		{"POST", "/api/v1/assistants/agent-link:peer:codex:extra", false},
		{"POST", "/api/v1/assistants/agent-link:..:codex", false},
		{"GET", "/api/v1/agent-links", true},
		{"POST", "/api/v1/agent-links", true},
		{"GET", "/api/v1/agent-links/mcp", true},
		{"POST", "/api/v1/agent-links/mcp", true},
		{"DELETE", "/api/v1/agent-links/mcp", false},
		{"POST", "/api/v1/agent-links/mcp/private", false},
		{"POST", "/api/v1/agent-links/invitation", false},
		{"GET", "/api/v1/agent-links/invitation", false},
		{"DELETE", "/api/v1/agent-links", false},
		{"POST", "/api/v1/agent-links/private", false},
		{"GET", "/api/v1/runtime/control", false},
		{"POST", "/api/v1/runtime/control", false},
	} {
		if got := Allowed(route.method, route.path); got != route.allowed {
			t.Errorf("%s %s: got %t", route.method, route.path, got)
		}
	}
}
