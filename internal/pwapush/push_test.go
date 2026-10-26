package pwapush

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
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func subscription(t *testing.T) (Subscription, *ecdh.PrivateKey) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := Subscription{Endpoint: "https://web.push.apple.com/fixture"}
	s.Keys.P256DH = encoding.EncodeToString(key.PublicKey().Bytes())
	s.Keys.Auth = encoding.EncodeToString(bytes.Repeat([]byte{7}, 16))
	return s, key
}
func decrypt(t *testing.T, body []byte, s Subscription, private *ecdh.PrivateKey) []byte {
	t.Helper()
	if len(body) < 102 || binary.BigEndian.Uint32(body[16:20]) != 4096 || body[20] != 65 {
		t.Fatal("invalid RFC8188 record header")
	}
	sender, err := ecdh.P256().NewPublicKey(body[21:86])
	if err != nil {
		t.Fatal(err)
	}
	shared, err := private.ECDH(sender)
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := encoding.DecodeString(s.Keys.Auth)
	info := append(append([]byte("WebPush: info\x00"), private.PublicKey().Bytes()...), sender.Bytes()...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := hkdf.Key(sha256.New, ikm, body[:16], "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, body[:16], "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	plain, err := aead.Open(nil, nonce, body[86:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatal("missing final-record delimiter")
	}
	return plain[:len(plain)-1]
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestEncryptedHTTPAndVAPIDAgainstIsolatedPushService(t *testing.T) {
	subscription, recipient := subscription(t)
	private, _ := GenerateKey()
	sender, err := New(private, "https://pot.tailnet.test")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"version":1,"event":{"owner":"alpha","task":"build"},"body":"完成"}`)
	var calls int
	status := 201
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "300" || r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Error("incorrect Web Push request")
		}
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("alpha")) || !bytes.Equal(decrypt(t, body, subscription, recipient), payload) {
			t.Error("payload was not correctly encrypted")
		}
		authorization := r.Header.Get("Authorization")
		prefix := "vapid t="
		if !strings.HasPrefix(authorization, prefix) {
			t.Error("missing VAPID")
		}
		token, key, _ := strings.Cut(strings.TrimPrefix(authorization, prefix), ", k=")
		if key != sender.PublicKey {
			t.Error("incorrect application server key")
		}
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatal("invalid JWT")
		}
		claims, _ := encoding.DecodeString(parts[1])
		var values map[string]any
		json.Unmarshal(claims, &values)
		if values["aud"] != "https://web.push.apple.com" || values["sub"] != "https://pot.tailnet.test" || int64(values["exp"].(float64)) < time.Now().Add(time.Hour).Unix() {
			t.Error(values)
		}
		public, _ := encoding.DecodeString(key)
		x, y := elliptic.Unmarshal(elliptic.P256(), public)
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		signature, _ := encoding.DecodeString(parts[2])
		if len(signature) != 64 || !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
			t.Error("invalid ES256 VAPID signature")
		}
		if status == 429 {
			w.Header().Set("Retry-After", "86400")
		}
		if status == 302 {
			w.Header().Set("Location", "http://127.0.0.1/private")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	// Inject the isolated TLS service without changing production endpoint policy.
	client := server.Client()
	transport := client.Transport
	client.CheckRedirect = sender.client.CheckRedirect
	client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		target, _ := http.NewRequest("POST", server.URL, nil)
		copy.URL = target.URL
		copy.Host = target.Host
		return transport.RoundTrip(copy)
	})
	sender.client = client
	for _, want := range []int{201, 404, 410, 429, 503, 302} {
		status = want
		result := sender.Send(context.Background(), subscription, payload)
		if result.Status != want || result.Invalid != (want == 404 || want == 410) || result.Unknown {
			t.Fatal(want, result)
		}
		if want == 429 && result.RetryAfter != time.Minute {
			t.Fatal("unbounded retry", result)
		}
	}
	if calls != 6 {
		t.Fatal("redirect was followed", calls)
	}
	sender.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })}
	if result := sender.Send(context.Background(), subscription, payload); !result.Unknown || result.RetryAfter != 0 {
		t.Fatal("uncertain send was retried", result)
	}
}
func TestSubscriptionValidationRejectsInternalTargetsAndInvalidKeys(t *testing.T) {
	s, _ := subscription(t)
	for _, endpoint := range []string{"http://web.push.apple.com/path", "https://127.0.0.1/a", "https://100.64.0.1/a", "https://web.push.apple.com.attacker.test/a", "https://attacker.push.apple.com.evil/a", "https://web.push.apple.com:443/a", "https://user@web.push.apple.com/a", "https://web.push.apple.com/a#fragment", "https://example.org/a"} {
		s.Endpoint = endpoint
		if Validate(s) == nil {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
	s, _ = subscription(t)
	s.Keys.P256DH = encoding.EncodeToString(make([]byte, 65))
	if Validate(s) == nil {
		t.Fatal("invalid P256 point accepted")
	}
	s, _ = subscription(t)
	s.Keys.Auth = "short"
	if Validate(s) == nil {
		t.Fatal("invalid auth key accepted")
	}
	for _, endpoint := range []string{"https://fcm.googleapis.com/fcm/send/synthetic", "https://updates.push.services.mozilla.com/wpush/v2/synthetic", "https://web.push.apple.com/synthetic"} {
		s, _ = subscription(t)
		s.Endpoint = endpoint
		if err := Validate(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDNSBoundaryRejectsTailnetAndSpecialNetworks(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "100.70.1.2", "10.0.0.1", "169.254.169.254", "192.0.2.1", "::1", "fd7a:115c:a1e0::1", "::ffff:127.0.0.1"} {
		if publicAddress(netip.MustParseAddr(value)) {
			t.Fatal(value)
		}
	}
	if !publicAddress(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}
