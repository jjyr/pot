package mcpreceiver

import "time"

// OAuthToken is the existing encrypted grant JSON v1 used by both receiver
// adapters. It is private credential material, never a model tool result.
type OAuthToken struct {
	SchemaVersion int       `json:"schema_version"`
	AccessToken   string    `json:"access_token"`
	TokenType     string    `json:"token_type"`
	ExpiresAt     time.Time `json:"expires_at"`
	ClientID      string    `json:"client_id"`
}
