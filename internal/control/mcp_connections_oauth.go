package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
	"golang.org/x/oauth2"
)

// PotMCPAuthorization starts a public OAuth client flow. The browser returns to
// its existing Pot UI; the initiating Pot requires no public receiver listener.
type PotMCPAuthorization struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
}

type potMCPToken = mcpreceiver.OAuthToken

type potMCPMetadata struct {
	Issuer        string `json:"issuer"`
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	Registration  string `json:"registration_endpoint"`
	Revocation    string `json:"revocation_endpoint"`
}

func potMCPURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, agentlink.ErrInvalid
	}
	return u, nil
}
func potMCPHTTP() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return agentlink.ErrUnauthorized }}
}
func potMCPJSON(ctx context.Context, client *http.Client, method, endpoint, contentType string, body io.Reader, out any) error {
	if _, e := potMCPURL(endpoint); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, e := client.Do(req)
	if e != nil {
		return agentlink.ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return agentlink.ErrUnauthorized
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 128*1024+1))
	if e != nil || len(raw) > 128*1024 {
		return agentlink.ErrInvalid
	}
	if out == nil {
		return nil
	}
	if json.Unmarshal(raw, out) != nil {
		return agentlink.ErrInvalid
	}
	return nil
}
func discoverPotMCP(ctx context.Context, endpoint string) (potMCPMetadata, error) {
	var m potMCPMetadata
	u, e := potMCPURL(endpoint)
	if e != nil {
		return m, e
	}
	origin := u.Scheme + "://" + u.Host
	var p struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	client := potMCPHTTP()
	if e = potMCPJSON(ctx, client, "GET", origin+"/.well-known/oauth-protected-resource"+u.Path, "", nil, &p); e != nil {
		return m, e
	}
	if p.Resource != endpoint || len(p.Servers) != 1 {
		return m, agentlink.ErrUnauthorized
	}
	issuer, e := potMCPURL(p.Servers[0])
	if e != nil {
		return m, e
	}
	if e = potMCPJSON(ctx, client, "GET", issuer.Scheme+"://"+issuer.Host+"/.well-known/oauth-authorization-server"+strings.TrimSuffix(issuer.Path, "/"), "", nil, &m); e != nil {
		return m, e
	}
	if m.Issuer != p.Servers[0] {
		return m, agentlink.ErrUnauthorized
	}
	for _, raw := range []string{m.Authorization, m.Token, m.Registration, m.Revocation} {
		target, e := potMCPURL(raw)
		if e != nil {
			return m, e
		}
		// Pot's issuer owns these endpoints. Refuse metadata that would send a code,
		// verifier or bearer to a different origin.
		if target.Scheme != issuer.Scheme || target.Host != issuer.Host {
			return m, agentlink.ErrUnauthorized
		}
	}
	return m, nil
}

func (s *Service) BeginPotMCPConnection(ctx context.Context, endpoint, redirect, existing string) (PotMCPAuthorization, error) {
	var result PotMCPAuthorization
	if !mcpRedirectValid(redirect) {
		return result, agentlink.ErrInvalid
	}
	clientID := ""
	if existing != "" {
		c, token, e := s.potMCPGrant(ctx, existing)
		if e != nil {
			return result, e
		}
		if c.State == "revoked" || endpoint != "" && endpoint != c.URL {
			return result, agentlink.ErrUnauthorized
		}
		endpoint = c.URL
		clientID = token.ClientID
	}
	metadata, e := discoverPotMCP(ctx, endpoint)
	if e != nil {
		return result, e
	}
	if clientID == "" {
		raw, _ := json.Marshal(mcpOAuthClient{RedirectURIs: []string{redirect}, ClientName: "Pot", TokenEndpointAuthMethod: "none", GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}, ReceiverNode: s.options.RuntimeID})
		var registered struct {
			ClientID string `json:"client_id"`
		}
		if e = potMCPJSON(ctx, potMCPHTTP(), "POST", metadata.Registration, "application/json", strings.NewReader(string(raw)), &registered); e != nil {
			return result, e
		}
		clientID = registered.ClientID
	}
	if clientID == "" || len(clientID) > 2048 {
		return result, agentlink.ErrInvalid
	}
	verifier := oauth2.GenerateVerifier()
	state := rand.Text()
	ref := "pot-mcp-pkce-" + state
	if e = s.integrationSecrets.Put(ctx, ref, []byte(verifier)); e != nil {
		return result, e
	}
	_, e = s.integrationDB.ExecContext(ctx, `INSERT INTO mcp_pot_authorizations(state,url,node_id,client_id,redirect_uri,connection_id,secret_ref,expires_at) VALUES(?,?,?,?,?,?,?,?)`, state, endpoint, s.options.RuntimeID, clientID, redirect, existing, ref, time.Now().Add(10*time.Minute).Unix())
	if e != nil {
		_ = s.integrationSecrets.Delete(ctx, ref)
		return result, e
	}
	config := oauth2.Config{ClientID: clientID, RedirectURL: redirect, Scopes: []string{mcpAccessScope}, Endpoint: oauth2.Endpoint{AuthURL: metadata.Authorization, TokenURL: metadata.Token, AuthStyle: oauth2.AuthStyleInParams}}
	result = PotMCPAuthorization{AuthorizationURL: config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", endpoint)), State: state}
	return result, nil
}

