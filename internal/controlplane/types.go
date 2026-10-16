// Package controlplane owns durable machine membership and authenticated outward
// connections. It does not start listeners, resolve provider credentials or run Agents.
package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"net/url"
	"strings"
)

var ErrUnauthorized = errors.New("Control authentication rejected")
var ErrDisconnected = errors.New("machine disconnected; inspect original request before recovery")
var ErrUncertain = errors.New("request delivery/result uncertain; inspect original request")

type Identity struct {
	RuntimeID   string `json:"runtime_id"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

func identity(runtime string, key ed25519.PublicKey) Identity {
	sum := sha256.Sum256(key)
	return Identity{runtime, base64.RawURLEncoding.EncodeToString(key), hex.EncodeToString(sum[:])}
}
func (i Identity) Validate() error {
	key, err := base64.RawURLEncoding.DecodeString(i.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || !core.Identifier(i.RuntimeID) || identity(i.RuntimeID, key) != i {
		return fmt.Errorf("invalid Control identity")
	}
	return nil
}

type Signed struct {
	Payload   json.RawMessage `json:"payload" required:"true"`
	Signature string          `json:"signature" required:"true"`
}

func sign(key ed25519.PrivateKey, value any) (Signed, error) {
	data, err := core.CanonicalJSON(value)
	if err != nil {
		return Signed{}, err
	}
	return Signed{data, base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, data))}, nil
}
func verify(i Identity, s Signed, value any) error {
	if err := i.Validate(); err != nil {
		return err
	}
	if len(s.Payload) > MaxMessage {
		return fmt.Errorf("signed payload too large")
	}
	if err := core.DecodeJSON(s.Payload, value); err != nil {
		return err
	}
	canonical, err := core.CanonicalJSON(value)
	if err != nil {
		return err
	}
	key, _ := base64.RawURLEncoding.DecodeString(i.PublicKey)
	sig, err := base64.RawURLEncoding.DecodeString(s.Signature)
	if err != nil || !ed25519.Verify(key, canonical, sig) {
		return ErrUnauthorized
	}
	return nil
}
func secret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func secureToken(a, b string) bool {
	return len(a) == 43 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func ValidOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("Control URL must be an HTTP(S) origin")
	}
	return nil
}
func labels(input []string) ([]string, error) {
	if input == nil {
		input = []string{}
	}
	if len(input) > 64 {
		return nil, fmt.Errorf("too many machine labels")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, s := range input {
		if len(s) < 1 || len(s) > 64 {
			return nil, fmt.Errorf("invalid machine label")
		}
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return nil, fmt.Errorf("invalid machine label")
			}
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

type Machine struct {
	Identity Identity `json:"identity"`
	Epoch    uint64   `json:"epoch"`
	Labels   []string `json:"labels"`
	Enabled  bool     `json:"enabled"`
	Online   bool     `json:"online"`
}
type Invitation struct {
	Code        string   `json:"invitation"`
	ExpiresAtMS int64    `json:"expires_at_ms"`
	Identity    Identity `json:"identity"`
}
type JoinRequest struct {
	Version    uint32   `json:"version" required:"true"`
	RequestID  string   `json:"request_id" required:"true"`
	ControlID  string   `json:"control_id" required:"true"`
	Caller     Identity `json:"caller" required:"true"`
	Invitation string   `json:"invitation" required:"true"`
	OS         string   `json:"os" required:"true"`
}
type Enrollment struct {
	Version   uint32   `json:"version"`
	RequestID string   `json:"request_id"`
	Control   Identity `json:"control"`
	RuntimeID string   `json:"runtime_id"`
	Epoch     uint64   `json:"epoch"`
	Token     string   `json:"token"`
}
type Membership struct {
	Origin     string     `json:"origin"`
	Enrollment Enrollment `json:"enrollment"`
}
type Principal struct {
	RuntimeID string
	Epoch     uint64
	Owner     bool
}
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type Handler func(context.Context, Principal, Request) (json.RawMessage, error)

func validRequest(r Request) error {
	if len(r.ID) < 1 || len(r.ID) > 256 || strings.ContainsRune(r.ID, 0) || len(r.Method) < 1 || len(r.Method) > 128 || len(r.Params) > MaxMessage/2 || !json.Valid(r.Params) {
		return fmt.Errorf("invalid RPC request")
	}
	return nil
}
