use anyhow::{Result, anyhow, bail, ensure};
use serde_json::{Value, json};

pub fn schemas(policy: &crate::ShellSettings) -> Vec<Value> {
    let shared = json!({"workdir":{"type":"string"},"timeout_ms":{"type":"integer","minimum":1,"maximum":policy.max_timeout_ms},"max_output_chars":{"type":"integer","minimum":1,"maximum":policy.max_output_bytes},"yield_time_ms":{"type":"integer","minimum":1,"maximum":policy.max_yield_ms}});
    let mut properties = shared;
    properties["cmd"] = json!({"type":"string"});
    vec![
        json!({"name":"exec_command","description":"Run a shell command. Omit yield_time_ms to wait until exit; specify it to return a live session_id after a bounded wait. Live pipe sessions are scoped to this turn and expire at timeout. Use write_stdin to poll or send input. Output is bounded and streams while waiting.","inputSchema":{"type":"object","properties":properties,"required":["cmd"],"additionalProperties":false}}),
        json!({"name":"write_stdin","description":"Send chars to a live process session or poll with empty chars. close_stdin sends EOF. Sessions are scoped to this active turn, not resumable after cancellation/restart.","inputSchema":{"type":"object","properties":{"session_id":{"type":"string"},"chars":{"type":"string"},"close_stdin":{"type":"boolean"},"yield_time_ms":{"type":"integer","minimum":1,"maximum":policy.max_yield_ms},"max_output_chars":{"type":"integer","minimum":1,"maximum":policy.max_output_bytes}},"required":["session_id"],"additionalProperties":false}}),
        json!({"name":"apply_patch","description":"Edit text files with Codex patch syntax. Pass patch: *** Begin Patch\n*** Add File: path\n+text\n*** Update File: path\n@@\n context\n-old\n+new\n*** Delete File: path\n*** End Patch. Update also supports *** Move to: path and *** End of File. Paths are relative to the workspace.","inputSchema":{"type":"object","properties":{"patch":{"type":"string"}},"required":["patch"],"additionalProperties":false}}),
    ]
}
pub fn validate(name: &str, args: &Value) -> Result<()> {
    let object = args
        .as_object()
        .ok_or_else(|| anyhow!("Tool arguments must be an object"))?;
    let fields: &[&str] = match name {
        "exec_command" => &[
            "cmd",
            "workdir",
            "timeout_ms",
            "max_output_chars",
            "yield_time_ms",
        ],
        "write_stdin" => &[
            "session_id",
            "chars",
            "close_stdin",
            "yield_time_ms",
            "max_output_chars",
        ],
        "apply_patch" => &["patch"],
        _ => bail!("Unknown tool"),
    };
    ensure!(
        object.keys().all(|key| fields.contains(&key.as_str())),
        "Unknown tool argument"
    );
    if let Some(workdir) = args.get("workdir") {
        ensure!(
            workdir.as_str().is_some_and(|s| !s.is_empty()),
            "Invalid workdir"
        );
    }
    if let Some(close) = args.get("close_stdin") {
        ensure!(close.is_boolean(), "Invalid close_stdin");
    }
    Ok(())
}
