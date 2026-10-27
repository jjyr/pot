package agenthost

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
)

// executionTools connects only to an already operated receiver Host. The model
// can ask for its verified context, but cannot supply an actor, run or credential.
// Remote callbacks need a separate trusted gateway integration; this context
// tool does not turn an ordinary model MCP login into an execution identity.
func executionTools(in io.Reader, out io.Writer, root string, adapter Runtime) error {
	jobID, runID := os.Getenv("POT_AGENT_JOB_ID"), os.Getenv("POT_AGENT_RUN_ID")
	if jobID == "" || runID == "" || os.Getenv("POT_AGENT_HOST_ROOT") != root {
		return agentlink.ErrUnauthorized
	}
	client, err := existingAgentHostClient(root, adapter)
	if err != nil {
		return err
	}
	defer client.client.CloseIdleConnections()
	if !client.healthy() {
		return agentlink.ErrUnavailable
	}
	return executionMCPServer(client, jobID, runID, os.Getenv("POT_AGENT_ASSISTANT_ID")).Run(context.Background(), &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}, MaxLineLength: 128 * 1024})
}

func executionMCPServer(client hostClient, jobID, runID, assistantID string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "pot-receiver-execution", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "pot_execution_context", Description: "Read this supervised execution's active actor, Task and run binding. This grants no owner or approval authority.", InputSchema: toolSchema(map[string]any{})}, func(ctx context.Context, _ *mcp.CallToolRequest, raw json.RawMessage) (*mcp.CallToolResult, any, error) {
		var input struct{}
		if err := agentlink.DecodeMCPContract(raw, &input); err != nil {
			return toolResult(nil, "invalid_payload"), nil, nil
		}
		request := hostToolInput{Operation: "execution_context", JobID: jobID, RunID: runID, FromAssistantID: assistantID}
		var binding agentlink.MCPExecutionBinding
		if err := client.call(ctx, "/api/v1/agent-host/tool", request, &binding); err != nil {
			return toolResult(nil, "execution_fenced"), nil, nil
		}
		return toolResult(binding, ""), nil, nil
	})
	return server
}

func (h *agentHost) executionContext(ctx context.Context, in hostToolInput) (agentlink.MCPExecutionBinding, error) {
	var binding agentlink.MCPExecutionBinding
	if in.JobID == "" || in.RunID == "" {
		return binding, agentlink.ErrUnauthorized
	}
	var raw string
	if err := h.tasks.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE id=?", in.JobID).Scan(&raw); err != nil {
		return binding, agentlink.ErrUnauthorized
	}
	var j job
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return binding, err
	}
	if j.Task.RunID != in.RunID || (j.Task.State != "running" && j.Task.State != "waiting_approval") {
		return binding, agentlink.ErrFenced
	}
	h.backend.mu.Lock()
	active := h.backend.active[in.JobID] != nil
	h.backend.mu.Unlock()
	if !active {
		return binding, agentlink.ErrFenced
	}
	task := protocol.AgentRef{NodeID: h.backend.nodeID, Kind: "task", AgentID: j.Task.ID}
	actor := task
	if j.Message != nil {
		actor = j.Task.Receiver.Address
	}
	if actor.Kind == "assistant" && in.FromAssistantID != actor.AgentID || actor.Kind == "task" && in.FromAssistantID != "" {
		return binding, agentlink.ErrUnauthorized
	}
	binding = agentlink.MCPExecutionBinding{Actor: actor, Task: task, RunID: in.RunID}
	return binding, binding.Validate()
}
