// Package agentlink connects explicitly bound assistant identities over a narrow
// message-only transport. It grants neither execution nor owner administration.
package agentlink

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

const Prefix = "/api/v1/agent-link"

var (
	ErrInvalid      = errors.New("agent_link_invalid")
	ErrNotFound     = errors.New("agent_link_not_found")
	ErrUnauthorized = errors.New("agent_link_authentication_failed")
	ErrExpired      = errors.New("agent_link_invitation_expired")
	ErrConflict     = errors.New("agent_link_identity_conflict")
	ErrRevoked      = errors.New("agent_link_revoked")
	ErrUnavailable  = errors.New("agent_link_unreachable")
)

type Identity struct {
	Address protocol.AgentRef `json:"address"`
	Name    string            `json:"name"`
	Adapter string            `json:"adapter"`
}

type Connection struct {
	ID                string     `json:"id"`
	Local             Identity   `json:"local"`
	Peer              Identity   `json:"peer"`
	Endpoint          string     `json:"endpoint,omitempty"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastSeen          *time.Time `json:"last_seen,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	RevocationPending bool       `json:"revocation_pending,omitempty"`
}

// Invitation.Address contains a single-use capability. Expose it only to the
// authenticated local user, never a model, logs or public connection metadata.
type Invitation struct {
	Address   string    `json:"address"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Mailbox owns durable business messages. Deliver commits before returning its
// immutable receipt. That receipt means inbox acceptance, not model processing.
// Pending MUST filter the exact two addresses before applying its bounded limit.
type Mailbox interface {
	Pending(context.Context, protocol.AgentRef, protocol.AgentRef) ([]protocol.Envelope, error)
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
	DB       *sql.DB
	Secrets  SecretStore
	Identity func(context.Context, string) (Identity, error)
	Mailbox  Mailbox
	// HTTPClient customizes HTTPS (for example CA roots). HTTP always uses the
	// private-network, proxy-free transport enforced by NewHTTPClient.
	HTTPClient *http.Client
	Changed    func()
	// PollInterval is bounded below by 100 ms; default is two seconds.
	PollInterval time.Duration
	// InvitationTTL defaults to one hour and cannot exceed 24 hours.
	InvitationTTL time.Duration
}
