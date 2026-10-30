package transport

import (
	"context"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/webui"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise real Control/SQLite/history and the production handler. Replacing a
// cancelled subscription must never consume another subscription's first wakeup.
func TestConversationStreamInitialFrameAfterCancellation(t *testing.T) {
	t.Parallel()
	app := historyApplication(t, 160)
	owner := localAssistantID(t, app.Control)
	var requests, active atomic.Int64
	handler := Handler(app, "synthetic-stream-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		active.Add(1)
		defer active.Add(-1)
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := server.Client()
	sentinel := errors.New("conversation frame")
	var group sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		group.Go(func() {
			for i := 0; i < 8; i++ {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				started := time.Now()
				err := localui.NewHTTPClient(server.URL, client, "synthetic-stream-token", app.RuntimeID).Stream(ctx, "/api/v1/events?assistant_id="+owner, func(event localui.Event) error {
					if event.Type == "conversation" {
						return sentinel
					}
					return nil
				})
				cancel()
				if !errors.Is(err, sentinel) {
					t.Error("initial frame", err)
					return
				}
				t.Logf("first frame: elapsed=%s", time.Since(started))
				// Real parallel directory/history reads contend for the same native
				// conversation, without manufacturing a held first frame or lock.
				r, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/api/v1/history?assistant_id="+owner+"&limit=80", nil)
				r.Header.Set("Authorization", "Bearer synthetic-stream-token")
				v, e := client.Do(r)
				if e != nil {
					t.Error(e)
					return
				}
				v.Body.Close()
			}
		})
	}
	group.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatal("cancelled stream handlers did not retire", active.Load())
	}
	t.Logf("requests=%d, active after cancellation=%d", requests.Load(), active.Load())
}

func TestConversationInitialWakeupBelongsToSubscription(t *testing.T) {
	t.Parallel()
	app := historyApplication(t, 8)
	owner := localAssistantID(t, app.Control)
	for i := 0; i < 40; i++ {
		oldCtx, cancel := context.WithCancel(t.Context())
		old, err := app.Control.SubscribeAgent(oldCtx, owner, "en")
		if err != nil {
			t.Fatal(err)
		}
		current, err := app.Control.SubscribeAgent(t.Context(), owner, "en")
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		old.Close()
		select {
		case _, ok := <-current.Updates:
			if !ok {
				t.Fatal("new subscription closed")
			}
		case <-time.After(time.Second):
			t.Fatal("new initial notification lost")
		}
		started := time.Now()
		frame, err := current.Frame(t.Context(), 0, true)
		current.Close()
		if err != nil || frame.Kind != "reset" {
			t.Fatal(frame.Kind, err)
		}
		t.Logf("subscribe/update/frame/close iteration=%d frame=%s", i, time.Since(started))
	}
}

// An opt-in real authenticated browser gateway, Control, SQLite and WS host.
// All roots and history are synthetic; no model request is made by this fixture.
func TestChatStreamBrowserFixture(t *testing.T) {
	file := os.Getenv("POT_STREAM_FIXTURE_URL_FILE")
	if file == "" {
		t.Skip("opt-in isolated browser fixture")
	}
	app := historyApplication(t, 160)
	owner := localAssistantID(t, app.Control)
	definitions, _ := app.Control.Registry.Definitions(t.Context())
	upstream := httptest.NewServer(Handler(app, "synthetic-control-only"))
	defer upstream.Close()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	var gateway *webui.Server
	var configForFixture webui.Config
	files := http.FileServer(http.Dir(repo))
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture/stop":
			select {
			case done <- struct{}{}:
			default:
			}
			return
		case "/fixture/meta":
			fmt.Fprintf(w, `{"owner":%q,"name":%q}`, owner, definitions[0].Name)
			return
		case "/fixture/login":
			r = r.Clone(r.Context())
			r.Method = "POST"
			r.URL.Path = "/access/token"
			r.Header.Set("Authorization", "Bearer "+configForFixture.Token)
			gateway.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/pkg/") {
			files.ServeHTTP(w, r)
			return
		}
		gateway.ServeHTTP(w, r)
	}))
	defer front.Close()
	configForFixture = webui.NewConfig()
	gateway, err = webui.New(webui.Options{Origin: front.URL, Config: configForFixture, Upstream: webui.Upstream{URL: upstream.URL, Credential: "synthetic-control-only", Client: upstream.Client()}, Assets: os.DirFS(filepath.Join(repo, "web", "src"))})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err = os.WriteFile(file, []byte(front.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Minute):
		t.Fatal("fixture expired")
	}
}
