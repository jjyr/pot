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
}
fn defaults() -> Vec<Tool> {
    ["pi", "codex", "claude", "agy"]
        .into_iter()
        .map(|id| Tool {
            id: id.into(),
            invocation: CommandSpec {
                executable: id.into(),
                argv: vec![],
            },
        })
        .collect()
}
pub fn tools(s: &Service) -> Result<Vec<Tool>> {
    let inner = s.inner.lock().unwrap();
    let mut tools: Vec<Tool> = inner
        .store
        .get("agent.tools.v1")?
        .map(|v| Ok(serde_json::from_str(&v)?))
        .unwrap_or_else(|| Ok::<_, anyhow::Error>(defaults()))?;
    let profiles = agent::profiles(&inner.store)?;
    for tool in &mut tools {
        if let Some(profile) = profiles
            .iter()
            .find(|p| p.id == format!("{}-default", tool.id))
        {
            if let Some(launch) = &profile.launch_spec {
                tool.invocation.executable = launch.executable.clone();
                tool.invocation.argv = launch.expand(&[])?;
            }
        }
    }
    Ok(tools)
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
        let name = tool.invocation.executable.as_str();
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
        if matches!(tool.id.as_str(), "codex" | "claude")
            && name == tool.id
            && tool.invocation.argv.is_empty()
            && present
        {
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
    fn discovery_uses_versioned_invocation_instead_of_old_catalog_command() {
        let s = crate::tests::service();
        let mut profile = agent::Profile::legacy(&pi::Config::default());
        let mut launch = agent::LaunchSpec::default();
        launch.executable = "/this/wrapper/is/missing".into();
        launch.argv.insert(
            0,
            agent::LaunchArgument::Literal("literal wrapper argument".into()),
        );
        profile.launch_spec = Some(launch);
        s.inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&vec![profile]).unwrap(),
            )
            .unwrap();
        let tools = tools(&s).unwrap();
        assert_eq!(tools[0].invocation.executable, "/this/wrapper/is/missing");
        assert_eq!(tools[0].invocation.argv, vec!["literal wrapper argument"]);
        assert!(executable(&tools[0].invocation.executable).is_none());
    }
    #[test]
    fn commands_are_argv_and_discovery_does_not_guess_auth() {
        let tools = defaults();
        assert_eq!(tools[1].invocation.executable, "codex");
        let old: Tool = serde_json::from_value(json!({"id":"codex","invocation":{"executable":"codex","argv":[]},"login":{"executable":"must-not-run","argv":[]}})).unwrap();
        assert!(serde_json::to_value(old).unwrap().get("login").is_none());
        assert!(executable("/this/cannot/exist").is_none());
    }
}
