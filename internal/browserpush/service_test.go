package browserpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type feedDouble struct {
	mu       sync.Mutex
	control  string
	messages []Notification
	offline  bool
}

func (f *feedDouble) Identity() string { f.mu.Lock(); defer f.mu.Unlock(); return f.control }
func (f *feedDouble) Feed(_ context.Context, after map[string]int64, initialize bool) (Feed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return Feed{}, errors.New("Control offline")
	}
	result := Feed{Cursors: map[string]int64{}}
	for _, n := range f.messages {
		result.Cursors[n.AssistantID] = max(result.Cursors[n.AssistantID], n.Sequence)
		if !initialize && n.Sequence > after[n.AssistantID] {
			result.Messages = append(result.Messages, n)
		}
	}
	return result, nil
}

type senderDouble struct {
	mu       sync.Mutex
	payloads [][]byte
	status   int
}

func (s *senderDouble) PublicKey() string { return "test-public-key" }
func (s *senderDouble) Send(_ context.Context, _ Subscription, data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payloads = append(s.payloads, append([]byte{}, data...))
	return s.status, nil
}
func (s *senderDouble) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.payloads) }
func browserSubscription(t *testing.T) (Subscription, *ecdh.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return Subscription{Endpoint: "https://fcm.googleapis.com/isolated-test", Keys: Keys{P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(auth)}}, key, auth
}
func setupPush(t *testing.T) (*Service, Options, *feedDouble, *senderDouble, *atomic.Int64) {
	t.Helper()
	source := &feedDouble{control: "control-one"}
	sender := &senderDouble{status: 201}
	epoch := &atomic.Int64{}
	epoch.Store(1)
	options := Options{Root: t.TempDir(), Source: source, Sender: sender, CredentialEpoch: func() string {
		if epoch.Load() == 1 {
			return "epoch-one"
		}
		return "epoch-two"
	}, Authorize: func(r *http.Request) (Identity, error) {
		if r.Header.Get("X-Test-Owner") == "" {
			return Identity{}, errors.New("login required")
		}
		return Identity{Owner: r.Header.Get("X-Test-Owner"), Valid: func() bool { return true }}, nil
	}, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") != "https://evil.invalid" }, SecureCookies: true}
	service, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	return service, options, source, sender, epoch
}
func notification(owner string, sequence int64, text string) Notification {
	return Notification{AssistantID: owner, Sequence: sequence, MessageID: owner + "-message", Text: text, CreatedAtMS: time.Now().UnixMilli(), Final: true}
}
func TestDurableOwnerCursorsForegroundAndControlBinding(t *testing.T) {
	s, options, source, sender, epoch := setupPush(t)
	defer func() { s.Close() }()
	source.messages = []Notification{notification("assistant-a", 1, "past")}
	sub, _, _ := browserSubscription(t)
	id, err := s.subscribe(context.Background(), Identity{Owner: "browser-a", Valid: func() bool { return true }}, "", sub, "en", false)
	if err != nil {
		t.Fatal(err)
	}
	source.messages = append(source.messages, notification("assistant-a", 2, "original assistant reply 🚀"), notification("assistant-b", 1, "second assistant"))
	if err := s.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 2 {
		t.Fatal(sender.count())
	}
	s.Close()
	s, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 2 {
		t.Fatalf("replayed after restart: %d %v", sender.count(), err)
	}
	s.mu.Lock()
	s.foreground[id] = time.Now().Add(25 * time.Second)
	s.mu.Unlock()
	source.messages = append(source.messages, notification("assistant-a", 3, "visible"))
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 2 {
		t.Fatal(sender.count(), err)
	}
	s.mu.Lock()
	delete(s.foreground, id)
	s.mu.Unlock()
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 2 {
		t.Fatal("foreground message replayed", sender.count(), err)
	}
	source.messages = append(source.messages, notification("assistant-a", 4, "pending while offline"))
	source.offline = true
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 2 {
		t.Fatal("offline fallback", sender.count(), err)
	}
	source.offline = false
	epoch.Store(2)
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 2 {
		t.Fatal("stale credential delivered", sender.count(), err)
	}
	state, err := s.State(context.Background(), "browser-a", id)
	if err != nil || state.Enabled {
		t.Fatal(state, err)
	}
	epoch.Store(1)
	source.control = "control-two"
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 2 {
		t.Fatal("wrong Control delivered", sender.count(), err)
	}
}
func TestOptOutEndpointDedupBackoffAndGone(t *testing.T) {
	s, _, source, sender, _ := setupPush(t)
	defer s.Close()
	sub, _, _ := browserSubscription(t)
	identity := Identity{Owner: "browser", Valid: func() bool { return true }}
	id, err := s.subscribe(context.Background(), identity, "", sub, "zh-CN", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.subscribe(context.Background(), identity, "", sub, "en", false)
	if err != nil || second == id {
		t.Fatal(second, err)
	}
	if _, err := s.load(context.Background(), id); err == nil {
		t.Fatal("duplicate endpoint retained")
	}
	source.messages = []Notification{notification("assistant", 1, "reply")}
	sender.status = 500
	if err := s.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 1 {
		t.Fatal(sender.count())
	}
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 1 {
		t.Fatal("backoff ignored", sender.count(), err)
	}
	r, err := s.load(context.Background(), second)
	if err != nil || r.Failures != 1 || r.Cursors["assistant"] != 0 {
		t.Fatal(r, err)
	}
	r.RetryAfterMS = 0
	if err := s.save(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	sender.status = 410
	if err := s.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.load(context.Background(), second); err == nil {
		t.Fatal("expired endpoint retained")
	}
	id, err = s.subscribe(context.Background(), identity, "", sub, "en", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(context.Background(), "other-browser", id); err == nil {
		t.Fatal("cross-login optout")
	}
	if err := s.Remove(context.Background(), "browser", id); err != nil {
		t.Fatal(err)
	}
}
func TestOnlyFinalRecentAssistantRepliesNotifyAndPayloadIsBounded(t *testing.T) {
	s, _, source, sender, _ := setupPush(t)
	defer s.Close()
	sub, _, _ := browserSubscription(t)
	if _, err := s.subscribe(context.Background(), Identity{Owner: "browser", Valid: func() bool { return true }}, "", sub, "en", false); err != nil {
		t.Fatal(err)
	}
	unfinished := notification("a", 1, "draft")
	unfinished.Final = false
	old := notification("b", 1, "stale")
	old.CreatedAtMS = time.Now().Add(-25 * time.Hour).UnixMilli()
	source.messages = []Notification{unfinished, old, notification("c", 1, "fresh")}
	if err := s.Dispatch(context.Background()); err != nil || sender.count() != 1 {
		t.Fatal(sender.count(), err)
	}
	n := notification("assistant", 1, strings.Repeat("\"\n😀", 2000))
	data := Payload(false, "en", n)
	if len(data) > 3000 {
		t.Fatal(len(data))
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(n.Text, decoded["body"]) {
		t.Fatal("preview changed original text")
	}
	data = Payload(true, "zh-CN", n)
	if bytes.Contains(data, []byte("😀")) || !bytes.Contains(data, []byte("助手发来新消息")) {
		t.Fatal(string(data))
	}
}

type captureTransport struct {
	request *http.Request
	body    []byte
}

func (t *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.request = r
	data, err := io.ReadAll(r.Body)
	t.body = data
	return &http.Response{StatusCode: 201, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, err
}
func TestVAPIDEncryptsOriginalReplyAndTamperFails(t *testing.T) {
	sub, receiver, auth := browserSubscription(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	transport := &captureTransport{}
	sender, err := NewVAPIDSender(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), "mailto:test@example.com", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	data := Payload(false, "en", notification("assistant", 1, "原始助手消息 🚀"))
	if code, err := sender.Send(context.Background(), sub, data); err != nil || code != 201 {
		t.Fatal(code, err)
	}
	if bytes.Contains(transport.body, []byte("原始助手消息")) || transport.request.Header.Get("Content-Encoding") != "aes128gcm" || !strings.Contains(transport.request.Header.Get("Authorization"), "vapid") {
		t.Fatal("unencrypted or unsigned push")
	}
	encrypted := transport.body
	if len(encrypted) < 86 || encrypted[20] != 65 {
		t.Fatal("invalid encrypted record")
	}
	salt, serverKey := encrypted[:16], encrypted[21:86]
	peer, err := ecdh.P256().NewPublicKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := receiver.ECDH(peer)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), receiver.PublicKey().Bytes()...), serverKey...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, nonce, encrypted[86:], nil)
	if err != nil {
		t.Fatal(err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 || !bytes.Equal(plain[:len(plain)-1], data) {
		t.Fatalf("decrypted payload mismatch %q", plain)
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := gcm.Open(nil, nonce, encrypted[86:], nil); err == nil {
		t.Fatal("tampered ciphertext authenticated")
	}
}
func TestPublicPushHTTPRequiresAuthenticationAndExplicitConsent(t *testing.T) {
	s, _, _, _, _ := setupPush(t)
	defer s.Close()
	sub, _, _ := browserSubscription(t)
	data, _ := json.Marshal(map[string]any{"subscription": sub, "locale": "en", "private_preview": true})
	request := httptest.NewRequest("POST", "https://pot.example/access/push", bytes.NewReader(data))
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal(response.Code)
	}
	request = httptest.NewRequest("POST", "https://pot.example/access/push", bytes.NewReader(data))
	request.Header.Set("X-Test-Owner", "browser")
	response = httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Name != installationCookie {
		t.Fatal(cookies)
	}
	for _, endpoint := range []string{"http://fcm.googleapis.com/x", "https://127.0.0.1/x", "https://fcm.googleapis.com.evil/x", "https://user@fcm.googleapis.com/x", "https://web.push.apple.com:444/x"} {
		bad := sub
		bad.Endpoint = endpoint
		if Validate(bad) == nil {
			t.Fatal(endpoint)
		}
	}
}
