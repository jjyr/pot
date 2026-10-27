package agenthost

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/jjyr/pot/internal/mcpreceiver"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReceiverOAuthUsesIndependentPKCEGrantAndResource(t *testing.T) {
	var server *httptest.Server
	var redirect, challenge string
	var registeredNode string
	tokenRequests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			json.NewEncoder(w).Encode(map[string]any{"resource": server.URL + "/mcp", "authorization_servers": []string{server.URL}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL, "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "registration_endpoint": server.URL + "/register"})
		case "/register":
			var in struct {
				Redirects []string `json:"redirect_uris"`
				Node      string   `json:"pot_receiver_node"`
				Auth      string   `json:"token_endpoint_auth_method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Redirects) != 1 || in.Auth != "none" {
				t.Error("invalid public registration", err)
				w.WriteHeader(400)
				return
			}
			redirect = in.Redirects[0]
			registeredNode = in.Node
			json.NewEncoder(w).Encode(map[string]string{"client_id": "fixture-public-client"})
		case "/authorize":
			q := r.URL.Query()
			if q.Get("resource") != server.URL+"/mcp" || q.Get("scope") != "pot:collaborate" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != redirect || q.Get("state") == "" {
				t.Error("invalid authorization parameters")
				w.WriteHeader(400)
				return
			}
			challenge = q.Get("code_challenge")
			callback, _ := url.Parse(redirect)
			values := callback.Query()
			values.Set("code", "fixture-one-use-code")
			values.Set("state", q.Get("state"))
			callback.RawQuery = values.Encode()
			http.Redirect(w, r, callback.String(), http.StatusFound)
		case "/token":
			tokenRequests++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("code") != "fixture-one-use-code" || r.Form.Get("resource") != server.URL+"/mcp" || r.Form.Get("redirect_uri") != redirect {
				t.Error("invalid PKCE token request")
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-receiver-secret", "token_type": "Bearer", "expires_in": 3600, "connection_id": "fixture-connection", "node_id": registeredNode})
		default:
			w.WriteHeader(404)
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, connection, err := authorizeReceiver(ctx, server.URL+"/mcp", "receiver-node", func(auth string) error {
		response, err := http.Get(auth)
		if err == nil {
			response.Body.Close()
		}
		return err
	})
	if err != nil || connection != "fixture-connection" || token.SchemaVersion != 1 || token.AccessToken != "fixture-receiver-secret" || tokenRequests != 1 || registeredNode != "receiver-node" {
		t.Fatal("grant mismatch", connection, tokenRequests, registeredNode, err)
	}
	if !token.ExpiresAt.After(time.Now()) {
		t.Fatal("missing expiry")
	}
}

func TestReceiverOAuthRejectsUnrelatedResourceAndInvalidURLs(t *testing.T) {
	for _, raw := range []string{"stdio://agent", "https://user:secret@example.invalid/mcp", "https://example.invalid/mcp#secret", "/relative"} {
		if _, err := receiverURL(raw); err == nil {
			t.Fatal("invalid resource accepted")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"resource": "http://other.invalid/mcp", "authorization_servers": []string{"http://other.invalid"}})
	}))
	defer server.Close()
	opened := false
	_, _, err := authorizeReceiver(context.Background(), server.URL+"/mcp", "receiver-node", func(string) error { opened = true; return nil })
	if err == nil || opened {
		t.Fatal("unrelated resource reached browser")
	}
}

func TestReceiverBearerNeverFollowsResourceOrCredentialRedirect(t *testing.T) {
	target, _ := url.Parse("http://pot.invalid/mcp")
	called := false
	transport := mcpreceiver.Bearer{Token: "fixture-secret", Endpoint: target, Base: hostRoundTripper(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("missing receiver credential")
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	for _, endpoint := range []string{"http://other.invalid/mcp", "http://pot.invalid/owner", "http://pot.invalid/mcp?extra=1"} {
		request, _ := http.NewRequest("POST", endpoint, strings.NewReader("{}"))
		if _, err := transport.RoundTrip(request); err == nil {
			t.Fatal("receiver credential escaped resource")
		}
	}
	if called {
		t.Fatal("foreign resource reached base transport")
	}
	request, _ := http.NewRequest("POST", target.String(), nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}
