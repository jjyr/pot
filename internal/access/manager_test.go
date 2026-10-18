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

type fakeTailnet struct{ connects, webs, closes int }

func (f *fakeTailnet) Connect(context.Context) error { f.connects++; return nil }
func (f *fakeTailnet) Status(context.Context) (any, error) {
	return map[string]string{"state": "Running"}, nil
}
func (f *fakeTailnet) SetWeb(_ context.Context, _ int, on bool) (string, error) {
	if on {
		f.webs++
		return "https://fixture.example", nil
	}
	return "", nil
}
func (f *fakeTailnet) Close() error { f.closes++; return nil }
func TestFreshAccessIsPrivateAndOwnerActionsRotateSessions(t *testing.T) {
	ctx := context.Background()
	node := &fakeTailnet{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	manager, err := Open(ctx, Options{Root: t.TempDir(), Upstream: webui.Upstream{URL: upstream.URL, Credential: "control", Client: upstream.Client(), ControlID: "control"}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}, Tailnet: node})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, err := manager.Call(ctx, "GET", nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.(map[string]any)["enabled"] != false || node.connects != 0 || node.webs != 0 {
		t.Fatal("fresh startup published network access")
	}
	if _, err = manager.Call(ctx, "POST", []byte(`{"action":"enable"}`)); err == nil {
		t.Fatal("enabled without connection")
	}
	if _, err = manager.Call(ctx, "POST", []byte(`{"action":"connect"}`)); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Call(ctx, "POST", []byte(`{"action":"enable"}`))
	if err != nil {
		t.Fatal(err)
	}
	if status.(map[string]any)["enabled"] != true || status.(map[string]any)["qr_data_url"] == nil {
		t.Fatal("missing connection QR")
	}
	config, _ := manager.store.Load(false)
	invoke := func(method, path, cookie, authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://fixture.example"+path, nil)
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, r)
		return w
	}
	w := invoke("POST", "/access/token", "", "Bearer "+config.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0].String()
	if w = invoke("GET", "/api/v1/runtime", cookie, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err = manager.Call(ctx, "POST", []byte(`{"action":"regenerate_token"}`)); err != nil {
		t.Fatal(err)
	}
	if w = invoke("GET", "/api/v1/runtime", cookie, ""); w.Code != 401 {
		t.Fatal("old cookie survived rotation", w.Code)
	}
	if _, err = manager.Call(ctx, "POST", []byte(`{"action":"disconnect"}`)); err != nil {
		t.Fatal(err)
	}
	if node.connects != 1 || node.webs != 1 || node.closes != 1 {
		t.Fatal(node)
	}
}

// Retired push state must not be opened or validated during browser startup.
func TestLegacyPushStateIsIgnored(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "browser-push.sqlite")
	const legacy = "retired subscription store, deliberately not a SQLite database"
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POT_ACCESS_VAPID_KEY", filepath.Join(root, "missing-vapid-key"))
	t.Setenv("POT_ACCESS_VAPID_CONTACT", "invalid retired configuration")
	manager, err := Open(context.Background(), Options{
		Root: root, Origin: "https://fixture.example",
		Upstream: webui.Upstream{URL: "https://control.invalid", Credential: "control", Client: &http.Client{}, ControlID: "control"},
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}, Tailnet: &fakeTailnet{},
	})
	if err != nil {
		t.Fatal("legacy push state blocked startup", err)
	}
	manager.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != legacy {
		t.Fatal("legacy push file was touched", string(data), err)
	}
}
