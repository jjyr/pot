// Package peer provides authenticated, framed TCP sessions for Pot services.
// It has no knowledge of assistants, machine membership, or execution policy.
// TLS 1.3 encrypts traffic and proves possession of pinned Ed25519 identities.
package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxPayload = 24 * 1024 * 1024
const MaxFrame = 32 * 1024 * 1024
const maxHandshake = 64 * 1024
const maxPending = 64

var ErrUnauthorized = errors.New("peer authentication rejected")
var ErrClosed = errors.New("peer session closed")

// ErrUncertain means a request may have reached its handler. The business
// service must reconcile its original durable receipt instead of replaying it.
var ErrUncertain = errors.New("peer request outcome uncertain")

type Identity struct {
	NodeID    string `json:"node_id"`
	PublicKey string `json:"public_key"`
}

func NewIdentity(id string, key ed25519.PublicKey) Identity {
	return Identity{NodeID: id, PublicKey: base64.RawURLEncoding.EncodeToString(key)}
}
func (i Identity) Validate() error {
	key, err := base64.RawURLEncoding.DecodeString(i.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || len(i.NodeID) == 0 || len(i.NodeID) > 256 || !utf8.ValidString(i.NodeID) || strings.IndexFunc(i.NodeID, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return ErrUnauthorized
	}
	return nil
}
func validLocal(i Identity, key ed25519.PrivateKey) error {
	if i.Validate() != nil || len(key) != ed25519.PrivateKeySize || NewIdentity(i.NodeID, key.Public().(ed25519.PublicKey)) != i {
		return ErrUnauthorized
	}
	// An Ed25519 private key includes a cached public suffix. Checking only
	// Public() would accept another node's pin pasted onto an unrelated seed.
	if !bytes.Equal(key, ed25519.NewKeyFromSeed(key.Seed())) {
		return ErrUnauthorized
	}
	return nil
}

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type Handler func(context.Context, Request) (json.RawMessage, error)
type Authorizer func(context.Context, Identity, string, json.RawMessage) (Handler, error)

// RemoteError carries a bounded service error. Data belongs to that service;
// peer never interprets business fault types or permission decisions.
type RemoteError struct {
	Message string          `json:"message"`
	Code    string          `json:"code,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RemoteError) Error() string { return e.Message }

type DialOptions struct {
	Identity   Identity
	PrivateKey ed25519.PrivateKey
	Expected   *Identity
	Service    string
	Auth       json.RawMessage
	Handler    Handler
	// Context controls the established session lifetime. Dial's ctx only
	// controls connection establishment and the handshake.
	Context context.Context
}

func validRequest(r Request) error {
	if len(r.ID) < 1 || len(r.ID) > 256 || strings.ContainsRune(r.ID, 0) || len(r.Method) < 1 || len(r.Method) > 128 || len(r.Params) > MaxPayload || !json.Valid(r.Params) {
		return fmt.Errorf("invalid peer request")
	}
	return nil
}
