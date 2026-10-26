package access

import (
	"context"
	"github.com/jjyr/pot/internal/webui"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestPrivateOperatorControlsOnlyIndependentBrowserService(t *testing.T) {
	root, err := os.MkdirTemp("", "pot-access-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	manager, err := Open(Options{Root: root, Origin: "https://browser.example", Upstream: webui.Upstream{URL: upstream.URL, Credential: "control", Client: upstream.Client(), ControlID: "control"}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	server, err := StartOperator(root, manager)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	endpoint, err := ReadOperator(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := CallOperator(context.Background(), endpoint, "status")
	if err != nil || status["enabled"] != true {
		t.Fatal(err)
	}
	before := status["token"]
	status, err = CallOperator(context.Background(), endpoint, "regenerate_token")
	if err != nil || status["token"] == before {
		t.Fatal("rotation failed", err)
	}
	forged := endpoint
	forged.Token = "different-private-credential"
	if _, err = CallOperator(context.Background(), forged, "disable"); err == nil {
		t.Fatal("forged operator accepted")
	}
	if err = os.Chmod(filepath.Join(root, "browser-operator.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadOperator(root); err == nil {
		t.Fatal("public operator credentials accepted")
	}
	if webui.Allowed("GET", "/api/v1/access") || webui.Allowed("POST", "/api/v1/access") {
		t.Fatal("browser exposed owner credential administration")
	}
}
