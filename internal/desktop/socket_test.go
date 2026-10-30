//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jjyr/pot/internal/localui"
)

func TestDesktopSocketHandshakeFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	caller := localui.Functions{
		Request: func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
		Events: func(context.Context, string, func(localui.Event) error) error {
			return nil
		},
	}

	sock, err := startDesktopSocket(ctx, "/api/v1/ws", caller, "node")
	if err != nil {
		t.Fatalf("start socket: %v", err)
	}
	defer sock.Close()

	if sock.bootstrap.URL == "" {
		t.Fatalf("bootstrap URL empty")
	}
	if sock.bootstrap.Token == "" {
		t.Fatalf("bootstrap token empty")
	}

	url := sock.bootstrap.URL
	token := sock.bootstrap.Token
	protocols := []string{"pot.v1", "pot-token." + token}

	dial := func(origin string, path string, protos []string) (*websocket.Conn, *http.Response, error) {
		u := url
		if path != "" {
			u = strings.TrimSuffix(url, "/api/v1/ws") + path
		}
		opts := &websocket.DialOptions{
			Subprotocols: protos,
		}
		if origin != "" {
			opts.HTTPHeader = http.Header{"Origin": []string{origin}}
		}
		return websocket.Dial(ctx, u, opts)
	}

	denied := []struct {
		name   string
		origin string
		path   string
		protos []string
	}{
		{"missing token", "wails://localhost", "", []string{"pot.v1"}},
		{"wrong token", "wails://localhost", "", []string{"pot.v1", "pot-token.wrong"}},
		{"foreign origin", "https://example.com", "", protocols},
		{"missing origin", "", "", protocols},
		{"bad path", "wails://localhost", "/api/v1/other", protocols},
		{"query token", "wails://localhost", "/api/v1/ws?token=" + token, protocols},
	}

	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			conn, resp, err := dial(tc.origin, tc.path, tc.protos)
			if err == nil {
				conn.CloseNow()
				t.Fatalf("expected denial, got success")
			}
			if resp != nil {
				resp.Body.Close()
			}
		})
	}

	accept := []struct {
		name   string
		origin string
	}{
		{"wails origin", "wails://localhost"},
		{"null origin", "null"},
	}
	for _, tc := range accept {
		t.Run(tc.name, func(t *testing.T) {
			conn, _, err := dial(tc.origin, "", protocols)
			if err != nil {
				t.Fatalf("expected accept, got err: %v", err)
			}
			conn.CloseNow()
		})
	}
}

func TestDesktopSocketRequestAndShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	caller := localui.Functions{
		Request: func(_ context.Context, method, path string, _ json.RawMessage) (json.RawMessage, error) {
			_ = method
			out, _ := json.Marshal(map[string]string{"path": path})
			return out, nil
		},
		Events: func(context.Context, string, func(localui.Event) error) error {
			return nil
		},
	}

	sock, err := startDesktopSocket(ctx, "/api/v1/ws", caller, "node")
	if err != nil {
		t.Fatalf("start socket: %v", err)
	}
	defer sock.Close()

	token := sock.bootstrap.Token
	conn, _, err := websocket.Dial(ctx, sock.bootstrap.URL, &websocket.DialOptions{
		Subprotocols: []string{"pot.v1", "pot-token." + token},
		HTTPHeader:   http.Header{"Origin": []string{"wails://localhost"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	req := map[string]any{
		"type":                "request",
		"id":                  "one",
		"method":              "GET",
		"path":                "/api/v1/runtime?space_id=node",
		"expected_control_id": "node",
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	var reply struct {
		Type   string          `json:"type"`
		ID     string          `json:"id"`
		Status int             `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	defer readCancel()
	if err := wsjson.Read(readCtx, conn, &reply); err != nil {
		t.Fatalf("read reply: %v", err)
	}

	if reply.Type != "response" {
		t.Fatalf("reply type = %q, want response", reply.Type)
	}
	if reply.ID != "one" {
		t.Fatalf("reply id = %q, want one", reply.ID)
	}
	if reply.Status != 200 {
		t.Fatalf("reply status = %d, want 200", reply.Status)
	}

	var result map[string]any
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	wantPath := "/api/v1/runtime?space_id=node"
	if got, _ := result["path"].(string); got != wantPath {
		t.Fatalf("result path = %q, want %q", got, wantPath)
	}

	if err := sock.Close(); err != nil {
		t.Fatalf("socket close: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 3*time.Second)
	defer shutdownCancel()
	if _, _, err := conn.Read(shutdownCtx); err == nil {
		t.Fatalf("expected read error after shutdown")
	}
	if shutdownCtx.Err() != nil {
		t.Fatalf("shutdown did not close socket: %v", shutdownCtx.Err())
	}
}

func TestDesktopSocketPaths(t *testing.T) {
	if err := validateAPIRequest("POST", "/api/v1/assistants/agent-link%3Apeer%3Aagent?locale=zh%2DCN"); err != nil {
		t.Fatalf("agent-link POST should succeed: %v", err)
	}
	if err := validateAPIRequest("GET", "/api/v1/runtime?space_id=node"); err != nil {
		t.Fatalf("runtime GET should succeed: %v", err)
	}

	rejected := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/ws"},
		{"POST", "/api/v1/lifecycle"},
		{"GET", "/api/v1/events"},
		{"GET", "/api/v1/runtime%2Fsecret"},
		{"GET", "/api/v1/runtime%5Csecret"},
		{"GET", "/api/v1/../etc/passwd"},
		{"GET", "/api/v1/runtime/../secret"},
		{"GET", "/api/v1/.."},
	}
	for _, tc := range rejected {
		if err := validateAPIRequest(tc.method, tc.path); err == nil {
			t.Fatalf("expected rejection for %s %s", tc.method, tc.path)
		}
	}

	scope, err := validateEventsPath("/api/v1/events?assistant_id=alpha")
	if err != nil {
		t.Fatalf("events empty scope: %v", err)
	}
	if scope != "" {
		t.Fatalf("events scope = %q, want empty", scope)
	}

	scope, err = validateEventsPath("/api/v1/events?assistant_id=alpha&space_id=node")
	if err != nil {
		t.Fatalf("events node scope: %v", err)
	}
	if scope != "node" {
		t.Fatalf("events scope = %q, want node", scope)
	}

	if _, err := validateEventsPath("/api/v1/lifecycle"); err == nil {
		t.Fatalf("expected lifecycle events path error")
	}
}
