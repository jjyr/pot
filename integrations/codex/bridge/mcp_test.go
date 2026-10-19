package bridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func TestMCPStreamableSDKClient(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connect := func(c ClientConfig) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "independent-agent", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.http.Client().Transport, c.Token}}, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	remote, local := connect(f.remote), connect(f.local)
	for _, tc := range []struct {
		session *mcp.ClientSession
		names   []string
	}{
		{remote, []string{"codex_submit", "codex_status", "codex_cancel", "codex_message", "codex_reply"}},
		{local, []string{"pot_notify", "pot_request", "pot_receipt"}},
	} {
		listed, err := tc.session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tool := range listed.Tools {
			names[tool.Name] = true
		}
		if len(names) != len(tc.names) {
			t.Fatal(names)
		}
		for _, name := range tc.names {
			if !names[name] {
				t.Fatal("missing", name)
			}
		}
	}
	call := func(session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %v", name, result, err)
		}
		return result
	}
	decode := func(result *mcp.CallToolResult, target any) {
		t.Helper()
		raw := result.Content[0].(*mcp.TextContent).Text
		if err := json.Unmarshal([]byte(raw), target); err != nil {
			t.Fatal(err)
		}
		if result.StructuredContent == nil {
			t.Fatal("missing structured result")
		}
	}
	args := Request{"sdk", "repo", "read-only", "independent agent task"}
	var first, retry Task
	decode(call(remote, "codex_submit", args), &first)
	decode(call(remote, "codex_submit", args), &retry)
	if first.ID != retry.ID || first.Status != "queued" {
		t.Fatal(first, retry)
	}
	call(remote, "codex_cancel", map[string]string{"task_id": first.ID})
	decode(call(remote, "codex_status", map[string]string{"task_id": first.ID}), &retry)
	if retry.Status != "cancelled" {
		t.Fatal(retry)
	}
	var message Message
	decode(call(local, "pot_request", map[string]string{"request_id": "sdk-message", "body": "please inspect"}), &message)
	call(remote, "codex_message", map[string]string{"message_id": message.ID})
	call(remote, "codex_reply", map[string]string{"message_id": message.ID, "result": "authorized answer"})
	decode(call(local, "pot_receipt", map[string]string{"message_id": message.ID}), &message)
	if message.Status != "answered" || message.Result != "authorized answer" {
		t.Fatal(message)
	}
	// Discovery and invocation both enforce credential roles.
	for _, tc := range []struct {
		session *mcp.ClientSession
		name    string
	}{{remote, "pot_request"}, {local, "codex_submit"}} {
		result, err := tc.session.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: map[string]any{}})
		if err == nil && !result.IsError {
			t.Fatal("cross-role call accepted")
		}
	}
}

func TestMCPTransportBoundaries(t *testing.T) {
	f := setup(t)
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	for _, tc := range []struct {
		name, method, path, body, origin, version, token string
		status                                           int
	}{
		{"unauthenticated", "POST", "/mcp", payload, "", "2025-06-18", "", 401},
		{"cross-origin", "POST", "/mcp", payload, "https://untrusted.invalid", "2025-06-18", f.remote.Token, 403},
		{"unsupported-version", "POST", "/mcp", payload, "", "1900-01-01", f.remote.Token, 400},
		{"no-standalone-stream", "GET", "/mcp", "", "", "2025-06-18", f.remote.Token, 405},
		{"no-session-state", "DELETE", "/mcp", "", "", "2025-06-18", f.remote.Token, 405},
		{"retired-task-api", "POST", "/v1/tasks", "{}", "", "", f.remote.Token, 404},
		{"retired-message-api", "GET", "/v1/messages/message", "", "", "", f.local.Token, 404},
		{"oversized-body", "POST", "/mcp", strings.Repeat("x", maxBody+1), "", "2025-06-18", f.remote.Token, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, f.http.URL+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.version != "" {
				req.Header.Set("MCP-Protocol-Version", tc.version)
			}
			resp, err := f.http.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				data, _ := io.ReadAll(resp.Body)
				t.Fatalf("status=%d want=%d: %s", resp.StatusCode, tc.status, data)
			}
		})
	}
	// Schema validation applies to both transports, including omitted, null,
	// unknown and wrong-type arguments. Byte bounds also apply to Unicode text.
	for _, args := range []any{nil, map[string]any{}, map[string]any{"message_id": "../escape"}, map[string]any{"message_id": 7}, map[string]any{"message_id": "valid", "extra": true}} {
		f.tool(f.local, "pot_receipt", args, "error")
	}
	f.tool(f.local, "pot_notify", map[string]any{"request_id": "unicode", "body": strings.Repeat("中", 5000)}, "invalid_message")
}
