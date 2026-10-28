package control

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/protocol"
)

// Observe the actual handler parked on dispatchMu rather than assume a request
// has reached admission after a delay. These tests are deliberately sequential;
// there is only one in-flight tool call while the dispatch lock is held.
func waitMCPDispatchHandler(t *testing.T) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	buf := make([]byte, 64*1024)
	for {
		n := runtime.Stack(buf, true)
		if n == len(buf) {
			if len(buf) >= 32*1024*1024 {
				t.Fatal("goroutine stack dump exceeded limit")
			}
			buf = make([]byte, len(buf)*2)
			continue
		}
		for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(stack, "(*MCPAccess).tool.func1") &&
				(strings.Contains(stack, "sync.(*Mutex).Lock") || strings.Contains(stack, "internal/sync.(*Mutex).lockSlow")) {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("MCP handler did not reach dispatch lock")
		}
	}
}

func TestMCPDispatchRechecksCredentialAfterWaiting(t *testing.T) {
	for _, invalidation := range []string{"revoked", "rotated", "expired"} {
		t.Run(invalidation, func(t *testing.T) {
			f := mcpAccessTestFixture(t)
			grant := f.grant(t, true)
			session := f.session(t, grant)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			task := protocol.AgentRef{NodeID: "receiver-fixture", Kind: "task", AgentID: "task"}
			binding := agentlink.MCPExecutionBinding{Actor: task, Task: task, RunID: "run"}
			input := agentlink.MCPNodeExecutionEnd{ExecutionID: "execution", Binding: binding}
			if _, err := f.queue.DB.Exec(`INSERT INTO mcp_node_executions VALUES(?,?,?,?,?,1)`, grant.Connection, mcpJSON(task), input.ExecutionID, mcpJSON(binding), ""); err != nil {
				t.Fatal(err)
			}
			fixtureCall(t, session, "pot_receiver_execution_end", input, nil)
			var active int
			if err := f.queue.DB.QueryRow(`SELECT active FROM mcp_node_executions WHERE connection_id=?`, grant.Connection).Scan(&active); err != nil || active != 0 {
				t.Fatalf("valid credential did not end execution: active=%d err=%v", active, err)
			}
			if _, err := f.queue.DB.Exec(`UPDATE mcp_node_executions SET active=1 WHERE connection_id=?`, grant.Connection); err != nil {
				t.Fatal(err)
			}

			f.queue.dispatchMu.Lock()
			locked := true
			defer func() {
				if locked {
					f.queue.dispatchMu.Unlock()
				}
			}()
			type outcome struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := session.CallTool(ctx, "pot_receiver_execution_end", json.RawMessage(mcpJSON(input)))
				done <- outcome{result, err}
			}()
			waitMCPDispatchHandler(t)
			// Invalidation commits while the handler is waiting, before dispatch.
			var err error
			switch invalidation {
			case "revoked":
				err = f.oauth.Revoke(ctx, grant.Connection)
			case "rotated":
				_, err = f.oauth.options.DB.Exec(`UPDATE mcp_oauth_tokens SET digest=? WHERE connection_id=?`, mcpCredentialDigest("replacement-credential"), grant.Connection)
			case "expired":
				_, err = f.oauth.options.DB.Exec(`UPDATE mcp_oauth_tokens SET expires_at=? WHERE connection_id=?`, time.Now().Add(-time.Second).Unix(), grant.Connection)
			}
			if err != nil {
				t.Fatal(err)
			}
			f.queue.dispatchMu.Unlock()
			locked = false
			var out outcome
			select {
			case out = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := f.queue.DB.QueryRow(`SELECT active FROM mcp_node_executions WHERE connection_id=?`, grant.Connection).Scan(&active); err != nil || active != 1 {
				t.Fatalf("%s credential mutated execution after dispatch wait: active=%d err=%v", invalidation, active, err)
			}
			var failure agentlink.MCPError
			if out.err != nil || out.result == nil || !out.result.IsError || mcpRead(mcpJSON(out.result.StructuredContent), &failure) != nil || failure.Code != agentlink.MCPUnauthorized {
				t.Fatalf("expected unauthorized after dispatch wait: result=%s err=%v", mcpJSON(out.result), out.err)
			}
		})
	}
}
