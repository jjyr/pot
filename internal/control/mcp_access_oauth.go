package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const mcpAccessScope = "pot:collaborate"
const mcpAccessLifetime = 12 * time.Hour

// MCPOAuthOptions binds consent to the existing owner browser session. Session
// returns an opaque, stable ID only after verifying the current owner login.
// No Runtime/owner bearer is accepted by this authorization server.
type MCPOAuthOptions struct {
	DB           *sql.DB
	Origin       string
	NodeID       string
	OwnerSession func(*http.Request) (string, bool)
	// LoginURL is an existing browser login entry point. The return destination
	// is always a validated, same-origin authorization request.
	LoginURL string
}
type MCPOAuthServer struct {
	options  MCPOAuthOptions
	origin   string
	resource string
}
type mcpOAuthClient struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	// A receiver declaration is untrusted until explicitly displayed and approved
	// by the owner. It never supplies an execution actor or owner authority.
	ReceiverNode string `json:"pot_receiver_node,omitempty"`
}

func NewMCPOAuthServer(o MCPOAuthOptions) (*MCPOAuthServer, error) {
	u, e := url.Parse(o.Origin)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || o.DB == nil || !core.Identifier(o.NodeID) || o.OwnerSession == nil {
		return nil, agentlink.ErrInvalid
	}
	if o.LoginURL != "" {
		login, e := url.Parse(o.LoginURL)
		if e != nil || login.IsAbs() || !strings.HasPrefix(login.Path, "/") || strings.HasPrefix(login.Path, "//") {
			return nil, agentlink.ErrInvalid
		}
	}
	return &MCPOAuthServer{options: o, origin: u.String(), resource: u.String() + "/mcp"}, nil
}
func mcpCredentialDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func mcpOAuthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func mcpOAuthError(w http.ResponseWriter, status int, code string) {
	mcpOAuthJSON(w, status, map[string]string{"error": code})
}
func mcpRedirectValid(value string) bool {
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}

