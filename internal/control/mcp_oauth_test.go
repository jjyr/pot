package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMCPOAuthPKCEStateRevisionAndSecretStorage(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	var origin, challenge string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{origin}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]string{"authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token", "registration_endpoint": origin + "/register"})
		case "/register":
			json.NewEncoder(w).Encode(map[string]string{"client_id": "public-client"})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("code") != "one-time-code" {
				t.Error("PKCE binding missing")
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "private-oauth-token", "token_type": "Bearer"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer issuer.Close()
	origin = issuer.URL
	value, err := s.ManageMCP(ctx, MCPCommand{Action: "save", Server: MCPServer{ID: "oauth", Name: "OAuth", Enabled: true, Transport: "http", URL: origin + "/mcp", Auth: "oauth"}})
	if err != nil {
		t.Fatal(err)
	}
	server := value.(MCPServer)
	value, err = s.BeginMCPOAuth(ctx, MCPOAuthCommand{ID: server.ID, Revision: server.Revision, Redirect: "http://127.0.0.1:12345/mcp-oauth-callback.html"})
	if err != nil {
		t.Fatal(err)
	}
	begin := value.(map[string]string)
	auth, err := url.Parse(begin["authorization_url"])
	if err != nil {
		t.Fatal(err)
	}
	challenge = auth.Query().Get("code_challenge")
	if challenge == "" || auth.Query().Get("state") != begin["state"] || auth.Query().Get("code_challenge_method") != "S256" {
		t.Fatal(auth)
	}
	if _, err = s.CompleteMCPOAuth(ctx, MCPOAuthCommand{State: "wrong", Code: "one-time-code"}); err == nil {
		t.Fatal("unbound state accepted")
	}
	result, err := s.CompleteMCPOAuth(ctx, MCPOAuthCommand{State: begin["state"], Code: "one-time-code"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private-oauth-token") {
		t.Fatal("token leaked")
	}
	if _, err = s.CompleteMCPOAuth(ctx, MCPOAuthCommand{State: begin["state"], Code: "one-time-code"}); err == nil {
		t.Fatal("authorization code replay accepted")
	}
	servers, _ := s.mcpServers(ctx)
	secret, err := s.Credential(ctx, "test", servers[0].SecretRef, false)
	if err != nil || !strings.Contains(secret, "private-oauth-token") {
		t.Fatal(err)
	}
	native, err := s.BeginMCPOAuth(ctx, MCPOAuthCommand{ID: servers[0].ID, Revision: servers[0].Revision})
	if err != nil {
		t.Fatal(err)
	}
	nativeURL, _ := url.Parse(native.(map[string]string)["authorization_url"])
	challenge = nativeURL.Query().Get("code_challenge")
	redirect, _ := url.Parse(nativeURL.Query().Get("redirect_uri"))
	query := redirect.Query()
	query.Set("state", nativeURL.Query().Get("state"))
	query.Set("code", "one-time-code")
	redirect.RawQuery = query.Encode()
	response, err := http.Get(redirect.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("native callback rejected", response.StatusCode)
	}

}
