package mcp

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Bearer authentication remains an SDK protocol boundary. Credential storage,
// consent, audience, revocation and business authority belong to the caller.
type TokenInfo = auth.TokenInfo
type TokenVerifier = auth.TokenVerifier
type RequireBearerTokenOptions = auth.RequireBearerTokenOptions

var ErrInvalidToken = auth.ErrInvalidToken

func RequireBearerToken(verifier TokenVerifier, options *RequireBearerTokenOptions) func(http.Handler) http.Handler {
	return auth.RequireBearerToken(verifier, options)
}
func TokenInfoFromContext(ctx context.Context) *TokenInfo { return auth.TokenInfoFromContext(ctx) }
