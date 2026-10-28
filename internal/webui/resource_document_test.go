package webui

import (
	"github.com/jjyr/pot/internal/resources"
	webassets "github.com/jjyr/pot/web"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceFactoryHasExactSeparatePolicy(t *testing.T) {
	server, err := New(Options{Origin: "http://localhost", Config: NewConfig(), Upstream: Upstream{URL: "http://runtime", Credential: "synthetic", Client: &http.Client{}}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost"+path, nil))
		return response
	}
	factory := get("/resource-document.html")
	if factory.Code != 200 || factory.Header().Get("Content-Security-Policy") != resources.HTMLPolicy+" allow-same-origin" {
		t.Fatalf("factory policy: %d %s", factory.Code, factory.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(factory.Body.String(), "event.origin!==location.origin") || !strings.Contains(factory.Body.String(), "frameElement.sandbox.contains('allow-same-origin')") || strings.Contains(factory.Body.String(), "document.write") {
		t.Fatal("factory must check exact parent origin/sandbox and never insert author HTML")
	}
	for _, path := range []string{"/", "/resource-document.html/", "/resource-document.htm", "/state/display-resources.mjs"} {
		response := get(path)
		if strings.Contains(response.Header().Get("Content-Security-Policy"), "sandbox allow-scripts allow-same-origin") {
			t.Fatalf("factory policy escaped exact asset path: %s", path)
		}
	}
	if !Allowed("GET", "/api/v1/resources/synthetic/render-document") || Allowed("POST", "/api/v1/resources/synthetic/render-document") {
		t.Fatal("display document allowlist must be read-only")
	}
}
