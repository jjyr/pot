package mcp

import (
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type StreamableHTTPHandler = sdk.StreamableHTTPHandler
type StreamableHTTPOptions = sdk.StreamableHTTPOptions

// NewStreamableHTTPHandler serves SDK-managed MCP sessions. Callers own routing,
// authentication, HTTP policy and server shutdown; no listener is started here.
func NewStreamableHTTPHandler(getServer func(*http.Request) *Server, options *StreamableHTTPOptions) *StreamableHTTPHandler {
	return sdk.NewStreamableHTTPHandler(getServer, options)
}
