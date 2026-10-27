package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
)

func (f *mcpAccessFixture) registerClient(t *testing.T, redirect string) string {
	t.Helper()
	var result struct {
		ID string `json:"client_id"`
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/register", "", mcpOAuthClient{RedirectURIs: []string{redirect}, ClientName: "Validation fixture"}), 201, &result)
	return result.ID
}
func (f *mcpAccessFixture) authorizationValues(client, redirect string) url.Values {
	sum := sha256.Sum256([]byte(strings.Repeat("v", 43)))
	return url.Values{"client_id": {client}, "redirect_uri": {redirect}, "resource": {f.http.URL + "/mcp"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "scope": {mcpAccessScope}, "state": {"kept-state"}}
}
func (f *mcpAccessFixture) pending(t *testing.T, client, redirect string) string {
	t.Helper()
	response := f.request(t, "GET", "/oauth/authorize?"+f.authorizationValues(client, redirect).Encode(), "owner-one", nil)
	if response.StatusCode != 303 {
		fixtureResponse(t, response, 303, nil)
	}
	location, e := url.Parse(response.Header.Get("Location"))
	response.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	return location.Query().Get("request")
}
func (f *mcpAccessFixture) consentCode(t *testing.T, client, redirect string) string {
	t.Helper()
	pending := f.pending(t, client, redirect)
	var result struct {
		Redirect string `json:"redirect"`
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "owner-one", mcpConsentInput{RequestID: pending, Approve: true}), 200, &result)
	u, e := url.Parse(result.Redirect)
	if e != nil {
		t.Fatal(e)
	}
	return u.Query().Get("code")
}
func (f *mcpAccessFixture) tokenValues(client, redirect, code string) url.Values {
	return url.Values{"client_id": {client}, "redirect_uri": {redirect}, "resource": {f.http.URL + "/mcp"}, "grant_type": {"authorization_code"}, "code_verifier": {strings.Repeat("v", 43)}, "code": {code}}
}
func TestMCPAccessOAuthCodeAndSessionBinding(t *testing.T) {
	f := mcpAccessTestFixture(t)
	redirect := "https://client.example/callback?source=fixture"
	client := f.registerClient(t, redirect)
	for _, test := range []struct{ name, key, value string }{
		{"redirect path", "redirect_uri", "https://client.example/elsewhere"}, {"redirect query", "redirect_uri", "https://client.example/callback?source=other"}, {"redirect port", "redirect_uri", "https://client.example:8080/callback?source=fixture"}, {"resource", "resource", "https://another-pot.example/mcp"}, {"scope owner", "scope", "owner"}, {"implicit", "response_type", "token"}, {"plain PKCE", "code_challenge_method", "plain"}, {"bad challenge", "code_challenge", "short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := f.authorizationValues(client, redirect)
			values.Set(test.key, test.value)
			fixtureResponse(t, f.request(t, "GET", "/oauth/authorize?"+values.Encode(), "owner-one", nil), 400, nil)
		})
	}
	duplicate := f.authorizationValues(client, redirect)
	duplicate.Add("client_id", client)
	fixtureResponse(t, f.request(t, "GET", "/oauth/authorize?"+duplicate.Encode(), "owner-one", nil), 400, nil)
	pending := f.pending(t, client, redirect)
	fixtureResponse(t, f.request(t, "GET", "/oauth/consent?request="+pending, "owner-two", nil), 400, nil)
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "owner-two", mcpConsentInput{RequestID: pending, Approve: true}), 400, nil)
	// An exact nonce is insufficient without the original owner session and Origin.
	r, e := http.NewRequest("POST", f.http.URL+"/oauth/consent", strings.NewReader(mcpJSON(mcpConsentInput{RequestID: pending, Approve: true})))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("X-Fixture-Owner", "owner-one")
	response, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	fixtureResponse(t, response, 403, nil)
	var denied struct {
		Redirect string `json:"redirect"`
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "owner-one", mcpConsentInput{RequestID: pending, Approve: false}), 200, &denied)
	u, e := url.Parse(denied.Redirect)
	if e != nil || u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "kept-state" || u.Query().Get("code") != "" {
		t.Fatal("invalid denial redirect", e)
	}
	fixtureResponse(t, f.request(t, "POST", "/oauth/consent", "owner-one", mcpConsentInput{RequestID: pending, Approve: true}), 400, nil)
	for _, test := range []struct{ name, key, value string }{
		{"verifier", "code_verifier", strings.Repeat("x", 43)}, {"missing verifier", "code_verifier", ""}, {"wrong client", "client_id", "unknown"}, {"redirect exact exchange", "redirect_uri", "https://client.example/callback"}, {"wrong resource", "resource", "https://another.example/mcp"}, {"refresh", "grant_type", "refresh_token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code := f.consentCode(t, client, redirect)
			values := f.tokenValues(client, redirect, code)
			values.Set(test.key, test.value)
			response, e := http.PostForm(f.http.URL+"/oauth/token", values)
			if e != nil {
				t.Fatal(e)
			}
			fixtureResponse(t, response, 400, nil)
		})
	}
	code := f.consentCode(t, client, redirect)
	if _, e = f.oauth.options.DB.Exec("UPDATE mcp_oauth_codes SET expires_at=? WHERE digest=?", time.Now().Add(-time.Second).Unix(), mcpCredentialDigest(code)); e != nil {
		t.Fatal(e)
	}
	response, e = http.PostForm(f.http.URL+"/oauth/token", f.tokenValues(client, redirect, code))
	if e != nil {
		t.Fatal(e)
	}
	fixtureResponse(t, response, 400, nil)
	code = f.consentCode(t, client, redirect)
	if _, e = f.oauth.options.DB.Exec("UPDATE mcp_oauth_connections SET revoked_at=?", time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	response, e = http.PostForm(f.http.URL+"/oauth/token", f.tokenValues(client, redirect, code))
	if e != nil {
		t.Fatal(e)
	}
	fixtureResponse(t, response, 400, nil)
}
func TestMCPAccessLoopbackReauthorizationKeepsCaller(t *testing.T) {
	f := mcpAccessTestFixture(t)
	registered := "http://127.0.0.1:9000/callback?native=1"
	client := f.registerClient(t, registered)
	var prior mcpFixtureGrant
	for index, redirect := range []string{registered, "http://127.0.0.1:9001/callback?native=1"} {
		code := f.consentCode(t, client, redirect)
		// Exchange is still exact to the selected URI, despite RFC8252 registration matching.
		bad := f.tokenValues(client, registered, code)
		if index == 1 {
			response, e := http.PostForm(f.http.URL+"/oauth/token", bad)
			if e != nil {
				t.Fatal(e)
			}
			fixtureResponse(t, response, 400, nil)
		}
		response, e := http.PostForm(f.http.URL+"/oauth/token", f.tokenValues(client, redirect, code))
		if e != nil {
			t.Fatal(e)
		}
		var grant mcpFixtureGrant
		fixtureResponse(t, response, 200, &grant)
		if index == 1 {
			if grant.Connection != prior.Connection || grant.Token == prior.Token {
				t.Fatal("credential rotation replaced caller")
			}
			old, e := f.oauth.Verify(context.Background(), prior.Token, nil)
			if e != nil {
				t.Fatal(e)
			}
			next, e := f.oauth.Verify(context.Background(), grant.Token, nil)
			if e != nil || old.Extra["caller"].(agentlink.MCPCaller).Actor() != next.Extra["caller"].(agentlink.MCPCaller).Actor() {
				t.Fatal("actor rotated", e)
			}
		}
		prior = grant
	}
	for _, bad := range []string{"http://localhost:9001/callback?native=1", "http://127.0.0.2:9001/callback?native=1", "http://127.0.0.1:9001/other?native=1", "http://127.0.0.1:9001/callback?native=2", "http://127.0.0.1:9001/callback?native=1#fragment"} {
		if mcpOAuthRedirectMatches([]string{registered}, bad) {
			t.Fatal("redirect broadened", bad)
		}
	}
	if mcpOAuthRedirectMatches([]string{"http://localhost:9000/callback"}, "http://localhost:9001/callback") {
		t.Fatal("localhost received IP-loopback exception")
	}
}
func TestMCPAccessRejectsSpoofedToolInputsAndSession(t *testing.T) {
	f := mcpAccessTestFixture(t)
	grant := f.grant(t, false)
	s := f.session(t, grant)
	for _, raw := range []string{`{"request_id":"x","target":{"node_id":"receiver","kind":"node","agent_id":"receiver"},"text":"x","from":{"node_id":"pot-fixture","kind":"user","agent_id":"owner"}}`, `{"request_id":"x","request_id":"y","target":{"node_id":"receiver","kind":"node","agent_id":"receiver"},"text":"x"}`, `{"request_id":null,"target":{"node_id":"receiver","kind":"node","agent_id":"receiver"},"text":"x"}`, `{"request_id":"x","target":{"node_id":"receiver","kind":"node","agent_id":"receiver"},"text":"x","workspace":"/private"}`} {
		result, e := s.CallTool(context.Background(), "pot_task_submit", json.RawMessage(raw))
		if e == nil && !result.IsError {
			t.Fatal("spoofed input accepted", raw)
		}
	}
	if _, e := s.CallTool(context.Background(), "owner", json.RawMessage(`{}`)); e == nil {
		t.Fatal("owner tool exposed")
	}
	var count int
	if e := f.queue.DB.QueryRow("SELECT count(*) FROM mcp_service_tasks").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	// Exercise the SDK's real session binding with another valid connection token.
	other := f.grant(t, false)
	var sessionID string
	transport := &mcpCaptureTransport{token: grant.Token, session: &sessionID}
	sdkSession, e := mcp.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, MaxRetries: -1}, mcp.Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer sdkSession.Close()
	if sessionID == "" {
		t.Fatal("SDK did not create session")
	}
	request, e := http.NewRequest("POST", f.http.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"ping"}`))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Authorization", "Bearer "+other.Token)
	request.Header.Set("Mcp-Session-Id", sessionID)
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, e := http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("foreign connection reused SDK session: %d", response.StatusCode)
	}
}

type mcpCaptureTransport struct {
	token   string
	session *string
}

func (t *mcpCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, e := (mcpBearerTransport{t.token, http.DefaultTransport}).RoundTrip(request)
	if e == nil && response.Header.Get("Mcp-Session-Id") != "" {
		*t.session = response.Header.Get("Mcp-Session-Id")
	}
	return response, e
}
