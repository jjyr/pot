package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
)

// This envelope travels only between the supervised stdio process and its local
// Host. Model arguments are decoded separately and cannot fill its binding.
type executionToolRequest struct {
	JobID       string          `json:"job_id" required:"true"`
	RunID       string          `json:"run_id" required:"true"`
	AssistantID string          `json:"assistant_id" required:"true"`
	Tool        string          `json:"tool" required:"true"`
	Input       json.RawMessage `json:"input" required:"true"`
}
type executionToolResponse struct {
	Value json.RawMessage     `json:"value,omitempty"`
	Error *agentlink.MCPError `json:"error,omitempty"`
}

var executionToolNames = []string{"pot_execution_context", "pot_directory", "pot_message", "pot_task_submit", "pot_task_read", "pot_task_input", "pot_task_cancel"}

func executionProblem(err error) *agentlink.MCPError {
	var problem agentlink.MCPError
	if errors.As(err, &problem) {
		return &problem
	}
	problem.Code = agentlink.MCPUnknown
	switch {
	case errors.Is(err, agentlink.ErrInvalid):
		problem.Code = agentlink.MCPInvalid
	case errors.Is(err, agentlink.ErrUnauthorized):
		problem.Code = agentlink.MCPUnauthorized
	case errors.Is(err, agentlink.ErrFenced):
		problem.Code = agentlink.MCPFenced
	case errors.Is(err, agentlink.ErrConflict):
		problem.Code = agentlink.MCPConflict
	case errors.Is(err, agentlink.ErrUnsupported):
		problem.Code = agentlink.MCPUnsupported
	case errors.Is(err, agentlink.ErrUnavailable), errors.Is(err, agentlink.ErrNotFound):
		problem.Code = agentlink.MCPUnavailable
	}
	return &problem
}

func addExecutionTools(server *mcp.Server, client hostClient, jobID, runID, assistantID string) {
	ref := toolSchema(map[string]any{"node_id": idSchema(), "kind": idSchema(), "agent_id": idSchema()}, "node_id", "kind", "agent_id")
	schemas := map[string]any{
		"pot_directory":   toolSchema(map[string]any{}),
		"pot_message":     toolSchema(map[string]any{"request_id": idSchema(), "message": toolSchema(map[string]any{"to": ref, "text": textSchema(65536)}, "to", "text")}, "request_id", "message"),
		"pot_task_submit": toolSchema(map[string]any{"request_id": idSchema(), "target": ref, "text": textSchema(65536)}, "request_id", "target", "text"),
		"pot_task_read":   toolSchema(map[string]any{"task_id": idSchema()}, "task_id"),
		"pot_task_input":  toolSchema(map[string]any{"request_id": idSchema(), "task_id": idSchema(), "run_id": idSchema(), "text": textSchema(24000)}, "request_id", "task_id", "run_id", "text"),
		"pot_task_cancel": toolSchema(map[string]any{"request_id": idSchema(), "task_id": idSchema(), "run_id": idSchema()}, "request_id", "task_id", "run_id"),
	}
	for _, name := range executionToolsFor(assistantID)[1:] {
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: "Call Pot using this active supervised execution. Reuse request IDs only for identical requests; reconcile unknown outcomes. No owner or human approval authority.", InputSchema: schemas[name]}, func(ctx context.Context, _ *mcp.CallToolRequest, raw json.RawMessage) (*mcp.CallToolResult, any, error) {
			request := executionToolRequest{JobID: jobID, RunID: runID, AssistantID: assistantID, Tool: name, Input: raw}
			var response executionToolResponse
			if err := client.call(ctx, "/api/v1/agent-host/execution", request, &response); err != nil {
				// A lost local response may follow remote acceptance. Never translate it
				// into permission to submit a new request or automatically replay a tool.
				response.Error = &agentlink.MCPError{Code: agentlink.MCPUnknown}
			}
			if response.Error != nil {
				return &mcp.CallToolResult{IsError: true, StructuredContent: response.Error, Content: []mcp.Content{&mcp.TextContent{Text: string(response.Error.Code)}}}, nil, nil
			}
			var value any
			if json.Unmarshal(response.Value, &value) != nil {
				return toolResult(nil, "invalid_payload"), nil, nil
			}
			return toolResult(value, ""), nil, nil
		})
	}
}

func (h *agentHost) executionHTTP(w http.ResponseWriter, r *http.Request) {
	var request executionToolRequest
	if !body(w, r, &request) {
		return
	}
	value, err := h.executionCall(r.Context(), request)
	response := executionToolResponse{}
	if err != nil {
		response.Error = executionProblem(err)
	} else {
		response.Value, _ = json.Marshal(value)
	}
	writeJSON(w, 200, response)
}

func executionToolsFor(assistantID string) []string {
	if assistantID == "" {
		return []string{"pot_execution_context"}
	}
	return executionToolNames
}
