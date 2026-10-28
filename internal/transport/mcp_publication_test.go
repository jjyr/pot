package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/access"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/mcp"
	webassets "github.com/jjyr/pot/web"
)

type publicationBearer struct {
	base  http.RoundTripper
	token string
}

func (b publicationBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// This uses the real access Manager, browser login and private Unix Control
// socket. No public owner listener, local native Host or model process exists.
func TestMCPProductionPublicationThroughPrivateSocket(t *testing.T) {
	// Keep the socket path below macOS sockaddr_un limits; t.TempDir includes
	// this long test name in its directory path.
	root, err := os.MkdirTemp("", "pot-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("publication", "boot", execution.DefaultLimits())
	service, e := control.Open(control.Options{Root: root, RuntimeID: "publication", BootID: "boot", Keys: keys, Manager: manager})
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	if _, e = service.Registry.Create(t.Context(), "fixture", "Fixture"); e != nil {
		t.Fatal(e)
	}
	socket, e := StartSocket(t.Context(), root, &Application{Control: service, RuntimeID: "publication", BootID: "boot"})
	if e != nil {
		t.Fatal(e)
	}
	defer socket.Close(t.Context())
	browserRoot := t.TempDir()
	store := webui.AccessStore{Path: filepath.Join(browserRoot, "browser-access.json")}
	config, e := store.Load(true)
	if e != nil {
		t.Fatal(e)
	}
	front := httptest.NewUnstartedServer(nil)
	origin := "http://" + front.Listener.Addr().String()
	browser, e := access.Open(access.Options{Root: browserRoot, Origin: origin, Upstream: webui.Upstream{URL: "http://runtime", Credential: socket.Endpoint.Token, ControlID: "publication", Client: socket.Endpoint.Client()}, Assets: webassets.FS()})
	if e != nil {
		t.Fatal(e)
	}
	defer browser.Close()
	front.Config.Handler = browser
	front.Start()
	defer front.Close()
	client := front.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	call := func(method, path string, body []byte, headers map[string]string, cookie *http.Cookie) *http.Response {
		t.Helper()
		r, _ := http.NewRequestWithContext(t.Context(), method, origin+path, bytes.NewReader(body))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	read := func(res *http.Response, status int, value any) {
		t.Helper()
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("status %d want %d: %s", res.StatusCode, status, raw)
		}
		if bytes.Contains(raw, []byte(socket.Endpoint.Token)) || bytes.Contains(raw, []byte(config.Token)) {
			t.Fatal("private credential leaked")
		}
		if value != nil {
			if e := json.Unmarshal(raw, value); e != nil {
				t.Fatal(e, string(raw))
			}
		}
	}
	forged := map[string]string{"X-Pot-MCP-Owner": "forged", "X-Pot-MCP-Authorization": "Bearer forged", "X-Pot-MCP-Origin": origin, "Authorization": "Bearer " + socket.Endpoint.Token}
	read(call("GET", "/oauth/connections", nil, forged, nil), 401, nil)
	challenge := call("POST", "/mcp", []byte(`{}`), forged, nil)
	if challenge.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("lost standard challenge")
	}
	read(challenge, 401, nil)
	read(call("GET", "/api/v1/control", nil, forged, nil), 401, nil)
	read(call("GET", "/.well-known/oauth-protected-resource/mcp", nil, nil, nil), 200, nil)
	read(call("GET", "/oauth/token", nil, nil, nil), 405, nil)
	// Login uses the existing synthetic browser credential, never the MCP bearer.
	login := call("POST", "/access/token", nil, map[string]string{"Authorization": "Bearer " + config.Token, "Origin": origin}, nil)
	if len(login.Cookies()) == 0 {
		t.Fatal("no browser cookie")
	}
	cookie := login.Cookies()[0]
	read(login, 200, nil)
	authorize := func(node string) (string, string) {
		t.Helper()
		metadata := map[string]any{"redirect_uris": []string{"http://127.0.0.1:9876/callback"}, "client_name": "Publication fixture", "token_endpoint_auth_method": "none"}
		if node != "" {
			metadata["pot_receiver_node"] = node
		}
		raw, _ := json.Marshal(metadata)
		var registration struct {
			ID string `json:"client_id"`
		}
		read(call("POST", "/oauth/register", raw, map[string]string{"Content-Type": "application/json"}, nil), 201, &registration)
		sum := sha256.Sum256([]byte(strings.Repeat("v", 43)))
		values := url.Values{"client_id": {registration.ID}, "redirect_uri": {"http://127.0.0.1:9876/callback"}, "resource": {origin + "/mcp"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "state": {"fixture"}}
		res := call("GET", "/oauth/authorize?"+values.Encode(), nil, nil, nil)
		if res.StatusCode != 303 || !strings.Contains(res.Header.Get("Location"), "return_to=") {
			t.Fatal("existing login not reached", res.StatusCode)
		}
		res.Body.Close()
		res = call("GET", "/oauth/authorize?"+values.Encode(), nil, nil, cookie)
		location, _ := url.Parse(res.Header.Get("Location"))
		read(res, 303, nil)
		pending := location.Query().Get("request")
		if pending == "" {
			t.Fatal("no consent")
		}
		read(call("GET", "/oauth/consent?request="+pending, nil, nil, cookie), 200, nil)
		raw, _ = json.Marshal(map[string]any{"request_id": pending, "approve": true})
		var consent struct{ Redirect string }
		read(call("POST", "/oauth/consent", raw, map[string]string{"Origin": origin}, cookie), 200, &consent)
		redirect, _ := url.Parse(consent.Redirect)
		values = url.Values{"client_id": {registration.ID}, "redirect_uri": {"http://127.0.0.1:9876/callback"}, "resource": {origin + "/mcp"}, "grant_type": {"authorization_code"}, "code_verifier": {strings.Repeat("v", 43)}, "code": {redirect.Query().Get("code")}}
		var grant struct {
			Token      string `json:"access_token"`
			Connection string `json:"connection_id"`
		}
		read(call("POST", "/oauth/token", []byte(values.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, nil), 200, &grant)
		return grant.Token, grant.Connection
	}
	for _, node := range []string{"", "receiver-node"} {
		token, connection := authorize(node)
		session, e := mcp.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: origin + "/mcp", HTTPClient: &http.Client{Transport: publicationBearer{base: client.Transport, token: token}}}, mcp.Options{})
		if e != nil {
			t.Fatal(e)
		}
		tools, e := session.ListTools(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		receiver := false
		for _, tool := range tools {
			if tool.Name == "pot_receiver_register" {
				receiver = true
			}
			if strings.Contains(tool.Name, "owner") || strings.Contains(tool.Name, "human_decide") {
				t.Fatal("owner tool advertised")
			}
		}
		if receiver != (node != "") {
			t.Fatal("caller/receiver catalogue mixed")
		}
		result, e := session.CallTool(t.Context(), "pot_directory", json.RawMessage(`{}`))
		if e != nil || result.IsError {
			t.Fatal(result, e)
		}
		read(call("GET", "/oauth/connections", nil, map[string]string{"Authorization": "Bearer " + token, "X-Pot-MCP-Owner": "forged"}, nil), 401, nil)
		read(call("GET", "/api/v1/control", nil, map[string]string{"Authorization": "Bearer " + token}, nil), 401, nil)
		raw, _ := json.Marshal(map[string]string{"connection_id": connection})
		read(call("POST", "/oauth/connections", raw, map[string]string{"Origin": origin}, cookie), 200, nil)
		read(call("POST", "/mcp", []byte(`{}`), map[string]string{"Authorization": "Bearer " + token}, nil), 401, nil)
		session.Close()
	}

	// Local and public browser publications can coexist on one Control. A second
	// configured origin gets its own audience and cannot borrow a bearer/cookie.
	token, _ := authorize("")
	second := httptest.NewUnstartedServer(nil)
	secondOrigin := "http://" + second.Listener.Addr().String()
	secondBrowser, e := access.Open(access.Options{Root: t.TempDir(), Origin: secondOrigin, Upstream: webui.Upstream{URL: "http://runtime", Credential: socket.Endpoint.Token, ControlID: "publication", Client: socket.Endpoint.Client()}, Assets: webassets.FS()})
	if e != nil {
		t.Fatal(e)
	}
	defer secondBrowser.Close()
	second.Config.Handler = secondBrowser
	second.Start()
	defer second.Close()
	res, e := client.Get(secondOrigin + "/.well-known/oauth-protected-resource/mcp")
	if e != nil {
		t.Fatal(e)
	}
	var metadata struct{ Resource string }
	read(res, 200, &metadata)
	if metadata.Resource != secondOrigin+"/mcp" {
		t.Fatal("publication audience mixed", metadata.Resource)
	}
	req, _ := http.NewRequestWithContext(t.Context(), "POST", secondOrigin+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	read(res, 401, nil)
	req, _ = http.NewRequestWithContext(t.Context(), "GET", secondOrigin+"/oauth/connections", nil)
	req.AddCookie(cookie)
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	read(res, 401, nil)
	req, _ = http.NewRequestWithContext(t.Context(), "GET", origin+"/.well-known/oauth-protected-resource", nil)
	req.Host = "foreign.example"
	res, e = client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	read(res, 403, nil)
	for _, path := range []string{"/oauth/../api/v1/control", "/mcp/owner", "/.well-known/owner", "/api/v1/agent-links/invitation"} {
		res := call("POST", path, []byte(`{}`), forged, nil)
		if res.StatusCode < 400 {
			t.Fatal("public path escaped", path, res.StatusCode)
		}
		res.Body.Close()
	}
	read(call("POST", "/mcp", []byte(`{}`), map[string]string{"Origin": "https://foreign.example"}, nil), 403, nil)
}
