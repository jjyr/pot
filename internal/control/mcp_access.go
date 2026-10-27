package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
)

// MCPAccessOptions does not accept an owner operation Port. Remote callers enter
// a collaboration-only typed boundary and never gain authority from SDK metadata.
type MCPAccessOptions struct {
	OAuth *MCPOAuthServer
	Queue *MCPAccessQueue
	// ReceiverCapabilities supplies the provisioned driver's effective description.
	// A nil callback disables registration, rather than trusting an advertisement.
	ReceiverCapabilities func(context.Context, agentlink.MCPReceiver, protocol.AgentRef) (agentlink.Description, error)
}
type MCPAccess struct {
	options MCPAccessOptions
	sdk     *mcp.Server
	handler http.Handler
}

func NewMCPAccess(o MCPAccessOptions) (*MCPAccess, error) {
	if o.OAuth == nil || o.Queue == nil || o.Queue.DB == nil || o.Queue.Tasks == nil || o.Queue.Tasks.DB() != o.Queue.DB || o.Queue.NodeID != o.OAuth.options.NodeID {
		return nil, agentlink.ErrInvalid
	}
	o.Queue.CheckConnection = o.OAuth.CheckConnection
	a := &MCPAccess{options: o, sdk: mcp.NewServer(&mcp.Implementation{Name: "pot", Version: "1"}, nil)}
	a.tools()
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.sdk }, &mcp.StreamableHTTPOptions{JSONResponse: true, SessionTimeout: 5 * time.Minute})
	a.handler = mcp.RequireBearerToken(a.verify, &mcp.RequireBearerTokenOptions{ResourceMetadataURL: o.OAuth.origin + "/.well-known/oauth-protected-resource/mcp", Scopes: []string{mcpAccessScope}})(transport)
	return a, nil
}

