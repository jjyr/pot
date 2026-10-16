package core

import "encoding/json"

func ExecutionReferenceSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"runtime_id":{"type":"string"},"target":{"oneOf":[{"type":"object","properties":{"kind":{"const":"host"}},"required":["kind"],"additionalProperties":false},{"type":"object","properties":{"kind":{"const":"sandbox"},"sandbox_id":{"type":"string"}},"required":["kind","sandbox_id"],"additionalProperties":false}]}},"required":["runtime_id","target"],"additionalProperties":false}`)
}
func ExecutionSelectorSchema() json.RawMessage {
	data, _ := json.Marshal(map[string]any{"oneOf": []any{map[string]any{"type": "string", "description": "Local host or sandbox:ID."}, ExecutionReferenceSchema()}, "description": "Execution destination. Omitted uses the current execution context. Use host for the same machine; policy determines approval. Task placement does not change."})
	return data
}

// ExecutionTools is the sole built-in execution schema source. The owner adds
// execution_target using ExecutionSelectorSchema only when routing is available.
func ExecutionTools(policy ShellSettings) []json.RawMessage {
	stringField := func() any { return map[string]any{"type": "string"} }
	boolean := func() any { return map[string]any{"type": "boolean"} }
	bounded := func(max uint64) any { return map[string]any{"type": "integer", "minimum": 1, "maximum": max} }
	schema := func(name, description string, properties map[string]any, required string) json.RawMessage {
		data, _ := json.Marshal(map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": []string{required}, "additionalProperties": false}})
		return data
	}
	return []json.RawMessage{
		schema("exec_command", "Run a shell command. Omit yield_time_ms to wait until exit; specify it to return a live session_id after a bounded wait. Sessions belong to this agent execution. Completion, cancellation, inactivity, explicit timeout, agent change or executor restart closes them. tty=true allocates a terminal with merged output; default uses pipes. Each command has its own cwd; cd/export do not affect later commands. Use write_stdin to interact. Output is bounded and streams while waiting.", map[string]any{"cmd": stringField(), "workdir": stringField(), "timeout_ms": bounded(policy.MaxTimeoutMS), "max_output_chars": bounded(policy.MaxOutputBytes), "yield_time_ms": bounded(policy.MaxYieldMS), "shell": map[string]any{"type": "string", "description": "Absolute path to sh, bash or zsh in the execution target."}, "login": map[string]any{"type": "boolean", "description": "Load login initialization; defaults to execution policy."}, "tty": map[string]any{"type": "boolean", "default": false}}, "cmd"),
		schema("write_stdin", "Send chars to a live process session or poll with empty chars. close_stdin sends EOF. Sessions belong to this agent session and original execution target; use that same target. interrupt sends SIGINT; terminate stops the process; rows/cols resize a PTY. Cancellation/restart invalidates sessions.", map[string]any{"session_id": stringField(), "chars": stringField(), "close_stdin": boolean(), "interrupt": boolean(), "terminate": boolean(), "rows": bounded(1000), "cols": bounded(1000), "yield_time_ms": bounded(policy.MaxYieldMS), "max_output_chars": bounded(policy.MaxOutputBytes)}, "session_id"),
		schema("apply_patch", "Edit text files with Codex patch syntax. Pass patch: *** Begin Patch\n*** Add File: path\n+text\n*** Update File: path\n@@\n context\n-old\n+new\n*** Delete File: path\n*** End Patch. Update supports *** Move to: path and *** End of File. Paths are relative to the workspace.", map[string]any{"patch": stringField()}, "patch"),
	}
}
