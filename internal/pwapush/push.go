// Package pwapush implements only standards-based subscription validation and
// encrypted transport. Event policy, foreground leases and retries belong to
// systemnotifications; this package never observes business resources.
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
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var encoding = base64.RawURLEncoding

type Subscription struct {
	Endpoint       string `json:"endpoint"`
	ExpirationTime *int64 `json:"expirationTime,omitempty"`
	Keys           struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// Restrict registration to actual browser push providers, then pin public DNS
// addresses at the connection boundary. No arbitrary host, private IP or redirect
// can turn an authenticated subscription into an internal HTTP client.
func Validate(s Subscription) error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" || len(s.Endpoint) > 4096 || u.Path == "" || u.Path == "/" {
		return errors.New("invalid push endpoint")
	}
	host := strings.ToLower(u.Hostname())
	allowed := host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || host == "web.push.apple.com" || strings.HasSuffix(host, ".push.apple.com") || host == "wns.windows.com" || strings.HasSuffix(host, ".notify.windows.com")
	if !allowed {
		return errors.New("unsupported push endpoint provider")
	}
	public, err := encoding.DecodeString(s.Keys.P256DH)
	if err != nil || len(public) != 65 {
		return errors.New("invalid push public key")
	}
	if _, err = ecdh.P256().NewPublicKey(public); err != nil {
		return errors.New("invalid push public key")
	}
	auth, err := encoding.DecodeString(s.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return errors.New("invalid push auth key")
	}
	return nil
}
func GenerateKey() ([]byte, error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return key.Bytes(), nil
}

type Sender struct {
	key       *ecdsa.PrivateKey
	PublicKey string
	Subject   string
	client    *http.Client
}

func New(private []byte, subject string) (*Sender, error) {
	ecdhKey, err := ecdh.P256().NewPrivateKey(private)
	if err != nil {
		return nil, err
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), ecdhKey.PublicKey().Bytes())
	key := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, D: new(big.Int).SetBytes(private)}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range addresses {
			if !publicAddress(ip) {
				return nil, errors.New("push endpoint resolved to a non-public address")
			}
		}
		var last error
		for _, ip := range addresses {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}}
	return &Sender{key: key, PublicKey: encoding.EncodeToString(ecdhKey.PublicKey().Bytes()), Subject: subject, client: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func derive(secret, salt, info []byte, n int) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, salt, string(info), n)
}

// Encrypt implements RFC 8291 / RFC 8188's single aes128gcm record (max 3 KiB).
func Encrypt(s Subscription, payload []byte) ([]byte, error) {
	if len(payload) > 3072 {
		return nil, errors.New("push payload too large")
	}
	public, err := encoding.DecodeString(s.Keys.P256DH)
	if err != nil {
		return nil, err
	}
	peer, err := ecdh.P256().NewPublicKey(public)
	if err != nil {
		return nil, err
	}
	auth, err := encoding.DecodeString(s.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return nil, errors.New("invalid push auth key")
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := ephemeral.ECDH(peer)
	if err != nil {
		return nil, err
	}
	info := append([]byte("WebPush: info\x00"), public...)
	info = append(info, ephemeral.PublicKey().Bytes()...)
	ikm, err := derive(shared, auth, info, 32)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := derive(ikm, salt, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, err
	}
	nonce, err := derive(ikm, salt, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	body := append([]byte{}, salt...)
	body = binary.BigEndian.AppendUint32(body, 4096)
	body = append(body, 65)
	body = append(body, ephemeral.PublicKey().Bytes()...)
	plain := append(append([]byte{}, payload...), 2)
	return aead.Seal(body, nonce, plain, nil), nil
}

type Result struct {
	Status     int
	Unknown    bool
	RetryAfter time.Duration
	Invalid    bool
}

func (s *Sender) Send(ctx context.Context, subscription Subscription, payload []byte) Result {
	if Validate(subscription) != nil {
		return Result{Invalid: true}
	}
	return s.send(ctx, subscription, payload)
}
func (s *Sender) send(ctx context.Context, subscription Subscription, payload []byte) Result {
	body, err := Encrypt(subscription, payload)
	if err != nil {
		return Result{}
	}
	endpoint, _ := url.Parse(subscription.Endpoint)
	claims, _ := json.Marshal(map[string]any{"aud": endpoint.Scheme + "://" + endpoint.Host, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": s.Subject})
	token := encoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + encoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(token))
	r, ss, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		return Result{}
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	ss.FillBytes(signature[32:])
	token += "." + encoding.EncodeToString(signature)
	request, err := http.NewRequestWithContext(ctx, "POST", subscription.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}
	}
	request.Header.Set("Authorization", "vapid t="+token+", k="+s.PublicKey)
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", "300")
	request.Header.Set("Urgency", "normal")
	response, err := s.client.Do(request)
	if err != nil {
		return Result{Unknown: true}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	result := Result{Status: response.StatusCode, Invalid: response.StatusCode == 404 || response.StatusCode == 410}
	if response.StatusCode == 429 || response.StatusCode >= 500 {
		result.RetryAfter = 5 * time.Second
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			result.RetryAfter = min(time.Duration(seconds)*time.Second, time.Minute)
		} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			result.RetryAfter = max(time.Second, min(time.Until(at), time.Minute))
		}
	}
	return result
}
func (r Result) String() string {
	return fmt.Sprintf("push HTTP status %d (unknown=%t)", r.Status, r.Unknown)
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.Is4In6() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}
