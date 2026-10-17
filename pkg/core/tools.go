package core

import (
	_ "embed"
	"encoding/json"
)

//go:embed product-tools.json
var productToolsJSON []byte

// ProductTools retains the original shared product tool contracts.
func ProductTools() []json.RawMessage {
	var tools []json.RawMessage
	if err := DecodeJSON(productToolsJSON, &tools); err != nil {
		panic(err)
	}
	return tools
}

func ExecutionReferenceSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"runtime_id":{"type":"string"}},"required":["runtime_id"],"additionalProperties":false}`)
}

// ExecutionTools is the sole built-in execution schema source. Tools remain bound to their run destination.
func ExecutionTools(policy ShellSettings) []json.RawMessage {
	stringField := func() any { return map[string]any{"type": "string"} }
	boolean := func() any { return map[string]any{"type": "boolean"} }
	bounded := func(max uint64) any { return map[string]any{"type": "integer", "minimum": 1, "maximum": max} }
	schema := func(name, description string, properties map[string]any, required string) json.RawMessage {
		data, _ := json.Marshal(map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": []string{required}, "additionalProperties": false}})
		return data
	}
	return []json.RawMessage{
		schema("exec_command", "Run a shell command under the machine policy and exact command grants. Protected machines use a filesystem-restricted sandbox by default; network is open. To request execution outside that sandbox, set request_unsandboxed=true and give a nonempty justification. This requests permission; the Runtime decides the actual execution mode. Known host-only access may be requested directly. Unless the user forbids host access or permission requests, if a sandbox restriction blocks an operation needed for the user task, submit a new narrowly scoped call with request_unsandboxed=true and explain the required access; do not stop at asking the user to move files or change permissions. A sandbox filesystem/socket denial is not an owner denial of a host request. A failed command may have partial effects: inspect them and request only the remaining operation, never blindly replay. If the owner denies a host request, stop that operation and report it; do not retry through another command, tool or destination. An existing session cannot change execution mode. Omit yield_time_ms to wait until exit; specify it to return a live session_id after a bounded wait. Sessions belong to this agent execution. Completion, cancellation, inactivity, explicit timeout, agent change or executor restart closes them. tty=true allocates a terminal with merged output; default uses pipes. Each command has its own cwd; cd/export do not affect later commands. Use write_stdin to interact. Output is bounded and streams while waiting.", map[string]any{"request_unsandboxed": map[string]any{"type": "boolean", "default": false, "description": "Request host execution outside SRT. Omit for ordinary execution under machine policy and existing grants; false does not force sandboxing. A true value requires a nonempty justification and may wait for owner approval."}, "justification": map[string]any{"type": "string", "description": "Specific reason host access is needed; required and nonempty when request_unsandboxed is true. Shown to the owner, not part of a permanent command rule."}, "interactive": map[string]any{"type": "boolean", "description": "Allow subsequent input to this process. A host interactive session can execute further code and requires matching explicit authorization."}, "cmd": stringField(), "credentials": CredentialBindingsSchema(), "refresh_credentials": map[string]any{"type": "boolean", "description": "Reread the selected service credentials before this command. Use after an authentication failure; this does not retry any earlier operation."}, "workdir": stringField(), "timeout_ms": bounded(policy.MaxTimeoutMS), "max_output_chars": bounded(policy.MaxOutputBytes), "yield_time_ms": bounded(policy.MaxYieldMS), "shell": map[string]any{"type": "string", "description": "Absolute path to sh, bash or zsh in the execution target."}, "login": map[string]any{"type": "boolean", "description": "Load login initialization; defaults to execution policy."}, "tty": map[string]any{"type": "boolean", "default": false}}, "cmd"),
		schema("write_stdin", "Send chars to a live process session or poll with empty chars. close_stdin sends EOF. Sessions belong to this agent session and original execution target; use that same target. interrupt sends SIGINT; terminate stops the process; rows/cols resize a PTY. Cancellation/restart invalidates sessions.", map[string]any{"session_id": stringField(), "chars": stringField(), "close_stdin": boolean(), "interrupt": boolean(), "terminate": boolean(), "rows": bounded(1000), "cols": bounded(1000), "yield_time_ms": bounded(policy.MaxYieldMS), "max_output_chars": bounded(policy.MaxOutputBytes)}, "session_id"),
		schema("apply_patch", "Edit text files with Codex patch syntax. Pass patch: *** Begin Patch\n*** Add File: path\n+text\n*** Update File: path\n@@\n context\n-old\n+new\n*** Delete File: path\n*** End Patch. Update supports *** Move to: path and *** End of File. Paths are relative to the workspace.", map[string]any{"patch": stringField()}, "patch"),
	}
}

func CredentialBindingsSchema() any {
	return map[string]any{"type": "object", "maxProperties": 16, "description": "Inject configured Control service credentials into environment variables, e.g. GH_TOKEN: {service: github}. Values never enter tool arguments. Each service has one connected account.", "additionalProperties": map[string]any{"type": "object", "properties": map[string]any{"service": map[string]any{"type": "string"}}, "required": []string{"service"}, "additionalProperties": false}}
}
