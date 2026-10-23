package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type mcpOAuthSession struct {
	Server                                      MCPServer
	Verifier, Redirect, TokenEndpoint, ClientID string
	Expires                                     time.Time
}
type MCPOAuthCommand struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Redirect string `json:"redirect_uri"`
	State    string `json:"state"`
	Code     string `json:"code"`
}

func oauthURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))
}
func oauthJSON(ctx context.Context, method, endpoint string, body io.Reader, contentType string, target any) error {
	if !oauthURL(endpoint) {
		return errors.New("invalid OAuth endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("OAuth redirects not allowed") }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("OAuth endpoint unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("OAuth request rejected")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
	if err != nil || len(data) > 128*1024 {
		return errors.New("OAuth response limit")
	}
	if json.Unmarshal(data, target) != nil {
		return errors.New("invalid OAuth response")
	}
	return nil
}
func (s *Service) BeginMCPOAuth(ctx context.Context, c MCPOAuthCommand) (any, error) {
	state := rand.Text()
	var callback *http.Server
	handedOff := false
	defer func() {
		if callback != nil && !handedOff {
			_ = callback.Close()
		}
	}()
	if c.Redirect == "" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		callback = &http.Server{ReadHeaderTimeout: 5 * time.Second}
		c.Redirect = "http://" + listener.Addr().String() + "/mcp/callback"
		callback.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/mcp/callback" || r.URL.Query().Get("state") != state {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()
			_, err := s.CompleteMCPOAuth(ctx, MCPOAuthCommand{State: state, Code: r.URL.Query().Get("code")})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				w.WriteHeader(400)
			}
			_, _ = io.WriteString(w, "<html><title>Pot</title><script>window.close()</script></html>")
			// Close only after the callback response has been flushed. Closing
			// the active connection here races the client's one-time-code reply.
			go func() {
				shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if callback.Shutdown(shutdownCtx) != nil {
					_ = callback.Close()
				}
			}()
		})
		go callback.Serve(listener)
		go func() {
			select {
			case <-s.ctx.Done():
			case <-time.After(10 * time.Minute):
			}
			_ = callback.Close()
		}()
	}
	if !oauthURL(c.Redirect) {
		return nil, errors.New("OAuth requires an HTTPS or loopback callback")
	}
	servers, err := s.mcpServers(ctx)
	if err != nil {
		return nil, err
	}
	var server *MCPServer
	for _, item := range servers {
		if item.ID == c.ID {
			copy := item
			server = &copy
		}
	}
	if server == nil || server.Revision != c.Revision || server.Transport != "http" || server.Auth != "oauth" {
		return nil, errors.New("OAuth server configuration changed")
	}
	endpoint, _ := url.Parse(server.URL)
	origin := endpoint.Scheme + "://" + endpoint.Host
	issuer := origin
	var protected struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if oauthJSON(ctx, "GET", origin+"/.well-known/oauth-protected-resource", nil, "", &protected) == nil && len(protected.AuthorizationServers) > 0 {
		issuer = protected.AuthorizationServers[0]
	}
	if !oauthURL(issuer) {
		return nil, errors.New("invalid OAuth issuer")
	}
	authorizationServer, _ := url.Parse(issuer)
	var metadata struct {
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		Registration  string   `json:"registration_endpoint"`
		Scopes        []string `json:"scopes_supported"`
	}
	metadataURL := authorizationServer.Scheme + "://" + authorizationServer.Host + "/.well-known/oauth-authorization-server" + strings.TrimSuffix(authorizationServer.Path, "/")
	if err = oauthJSON(ctx, "GET", metadataURL, nil, "", &metadata); err != nil {
		return nil, err
	}
	if !oauthURL(metadata.Authorization) || !oauthURL(metadata.Token) {
		return nil, errors.New("invalid OAuth metadata")
	}
	clientID := server.ClientID
	if clientID == "" {
		raw, _ := json.Marshal(map[string]any{"client_name": "Pot", "redirect_uris": []string{c.Redirect}, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code"}, "response_types": []string{"code"}})
		var registration struct {
			ID string `json:"client_id"`
		}
		if err = oauthJSON(ctx, "POST", metadata.Registration, strings.NewReader(string(raw)), "application/json", &registration); err != nil {
			return nil, errors.New("OAuth client registration unavailable; configure a client ID")
		}
		clientID = registration.ID
	}
	if clientID == "" || len(clientID) > 2048 {
		return nil, errors.New("invalid OAuth client")
	}
	verifier := rand.Text() + rand.Text()
	challenge := sha256.Sum256([]byte(verifier))
	auth, _ := url.Parse(metadata.Authorization)
	query := auth.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", c.Redirect)
	query.Set("state", state)
	query.Set("code_challenge_method", "S256")
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("resource", server.URL)
	if server.Scope != "" {
		query.Set("scope", server.Scope)
	}
	auth.RawQuery = query.Encode()
	s.mu.Lock()
	if s.mcpOAuth == nil {
		s.mcpOAuth = map[string]mcpOAuthSession{}
	}
	for key, session := range s.mcpOAuth {
		if time.Now().After(session.Expires) {
			delete(s.mcpOAuth, key)
		}
	}
	if len(s.mcpOAuth) >= 16 {
		s.mu.Unlock()
		return nil, errors.New("OAuth session limit")
	}
	s.mcpOAuth[state] = mcpOAuthSession{Server: *server, Verifier: verifier, Redirect: c.Redirect, TokenEndpoint: metadata.Token, ClientID: clientID, Expires: time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	handedOff = true
	return map[string]string{"authorization_url": auth.String(), "state": state}, nil
}
func (s *Service) CompleteMCPOAuth(ctx context.Context, c MCPOAuthCommand) (result any, resultErr error) {
	s.mu.Lock()
	session, exists := s.mcpOAuth[c.State]
	delete(s.mcpOAuth, c.State)
	s.mu.Unlock()
	if !exists || time.Now().After(session.Expires) || c.Code == "" || len(c.Code) > 8192 {
		return nil, errors.New("OAuth session expired or invalid")
	}
	defer func() {
		if resultErr == nil {
			return
		}
		_ = s.Machines.OwnerAction(func() error {
			s.extensionsMu.Lock()
			defer s.extensionsMu.Unlock()
			servers, err := s.mcpServers(ctx)
			if err != nil {
				return err
			}
			matched := false
			for _, current := range servers {
				a, _ := core.HashJSON(current)
				b, _ := core.HashJSON(session.Server)
				if current.ID == session.Server.ID && a == b {
					matched = true
				}
			}
			if !matched {
				return nil
			}
			server := session.Server
			server.Status = "authorization"
			server.Revision++
			err = s.saveMCP(server, false)
			s.Changes.Notify("extensions")
			return err
		})
	}()
	values := url.Values{"grant_type": {"authorization_code"}, "code": {c.Code}, "client_id": {session.ClientID}, "redirect_uri": {session.Redirect}, "code_verifier": {session.Verifier}, "resource": {session.Server.URL}}
	var token struct {
		Access string `json:"access_token"`
		Type   string `json:"token_type"`
	}
	if err := oauthJSON(ctx, "POST", session.TokenEndpoint, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", &token); err != nil {
		return nil, err
	}
	if token.Access == "" || !strings.EqualFold(token.Type, "Bearer") {
		return nil, errors.New("unsupported OAuth token")
	}
	credentials := MCPCredentials{Headers: map[string]string{"Authorization": "Bearer " + token.Access}}
	if session.Server.SecretRef != "" {
		raw, err := s.integrationSecrets.Get(ctx, session.Server.SecretRef)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &credentials) != nil {
			clear(raw)
			return nil, errors.New("invalid saved credentials")
		}
		clear(raw)
		if credentials.Headers == nil {
			credentials.Headers = map[string]string{}
		}
		credentials.Headers["Authorization"] = "Bearer " + token.Access
	}
	return s.ManageMCP(ctx, MCPCommand{Action: "save", Server: session.Server, Credentials: &credentials})
}
