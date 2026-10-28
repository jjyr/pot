package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAntigravityExecutionSupportMustBeEnabledWithHookAndContextServer(t *testing.T) {
	cases := []struct {
		metadata string
		valid    bool
	}{
		{`[{"name":"pot","enabled":true,"hooks":["pot-execution-scope"],"mcpServers":["pot"]}]`, false},
		{`[{"name":"pot-receiver","enabled":false,"hooks":["pot-execution-scope"],"mcpServers":["pot_receiver"]}]`, false},
		{`[{"name":"pot-receiver","enabled":true,"hooks":[],"mcpServers":["pot_receiver"]}]`, false},
		{`[{"name":"pot-receiver","enabled":true,"hooks":["pot-execution-scope"],"mcpServers":[]}]`, false},
		{`[{"name":"pot-receiver","enabled":true,"hooks":["pot-execution-scope"],"mcpServers":["pot_receiver"]}]`, true},
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "agy")
		// A synthetic native metadata process proves fail-closed validation;
		// it does not prove a real agy client loaded or enforced a hook.
		if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'METADATA'\n"+tc.metadata+"\nMETADATA\n"), 0700); err != nil {
			t.Fatal(err)
		}
		err := verifyAgyPlugin(context.Background(), path, Session{Workspace: t.TempDir()})
		if (err == nil) != tc.valid {
			t.Fatalf("support admitted=%v want=%v: %v", err == nil, tc.valid, err)
		}
	}
}

// These tests exercise Pot's decision contract. Real CLI permission enforcement
// still needs native validation; "allow" delegates to the client's policy.
func TestAntigravityPolicyKeepsNativePermissionsAndReadOnlyScope(t *testing.T) {
	cases := []struct {
		name, job, run, assistant, mode, tool, server, remoteTool, want string
	}{
		{"ordinary shell", "", "", "", "read-only", "run_command", "", "", "allow"},
		{"ordinary remote", "", "", "", "read-only", "call_mcp_tool", "pot", "pot_send", "allow"},
		{"ordinary context", "", "", "", "read-only", "call_mcp_tool", "pot_receiver", "pot_execution_context", "allow"},
		{"read file", "task", "run", "", "read-only", "view_file", "", "", "allow"},
		{"shell denied", "task", "run", "", "read-only", "run_command", "", "", "deny"},
		{"write denied", "task", "run", "", "read-only", "write_to_file", "", "", "deny"},
		{"subagent denied", "task", "run", "", "read-only", "invoke_subagent", "", "", "deny"},
		{"bound context", "task", "run", "", "read-only", "call_mcp_tool", "pot_receiver", "pot_execution_context", "allow"},
		{"missing run", "task", "", "", "read-only", "call_mcp_tool", "pot_receiver", "pot_execution_context", "deny"},
		{"wrong server", "task", "run", "", "read-only", "call_mcp_tool", "other", "pot_execution_context", "deny"},
		{"unknown context tool", "task", "run", "", "read-only", "call_mcp_tool", "pot_receiver", "pot_send", "deny"},
		{"worker reverse denied", "task", "run", "", "read-only", "call_mcp_tool", "pot_receiver", "pot_message", "deny"},
		{"assistant reverse policy", "task", "run", "assistant", "read-only", "call_mcp_tool", "pot_receiver", "pot_message", "allow"},
		{"legacy pot denied", "task", "run", "", "read-only", "call_mcp_tool", "pot", "pot_send", "deny"},
		{"legacy prefixed pot denied", "task", "run", "", "read-only", "call_mcp_tool", "pot_pot", "pot_send", "deny"},
		{"assistant legacy denied", "task", "run", "assistant", "read-only", "call_mcp_tool", "pot", "pot_send", "deny"},
		{"workspace remote policy", "task", "run", "", "workspace-write", "call_mcp_tool", "other", "user_tool", "allow"},
		{"workspace legacy policy", "task", "run", "", "workspace-write", "call_mcp_tool", "pot", "pot_send", "allow"},
		{"workspace context policy", "task", "run", "", "workspace-write", "call_mcp_tool", "pot_receiver", "pot_execution_context", "allow"},
		{"workspace shell policy", "task", "run", "", "workspace-write", "run_command", "", "", "allow"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POT_AGENT_JOB_ID", tc.job)
			t.Setenv("POT_AGENT_RUN_ID", tc.run)
			t.Setenv("POT_AGENT_ASSISTANT_ID", tc.assistant)
			t.Setenv("POT_AGENT_MODE", tc.mode)
			payload, err := json.Marshal(map[string]any{"toolCall": map[string]any{"name": tc.tool, "args": map[string]string{"ServerName": tc.server, "ToolName": tc.remoteTool}}})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := agyPolicy(bytes.NewReader(payload), &out); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["decision"] != tc.want {
				t.Fatalf("decision=%s want=%s error=%v", out.String(), tc.want, err)
			}
			if result["permissionOverrides"] != nil || result["overwrite"] != nil {
				t.Fatal("hook changed native permissions or tool arguments", out.String())
			}
		})
	}
}
