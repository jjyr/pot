package consumer_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/mcp"
)

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
