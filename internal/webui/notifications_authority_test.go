package webui

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/pwapush"
	"github.com/jjyr/pot/internal/systemnotifications"
	"github.com/jjyr/pot/internal/testutil"
)

func TestNotificationRegistrationCannotOutliveAuthority(t *testing.T) {
	for _, revoke := range []string{"logout", "replace", "disable"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", revoke, existing), func(t *testing.T) {
				var mu sync.Mutex
				replyID := "old"
				count := 1
				version := 1
				updates := make(chan struct{}, 16)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer private-control" {
						t.Error("missing server credential")
						w.WriteHeader(401)
						return
					}
					if r.URL.Path == "/api/v1/changes" {
						w.Header().Set("Content-Type", "text/event-stream")
						flusher := w.(http.Flusher)
						for {
							mu.Lock()
							v := version
							mu.Unlock()
							fmt.Fprintf(w, "event: changed\ndata: {\"assistant-mail\":%d}\n\n", v)
							flusher.Flush()
							select {
							case <-r.Context().Done():
								return
							case <-updates:
							}
						}
					}
					mu.Lock()
					defer mu.Unlock()
					switch r.URL.Path {
					case "/api/v1/runtime":
						json.NewEncoder(w).Encode(map[string]string{"runtime_id": "node", "boot_id": "boot"})
					case "/api/v1/assistants":
						json.NewEncoder(w).Encode([]any{map[string]any{"id": "alpha", "name": "Alpha", "reply_id": replyID, "reply_count": count, "preview": "Synthetic completed reply"}})
					case "/api/v1/agent-links":
						w.Write([]byte(`{"items":[]}`))
					default:
						t.Error("unexpected upstream request", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer upstream.Close()
				config := NewConfig()
				server, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: upstream.URL, Credential: "private-control", Client: upstream.Client()}, Assets: fstest.MapFS{}})
				if err != nil {
					t.Fatal(err)
				}
				hub, err := systemnotifications.OpenHub(t.TempDir() + "/control.sqlite")
				if err != nil {
					t.Fatal(err)
				}
				key, _ := pwapush.GenerateKey()
				sender, _ := pwapush.New(key, "http://localhost")
				ctx, cancel := context.WithCancel(context.Background())
				n := &notificationService{hub: hub, push: sender, cancel: cancel}
				server.notifications = n
				pushed := make(chan string, 8)
				hub.Push = func(_ context.Context, d systemnotifications.Device, e systemnotifications.Event) systemnotifications.Result {
					pushed <- d.ID + ":" + e.Owner
					return systemnotifications.Result{State: "accepted"}
				}
				n.wg.Add(2)
				go func() {
					defer n.wg.Done()
					hub.Observe(ctx, localui.NewHTTPClient(upstream.URL, upstream.Client(), "private-control", ""))
				}()
				go func() { defer n.wg.Done(); hub.Dispatch(ctx) }()
				defer server.Close()
				for deadline := time.Now().Add(testutil.Timeout(t, time.Second)); hub.Control() == ""; {
					if time.Now().After(deadline) {
						t.Fatal("source did not establish baseline")
					}
					time.Sleep(time.Millisecond)
				}
				invoke := func(path string, body any, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
					t.Helper()
					data, _ := json.Marshal(body)
					r := httptest.NewRequest("POST", "http://localhost"+path, bytes.NewReader(data))
					if cookie != nil {
						r.AddCookie(cookie)
					}
					r.Header.Set("Origin", origin)
					w := httptest.NewRecorder()
					server.ServeHTTP(w, r)
					return w
				}
				login := func() *http.Cookie {
					r := httptest.NewRequest("POST", "http://localhost/access/token", nil)
					r.Header.Set("Authorization", "Bearer "+config.Token)
					w := httptest.NewRecorder()
					server.ServeHTTP(w, r)
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body)
					}
					return w.Result().Cookies()[0]
				}

				cookie := login()
				otherCookie := login()
				public, _ := ecdh.P256().GenerateKey(rand.Reader)
				sub := pwapush.Subscription{Endpoint: "https://web.push.apple.com/synthetic-review"}
				sub.Keys.P256DH = base64.RawURLEncoding.EncodeToString(public.PublicKey().Bytes())
				sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
				id := strings.Repeat("r", 24)
				input := func(id string) map[string]any {
					next := sub
					next.Endpoint += "/" + id
					return map[string]any{"device": id, "control": "node", "space": "node", "channel": "push", "enabled": true, "permission": "granted", "locale": "en", "subscription": next}
				}
				other := strings.Repeat("s", 24)
				if w := invoke("/api/v1/notifications/device", input(other), otherCookie, ""); w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
				if existing {
					if w := invoke("/api/v1/notifications/device", input(id), cookie, ""); w.Code != 200 {
						t.Fatal(w.Code, w.Body)
					}
				}
				body, _ := json.Marshal(input(id))
				blocked := &notificationBlockedBody{Reader: bytes.NewReader(body), entered: make(chan struct{}), release: make(chan struct{})}
				request := httptest.NewRequest("POST", "http://localhost/api/v1/notifications/device", blocked)
				request.AddCookie(cookie)
				response := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); server.ServeHTTP(response, request) }()
				select {
				case <-blocked.entered:
				case <-time.After(testutil.Timeout(t, 2*time.Second)):
					t.Fatal("registration did not reach body after authentication")
				}
				// Body IO must not hold the authority lock: revocation completes before release.
				switch revoke {
				case "logout":
					if w := invoke("/access/logout", nil, cookie, ""); w.Code != 200 {
						t.Fatal("logout", w.Code)
					}
				default:
					next := NewConfig()
					next.Generation = config.Generation + 1
					next.Disabled = revoke == "disable"
					if err := server.ReplaceAccess(next); err != nil {
						t.Fatal(err)
					}
				}
				close(blocked.release)
				select {
				case <-done:
				case <-time.After(testutil.Timeout(t, 2*time.Second)):
					t.Fatal("registration did not finish")
				}
				if response.Code != 401 {
					t.Errorf("late registration revived authority: HTTP %d %s", response.Code, response.Body.String())
				}
				statusRequest := httptest.NewRequest("GET", "http://localhost/access/status", nil)
				statusRequest.AddCookie(cookie)
				statusResponse := httptest.NewRecorder()
				server.ServeHTTP(statusResponse, statusRequest)
				if !strings.Contains(statusResponse.Body.String(), `"authenticated":false`) {
					t.Fatal(statusResponse.Body)
				}
				mu.Lock()
				replyID = "new-after-revocation"
				count++
				version++
				mu.Unlock()
				updates <- struct{}{}
				if revoke == "logout" {
					select {
					case device := <-pushed:
						if device != other+":alpha" {
							t.Errorf("revoked device received event: %s", device)
						}
					case <-time.After(testutil.Timeout(t, 2*time.Second)):
						t.Fatal("logout wrongly revoked another device")
					}
				}
				select {
				case device := <-pushed:
					t.Errorf("post-revocation event dispatched to %s", device)
				case <-time.After(150 * time.Millisecond):
				}
			})
		}
	}
}

type notificationBlockedBody struct {
	*bytes.Reader
	entered, release chan struct{}
	once             sync.Once
}

func (b *notificationBlockedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	if b.Len() == 0 {
		return 0, io.EOF
	}
	return b.Reader.Read(p)
}
