package webui

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/pwapush"
	"github.com/jjyr/pot/internal/systemnotifications"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fixturePush func(context.Context, pwapush.Subscription, []byte) pwapush.Result

func (f fixturePush) Send(ctx context.Context, s pwapush.Subscription, p []byte) pwapush.Result {
	return f(ctx, s, p)
}

type notificationFixture struct {
	server  *Server
	config  Config
	pushed  chan string
	advance func(string)
	invoke  func(string, any, *http.Cookie, string) *httptest.ResponseRecorder
	login   func() *http.Cookie
}

// One isolated source/service/HTTP fixture protects both normal delivery and the
// blocked-body authority races, instead of maintaining two copies of startup.
func notificationHarness(t *testing.T) notificationFixture {
	t.Helper()
	updates := make(chan protocol.LifecycleEvent, 16)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-control" {
			t.Error("missing server credential")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/api/v1/ws" {
			t.Error("notification consumer scanned business resources", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		localui.ServeWebSocket(w, r, localui.SocketOptions{
			Caller: localui.Functions{Events: func(ctx context.Context, path string, receive func(localui.Event) error) error {
				if path != "/api/v1/lifecycle" {
					return fmt.Errorf("unexpected events path %q", path)
				}
				authority, _ := json.Marshal(hooks.Authority{Control: "node", Boot: "boot", Owners: []hooks.Owner{{ID: "alpha", Name: "Alpha", Boot: "boot", Control: "node", Space: "node"}}})
				if err := receive(localui.Event{Type: "authority", Data: authority}); err != nil {
					return err
				}
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case e := <-updates:
						data, _ := json.Marshal(e)
						if err := receive(localui.Event{Type: "lifecycle", Data: data}); err != nil {
							return err
						}
					}
				}
			}},
		})
	}))
	t.Cleanup(upstream.Close)
	config := NewConfig()
	server, err := New(Options{Origin: "http://localhost", Config: config, Upstream: Upstream{URL: upstream.URL, Credential: "private-control", Client: upstream.Client()}, Assets: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	pushed := make(chan string, 16)
	transport := fixturePush(func(_ context.Context, _ pwapush.Subscription, p []byte) pwapush.Result {
		var data struct {
			Device string
			Event  systemnotifications.Event
		}
		if err := json.Unmarshal(p, &data); err != nil {
			t.Error(err)
		}
		pushed <- data.Device + ":" + data.Event.Owner
		return pwapush.Result{Status: 201}
	})
	n, err := startNotifications(t.TempDir(), server.upstream, "http://localhost", config, transport)
	if err != nil {
		t.Fatal(err)
	}
	server.notifications = n
	t.Cleanup(func() { server.Close() })
	for deadline := time.Now().Add(testutil.Timeout(t, time.Second)); ; {
		if n.events.ValidateScope("node", "node", []string{"alpha"}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source baseline unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	invoke := func(path string, body any, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		if strings.HasPrefix(path, "/api/") {
			return invokeSocket(t, server, "POST", path, data, cookie, origin)
		}
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
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost/access/token", nil)
		r.Header.Set("Authorization", "Bearer "+config.Token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		return w.Result().Cookies()[0]
	}
	return notificationFixture{server: server, config: config, pushed: pushed, advance: func(id string) {
		updates <- protocol.LifecycleEvent{ID: id, Kind: "reply_completed", Owner: "alpha", Preview: "Synthetic completed reply"}
	}, invoke: invoke, login: login}
}
func TestAuthenticatedNotificationDevicesAndClosedPageDelivery(t *testing.T) {
	f := notificationHarness(t)
	server, config, invoke, login, pushed := f.server, f.config, f.invoke, f.login, f.pushed
	cookieA := login()
	cookieB := login()
	idA := strings.Repeat("a", 24)
	idB := strings.Repeat("b", 24)
	local := map[string]any{"device": idA, "control": "node", "space": "node", "channel": "local", "enabled": true, "permission": "granted", "locale": "en"}
	if got := invoke("/api/v1/notifications/device", local, nil, "").Code; got != 401 {
		t.Fatal("cookie not required", got)
	}
	if got := invoke("/api/v1/notifications/device", local, cookieA, "https://attacker.example").Code; got != 403 {
		t.Fatal("origin not enforced", got)
	}
	if w := invoke("/api/v1/notifications/device", local, cookieA, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := invoke("/api/v1/notifications/view", map[string]any{"device": idA, "session": "tab", "control": "node", "view": map[string]any{"space": "node", "owner": "alpha", "route": "chat", "focused": false, "sequence": 1}}, cookieA, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	local["space"] = "other"
	if got := invoke("/api/v1/notifications/device", local, cookieA, "").Code; got != 409 {
		t.Fatal("cross-space registration accepted", got)
	}
	local["space"] = "node"
	if got := invoke("/api/v1/notifications/device", local, cookieB, "").Code; got != 409 {
		t.Fatal("another session took device", got)
	}
	public, _ := ecdh.P256().GenerateKey(rand.Reader)
	sub := pwapush.Subscription{Endpoint: "https://web.push.apple.com/synthetic"}
	sub.Keys.P256DH = base64.RawURLEncoding.EncodeToString(public.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	push := map[string]any{"device": idB, "control": "node", "space": "node", "channel": "push", "enabled": true, "permission": "granted", "locale": "zh-CN", "subscription": sub}
	unsafe := sub
	unsafe.Endpoint = "https://100.70.0.1/internal"
	push["subscription"] = unsafe
	if got := invoke("/api/v1/notifications/device", push, cookieB, "").Code; got != 400 {
		t.Fatal("internal endpoint accepted", got)
	}
	push["subscription"] = sub
	if w := invoke("/api/v1/notifications/device", push, cookieB, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	local["subscription"] = sub
	if got := invoke("/api/v1/notifications/device", local, cookieA, "").Code; got != 400 {
		t.Fatal("local device accepted Push", got)
	}
	delete(local, "subscription")
	// No page is running. The authenticated stream carries the committed fact
	// directly, without catalogue/task/approval resource reads.
	f.advance("new")
	select {
	case got := <-pushed:
		if got != idB+":alpha" {
			t.Fatal("wrong device", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed-page event did not reach Push")
	}
	select {
	case got := <-pushed:
		t.Fatal("local device also pushed", got)
	default:
	}
	if w := invoke("/access/logout", nil, cookieB, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	f.advance("after-logout")
	// An authoritative new source read is observable through the local queue;
	// logging out B must not consume A's pending device notifications.
	w := invoke("/api/v1/notifications/next", map[string]string{"device": idA, "session": "tab"}, cookieA, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"owner":"alpha"`) {
		t.Fatal(w.Code, w.Body)
	}
	select {
	case got := <-pushed:
		t.Fatal("logged-out installation still pushed", got)
	case <-time.After(30 * time.Millisecond):
	}
	next := NewConfig()
	next.Generation = config.Generation + 1
	if err := server.ReplaceAccess(next); err != nil {
		t.Fatal(err)
	}
	if got := invoke("/api/v1/notifications/device", local, cookieA, "").Code; got != 401 {
		t.Fatal("old generation retained authority", got)
	}
}
