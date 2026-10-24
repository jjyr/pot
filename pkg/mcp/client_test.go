package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKNegotiationMetadataErrorsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, serverOutput := io.Pipe()
	serverInput, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	defer serverOutput.Close()
	defer serverInput.Close()
	server := NewServer(&Implementation{Name: "fixture", Version: "1"}, &ServerOptions{SupportedProtocolVersions: []string{"2025-06-18"}})
	var calls atomic.Int32
	started, canceled := make(chan struct{}), make(chan struct{})
	server.AddTool(&Tool{Name: "standard.tool", Description: "metadata fixture", Title: "Display title", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := request.Params.Arguments
		calls.Add(1)
		if string(args) == `{"wait":true}` {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		if string(args) != `{"large":9007199254740993}` {
			t.Errorf("argument precision lost: %s", args)
		}
		return &CallToolResult{Meta: sdk.Meta{"fixture": "retained"}, Content: []Content{&TextContent{Text: "fixture", Annotations: &sdk.Annotations{Audience: []sdk.Role{"user"}}}}, StructuredContent: map[string]any{"ok": true}}, nil
	})
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, &IOTransport{Reader: serverInput, Writer: serverOutput}) }()
	session, err := ConnectStdio(ctx, input, output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if session.InitializeResult().ProtocolVersion != "2025-06-18" {
		t.Fatal(session.InitializeResult())
	}
	tools, err := session.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Title != "Display title" || tools[0].Annotations == nil {
		t.Fatal(tools, err)
	}
	result, err := session.CallTool(ctx, "standard.tool", json.RawMessage(`{"large":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var decoded map[string]any
	json.Unmarshal(raw, &decoded)
	if decoded["_meta"].(map[string]any)["fixture"] != "retained" || result.Content[0].(*TextContent).Annotations == nil {
		t.Fatal(string(raw))
	}
	if _, err = session.CallTool(ctx, "standard.tool", json.RawMessage(`null`)); err == nil {
		t.Fatal("null arguments accepted")
	}
	requestCtx, stop := context.WithCancel(ctx)
	callDone := make(chan error, 1)
	go func() {
		_, e := session.CallTool(requestCtx, "standard.tool", json.RawMessage(`{"wait":true}`))
		callDone <- e
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("call did not start")
	}
	stop()
	select {
	case err = <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not return")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("cancellation not propagated to server")
	}
	if _, err = session.CallTool(ctx, "unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown tool accepted")
	} else {
		var wire *jsonrpc.Error
		if !errors.As(err, &wire) || wire.Code != jsonrpc.CodeInvalidParams {
			t.Fatal("lost RPC code", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("calls were retried", calls.Load())
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection did not close")
	}
}

func TestSDKStreamableHTTPKeepsTransportSessionAndErrorCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
	defer cancel()
	server := NewServer(&Implementation{Name: "http-fixture", Version: "1"}, nil)
	var calls atomic.Int32
	AddTool(server, &Tool{Name: "echo", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *CallToolRequest, json.RawMessage) (*CallToolResult, any, error) {
		calls.Add(1)
		return &CallToolResult{IsError: true, Content: []Content{&TextContent{Text: "expected tool error"}}}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	api := httptest.NewServer(handler)
	defer api.Close()
	session, err := Connect(ctx, &StreamableClientTransport{Endpoint: api.URL, HTTPClient: api.Client(), DisableStandaloneSSE: true, MaxRetries: -1}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx)
	if err != nil || len(tools) != 1 {
		t.Fatal(tools, err)
	}
	result, err := session.CallTool(ctx, "echo", json.RawMessage(`{}`))
	if err != nil || !result.IsError {
		t.Fatal(result, err)
	}
	if calls.Load() != 1 {
		t.Fatal("automatic retry", calls.Load())
	}
}
