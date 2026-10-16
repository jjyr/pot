package browserpush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type VAPIDSender struct {
	public, private, contact string
	client                   *http.Client
}

func FromEnvironment() (*VAPIDSender, error) {
	path := os.Getenv("POT_ACCESS_VAPID_KEY")
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return nil, errors.New("VAPID key must be a private bounded current-user file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, errors.New("VAPID key too large")
	}
	return NewVAPIDSender(data, os.Getenv("POT_ACCESS_VAPID_CONTACT"), nil)
}
func NewVAPIDSender(data []byte, contact string, client *http.Client) (*VAPIDSender, error) {
	parsed, err := url.Parse(contact)
	if err != nil || (parsed.Scheme != "mailto" && parsed.Scheme != "https") || parsed.User != nil || strings.ContainsAny(contact, "\r\n") || (parsed.Scheme == "https" && parsed.Host == "") || (parsed.Scheme == "mailto" && parsed.Opaque == "") {
		return nil, errors.New("VAPID contact must be mailto or HTTPS")
	}
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid VAPID PEM key")
	}
	var key *ecdsa.PrivateKey
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var value any
		value, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			key, _ = value.(*ecdsa.PrivateKey)
		}
	default:
		err = errors.New("unsupported VAPID PEM key")
	}
	if err != nil || key == nil || key.Curve != elliptic.P256() {
		return nil, errors.New("VAPID key must use P-256")
	}
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), Timeout: 15 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copyClient.Timeout = 15 * time.Second
	return &VAPIDSender{public: base64.RawURLEncoding.EncodeToString(elliptic.Marshal(key.Curve, key.X, key.Y)), private: base64.RawURLEncoding.EncodeToString(key.D.FillBytes(make([]byte, 32))), contact: contact, client: &copyClient}, nil
}
func (s *VAPIDSender) PublicKey() string { return s.public }
func (s *VAPIDSender) Send(ctx context.Context, subscription Subscription, payload []byte) (int, error) {
	if err := Validate(subscription); err != nil {
		return 0, err
	}
	if len(payload) > 3000 {
		return 0, errors.New("push payload exceeds bound")
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: subscription.Endpoint, Keys: webpush.Keys{P256dh: subscription.Keys.P256dh, Auth: subscription.Keys.Auth}}, &webpush.Options{HTTPClient: s.client, Subscriber: s.contact, VAPIDPublicKey: s.public, VAPIDPrivateKey: s.private, TTL: 86400, Urgency: webpush.UrgencyNormal})
	if err != nil {
		return 0, errors.New("encrypted push exchange unavailable")
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode, nil
}
