package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/protocol"
	webassets "github.com/jjyr/pot/web"
)

func TestUpdateAuthenticatedHTTPAndBrowserFixture(t *testing.T) {
	app := historyApplication(t, 0)
	var mu sync.Mutex
	status := selfupdate.Status{Current: "0.1.0", State: selfupdate.State{Version: "0.2.0", Phase: "available"}, ReleaseURL: "https://github.com/jjyr/pot/releases/tag/v0.2.0", Discovery: "manual", Reason: "migration_path"}
	var calls []selfupdate.Command
	app.Updates = func(ctx context.Context, method string, body json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		if method == "POST" {
			var command selfupdate.Command
			if err := protocol.DecodeStrict(body, &command); err != nil {
				return nil, err
			}
			if command.Action != "check" || command.RequestID == "" || command.Version != "" || command.Interrupt {
				return nil, protocol.APIError{Code: "invalid_update"}
			}
			calls = append(calls, command)
		}
		return status, nil
	}
	app.Maintenance = func() bool { return true }
	handler := Handler(app, "fixture-token")
	for _, test := range []struct {
		token, path, body string
		code              int
	}{
		{"", "/api/v1/updates", `{"action":"check","request_id":"check"}`, 401},
		{"fixture-token", "/api/v1/updates", `{"action":"check","request_id":"check"}`, 200},
		{"fixture-token", "/api/v1/updates", `{"action":"check","request_id":"check","arbitrary":"command"}`, 400},
		{"fixture-token", "/api/v1/tasks", `{}`, 503},
	} {
		request := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatal(test.path, response.Code, response.Body.String())
		}
		if test.code == 200 && !strings.Contains(response.Body.String(), status.ReleaseURL) {
			t.Fatal("manual release link missing", response.Body.String())
		}
	}
	urlFile := os.Getenv("POT_UPDATES_FIXTURE_URL_FILE")
	if urlFile == "" {
		return
	}
	app.Maintenance = nil
	upstream := httptest.NewServer(handler)
	defer upstream.Close()
	config := webui.NewConfig()
	server := httptest.NewUnstartedServer(nil)
	browser, err := webui.New(webui.Options{Origin: "http://" + server.Listener.Addr().String(), Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "fixture-token", Client: upstream.Client()}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	login := httptest.NewRequest("POST", "http://"+server.Listener.Addr().String()+"/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	logged := httptest.NewRecorder()
	browser.ServeHTTP(logged, login)
	cookie := logged.Result().Cookies()[0]
	done := make(chan struct{}, 1)
	repo, _ := filepath.Abs("../..")
	files := http.FileServer(http.Dir(repo))
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture/stop":
			select {
			case done <- struct{}{}:
			default:
			}
			return
		case "/fixture/update":
			mu.Lock()
			defer mu.Unlock()
			if err := json.NewDecoder(r.Body).Decode(&status); err != nil {
				http.Error(w, err.Error(), 400)
			}
			return
		case "/fixture/calls":
			mu.Lock()
			defer mu.Unlock()
			json.NewEncoder(w).Encode(calls)
			return
		}
		r.AddCookie(cookie)
		if strings.HasPrefix(r.URL.Path, "/scripts/") || strings.HasPrefix(r.URL.Path, "/web/") || strings.HasPrefix(r.URL.Path, "/output/update-before/") {
			files.ServeHTTP(w, r)
			return
		}
		browser.ServeHTTP(w, r)
	})
	server.Start()
	defer func() { server.CloseClientConnections(); server.Close() }()
	if err := os.WriteFile(urlFile, []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(25 * time.Minute):
		t.Fatal("update browser fixture expired")
	}
}
