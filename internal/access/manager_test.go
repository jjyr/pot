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

func TestExplicitBrowserAccessRotatesAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := webui.AccessStore{Path: filepath.Join(root, "browser-access.json")}
	if _, err := store.Load(true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Change("disable"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "access-settings.json"), []byte(`{"connected":true,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	options := Options{Root: root, Origin: "https://fixture.example", Upstream: webui.Upstream{URL: upstream.URL, Credential: "control", Client: upstream.Client(), ControlID: "control"}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}}
	missingOrigin := options
	missingOrigin.Origin = ""
	if _, err := Open(missingOrigin); err == nil {
		t.Fatal("accepted implicit browser publication")
	}
	manager, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, err := manager.Call(ctx, "GET", nil)
	if err != nil {
		t.Fatal(err)
	}
	value := status.(map[string]any)
	if value["enabled"] != false || value["tailscale"] != nil {
		t.Fatal("unexpected browser status", value["enabled"])
	}
	generation := value["generation"]
	for _, action := range []string{"connect", "disconnect"} {
		if _, err = manager.Call(ctx, "POST", []byte(`{"action":"`+action+`"}`)); err == nil {
			t.Fatal("retired network action accepted", action)
		}
	}
	status, err = manager.Call(ctx, "GET", nil)
	if err != nil || status.(map[string]any)["generation"] != generation {
		t.Fatal("retired network actions changed access")
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
	if _, err = manager.Call(ctx, "POST", []byte(`{"action":"disable"}`)); err != nil {
		t.Fatal(err)
	}
	config, _ = manager.store.Load(false)
	if w = invoke("POST", "/access/token", "", "Bearer "+config.Token); w.Code != 403 {
		t.Fatal("disabled browser accepted login", w.Code)
	}
	if _, err = os.Stat(filepath.Join(root, "access-settings.json")); !os.IsNotExist(err) {
		t.Fatal("retired settings were recreated", err)
	}
}

func TestEmbeddedTailnetUpgradePreservesBrowserAccess(t *testing.T) {
	root := t.TempDir()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	options := Options{Root: root, Origin: "https://fixture.example", Upstream: webui.Upstream{URL: upstream.URL, Credential: "control", Client: upstream.Client(), ControlID: "control"}, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}}}
	manager, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	config, err := manager.store.Load(false)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "https://fixture.example/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, login)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	manager.Close()
	before, err := os.ReadFile(filepath.Join(root, "browser-access.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "access-settings.json"), []byte(`{"connected":true,"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		manager, err = Open(options)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("GET", "https://fixture.example/api/v1/runtime", nil)
		request.AddCookie(cookie)
		w = httptest.NewRecorder()
		manager.ServeHTTP(w, request)
		manager.Close()
		if w.Code != 200 {
			t.Fatal("browser session lost during upgrade", attempt, w.Code)
		}
		after, err := os.ReadFile(filepath.Join(root, "browser-access.json"))
		if err != nil || string(before) != string(after) {
			t.Fatal("upgrade modified browser credentials or session state", err)
		}
	}
	if _, err = os.Stat(filepath.Join(root, "access-settings.json")); !os.IsNotExist(err) {
		t.Fatal("obsolete settings survived upgrade", err)
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
	manager, err := Open(Options{
		Root: root, Origin: "https://fixture.example",
		Upstream: webui.Upstream{URL: "https://control.invalid", Credential: "control", Client: &http.Client{}, ControlID: "control"},
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}},
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
