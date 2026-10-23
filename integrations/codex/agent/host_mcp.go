package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
)

type hostToolInput struct {
	JobID           string  `json:"job_id,omitempty"`
	Operation       string  `json:"operation"`
	ConnectionID    string  `json:"connection_id,omitempty"`
	RequestID       string  `json:"request_id,omitempty"`
	MessageID       string  `json:"message_id,omitempty"`
	Text            string  `json:"text,omitempty"`
	AssistantID     string  `json:"assistant_id,omitempty"`
	FromAssistantID string  `json:"from_assistant_id,omitempty"`
	Name            *string `json:"name,omitempty"`
	AvatarPath      string  `json:"avatar_path,omitempty"`
	ResetAvatar     bool    `json:"reset_avatar,omitempty"`
}

func (h *agentHost) tool(ctx context.Context, in hostToolInput) (any, error) {
	switch in.Operation {
	case "connections":
		return h.links.List(ctx)
	case "assistants":
		return h.backend.Directory(ctx)
	case "profile":
		id := in.FromAssistantID
		if id == "" {
			id = in.AssistantID
		}
		if id == "" {
			return nil, agentlink.ErrInvalid
		}
		if in.FromAssistantID != "" && in.AssistantID != "" && in.AssistantID != id {
			return nil, agentlink.ErrUnauthorized
		}
		if in.ResetAvatar && in.AvatarPath != "" {
			return nil, agentlink.ErrInvalid
		}
		a, err := h.backend.resolve(ctx, id)
		if err != nil {
			return nil, err
		}
		r := agentlink.ProfileRequest{AssistantID: id, Action: "get", Revision: a.Revision, Name: in.Name}
		if in.ResetAvatar {
			empty := ""
			r.Avatar = &empty
		}
		if in.AvatarPath != "" {
			image, err := h.backend.profileImage(ctx, id, in.AvatarPath)
			if err != nil {
				return nil, err
			}
			r.Avatar = &image
		}
		if r.Name != nil || r.Avatar != nil {
			r.Action = "update"
		}
		return h.backend.Profile(ctx, r)
	case "send":
		if in.JobID != "" && in.FromAssistantID == "" {
			return nil, agentlink.ErrUnauthorized
		}
		if in.AssistantID == "" {
			return nil, errors.New("agent_link_target_required")
		}
		connections, err := h.links.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range connections {
			if c.ID == in.ConnectionID && c.State != "revoked" {
				from, err := h.backend.resolve(ctx, in.FromAssistantID)
				if err != nil {
					return nil, err
				}
				directory, err := h.links.Directory(ctx, c.ID)
				if err != nil {
					return nil, err
				}
				target := in.AssistantID
				for _, a := range directory.Assistants() {
					if a.Address.AgentID == target {
						return h.mailbox.sendFrom(ctx, h.backend.identity(from).Address, a.Address, in.RequestID, in.Text, in.JobID)
					}
				}
				return nil, errors.New("agent_link_not_found")
			}
		}
		return nil, errors.New("agent_link_not_found")
	case "receipt":
		var state string
		err := h.tasks.QueryRowContext(ctx, "SELECT state FROM agent_messages WHERE id=? AND direction='outgoing'", in.MessageID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("message_not_found")
		}
		if err != nil {
			return nil, err
		}
		return map[string]string{"message_id": in.MessageID, "state": state}, nil
	default:
		return nil, errors.New("unknown_message_operation")
	}
}

type nopWriteCloser struct{ io.Writer }

func (n nopWriteCloser) Close() error { return nil }
func hostMCP(in io.Reader, out io.Writer, root string) error {
	client, err := ensureAgentHost(root)
	if err != nil {
		return err
	}
	defer client.client.CloseIdleConnections()
	return hostMCPServer(client).Run(context.Background(), &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}, MaxLineLength: 128 * 1024})
}
func hostMCPServer(client hostClient) *mcp.Server {
	assistantID := os.Getenv("POT_CODEX_ASSISTANT_ID")
	jobID := os.Getenv("POT_CODEX_JOB_ID")
	server := mcp.NewServer(&mcp.Implementation{Name: "pot-agent-link", Version: hostVersion}, nil)
	tools := []*mcp.Tool{
		{Name: "pot_open_connections", Description: "Prepare the local Pot connections page. Open the returned temporary URL in the Codex in-app browser using open_in_codex (target type browser). Do not print or share the URL. It grants short-lived connection-page access, never the durable owner credential. Invitations stay in the connection page and are never returned to this tool.", InputSchema: toolSchema(map[string]any{})},
		{Name: "pot_connections", Description: "List established Agent Link nodes and their available assistants and capabilities. Receiver-owned task execution uses the same authenticated connection; owner administration remains local.", InputSchema: toolSchema(map[string]any{})},
		{Name: "pot_assistants", Description: "List this node's persistent virtual assistants and its default. Each assistant owns its message session. Explicit Codex tasks own separate sessions.", InputSchema: toolSchema(map[string]any{})},
		{Name: "pot_update_profile", Description: "Read or update this Pot assistant's saved name and avatar when the user requests it. A running virtual assistant is bound to its own identity; outside one, supply an explicit assistant_id from pot_assistants. Omit all edits to read. avatar_path is a workspace-relative PNG, JPEG or WebP file up to 8 MiB; reset_avatar restores the original character. Changes retain the Codex thread and appear in connected Pot directories.", InputSchema: toolSchema(map[string]any{"assistant_id": idSchema(), "name": textSchema(80), "avatar_path": textSchema(4096), "reset_avatar": map[string]any{"type": "boolean"}})},
		{Name: "pot_send", Description: "Send an explicitly authorized peer message to an explicit assistant_id from the connection directory. No default recipient is selected. The host binds the processing parent from this virtual assistant's active message, preserving its conversation and causal bound. Reuse request_id only for an identical retry. Durable acceptance is not remote assistant completion.", InputSchema: toolSchema(map[string]any{"connection_id": idSchema(), "assistant_id": idSchema(), "request_id": idSchema(), "text": textSchema(65536)}, "connection_id", "assistant_id", "request_id", "text")},
		{Name: "pot_message_receipt", Description: "Inspect an outgoing message's transport receipt. Received means durable remote inbox acceptance, not remote processing or task execution.", InputSchema: toolSchema(map[string]any{"message_id": idSchema()}, "message_id")},
	}
	for _, tool := range tools {
		mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args json.RawMessage) (*mcp.CallToolResult, any, error) {
			if tool.Name == "pot_open_connections" {
				var page map[string]any
				if err := client.call(ctx, "/api/v1/agent-host/browser", map[string]any{}, &page); err != nil {
					return toolResult(nil, "local_browser_prepare_failed"), nil, nil
				}
				return toolResult(page, ""), nil, nil
			}
			var data hostToolInput
			if err := json.Unmarshal(args, &data); err != nil {
				return toolResult(nil, "invalid_payload"), nil, nil
			}
			data.FromAssistantID = assistantID
			data.JobID = jobID
			operations := map[string]string{"pot_connections": "connections", "pot_assistants": "assistants", "pot_update_profile": "profile", "pot_send": "send", "pot_message_receipt": "receipt"}
			data.Operation = operations[tool.Name]
			var value any
			err := client.call(ctx, "/api/v1/agent-host/tool", data, &value)
			if err != nil {
				return toolResult(nil, "agent_message_operation_failed"), nil, nil
			}
			return toolResult(value, ""), nil, nil
		})
	}
	return server
}
