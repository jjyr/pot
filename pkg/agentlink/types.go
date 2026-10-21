// Package agentlink connects autonomous nodes through a shared collaboration port.
package agentlink

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/peer"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

const ServiceName = "agent-link"

var (
	ErrInvalid           = errors.New("agent_link_invalid")
	ErrNotFound          = errors.New("agent_link_not_found")
	ErrUnauthorized      = errors.New("agent_link_authentication_failed")
	ErrExpired           = errors.New("agent_link_invitation_expired")
	ErrUsed              = errors.New("agent_link_invitation_used")
	ErrConflict          = errors.New("agent_link_identity_conflict")
	ErrRevoked           = errors.New("agent_link_revoked")
	ErrReconnectRequired = errors.New("agent_link_reconnect_required")
	ErrUnavailable       = errors.New("agent_link_unreachable")
)

type Identity struct {
	Address protocol.AgentRef `json:"address"`
	Name    string            `json:"name"`
	Avatar  string            `json:"avatar,omitempty"`
	Adapter string            `json:"adapter"`
}

type Connection struct {
	SchemaVersion     int           `json:"schema_version"`
	PeerHost          peer.Identity `json:"peer_host"`
	ID                string        `json:"id"`
	Local             Identity      `json:"local"`
	Peer              Identity      `json:"peer"`
	Endpoint          string        `json:"endpoint,omitempty"`
	State             string        `json:"state"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	LastSeen          *time.Time    `json:"last_seen,omitempty"`
	LastError         string        `json:"last_error,omitempty"`
	Directory         *Directory    `json:"directory,omitempty"`
	RevocationPending bool          `json:"revocation_pending,omitempty"`
}

// Invitation.Address contains a single-use capability. Expose it only to the
// authenticated local user, never a model, logs or public connection metadata.
type Invitation struct {
	Address   string    `json:"address"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Preview is a live, read-only verification result. It contains no invitation
// material and grants no communication or execution authority.
type Preview struct {
	Identity    Identity      `json:"identity"`
	Host        peer.Identity `json:"host"`
	Endpoint    string        `json:"endpoint"`
	Fingerprint string        `json:"fingerprint"`
	ExpiresAt   time.Time     `json:"expires_at"`
}

// Mailbox owns durable business messages. Deliver commits before returning its
// immutable receipt. That receipt means inbox acceptance, not model processing.
// Pending MUST filter the exact two addresses before applying its bounded limit.
type Mailbox interface {
	PendingNode(context.Context, string, string) ([]protocol.Envelope, error)
	Deliver(context.Context, protocol.Envelope) (protocol.Receipt, error)
	Acknowledge(context.Context, protocol.Receipt) error
}

type SecretStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

type Options struct {
	// DB is a caller-owned, migrated storage.ControlDatabase. Service never closes it.
	Backend Backend
	DB      *sql.DB
	Secrets SecretStore
	// Identity resolves the node for an empty selector, and assistants for explicit IDs.
	Identity     func(context.Context, string) (Identity, error)
	Mailbox      Mailbox
	HostIdentity peer.Identity
	HostKey      ed25519.PrivateKey
	Changed      func()
	// PollInterval is bounded below by 100 ms; default is two seconds.
	PollInterval time.Duration
	// InvitationTTL defaults to one hour and cannot exceed 24 hours.
	InvitationTTL time.Duration
}
