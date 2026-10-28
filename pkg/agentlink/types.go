// Package agentlink connects autonomous nodes through a shared collaboration port.
package agentlink

import (
	"database/sql"
	"errors"

	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

var (
	ErrAssistantBusy    = errors.New("assistant_busy")
	ErrAssistantChanged = errors.New("assistant_changed")
	ErrInvalid          = errors.New("agent_link_invalid")
	ErrNotFound         = errors.New("agent_link_not_found")
	ErrUnauthorized     = errors.New("agent_link_authentication_failed")
	ErrConflict         = errors.New("agent_link_identity_conflict")
	ErrRevoked          = errors.New("agent_link_revoked")
	ErrUnavailable      = errors.New("agent_link_unreachable")
)

type Identity struct {
	Address protocol.AgentRef `json:"address"`
	Name    string            `json:"name"`
	Avatar  string            `json:"avatar,omitempty"`
	Adapter string            `json:"adapter"`
}

// HistoricalConnection is the post-cutover display record, never an authorization.
type HistoricalConnection struct {
	SchemaVersion  int        `json:"schema_version"`
	ID             string     `json:"id"`
	Local          Identity   `json:"local"`
	Peer           Identity   `json:"peer"`
	Endpoint       string     `json:"endpoint"`
	State          string     `json:"state"`
	LastKnownState string     `json:"last_known_state"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastSeen       *time.Time `json:"last_seen"`
	Directory      *Directory `json:"directory"`
}

// History exposes saved metadata only. It cannot authenticate or execute an old connection.
type History struct{ DB *sql.DB }
