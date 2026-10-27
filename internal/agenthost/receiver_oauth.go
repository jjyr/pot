package agenthost

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"golang.org/x/oauth2"
)

type receiverToken struct {
	SchemaVersion int       `json:"schema_version"`
	AccessToken   string    `json:"access_token"`
	TokenType     string    `json:"token_type"`
	ExpiresAt     time.Time `json:"expires_at"`
	ClientID      string    `json:"client_id"`
}

// receiverURL applies no certificate or reachability policy. Deployers own the
// configured HTTP(S) resource; userinfo and fragments are never credentials.
func receiverURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, agentlink.ErrInvalid
	}
	return u, nil
}

func receiverJSON(ctx context.Context, client *http.Client, method, endpoint, contentType string, body io.Reader, target any) error {
	if _, err := receiverURL(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("receiver_oauth_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("receiver_oauth_rejected")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
	if err != nil || len(data) > 128*1024 {
		return agentlink.ErrInvalid
	}
	if json.Unmarshal(data, target) != nil {
		return agentlink.ErrInvalid
	}
	return nil
}

// authorizeReceiver is an independent public OAuth client. It never reads a
// native client's configuration or login. PKCE/state and its loopback listener
// exist only for this one bounded authorization attempt.
func authorizeReceiver(ctx context.Context, endpoint, node string, open func(string) error, existingClientID ...string) (receiverToken, string, error) {
	var saved receiverToken
	resource, err := receiverURL(endpoint)
	if err != nil {
		return saved, "", err
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return agentlink.ErrUnauthorized }}
	origin := resource.Scheme + "://" + resource.Host
	var protected struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err = receiverJSON(ctx, client, "GET", origin+"/.well-known/oauth-protected-resource", "", nil, &protected); err != nil {
		return saved, "", err
	}
	if protected.Resource != endpoint || len(protected.AuthorizationServers) != 1 {
		return saved, "", agentlink.ErrUnauthorized
	}
	issuer, err := receiverURL(protected.AuthorizationServers[0])
	if err != nil {
		return saved, "", err
	}
	var metadata struct {
		Issuer        string `json:"issuer"`
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
		Registration  string `json:"registration_endpoint"`
	}
	metaURL := issuer.Scheme + "://" + issuer.Host + "/.well-known/oauth-authorization-server" + strings.TrimSuffix(issuer.Path, "/")
	if err = receiverJSON(ctx, client, "GET", metaURL, "", nil, &metadata); err != nil {
		return saved, "", err
	}
	if metadata.Issuer != protected.AuthorizationServers[0] {
		return saved, "", agentlink.ErrUnauthorized
	}
	for _, raw := range []string{metadata.Authorization, metadata.Token, metadata.Registration} {
		if _, err = receiverURL(raw); err != nil {
			return saved, "", err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return saved, "", err
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/oauth/callback"
	clientID := ""
	if len(existingClientID) > 1 {
		return saved, "", agentlink.ErrInvalid
	}
	if len(existingClientID) == 1 {
		clientID = existingClientID[0]
	}
	if clientID == "" {
		registration := struct {
			Name         string   `json:"client_name"`
			Redirects    []string `json:"redirect_uris"`
			Auth         string   `json:"token_endpoint_auth_method"`
			Grants       []string `json:"grant_types"`
			Responses    []string `json:"response_types"`
			ReceiverNode string   `json:"pot_receiver_node"`
		}{"Pot receiver", []string{redirect}, "none", []string{"authorization_code"}, []string{"code"}, node}
		raw, err := json.Marshal(registration)
		if err != nil {
			return saved, "", err
		}
		var registered struct {
			ClientID string `json:"client_id"`
		}
		if err = receiverJSON(ctx, client, "POST", metadata.Registration, "application/json", strings.NewReader(string(raw)), &registered); err != nil {
			return saved, "", err
		}
		clientID = registered.ClientID
	}
	if clientID == "" || len(clientID) > 2048 {
		return saved, "", agentlink.ErrInvalid
	}
	verifier := oauth2.GenerateVerifier()
	state := rand.Text()
	config := oauth2.Config{ClientID: clientID, RedirectURL: redirect, Scopes: []string{"pot:collaborate"}, Endpoint: oauth2.Endpoint{AuthURL: metadata.Authorization, TokenURL: metadata.Token, AuthStyle: oauth2.AuthStyleInParams}}
	authURL := config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", endpoint))
	code := make(chan string, 1)
	var once sync.Once
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		query := r.URL.Query()
		if r.Method != "GET" || r.Host != listener.Addr().String() || r.URL.Path != "/oauth/callback" || len(query["state"]) != 1 || query.Get("state") != state || len(query["code"]) > 1 || len(query["error"]) > 1 || (query.Get("code") == "" && query.Get("error") == "") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		once.Do(func() {
			if query.Get("error") != "" {
				code <- ""
			} else {
				code <- query.Get("code")
			}
		})
		w.WriteHeader(http.StatusNoContent)
	})
	go server.Serve(listener)
	defer server.Close()
	if err = open(authURL); err != nil {
		return saved, "", errors.New("receiver_browser_unavailable")
	}
	var authorizationCode string
	select {
	case <-ctx.Done():
		return saved, "", ctx.Err()
	case authorizationCode = <-code:
	}
	if authorizationCode == "" {
		return saved, "", agentlink.ErrUnauthorized
	}
	exchangeCtx := context.WithValue(ctx, oauth2.HTTPClient, client)
	token, err := config.Exchange(exchangeCtx, authorizationCode, oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", endpoint))
	if err != nil {
		return saved, "", errors.New("receiver_oauth_exchange_failed")
	}
	connection, _ := token.Extra("connection_id").(string)
	tokenNode, _ := token.Extra("node_id").(string)
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || token.Expiry.IsZero() || tokenNode != node {
		return saved, "", agentlink.ErrUnauthorized
	}
	if _, err = agentlink.NewMCPReceiver(connection, node); err != nil {
		return saved, "", err
	}
	saved = receiverToken{1, token.AccessToken, "Bearer", token.Expiry, clientID}
	return saved, connection, nil
}
