// Package mcp supplies protocol sessions independent of plugin, execution and
// product configuration. Process supervision and credentials belong to callers.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool = sdk.Tool
type ToolAnnotations = sdk.ToolAnnotations
type CallToolResult = sdk.CallToolResult
type TextContent = sdk.TextContent
type Server = sdk.Server
type Implementation = sdk.Implementation
type ServerOptions = sdk.ServerOptions
type CallToolRequest = sdk.CallToolRequest
type Content = sdk.Content
type IOTransport = sdk.IOTransport
type Transport = sdk.Transport
type StreamableClientTransport = sdk.StreamableClientTransport

func NewServer(info *Implementation, options *ServerOptions) *Server {
	return sdk.NewServer(info, options)
}

func AddTool(server *Server, tool *Tool, handler func(context.Context, *CallToolRequest, json.RawMessage) (*CallToolResult, any, error)) {
	sdk.AddTool(server, tool, handler)
}

// Extension methods are opt-in on a connection. The protocol layer attaches no
// business semantics or authority to their names. The host validates capability
// negotiation and the scope of each callback.
type Handler func(context.Context, string, json.RawMessage) (any, error)
type Options struct {
	Experimental map[string]any
	Handle       Handler
	Methods      []string
	// Inspect sees incoming messages before protocol handlers, for host-specific limits
	// and credential disclosure checks. It must not log credential-bearing bytes.
	Inspect func([]byte) error
}
type Session struct{ session *sdk.ClientSession }

func ConnectStdio(ctx context.Context, reader io.ReadCloser, writer io.WriteCloser, options Options) (*Session, error) {
	return Connect(ctx, &sdk.IOTransport{Reader: reader, Writer: writer, MaxLineLength: 3 * 1024 * 1024}, options)
}

// Connect accepts an SDK transport so callers can inject stdio, HTTP or in-memory
// IO without moving process or authorization policy into this package.
func Connect(ctx context.Context, transport Transport, options Options) (*Session, error) {
	var wrapped *extensionTransport
	if len(options.Methods) > 0 || options.Inspect != nil {
		if _, httpTransport := transport.(*sdk.StreamableClientTransport); httpTransport {
			return nil, errors.New("custom incoming methods require stdio transport")
		}
		wrapped = &extensionTransport{Transport: transport, options: options}
		transport = wrapped
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "pot", Version: "1"}, &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{Experimental: options.Experimental}, MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true}})
	// Both SDKs negotiate the modern initialize protocol, including a server's
	// supported older version. Do not pin a server to one exact version.
	session, err := client.Connect(ctx, transport, &sdk.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		return nil, err
	}
	if wrapped != nil {
		wrapped.connection.ready.Store(true)
	}
	return &Session{session: session}, nil
}
func (s *Session) InitializeResult() *sdk.InitializeResult { return s.session.InitializeResult() }
func (s *Session) Close() error                            { return s.session.Close() }
func (s *Session) ListTools(ctx context.Context) ([]*Tool, error) {
	tools := []*Tool{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	catalogBytes := 0
	for page := 0; page < 128; page++ {
		result, err := s.session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if len(tools)+len(result.Tools) > 128 {
			return nil, errors.New("MCP tool limit")
		}
		for _, tool := range result.Tools {
			if tool == nil || tool.Name == "" || len(tool.Name) > 128 || seen[tool.Name] {
				return nil, errors.New("invalid MCP tool")
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil || len(schema) > 65536 || len(tool.Description) > 8192 {
				return nil, errors.New("MCP tool schema limit")
			}
			raw, err := json.Marshal(tool)
			if err != nil {
				return nil, err
			}
			catalogBytes += len(raw)
			if catalogBytes > 128*1024 {
				return nil, errors.New("MCP catalog limit")
			}
			seen[tool.Name] = true
			tools = append(tools, tool)
		}
		cursor = result.NextCursor
		if cursor == "" {
			return tools, nil
		}
		if len(cursor) > 4096 || cursors[cursor] {
			return nil, errors.New("invalid MCP pagination")
		}
		cursors[cursor] = true
	}
	return nil, errors.New("MCP page limit")
}
func (s *Session) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(args, &object); err != nil || object == nil {
		return nil, errors.New("MCP arguments must be an object")
	}
	return s.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
}

type extensionTransport struct {
	sdk.Transport
	options    Options
	connection *extensionConnection
}

func (t *extensionTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	connection, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	methods := map[string]bool{}
	for _, method := range t.options.Methods {
		methods[method] = true
	}
	t.connection = &extensionConnection{Connection: connection, options: t.options, methods: methods}
	return t.connection, nil
}

// SDK v1.8 exposes server custom-method registration, but not client incoming
// custom methods. This small Connection decorator intercepts only the declared
// extension requests. The SDK still owns all framing, IDs and standard methods.
type extensionConnection struct {
	sdk.Connection
	options Options
	methods map[string]bool
	ready   atomic.Bool
	bytes   int
	calls   int
}

func (c *extensionConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		message, err := c.Connection.Read(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := jsonrpc.EncodeMessage(message)
		if err != nil {
			return nil, err
		}
		c.bytes += len(raw)
		if c.bytes > 8*1024*1024 {
			return nil, errors.New("MCP exchange limit")
		}
		if c.options.Inspect != nil {
			if err = c.options.Inspect(raw); err != nil {
				return nil, err
			}
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok || !request.ID.IsValid() || !c.methods[request.Method] {
			return message, nil
		}
		c.calls++
		if c.calls > 128 {
			return nil, errors.New("MCP extension request limit")
		}
		var value any
		if !c.ready.Load() || c.options.Handle == nil {
			err = errors.New("MCP extension unavailable before initialization")
		} else {
			value, err = c.options.Handle(ctx, request.Method, request.Params)
		}
		var result json.RawMessage
		if err == nil {
			result, err = json.Marshal(value)
		}
		if err != nil {
			err = &jsonrpc.Error{Code: -32000, Message: err.Error()}
		}
		if err = c.Connection.Write(ctx, &jsonrpc.Response{ID: request.ID, Result: result, Error: err}); err != nil {
			return nil, fmt.Errorf("MCP extension response: %w", err)
		}
	}
}