// ServeHTTP mounts only /mcp and OAuth routes. No listener or owner API proxy is
// created; the deployment/browser host retains its own routing and lifecycle.
func (a *MCPAccess) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		a.options.OAuth.ServeHTTP(w, r)
		return
	}
	// An explicit Origin, when supplied, must be the deployer's configured origin.
	if origin := r.Header.Get("Origin"); origin != "" && origin != a.options.OAuth.origin {
		mcpOAuthError(w, 403, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, agentlink.MCPMaxPayloadBytes)
	a.handler.ServeHTTP(w, r)
}
func (a *MCPAccess) Close() {
	for session := range a.sdk.Sessions() {
		_ = session.Close()
	}
}
func (a *MCPAccess) verify(ctx context.Context, token string, r *http.Request) (*mcp.TokenInfo, error) {
	return a.options.OAuth.Verify(ctx, token, r)
}
func mcpDecodeBody(w http.ResponseWriter, r *http.Request, limit int64, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		return agentlink.ErrInvalid
	}
	return agentlink.DecodeMCPContract(raw, value)
}
func mcpToolError(err error) *mcp.CallToolResult {
	code := agentlink.MCPUnknown
	switch {
	case errors.Is(err, agentlink.ErrInvalid):
		code = agentlink.MCPInvalid
	case errors.Is(err, agentlink.ErrUnauthorized):
		code = agentlink.MCPUnauthorized
	case errors.Is(err, agentlink.ErrUnsupported):
		code = agentlink.MCPUnsupported
	case errors.Is(err, agentlink.ErrConflict):
		code = agentlink.MCPConflict
	case errors.Is(err, agentlink.ErrFenced):
		code = agentlink.MCPFenced
	case errors.Is(err, agentlink.ErrNotFound), errors.Is(err, agentlink.ErrUnavailable):
		code = agentlink.MCPUnavailable
	}
	return &mcp.CallToolResult{IsError: true, StructuredContent: agentlink.MCPError{Code: code}, Content: []mcp.Content{&mcp.TextContent{Text: string(code)}}}
}
func mcpToolResult(value any) *mcp.CallToolResult {
	raw, _ := json.Marshal(value)
	return &mcp.CallToolResult{StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}
func mcpCaller(request *mcp.CallToolRequest) (agentlink.MCPCaller, error) {
	if request.Extra == nil || request.Extra.TokenInfo == nil {
		return agentlink.MCPCaller{}, agentlink.ErrUnauthorized
	}
	caller, ok := request.Extra.TokenInfo.Extra["caller"].(agentlink.MCPCaller)
	if !ok || caller.ConnectionID() != request.Extra.TokenInfo.UserID {
		return agentlink.MCPCaller{}, agentlink.ErrUnauthorized
	}
	return caller, nil
}
func mcpReceiver(request *mcp.CallToolRequest) (agentlink.MCPReceiver, error) {
	caller, e := mcpCaller(request)
	if e != nil {
		return agentlink.MCPReceiver{}, e
	}
	node, _ := request.Extra.TokenInfo.Extra["receiver_node"].(string)
	return agentlink.NewMCPReceiver(caller.ConnectionID(), node)
}
func mcpSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func mcpStringSchema() map[string]any { return map[string]any{"type": "string"} }
func mcpRefSchema() map[string]any {
	return mcpSchema(map[string]any{"node_id": mcpStringSchema(), "kind": mcpStringSchema(), "agent_id": mcpStringSchema()}, "node_id", "kind", "agent_id")
}

// Contract schemas use the same typed decoder at admission, including nested
// additionalProperties. The schema is derived only from known Go DTO fields.
func (a *MCPAccess) tool(name string, schema any, fn func(context.Context, *mcp.CallToolRequest, json.RawMessage) (any, error)) {
	mcp.AddTool(a.sdk, &mcp.Tool{Name: name, InputSchema: schema}, func(ctx context.Context, request *mcp.CallToolRequest, raw json.RawMessage) (*mcp.CallToolResult, any, error) {
		// Reverify the credential at tool entry as well as HTTP entry. Queued SDK
		// requests cannot outlive a revocation check by reusing a session principal.
		if request.Extra == nil || request.Extra.TokenInfo == nil {
			return mcpToolError(agentlink.ErrUnauthorized), nil, nil
		}
		parts := strings.Fields(request.Extra.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return mcpToolError(agentlink.ErrUnauthorized), nil, nil
		}
		current, e := a.options.OAuth.Verify(ctx, parts[1], nil)
		if e != nil || current.UserID != request.Extra.TokenInfo.UserID {
			return mcpToolError(agentlink.ErrUnauthorized), nil, nil
		}
		value, e := fn(ctx, request, raw)
		if e != nil {
			return mcpToolError(e), nil, nil
		}
		return mcpToolResult(value), nil, nil
	})
}
func (a *MCPAccess) tools() {
	a.tool("pot_directory", mcpSchema(map[string]any{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var input struct{}
		if e := agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		if _, e := mcpCaller(r); e != nil {
			return nil, e
		}
		return a.options.Queue.Directory(ctx)
	})
	a.tool("pot_task_submit", mcpSchema(map[string]any{"request_id": mcpStringSchema(), "target": mcpRefSchema(), "text": mcpStringSchema()}, "request_id", "target", "text"), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var input agentlink.MCPSubmitInput
		if e := agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := mcpCaller(r)
		if e != nil {
			return nil, e
		}
		return a.options.Queue.Submit(ctx, c, input)
	})
	a.tool("pot_task_read", mcpSchema(map[string]any{"task_id": mcpStringSchema()}, "task_id"), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var input struct {
			TaskID string `json:"task_id" required:"true"`
		}
		if e := agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := mcpCaller(r)
		if e != nil {
			return nil, e
		}
		return a.options.Queue.Task(ctx, c, input.TaskID)
	})
	a.tool("pot_task_input", mcpSchema(map[string]any{"request_id": mcpStringSchema(), "task_id": mcpStringSchema(), "run_id": mcpStringSchema(), "text": mcpStringSchema()}, "request_id", "task_id", "run_id", "text"), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var input agentlink.MCPTaskInput
		if e := agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := mcpCaller(r)
		if e != nil {
			return nil, e
		}
		return a.options.Queue.Control(ctx, c, &input, nil)
	})
	a.tool("pot_task_cancel", mcpSchema(map[string]any{"request_id": mcpStringSchema(), "task_id": mcpStringSchema(), "run_id": mcpStringSchema()}, "request_id", "task_id", "run_id"), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		var input agentlink.MCPCancelInput
		if e := agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		c, e := mcpCaller(r)
		if e != nil {
			return nil, e
		}
		return a.options.Queue.Control(ctx, c, nil, &input)
	})
	a.tool("pot_receiver_register", mcpContractSchema(agentlink.MCPReceiverRegistration{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		var input agentlink.MCPReceiverRegistration
		if e = agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		if a.options.ReceiverCapabilities == nil {
			return nil, agentlink.ErrUnsupported
		}
		actual, e := a.options.ReceiverCapabilities(ctx, receiver, input.Receiver.Address)
		if e != nil {
			return nil, e
		}
		if e = a.options.Queue.Register(ctx, receiver, input, actual); e != nil {
			return nil, e
		}
		return map[string]string{"connection_id": receiver.ConnectionID(), "node_id": receiver.NodeID()}, nil
	})
	a.tool("pot_receiver_claim", mcpSchema(map[string]any{"request_id": mcpStringSchema(), "wait_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 30000}}, "request_id", "wait_ms"), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		var input struct {
			RequestID string `json:"request_id" required:"true"`
			WaitMS    int64  `json:"wait_ms" required:"true"`
		}
		if e = agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		if input.WaitMS < 0 || input.WaitMS > 30000 {
			return nil, agentlink.ErrInvalid
		}
		result, e := a.options.Queue.Claim(ctx, receiver, input.RequestID, time.Duration(input.WaitMS)*time.Millisecond)
		if result == nil && e == nil {
			return map[string]any{}, nil
		}
		return result, e
	})
	a.tool("pot_receiver_ack", mcpContractSchema(agentlink.MCPDeliveryAck{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		var input agentlink.MCPDeliveryAck
		if e = agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		return input, a.options.Queue.Ack(ctx, receiver, input)
	})
	a.tool("pot_receiver_admit", mcpContractSchema(agentlink.MCPRunAdmission{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		var input agentlink.MCPRunAdmission
		if e = agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		return input, a.options.Queue.Admit(ctx, receiver, input)
	})
	a.tool("pot_receiver_event", mcpContractSchema(agentlink.MCPExecutionEvent{}), func(ctx context.Context, r *mcp.CallToolRequest, raw json.RawMessage) (any, error) {
		receiver, e := mcpReceiver(r)
		if e != nil {
			return nil, e
		}
		var input agentlink.MCPExecutionEvent
		if e = agentlink.DecodeMCPContract(raw, &input); e != nil {
			return nil, agentlink.ErrInvalid
		}
		return a.options.Queue.Event(ctx, receiver, input)
	})
}
