// Package integrations manages service login and credentials through injected
// provider, metadata and secret ports. It has no dependency on the product host.
package integrations

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("integration not found")
var ErrChanged = errors.New("integration changed; refresh before continuing")

type Identity struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
}
type Account struct {
	Service   string     `json:"service"`
	Identity  Identity   `json:"identity"`
	Revision  uint64     `json:"revision"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	SecretRef string     `json:"-"`
}

// AccountStore implements single-account-per-service persistence and revision checks.
// expected=0 means insert only. Delete must retain a revision tombstone.
type AccountStore interface {
	List(ctx context.Context) ([]Account, error)
	Revision(ctx context.Context, service string) (uint64, error)
	Save(ctx context.Context, account Account, expectedRevision uint64) (Account, error)
	Delete(ctx context.Context, service string, expectedRevision uint64) error
}
type SecretStore interface {
	Put(ctx context.Context, reference string, value []byte) error
	Get(ctx context.Context, reference string) ([]byte, error)
	Delete(ctx context.Context, reference string) error
}
type Credential struct {
	AccessToken      string     `json:"access_token"`
	RefreshToken     string     `json:"refresh_token,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at,omitempty"`
}
type Grant struct {
	Identity   Identity
	Credential Credential
}
type Descriptor struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	LoginAvailable bool   `json:"login_available"`
}

// Challenge contains public browser instructions and provider-private state.
// Private state never crosses the management API.
type Challenge struct {
	URL       string
	UserCode  string
	ExpiresAt time.Time
	Private   any
}

// Adapter owns a service's authorization protocol. Authorize waits for approval
// or a terminal result; both authorization methods must honor cancellation.
type Adapter interface {
	Descriptor() Descriptor
	Begin(ctx context.Context) (Challenge, error)
	Authorize(ctx context.Context, challenge Challenge) (Grant, error)
}

// Refresher is optional. Services without refresh support require a new login
// after their access credential expires.
type Refresher interface {
	Refresh(ctx context.Context, previous Credential) (Credential, error)
}
type ConnectionStatus string

const (
	Disconnected ConnectionStatus = "disconnected"
	Connected    ConnectionStatus = "connected"
	Expired      ConnectionStatus = "expired"
)

type LoginState string

const (
	LoginPending   LoginState = "pending"
	LoginSucceeded LoginState = "succeeded"
	LoginFailed    LoginState = "failed"
	LoginExpired   LoginState = "expired"
	LoginCancelled LoginState = "cancelled"
)

type Integration struct {
	Descriptor
	Status  ConnectionStatus `json:"status"`
	Account *Account         `json:"account,omitempty"`
	Login   *Login           `json:"login,omitempty"`
}
type Login struct {
	ID               string     `json:"id"`
	Service          string     `json:"service"`
	Status           LoginState `json:"status"`
	AuthorizationURL string     `json:"authorization_url,omitempty"`
	UserCode         string     `json:"user_code,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	Error            string     `json:"error,omitempty"`
}

// CredentialResolver is the only port execution callers need. scope is an opaque
// caller-owned cache key, with no Task, Assistant or Control semantics here.
type CredentialResolver interface {
	Credential(ctx context.Context, scope, service string, refresh bool) (string, error)
	Forget(scope string)
}
