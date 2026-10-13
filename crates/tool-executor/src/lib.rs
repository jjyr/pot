//! Local execution backend for authorized Host calls, Sandbox runners and eval fixtures.
pub mod files;
pub mod plugins;
mod processes;
mod snapshot;
mod terminal;
pub mod tools;
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::{ExecutionContext, ShellSettings, ToolExecutor, Update};
pub use processes::Processes;
use serde_json::Value;
#[derive(Default)]
pub struct LocalTools(
    tokio::sync::Mutex<Processes>,
    tokio::sync::Mutex<plugins::Plugins>,
);
impl LocalTools {
    pub fn host() -> Self {
        Self(
            Default::default(),
            tokio::sync::Mutex::new(plugins::Plugins::host()),
        )
    }
    /// Read-only operator metadata; outputs, command text and credentials are excluded.
    pub async fn session_inventory(&self) -> Value {
        serde_json::json!({"processes":self.0.lock().await.inspect(),
            "plugins":self.1.lock().await.sessions()})
    }
    pub async fn has_session(&self, session: &str) -> bool {
        self.plugin_sessions().await.iter().any(|id| id == session)
            || self.0.lock().await.inspect()["sessions"]
                .as_array()
                .is_some_and(|ids| ids.iter().any(|id| id == session))
    }
    pub async fn plugin_sessions(&self) -> Vec<String> {
        self.1.lock().await.sessions()
    }
    pub async fn reap_plugins(&self) -> Result<()> {
        self.1.lock().await.reap_idle().await
    }
    pub async fn shutdown(&self) -> Result<()> {
        self.1.lock().await.shutdown().await?;
        self.0.lock().await.shutdown().await
    }
}
impl ToolExecutor for LocalTools {
    fn execute<'a>(
        &'a self,
        context: &'a ExecutionContext,
        policy: &'a ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            pot_core::execution_tools::validate(name, args)?;
            if name == "apply_patch" {
                tools::patch(context, args)
            } else if name == "plugin_call" {
                self.1
                    .lock()
                    .await
                    .execute_with_idle(args, policy.session_idle_timeout_ms)
                    .await
            } else {
                self.0
                    .lock()
                    .await
                    .execute(context, policy, name, args, call, emit)
                    .await
            }
        })
    }
    fn finish<'a>(&'a self, _: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async {
            self.1.lock().await.shutdown().await?;
            self.0.lock().await.shutdown().await
        })
    }
}

fn validate_plugin_image(block: &Value) -> Result<()> {
    anyhow::ensure!(
        matches!(
            block["mimeType"].as_str(),
            Some("image/jpeg" | "image/png" | "image/webp")
        ) && block["data"].as_str().is_some_and(|s| !s.is_empty()
            && s.len() <= 1_400_000
            && s.bytes()
                .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'+' | b'/' | b'=')))
            && block["width"]
                .as_u64()
                .is_some_and(|n| (1..=4096).contains(&n))
            && block["height"]
                .as_u64()
                .is_some_and(|n| (1..=4096).contains(&n)),
        "Invalid plugin image"
    );
    Ok(())
}
