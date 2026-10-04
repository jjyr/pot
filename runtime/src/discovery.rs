//! Explicit, bounded official status discovery. Never reads native auth files.
use crate::*;
use serde::{Deserialize, Serialize};
use std::{os::unix::fs::PermissionsExt, time::Duration};
use tokio::io::AsyncReadExt;
#[derive(Clone, Serialize, Deserialize)]
pub struct CommandSpec {
    pub executable: String,
    pub argv: Vec<String>,
}
#[derive(Clone, Serialize, Deserialize)]
pub struct Tool {
    pub id: String,
    pub invocation: CommandSpec,
    pub login: Option<CommandSpec>,
}
fn defaults() -> Vec<Tool> {
    vec![
        Tool {
            id: "pi".into(),
            invocation: CommandSpec {
                executable: "pi".into(),
                argv: vec![],
            },
            login: None,
        },
        Tool {
            id: "codex".into(),
            invocation: CommandSpec {
                executable: "codex".into(),
                argv: vec![],
            },
            login: Some(CommandSpec {
                executable: "codex".into(),
                argv: vec!["login".into(), "--device-auth".into()],
            }),
        },
        Tool {
            id: "claude".into(),
            invocation: CommandSpec {
                executable: "claude".into(),
                argv: vec![],
            },
            login: Some(CommandSpec {
                executable: "claude".into(),
                argv: vec!["auth".into(), "login".into()],
            }),
        },
        Tool {
            id: "agy".into(),
            invocation: CommandSpec {
                executable: "agy".into(),
                argv: vec![],
            },
            login: Some(CommandSpec {
                executable: "agy".into(),
                argv: vec![],
            }),
        },
    ]
}
pub fn tools(s: &Service) -> Result<Vec<Tool>> {
    s.inner
        .lock()
        .unwrap()
        .store
        .get("agent.tools.v1")?
        .map(|v| Ok(serde_json::from_str(&v)?))
        .unwrap_or_else(|| Ok(defaults()))
}
fn valid(command: &CommandSpec) -> bool {
    !command.executable.is_empty()
        && command.executable.len() <= 4096
        && !command.executable.contains('\0')
        && command.argv.len() <= 64
        && command
            .argv
            .iter()
            .all(|v| v.len() <= 4096 && !v.contains('\0'))
}
pub fn executable(name: &str) -> Option<PathBuf> {
    let check = |p: PathBuf| {
        p.metadata()
            .ok()
            .filter(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
            .map(|_| p)
    };
    if name.contains('/') {
        return check(PathBuf::from(name));
    }
    std::env::split_paths(&std::env::var_os("PATH").unwrap_or_default())
        .find_map(|p| check(p.join(name)))
}
pub async fn list(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(json!({"tools":tools(&s).map_err(internal)?})))
}
pub async fn save(
    State(s): State<Service>,
    h: HeaderMap,
    Json(tool): Json<Tool>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if !valid(&tool.invocation) || tool.login.as_ref().is_some_and(|c| !valid(c)) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "command",
            "Invalid executable or argument array",
        ));
    }
    // Invocation edits must use Profile revisions; this catalog edits login only.
    let mut all = tools(&s).map_err(internal)?;
    let Some(current) = all.iter_mut().find(|t| t.id == tool.id) else {
        return Err(error(StatusCode::BAD_REQUEST, "agent", "Unknown agent"));
    };
    if serde_json::to_value(&current.invocation).unwrap()
        != serde_json::to_value(&tool.invocation).unwrap()
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "command",
            "Edit invocation through the versioned profile API",
        ));
    }
    *current = tool;
    s.inner
        .lock()
        .unwrap()
        .store
        .set("agent.tools.v1", &serde_json::to_string(&all).unwrap())
        .map_err(internal)?;
    Ok(Json(json!({"tools":all})))
}
pub async fn refresh(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    static PROBE: tokio::sync::Semaphore = tokio::sync::Semaphore::const_new(1);
    let _permit = PROBE.try_acquire().map_err(|_| {
        error(
            StatusCode::CONFLICT,
            "discovery_busy",
            "Discovery is already running",
        )
    })?;
    let mut results = Vec::new();
    for tool in tools(&s).map_err(internal)? {
        let name = tool
            .login
            .as_ref()
            .map(|c| c.executable.as_str())
            .unwrap_or(&tool.invocation.executable);
        let present = executable(name).is_some();
        let exists = if name.contains('/') {
            std::path::Path::new(name).exists()
        } else {
            std::env::split_paths(&std::env::var_os("PATH").unwrap_or_default())
                .any(|p| p.join(name).exists())
        };
        let installation = if present {
            "present"
        } else if exists {
            "executable_error"
        } else {
            "missing"
        };
        let mut auth = "unknown";
        let mut check = "unsupported";
        // Only official, documented status commands are queried; wrappers stay unknown.
        if matches!(tool.id.as_str(), "codex" | "claude") && name == tool.id && present {
            match native_status(&tool.id, &s.workspace).await {
                Ok(Some(true)) => {
                    auth = "authenticated";
                    check = "complete";
                }
                Ok(Some(false)) => {
                    auth = "required";
                    check = "complete";
                }
                Ok(None) => check = "unknown",
                Err(_) => check = "failed",
            }
        }
        results.push(json!({"id":tool.id,"installation":installation,
            "authentication":auth,"auth_type":"unknown","status_check":check,
            "compatibility":if tool.id == "pi" {"compatible"} else {"unchecked"},
            "readiness":"unchecked","accepted_roles":if tool.id == "pi" {vec!["main","worker","summary"]} else {vec![]}}));
    }
    Ok(Json(json!({"agents":results})))
}
async fn native_status(engine: &str, cwd: &std::path::Path) -> Result<Option<bool>> {
    let args: &[&str] = if engine == "claude" {
        &["auth", "status", "--json"]
    } else {
        &["login", "status"]
    };
    let mut child = Command::new(engine)
        .args(args)
        .current_dir(cwd)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .process_group(0)
        .kill_on_drop(true)
        .spawn()?;
    let pid = child.id().unwrap() as i32;
    let stdout = child.stdout.take().unwrap();
    let stderr = child.stderr.take().unwrap();
    let result = tokio::time::timeout(Duration::from_secs(5), async {
        let read = |stream: std::pin::Pin<Box<dyn tokio::io::AsyncRead + Send>>| async move {
            let mut bytes = vec![];
            stream.take(16385).read_to_end(&mut bytes).await?;
            anyhow::ensure!(bytes.len() <= 16384, "Status output too large");
            Ok::<_, anyhow::Error>(bytes)
        };
        let (out, err) = tokio::try_join!(read(Box::pin(stdout)), read(Box::pin(stderr)))?;
        let status = child.wait().await?;
        let text = format!(
            "{}\n{}",
            String::from_utf8_lossy(&out),
            String::from_utf8_lossy(&err)
        );
        if engine == "claude" {
            let value: Value = serde_json::from_slice(&out)?;
            return Ok::<_, anyhow::Error>(match value["loggedIn"].as_bool() {
                Some(true) if status.success() => Some(true),
                Some(false) if status.code() == Some(1) => Some(false),
                _ => None,
            });
        }
        Ok::<_, anyhow::Error>(if status.success() && text.contains("Logged in") {
            Some(true)
        } else if !status.success() && text.trim() == "Not logged in" {
            Some(false)
        } else {
            None
        })
    })
    .await;
    unsafe {
        libc::kill(-pid, libc::SIGKILL);
    }
    let _ = child.wait().await;
    result?
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn commands_are_argv_and_discovery_does_not_guess_auth() {
        let tools = defaults();
        assert_eq!(
            tools[1].login.as_ref().unwrap().argv,
            vec!["login", "--device-auth"]
        );
        assert!(tools[0].login.is_none());
        assert!(executable("/this/cannot/exist").is_none());
        assert!(!valid(&CommandSpec {
            executable: "".into(),
            argv: vec![]
        }));
    }
}
