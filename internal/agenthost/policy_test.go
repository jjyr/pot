package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		err := verifyAgyPlugin(context.Background(), path)
		if (err == nil) != tc.valid {
			t.Fatalf("support admitted=%v want=%v: %v", err == nil, tc.valid, err)
		}
	}
}

func TestAntigravityPolicyKeepsReadOnlyEnforcementAndBoundToolIdentity(t *testing.T) {
	t.Setenv("POT_AGENT_JOB_ID", "task")
	t.Setenv("POT_AGENT_RUN_ID", "run")
	t.Setenv("POT_AGENT_MODE", "read-only")
	for _, tc := range []struct{ payload, want string }{
		{`{"toolCall":{"name":"view_file","args":{}}}`, "allow"},
		{`{"toolCall":{"name":"run_command","args":{}}}`, "deny"},
		{`{"toolCall":{"name":"write_to_file","args":{}}}`, "deny"},
		{`{"toolCall":{"name":"invoke_subagent","args":{}}}`, "deny"},
		{`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"pot_receiver","ToolName":"pot_execution_context"}}}`, "auto_approve"},
		{`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"other","ToolName":"pot_execution_context"}}}`, "deny"},
		{`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"pot_receiver","ToolName":"pot_send"}}}`, "deny"},
		{`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"pot","ToolName":"pot_send"}}}`, "auto_approve"},
	} {
		var out bytes.Buffer
		if err := agyPolicy(strings.NewReader(tc.payload), &out); err != nil {
			t.Fatal(err)
		}
		var decision struct {
			Decision string `json:"decision"`
		}
		if err := json.Unmarshal(out.Bytes(), &decision); err != nil || decision.Decision != tc.want {
			t.Fatal(out.String(), err, tc.want)
		}
	}
	t.Setenv("POT_AGENT_MODE", "workspace-write")
	var out bytes.Buffer
	if err := agyPolicy(strings.NewReader(`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"other","ToolName":"user_tool"}}}`), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"decision":"allow"`) {
		t.Fatal("existing native MCP policy changed", out.String())
	}
}
