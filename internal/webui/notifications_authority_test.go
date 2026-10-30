package webui

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/pwapush"
	"github.com/jjyr/pot/internal/testutil"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNotificationRegistrationCannotOutliveAuthority(t *testing.T) {
	for _, revoke := range []string{"logout", "replace", "disable"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", revoke, existing), func(t *testing.T) {
				f := notificationHarness(t)
				server, config, invoke, login, pushed := f.server, f.config, f.invoke, f.login, f.pushed

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
				originalConfig := config
				go func() { defer close(done); server.notificationRoute(response, request, originalConfig) }()
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
				f.advance("new-after-revocation")
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
