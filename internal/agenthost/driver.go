package agenthost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/pkg/agentlink"
)

// ReceiverCapabilities describes the implemented receiver extension, not the
// full local Host API. Events are durable lifecycle snapshots, input is terminal
// continuation. Native resume is an implementation detail, not replay authority.
// Local Codex review and assistant mail remain separate existing Host paths;
// they are not yet connected to the receiver extension. Paths are not file bytes.
func (r Runtime) ReceiverCapabilities() agentlink.Description {
	d := agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{}}
	if r.Kind == "codex" || r.Kind == "antigravity" {
		d.Operations = []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.TaskEvents}
	}
	return d
}

// Native children inherit the receiver's local login/configuration. Reserved
// execution context must be replaced, never appended to inherited duplicates:
// subprocess libraries disagree about whether the first or last duplicate wins.
// No OAuth receiver bearer or caller-selected identity belongs in this context.
func nativeEnvironment(parent []string, s Session) []string {
	env := make([]string, 0, len(parent)+6)
	for _, value := range parent {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(key, "POT_AGENT_") {
			env = append(env, value)
		}
	}
	return append(env,
		"POT_AGENT_ASSISTANT_ID="+s.AssistantID,
		"POT_AGENT_JOB_ID="+s.JobID,
		"POT_AGENT_RUN_ID="+s.RunID,
		"POT_AGENT_MODE="+s.Mode,
		"POT_AGENT_HOST_ROOT="+s.HostRoot,
	)
}

// Codex's thread config uses the same dotted overrides as its CLI. Inject only
// the receiver-local context server, with explicit env: native stdio transports
// need not inherit arbitrary parent variables. This is neither a global MCP
// login nor a replacement for the user's independent remote Pot server.
func codexExecutionConfig(s Session) (map[string]any, error) {
	if s.RunID == "" {
		return nil, nil
	}
	if s.JobID == "" || !filepath.IsAbs(s.HostRoot) {
		return nil, errors.New("codex_invalid_execution_binding")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("codex_execution_tools_unavailable")
	}
	env := map[string]string{}
	for _, value := range nativeEnvironment(nil, s) {
		key, val, _ := strings.Cut(value, "=")
		env[key] = val
	}
	return map[string]any{
		"mcp_servers.pot_receiver": map[string]any{
			"command":       executable,
			"args":          []string{"execution-tools", "--root", s.HostRoot},
			"env":           env,
			"enabled":       true,
			"required":      true,
			"enabled_tools": []string{"pot_execution_context"},
		},
	}, nil
}
