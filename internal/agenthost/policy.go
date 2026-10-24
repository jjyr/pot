package agenthost

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

// A global plugin hook is inert in ordinary Antigravity conversations. The host
// binds the execution identity and scope through the supervised child's env.
func agyPolicy(in io.Reader, out io.Writer) error {
	var payload struct {
		ToolCall struct {
			Name string         `json:"name"`
			Args map[string]any `json:"args"`
		} `json:"toolCall"`
	}
	if err := json.NewDecoder(io.LimitReader(in, maxBody)).Decode(&payload); err != nil {
		return err
	}
	decision, reason := "allow", ""
	if os.Getenv("POT_AGENT_JOB_ID") != "" {
		name := payload.ToolCall.Name
		if name == "call_mcp_tool" {
			server, _ := payload.ToolCall.Args["ServerName"].(string)
			tool, _ := payload.ToolCall.Args["ToolName"].(string)
			if server == "" {
				server, _ = payload.ToolCall.Args["server_name"].(string)
			}
			if tool == "" {
				tool, _ = payload.ToolCall.Args["tool_name"].(string)
			}
			if (server == "pot" || server == "pot_pot") && (tool == "pot_connections" || tool == "pot_assistants" || tool == "pot_send" || tool == "pot_message_receipt") {
				decision = "auto_approve"
			} else if os.Getenv("POT_AGENT_MODE") == "read-only" {
				decision = "deny"
				reason = "pot_read_only_scope"
			}
		} else if os.Getenv("POT_AGENT_MODE") == "read-only" {
			allowed := map[string]bool{"view_file": true, "read_file": true, "list_dir": true, "find_by_name": true, "grep_search": true, "read_url_content": true, "search_web": true, "list_resources": true, "read_resource": true, "list_permissions": true, "finish": true, "wait": true, "wait_5_seconds": true}
			if !allowed[name] {
				decision = "deny"
				reason = "pot_read_only_scope"
			}
		}
		// Hooks cannot implement a remote interactive approval protocol. Default
		// policy still owns all non-read-only execution and explicit deny rules.
	}
	return json.NewEncoder(out).Encode(map[string]string{"decision": decision, "reason": strings.TrimSpace(reason)})
}
