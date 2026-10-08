use anyhow::{Result, anyhow, bail, ensure};
use serde_json::{Value, json};

pub fn schemas(policy: &crate::ShellSettings) -> Vec<Value> {
    let shared = json!({"workdir":{"type":"string"},"timeout_ms":{"type":"integer","minimum":1,"maximum":policy.max_timeout_ms},"max_output_chars":{"type":"integer","minimum":1,"maximum":policy.max_output_bytes},"yield_time_ms":{"type":"integer","minimum":1,"maximum":policy.max_yield_ms}});
    let mut properties = shared;
    properties["shell"] = json!({"type":"string","description":"Absolute path to sh, bash or zsh in the execution environment."});
    properties["login"] = json!({"type":"boolean","description":"Load login initialization; defaults to the environment policy."});
    properties["tty"] = json!({"type":"boolean","default":false});
    properties["cmd"] = json!({"type":"string"});
    vec![
        json!({"name":"exec_command","description":"Run a shell command. Omit yield_time_ms to wait until exit; specify it to return a live session_id after a bounded wait. Running sessions survive normal turns in this agent session. They expire after inactivity, explicit timeout, cancellation, agent change or executor restart. tty=true allocates a terminal with merged output; default uses pipes. Each command has its own cwd; cd/export do not affect later commands. Use write_stdin to poll or interact. Output is bounded and streams while waiting.","inputSchema":{"type":"object","properties":properties,"required":["cmd"],"additionalProperties":false}}),
        json!({"name":"write_stdin","description":"Send chars to a live process session or poll with empty chars. close_stdin sends EOF. Sessions belong to this agent session and their original execution environment; use that same environment for subsequent calls. interrupt sends SIGINT; terminate stops the process. rows/cols resize a PTY. Cancellation/restart invalidates sessions.","inputSchema":{"type":"object","properties":{"session_id":{"type":"string"},"chars":{"type":"string"},"close_stdin":{"type":"boolean"},"interrupt":{"type":"boolean"},"terminate":{"type":"boolean"},"rows":{"type":"integer","minimum":1,"maximum":1000},"cols":{"type":"integer","minimum":1,"maximum":1000},"yield_time_ms":{"type":"integer","minimum":1,"maximum":policy.max_yield_ms},"max_output_chars":{"type":"integer","minimum":1,"maximum":policy.max_output_bytes}},"required":["session_id"],"additionalProperties":false}}),
        json!({"name":"apply_patch","description":"Edit text files with Codex patch syntax. Pass patch: *** Begin Patch\n*** Add File: path\n+text\n*** Update File: path\n@@\n context\n-old\n+new\n*** Delete File: path\n*** End Patch. Update also supports *** Move to: path and *** End of File. Paths are relative to the workspace.","inputSchema":{"type":"object","properties":{"patch":{"type":"string"}},"required":["patch"],"additionalProperties":false}}),
    ]
}
pub fn validate(name: &str, args: &Value) -> Result<()> {
    if name == "plugin_call" {
        crate::plugins::validate_call(args)?;
        return Ok(());
    }
    let object = args
        .as_object()
        .ok_or_else(|| anyhow!("Tool arguments must be an object"))?;
    let fields: &[&str] = match name {
        "exec_command" => &[
            "cmd",
            "shell",
            "login",
            "tty",
            "workdir",
            "timeout_ms",
            "max_output_chars",
            "yield_time_ms",
        ],
        "write_stdin" => &[
            "session_id",
            "chars",
            "close_stdin",
            "interrupt",
            "terminate",
            "rows",
            "cols",
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
    let required = match name {
        "exec_command" => Some("cmd"),
        "apply_patch" => Some("patch"),
        "write_stdin" => Some("session_id"),
        _ => None,
    };
    if let Some(field) = required {
        ensure!(
            args[field].as_str().is_some_and(|s| !s.is_empty()),
            "Missing string argument: {field}"
        );
    }
    if let Some(workdir) = args.get("workdir") {
        ensure!(
            workdir.as_str().is_some_and(|s| !s.is_empty()),
            "Invalid workdir"
        );
    }
    for key in ["close_stdin", "interrupt", "terminate", "login", "tty"] {
        if let Some(value) = args.get(key) {
            ensure!(value.is_boolean(), "Invalid {key}");
        }
    }
    if let Some(shell) = args.get("shell") {
        ensure!(
            shell
                .as_str()
                .is_some_and(|s| s.starts_with('/') && !s.contains('\0')),
            "Invalid shell"
        );
    }
    for key in ["rows", "cols"] {
        if let Some(value) = args.get(key) {
            ensure!(
                value.as_u64().is_some_and(|n| (1..=1000).contains(&n)),
                "Invalid {key}"
            );
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shell_and_terminal_arguments_are_shared_and_type_checked() {
        let tools: Vec<_> = schemas(&crate::ShellSettings::default())
            .into_iter()
            .filter(|t| t["name"] != "browser")
            .collect();
        for key in ["shell", "login", "tty"] {
            assert!(tools[0]["inputSchema"]["properties"].get(key).is_some());
        }
        for key in ["interrupt", "terminate", "rows", "cols"] {
            assert!(tools[1]["inputSchema"]["properties"].get(key).is_some());
        }
        validate(
            "exec_command",
            &json!({"cmd":"true","shell":"/bin/bash","login":false,"tty":true}),
        )
        .unwrap();
        validate(
            "write_stdin",
            &json!({"session_id":"id","interrupt":true,"rows":24,"cols":80}),
        )
        .unwrap();
        for (name, args) in [
            ("exec_command", json!({"cmd":"true","tty":"true"})),
            ("exec_command", json!({"cmd":"true","shell":"bash"})),
            ("write_stdin", json!({"session_id":"id","rows":0})),
            ("write_stdin", json!({"session_id":"id","terminate":1})),
            ("write_stdin", json!({"session_id":"id","cols":1001})),
        ] {
            assert!(validate(name, &args).is_err());
        }
    }
}