// RFC 8252 permits a native client's ephemeral port to change only for an
// IP-literal loopback HTTP redirect. Everything else remains an exact match;
// the authorization code stores the exact requested URI for token exchange.
func mcpOAuthRedirectMatches(registered []string, requested string) bool {
	if slices.Contains(registered, requested) {
		return true
	}
	candidate, e := url.Parse(requested)
	if e != nil || candidate.Scheme != "http" || candidate.User != nil || candidate.Fragment != "" {
		return false
	}
	ip := net.ParseIP(candidate.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	for _, raw := range registered {
		fixed, e := url.Parse(raw)
		if e != nil || fixed.Scheme != "http" || fixed.Hostname() != candidate.Hostname() {
			continue
		}
		a, b := *fixed, *candidate
		a.Host = net.JoinHostPort(a.Hostname(), "0")
		b.Host = net.JoinHostPort(b.Hostname(), "0")
		if a.String() == b.String() {
			return true
		}
	}
	return false
}

func (s *MCPOAuthServer) client(ctx context.Context, id string) (client mcpOAuthClient, err error) {
	var raw string
	err = s.options.DB.QueryRowContext(ctx, "SELECT metadata FROM mcp_oauth_clients WHERE id=?", id).Scan(&raw)
	if err == nil {
		err = mcpRead(raw, &client)
	}
	return
}
func (s *MCPOAuthServer) sameOrigin(r *http.Request) bool {
	return r.Host == strings.TrimPrefix(strings.TrimPrefix(s.origin, "https://"), "http://") && (r.Header.Get("Origin") == "" || r.Header.Get("Origin") == s.origin) && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func (s *MCPOAuthServer) owner(r *http.Request) (string, bool) {
	if !s.sameOrigin(r) {
		return "", false
	}
	id, ok := s.options.OwnerSession(r)
	return id, ok && id != ""
}
func (s *MCPOAuthServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		if r.Method != "GET" {
			mcpOAuthError(w, 405, "invalid_request")
			return
		}
		mcpOAuthJSON(w, 200, map[string]any{"resource": s.resource, "authorization_servers": []string{s.origin}, "scopes_supported": []string{mcpAccessScope}, "bearer_methods_supported": []string{"header"}})
	case "/.well-known/oauth-authorization-server":
		if r.Method != "GET" {
			mcpOAuthError(w, 405, "invalid_request")
			return
		}
		mcpOAuthJSON(w, 200, map[string]any{"issuer": s.origin, "authorization_endpoint": s.origin + "/oauth/authorize", "token_endpoint": s.origin + "/oauth/token", "registration_endpoint": s.origin + "/oauth/register", "revocation_endpoint": s.origin + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code"}, "token_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{mcpAccessScope}})
	case "/oauth/register":
		s.register(w, r)
	case "/oauth/authorize":
		s.authorize(w, r)
	case "/oauth/consent":
		s.consent(w, r)
	case "/oauth/token":
		s.token(w, r)
	case "/oauth/revoke":
		s.revokeToken(w, r)
	case "/oauth/connections":
		s.connections(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (s *MCPOAuthServer) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		mcpOAuthError(w, 405, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	var client mcpOAuthClient
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&client) != nil || decoder.Decode(new(any)) != io.EOF {
		mcpOAuthError(w, 400, "invalid_client_metadata")
		return
	}
	// Standard public-client DCR only; no client secrets, implicit or password flow.
	if len(client.RedirectURIs) == 0 || len(client.RedirectURIs) > 8 || len(client.ClientName) > 200 || (client.TokenEndpointAuthMethod != "" && client.TokenEndpointAuthMethod != "none") || (len(client.GrantTypes) > 0 && !slices.Equal(client.GrantTypes, []string{"authorization_code"})) || (len(client.ResponseTypes) > 0 && !slices.Equal(client.ResponseTypes, []string{"code"})) || (client.ReceiverNode != "" && !core.Identifier(client.ReceiverNode)) {
		mcpOAuthError(w, 400, "invalid_client_metadata")
		return
	}
	for _, uri := range client.RedirectURIs {
		if len(uri) > 2048 || !mcpRedirectValid(uri) {
			mcpOAuthError(w, 400, "invalid_redirect_uri")
			return
		}
	}
	client.TokenEndpointAuthMethod = "none"
	client.GrantTypes = []string{"authorization_code"}
	client.ResponseTypes = []string{"code"}
	id := rand.Text()
	if _, e := s.options.DB.ExecContext(r.Context(), "INSERT INTO mcp_oauth_clients VALUES(?,?)", id, mcpJSON(client)); e != nil {
		mcpOAuthError(w, 500, "server_error")
		return
	}
	mcpOAuthJSON(w, 201, map[string]any{"client_id": id, "redirect_uris": client.RedirectURIs, "client_name": client.ClientName, "token_endpoint_auth_method": "none", "grant_types": client.GrantTypes, "response_types": client.ResponseTypes})
}
func mcpSingleValues(values url.Values) bool {
	for _, v := range values {
		if len(v) != 1 {
			return false
		}
	}
	return true
}
func (s *MCPOAuthServer) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		mcpOAuthError(w, 405, "invalid_request")
		return
	}
	v := r.URL.Query()
	client, e := s.client(r.Context(), v.Get("client_id"))
	challenge, e2 := base64.RawURLEncoding.DecodeString(v.Get("code_challenge"))
	if e != nil || !mcpSingleValues(v) || !mcpOAuthRedirectMatches(client.RedirectURIs, v.Get("redirect_uri")) || v.Get("response_type") != "code" || v.Get("resource") != s.resource || (v.Get("scope") != "" && v.Get("scope") != mcpAccessScope) || v.Get("code_challenge_method") != "S256" || e2 != nil || len(challenge) != 32 || len(v.Get("state")) > 1024 {
		mcpOAuthError(w, 400, "invalid_request")
		return
	}
	owner, ok := s.owner(r)
	if !ok {
		if s.options.LoginURL != "" {
			login, _ := url.Parse(s.options.LoginURL)
			query := login.Query()
			query.Set("return_to", r.URL.RequestURI())
			login.RawQuery = query.Encode()
			http.Redirect(w, r, login.String(), http.StatusSeeOther)
		} else {
			mcpOAuthError(w, 401, "login_required")
		}
		return
	}
	id := rand.Text()
	_, e = s.options.DB.ExecContext(r.Context(), `INSERT INTO mcp_oauth_pending VALUES(?,?,?,?,?,?,?,?)`, id, v.Get("client_id"), v.Get("redirect_uri"), s.resource, v.Get("code_challenge"), v.Get("state"), mcpCredentialDigest(owner), time.Now().Add(5*time.Minute).Unix())
	if e != nil {
		mcpOAuthError(w, 500, "server_error")
		return
	}
	http.Redirect(w, r, "/mcp-connect.html?request="+url.QueryEscape(id), http.StatusSeeOther)
}

// mcpConsentInput contains only the owner's binary connection decision.
type mcpConsentInput struct {
	RequestID string `json:"request_id" required:"true"`
	Approve   bool   `json:"approve" required:"true"`
}

func (s *MCPOAuthServer) consent(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.owner(r)
	if !ok {
		mcpOAuthError(w, 401, "login_required")
		return
	}
	if r.Method == "GET" {
		var clientID, session string
		var expires int64
		e := s.options.DB.QueryRowContext(r.Context(), "SELECT client_id,session_digest,expires_at FROM mcp_oauth_pending WHERE id=?", r.URL.Query().Get("request")).Scan(&clientID, &session, &expires)
		if e != nil || session != mcpCredentialDigest(owner) || expires <= time.Now().Unix() {
			mcpOAuthError(w, 400, "invalid_request")
			return
		}
		client, e := s.client(r.Context(), clientID)
		if e != nil {
			mcpOAuthError(w, 400, "invalid_client")
			return
		}
		mcpOAuthJSON(w, 200, map[string]any{"client_name": client.ClientName, "receiver_node": client.ReceiverNode})
		return
	}
	if r.Method != "POST" || r.Header.Get("Origin") != s.origin {
		mcpOAuthError(w, 403, "invalid_request")
		return
	}
	var input mcpConsentInput
	if e := mcpDecodeBody(w, r, 4096, &input); e != nil {
		mcpOAuthError(w, 400, "invalid_request")
		return
	}
	code := rand.Text()
	var redirect, state string
	err := storage.Transaction(r.Context(), s.options.DB, func(tx *sql.Tx) error {
		var clientID, resource, challenge, session string
		var expires int64
		e := tx.QueryRowContext(r.Context(), "SELECT client_id,redirect_uri,resource,challenge,state,session_digest,expires_at FROM mcp_oauth_pending WHERE id=?", input.RequestID).Scan(&clientID, &redirect, &resource, &challenge, &state, &session, &expires)
		if e != nil || session != mcpCredentialDigest(owner) || expires <= time.Now().Unix() {
			return agentlink.ErrUnauthorized
		}
		if _, e = tx.ExecContext(r.Context(), "DELETE FROM mcp_oauth_pending WHERE id=?", input.RequestID); e != nil {
			return e
		}
		if !input.Approve {
			return nil
		}
		var raw string
		if e = tx.QueryRowContext(r.Context(), "SELECT metadata FROM mcp_oauth_clients WHERE id=?", clientID).Scan(&raw); e != nil {
			return e
		}
		var client mcpOAuthClient
		if e = mcpRead(raw, &client); e != nil {
			return e
		}
		connection := ""
		// Reauthorization of this public client rotates credentials while keeping
		// its logical caller and accepted receipts. A revoked connection is never
		// resurrected by another code exchange.
		e = tx.QueryRowContext(r.Context(), "SELECT id FROM mcp_oauth_connections WHERE client_id=? AND receiver_node=? AND revoked_at=0 ORDER BY created_at DESC LIMIT 1", clientID, client.ReceiverNode).Scan(&connection)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if connection == "" {
			connection = rand.Text()
			actor := protocol.AgentRef{NodeID: s.options.NodeID, Kind: "user", AgentID: rand.Text()}
			_, e = tx.ExecContext(r.Context(), "INSERT INTO mcp_oauth_connections(id,client_id,actor,receiver_node,created_at) VALUES(?,?,?,?,?)", connection, clientID, mcpJSON(actor), client.ReceiverNode, time.Now().Unix())
			if e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(r.Context(), "INSERT INTO mcp_oauth_codes VALUES(?,?,?,?,?,?,?,0)", mcpCredentialDigest(code), connection, clientID, redirect, resource, challenge, time.Now().Add(time.Minute).Unix())
		return e
	})
	if err != nil {
		mcpOAuthError(w, 400, "invalid_request")
		return
	}
	u, _ := url.Parse(redirect)
	v := u.Query()
	if input.Approve {
		v.Set("code", code)
	} else {
		v.Set("error", "access_denied")
	}
	if state != "" {
		v.Set("state", state)
	}
	u.RawQuery = v.Encode()
	mcpOAuthJSON(w, 200, map[string]string{"redirect": u.String()})
}
func mcpPKCEVerifier(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}
func (s *MCPOAuthServer) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		mcpOAuthError(w, 405, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil || !mcpSingleValues(r.PostForm) || r.PostForm.Get("grant_type") != "authorization_code" || r.Header.Get("Authorization") != "" || r.PostForm.Get("client_secret") != "" {
		mcpOAuthError(w, 400, "unsupported_grant_type")
		return
	}
	v := r.PostForm
	verifier := v.Get("code_verifier")
	if !mcpPKCEVerifier(verifier) {
		mcpOAuthError(w, 400, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	token := rand.Text()
	var connection, node string
	err := storage.Transaction(r.Context(), s.options.DB, func(tx *sql.Tx) error {
		var id, client, redirect, resource, saved string
		var expires int64
		var consumed, revoked int
		e := tx.QueryRowContext(r.Context(), `SELECT c.connection_id,c.client_id,c.redirect_uri,c.resource,c.challenge,c.expires_at,c.consumed,n.revoked_at,n.receiver_node FROM mcp_oauth_codes c JOIN mcp_oauth_connections n ON n.id=c.connection_id WHERE c.digest=?`, mcpCredentialDigest(v.Get("code"))).Scan(&id, &client, &redirect, &resource, &saved, &expires, &consumed, &revoked, &node)
		if e != nil || consumed != 0 || revoked != 0 || expires <= time.Now().Unix() || client != v.Get("client_id") || redirect != v.Get("redirect_uri") || resource != v.Get("resource") || resource != s.resource || saved != challenge {
			return agentlink.ErrUnauthorized
		}
		if _, e = tx.ExecContext(r.Context(), "UPDATE mcp_oauth_codes SET consumed=1 WHERE digest=?", mcpCredentialDigest(v.Get("code"))); e != nil {
			return e
		}
		_, e = tx.ExecContext(r.Context(), "INSERT INTO mcp_oauth_tokens VALUES(?,?,?,?)", mcpCredentialDigest(token), id, resource, time.Now().Add(mcpAccessLifetime).Unix())
		connection = id
		return e
	})
	if err != nil {
		mcpOAuthError(w, 400, "invalid_grant")
		return
	}
	mcpOAuthJSON(w, 200, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": int(mcpAccessLifetime.Seconds()), "scope": mcpAccessScope, "connection_id": connection, "node_id": node})
}

// Verify rechecks revocation, expiry and audience for every HTTP request. UserID
// is logical connection identity so SDK session ownership survives token rotation.
func (s *MCPOAuthServer) Verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	var connection, resource, actorRaw, node string
	var expires, revoked int64
	e := s.options.DB.QueryRowContext(ctx, `SELECT t.connection_id,t.resource,t.expires_at,c.revoked_at,c.actor,c.receiver_node FROM mcp_oauth_tokens t JOIN mcp_oauth_connections c ON c.id=t.connection_id WHERE t.digest=?`, mcpCredentialDigest(token)).Scan(&connection, &resource, &expires, &revoked, &actorRaw, &node)
	if e != nil || resource != s.resource || revoked != 0 || expires <= time.Now().Unix() {
		return nil, auth.ErrInvalidToken
	}
	var actor protocol.AgentRef
	if mcpRead(actorRaw, &actor) != nil {
		return nil, auth.ErrInvalidToken
	}
	caller, e := agentlink.NewMCPCaller(connection, actor)
	if e != nil {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: connection, Expiration: time.Unix(expires, 0), Scopes: []string{mcpAccessScope}, Extra: map[string]any{"caller": caller, "receiver_node": node}}, nil
}
func (s *MCPOAuthServer) revokeToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		mcpOAuthError(w, 405, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || !mcpSingleValues(r.PostForm) {
		mcpOAuthError(w, 400, "invalid_request")
		return
	}
	_, e := s.options.DB.ExecContext(r.Context(), `UPDATE mcp_oauth_connections SET revoked_at=? WHERE id IN (SELECT t.connection_id FROM mcp_oauth_tokens t JOIN mcp_oauth_connections c ON c.id=t.connection_id WHERE t.digest=? AND c.client_id=?)`, time.Now().Unix(), mcpCredentialDigest(r.PostForm.Get("token")), r.PostForm.Get("client_id"))
	if e != nil {
		mcpOAuthError(w, 500, "server_error")
		return
	}
	mcpOAuthJSON(w, 200, map[string]any{})
}
func (s *MCPOAuthServer) Revoke(ctx context.Context, id string) error {
	_, e := s.options.DB.ExecContext(ctx, "UPDATE mcp_oauth_connections SET revoked_at=? WHERE id=?", time.Now().Unix(), id)
	return e
}
func (s *MCPOAuthServer) connections(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.owner(r); !ok {
		mcpOAuthError(w, 401, "login_required")
		return
	}
	if r.Method == "POST" {
		if r.Header.Get("Origin") != s.origin {
			mcpOAuthError(w, 403, "invalid_request")
			return
		}
		var input struct {
			ConnectionID string `json:"connection_id" required:"true"`
		}
		if mcpDecodeBody(w, r, 4096, &input) != nil || !core.Identifier(input.ConnectionID) {
			mcpOAuthError(w, 400, "invalid_request")
			return
		}
		if s.Revoke(r.Context(), input.ConnectionID) != nil {
			mcpOAuthError(w, 500, "server_error")
			return
		}
		mcpOAuthJSON(w, 200, map[string]any{})
		return
	}
	if r.Method != "GET" {
		mcpOAuthError(w, 405, "invalid_request")
		return
	}
	rows, e := s.options.DB.QueryContext(r.Context(), `SELECT c.id,c.created_at,c.revoked_at,c.receiver_node,j.metadata FROM mcp_oauth_connections c JOIN mcp_oauth_clients j ON j.id=c.client_id ORDER BY c.created_at DESC`)
	if e != nil {
		mcpOAuthError(w, 500, "server_error")
		return
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, node, raw string
		var created, revoked int64
		if e = rows.Scan(&id, &created, &revoked, &node, &raw); e != nil {
			mcpOAuthError(w, 500, "server_error")
			return
		}
		var client mcpOAuthClient
		if mcpRead(raw, &client) != nil {
			mcpOAuthError(w, 500, "server_error")
			return
		}
		result = append(result, map[string]any{"connection_id": id, "client_name": client.ClientName, "receiver_node": node, "created_at": created, "revoked_at": revoked})
	}
	if rows.Err() != nil {
		mcpOAuthError(w, 500, "server_error")
		return
	}
	mcpOAuthJSON(w, 200, result)
}

func (s *MCPOAuthServer) CheckConnection(ctx context.Context, id string) error {
	var revoked int64
	if s.options.DB.QueryRowContext(ctx, "SELECT revoked_at FROM mcp_oauth_connections WHERE id=?", id).Scan(&revoked) != nil || revoked != 0 {
		return agentlink.ErrUnauthorized
	}
	return nil
}
