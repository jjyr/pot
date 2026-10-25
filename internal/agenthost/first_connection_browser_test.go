package agenthost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
)

// Opt-in real browser gateway and authenticated Agent Link pairing. Only native
// model metadata is faked. No worker is started and no installation is executed.
func TestFirstConnectionBrowserFixture(t *testing.T) {
	file := os.Getenv("POT_FIRST_CONNECTION_FIXTURE_URL_FILE")
	if file == "" {
		t.Skip("opt-in isolated first-connection browser fixture")
	}
	ctx := context.Background()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	urls := map[string]string{}
	for _, runtime := range []Runtime{Codex(), Antigravity()} {
		runtime.Binary = fakeBinary
		if runtime.Kind == "antigravity" {
			runtime.Binary = fakeAgyBinary
		}
		h, e := openAgentHost(filepath.Join(t.TempDir(), "receiver"), runtime)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			connections, _ := h.links.List(ctx)
			for _, connection := range connections {
				if connection.State != "revoked" {
					_ = h.links.Disconnect(ctx, connection.ID)
				}
			}
			h.close()
		})
		if e = h.backend.ownerConfigure(ctx, assistant{ID: "default", Name: "Fixture default", Model: "model-one", Workspace: filepath.Join(h.backend.root, "workspace"), Mode: "read-only", Default: true}); e != nil {
			t.Fatal(e)
		}
		listener, e := linkhost.ListenPeer(ctx, "127.0.0.1:0", h.peerServer())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { listener.Close() })
		for _, intent := range []string{"create", "manage", "observe"} {
			key := runtime.Kind + "-" + intent
			manager, e := execution.New(key, "fixture-boot", execution.DefaultLimits())
			if e != nil {
				t.Fatal(e)
			}
			keys, _ := secrets.NewMemoryKey(make([]byte, 32))
			s, e := control.Open(control.Options{Root: t.TempDir(), RuntimeID: key, BootID: "fixture-boot", Keys: keys, Manager: manager})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { s.Close() })
			app := &transport.Application{Control: s, RuntimeID: key, BootID: "fixture-boot"}
			upstream := httptest.NewServer(transport.Handler(app, "private-fixture"))
			t.Cleanup(upstream.Close)
			server := httptest.NewUnstartedServer(nil)
			origin := "http://" + server.Listener.Addr().String()
			config := webui.NewConfig()
			browser, e := webui.New(webui.Options{Origin: origin, Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "private-fixture", Client: upstream.Client()}, Assets: os.DirFS(repo)})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { browser.Close() })
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					switch r.URL.Path {
					case "/fixture/login":
						login := httptest.NewRequest(http.MethodPost, origin+"/access/token", nil)
						login.Header.Set("Authorization", "Bearer "+config.Token)
						browser.ServeHTTP(w, login)
						return
					case "/fixture/invitation":
						invitation, err := h.links.Invite(ctx, "tcp://"+listener.Addr())
						if err != nil {
							http.Error(w, "fixture invitation unavailable", 500)
							return
						}
						_ = json.NewEncoder(w).Encode(map[string]string{"address": invitation.Address})
						return
					case "/fixture/stop":
						select {
						case done <- struct{}{}:
						default:
						}
						return
					}
				}
				if r.URL.Path == "/fixture/state" {
					assistants, err := h.backend.list(ctx)
					if err != nil {
						http.Error(w, "fixture directory unavailable", 500)
						return
					}
					ids := []string{}
					for _, assistant := range assistants {
						ids = append(ids, assistant.ID)
					}
					var jobs int
					var model, effort string
					_ = h.tasks.QueryRow("SELECT COUNT(*) FROM agent_adapter_jobs").Scan(&jobs)
					_ = h.tasks.QueryRow("SELECT model,effort FROM agent_adapter_executor WHERE singleton=1").Scan(&model, &effort)
					_ = json.NewEncoder(w).Encode(map[string]any{"node_id": h.backend.nodeID, "assistant_ids": ids, "jobs": jobs, "executor_model": model, "executor_effort": effort})
					return
				}
				browser.ServeHTTP(w, r)
			})
			server.Start()
			t.Cleanup(server.Close)
			urls[key] = server.URL
		}
	}
	data, _ := json.Marshal(urls)
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(25 * time.Minute):
		t.Fatal("fixture expired")
	}
}
