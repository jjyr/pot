package consumer_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/mcp"
)

func TestIndependentMCPHTTPServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "external-http-consumer", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest, json.RawMessage) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"source": "external"}}, nil, nil
	})
	var handler *mcp.StreamableHTTPHandler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	api := httptest.NewServer(handler)
	defer api.Close()
	defer func() {
		for session := range server.Sessions() {
			session.Close()
		}
	}()
	session, err := mcp.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: api.URL, HTTPClient: api.Client(), DisableStandaloneSSE: true, MaxRetries: -1}, mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil || string(structured) != `{"source":"external"}` {
		t.Fatalf("structured result: %s, %v", structured, err)
	}
}

func TestIndependentMCPProtocolSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input, serverOutput := io.Pipe()
	serverInput, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	defer serverInput.Close()
	defer serverOutput.Close()
	server := mcp.NewServer(&mcp.Implementation{Name: "external-consumer", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest, json.RawMessage) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "external"}}}, nil, nil
	})
	go server.Run(ctx, &mcp.IOTransport{Reader: serverInput, Writer: serverOutput})
	session, err := mcp.ConnectStdio(ctx, input, output, mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx)
	if err != nil || len(tools) != 1 {
		t.Fatal(tools, err)
	}
	result, err := session.CallTool(ctx, "echo", json.RawMessage(`{}`))
	if err != nil || result.Content[0].(*mcp.TextContent).Text != "external" {
		t.Fatal(result, err)
	}
}
