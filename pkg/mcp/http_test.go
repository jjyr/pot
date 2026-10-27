package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestStreamableHTTPFacade(t *testing.T) {
	for _, fixture := range []struct {
		name, version string
		jsonResponse  bool
	}{
		{"SSE", "2025-11-25", false},
		{"JSONOlderVersion", "2025-06-18", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 5*time.Second))
			defer cancel()
			server := NewServer(&Implementation{Name: "http-fixture", Version: "1"}, &ServerOptions{
				SupportedProtocolVersions: []string{fixture.version},
			})
			var calls atomic.Int32
			started, canceled := make(chan struct{}), make(chan struct{})
			AddTool(server, &Tool{Name: "fixture", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *CallToolRequest, args json.RawMessage) (*CallToolResult, any, error) {
				calls.Add(1)
				switch string(args) {
				case `{"wait":true}`:
					close(started)
					<-ctx.Done()
					close(canceled)
					return nil, nil, ctx.Err()
				case `{"fail":true}`:
					return &CallToolResult{IsError: true, Content: []Content{&TextContent{Text: "synthetic tool failure"}}}, nil, nil
				default:
					return &CallToolResult{
						Content:           []Content{&TextContent{Text: "synthetic result"}},
						StructuredContent: map[string]any{"ok": true},
					}, nil, nil
				}
			})
			handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{JSONResponse: fixture.jsonResponse})
			var _ http.Handler = handler
			api := httptest.NewServer(handler)
			defer api.Close()
			// Also release sessions on assertion failures, before closing the listener.
			defer func() {
				for session := range server.Sessions() {
					session.Close()
				}
			}()
			session, err := Connect(ctx, &StreamableClientTransport{
				Endpoint: api.URL, HTTPClient: api.Client(), DisableStandaloneSSE: true, MaxRetries: -1,
			}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if got := session.InitializeResult(); got.ProtocolVersion != fixture.version || got.ServerInfo.Name != "http-fixture" {
				t.Fatalf("unexpected initialization: %+v", got)
			}
			if got := len(slices.Collect(server.Sessions())); got != 1 {
				t.Fatalf("initialized server sessions: %d", got)
			}
			tools, err := session.ListTools(ctx)
			if err != nil || len(tools) != 1 || tools[0].Name != "fixture" {
				t.Fatalf("tools: %+v, %v", tools, err)
			}
			result, err := session.CallTool(ctx, "fixture", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			structured, err := json.Marshal(result.StructuredContent)
			if err != nil || string(structured) != `{"ok":true}` || result.IsError || len(result.Content) != 1 || result.Content[0].(*TextContent).Text != "synthetic result" {
				t.Fatalf("result: %+v, structured: %s, %v", result, structured, err)
			}
			result, err = session.CallTool(ctx, "fixture", json.RawMessage(`{"fail":true}`))
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("tool error must remain a result: %+v, %v", result, err)
			}
			_, err = session.CallTool(ctx, "unknown", json.RawMessage(`{}`))
			var rpcError *jsonrpc.Error
			if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
				t.Fatalf("unknown tool must retain protocol error: %v", err)
			}
			callCtx, stop := context.WithCancel(ctx)
			defer stop()
			callDone := make(chan error, 1)
			go func() {
				_, err := session.CallTool(callCtx, "fixture", json.RawMessage(`{"wait":true}`))
				callDone <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("HTTP call did not start", ctx.Err())
			}
			stop()
			select {
			case err := <-callDone:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("HTTP call did not preserve cancellation", err)
				}
			case <-ctx.Done():
				t.Fatal("HTTP call did not return", ctx.Err())
			}
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("MCP cancellation did not reach HTTP tool handler", ctx.Err())
			}
			if calls.Load() != 3 {
				t.Fatal("unexpected tool calls or automatic retry", calls.Load())
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for len(slices.Collect(server.Sessions())) != 0 {
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("client Close did not release HTTP session", ctx.Err())
				}
			}
		})
	}
}
