package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestProviderAllowancesKeepOwnerCredentialsAtProxy(t *testing.T) {
	type observed struct{ Method, Path, Authorization, Cookie, Body string }
	requests := make(chan observed, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- observed{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Cookie"), string(body)}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ready"}`))
	}))
	defer upstream.Close()
	config := NewConfig()
	server, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: upstream.URL, Credential: "control-only", Client: upstream.Client()}, Assets: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	path := "/api/v1/providers/allowances?profile_id=connection-a&space_id=local"
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s accepted: %d", method, w.Code)
		}
	}
	if len(requests) != 0 {
		t.Fatal("unauthenticated allowance read reached Control")
	}
	login := httptest.NewRequest("POST", "http://localhost/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, login)
	if w.Code != http.StatusOK {
		t.Fatal("fixture login failed", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	for _, method := range []string{"GET", "POST"} {
		body := `{"profile_id":"connection-a","action":"refresh"}`
		w := invokeSocket(t, server, method, path, []byte(body), cookie, "http://localhost")
		if w.Code != http.StatusOK {
			t.Fatalf("owner %s failed: %d %s", method, w.Code, w.Body.String())
		}
		got := <-requests
		if got != (observed{method, path, "Bearer control-only", "", body}) {
			t.Fatalf("allowance proxy changed scope or leaked browser credentials: %+v", got)
		}
	}
	for _, route := range []struct{ method, path string }{
		{"DELETE", "/api/v1/providers/allowances"},
		{"PUT", "/api/v1/providers/allowances"},
		{"GET", "/api/v1/providers/allowances/private"},
		{"POST", "/api/v1/providers/allowances/private"},
	} {
		if Allowed(route.method, route.path) {
			t.Fatal("allowance route broadened unrelated authority", route)
		}
	}
}