func (s *Service) CompletePotMCPConnection(ctx context.Context, state, code string) (PotMCPConnection, error) {
	var result PotMCPConnection
	if len(state) > 128 || len(code) > 4096 || state == "" || code == "" {
		return result, agentlink.ErrInvalid
	}
	var endpoint, node, client, redirect, existing, ref string
	var expiry int64
	e := storage.Transaction(ctx, s.integrationDB, func(tx *sql.Tx) error {
		e := tx.QueryRowContext(ctx, `SELECT url,node_id,client_id,redirect_uri,connection_id,secret_ref,expires_at FROM mcp_pot_authorizations WHERE state=?`, state).Scan(&endpoint, &node, &client, &redirect, &existing, &ref, &expiry)
		if e != nil {
			return mcpMissing(e)
		}
		if node != s.options.RuntimeID || expiry <= time.Now().Unix() {
			return agentlink.ErrUnauthorized
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM mcp_pot_authorizations WHERE state=?", state)
		return e
	})
	if e != nil {
		return result, e
	}
	verifier, e := s.integrationSecrets.Get(ctx, ref)
	if e != nil {
		return result, e
	}
	defer clear(verifier)
	defer s.integrationSecrets.Delete(ctx, ref)
	metadata, e := discoverPotMCP(ctx, endpoint)
	if e != nil {
		return result, e
	}
	config := oauth2.Config{ClientID: client, RedirectURL: redirect, Endpoint: oauth2.Endpoint{AuthURL: metadata.Authorization, TokenURL: metadata.Token, AuthStyle: oauth2.AuthStyleInParams}}
	token, e := config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, potMCPHTTP()), code, oauth2.VerifierOption(string(verifier)), oauth2.SetAuthURLParam("resource", endpoint))
	if e != nil {
		return result, agentlink.ErrUnauthorized
	}
	id, _ := token.Extra("connection_id").(string)
	tokenNode, _ := token.Extra("node_id").(string)
	if tokenNode != node || existing != "" && id != existing || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || !token.Expiry.After(time.Now()) {
		return result, agentlink.ErrUnauthorized
	}
	if _, e = agentlink.NewMCPReceiver(id, node); e != nil {
		return result, e
	}
	saved := potMCPToken{SchemaVersion: 1, AccessToken: token.AccessToken, TokenType: "Bearer", ExpiresAt: token.Expiry, ClientID: client}
	if e = s.savePotMCPGrant(ctx, id, endpoint, saved); e != nil {
		return result, e
	}
	s.stopPotMCP(id)
	return s.PotMCPConnection(ctx, id)
}

func (s *Service) savePotMCPGrant(ctx context.Context, id, endpoint string, token potMCPToken) error {
	if _, e := potMCPURL(endpoint); e != nil {
		return e
	}
	if token.SchemaVersion != 1 || token.AccessToken == "" || token.TokenType != "Bearer" || !token.ExpiresAt.After(time.Now()) {
		return agentlink.ErrUnauthorized
	}
	if _, e := agentlink.NewMCPReceiver(id, s.options.RuntimeID); e != nil {
		return e
	}
	ref := "pot-mcp-grant-" + rand.Text()
	raw, e := json.Marshal(token)
	if e != nil {
		return e
	}
	defer clear(raw)
	if e = s.integrationSecrets.Put(ctx, ref, raw); e != nil {
		return e
	}
	var previous string
	e = storage.Transaction(ctx, s.integrationDB, func(tx *sql.Tx) error {
		var node, url, state string
		e := tx.QueryRowContext(ctx, "SELECT node_id,url,state,secret_ref FROM mcp_receiver_connections WHERE id=?", id).Scan(&node, &url, &state, &previous)
		if e == nil && (node != s.options.RuntimeID || url != endpoint || state == "revoked") {
			return agentlink.ErrUnauthorized
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO mcp_receiver_connections(id,url,node_id,secret_ref,state) VALUES(?,?,?,?,'active') ON CONFLICT(id) DO UPDATE SET secret_ref=excluded.secret_ref,state='active',last_error='',revision=mcp_receiver_connections.revision+1`, id, endpoint, s.options.RuntimeID, ref)
		return e
	})
	if e != nil {
		_ = s.integrationSecrets.Delete(ctx, ref)
		return e
	}
	if previous != "" {
		_ = s.integrationSecrets.Delete(ctx, previous)
	}
	s.Changes.Notify("agent-links")
	return nil
}
