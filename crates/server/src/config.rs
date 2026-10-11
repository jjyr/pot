use pot_protocol::RUNTIME_PORT;
use pot_runtime::RuntimeConfig;
use std::path::PathBuf;
#[derive(Clone, Debug)]
pub struct ServerConfig {
    pub runtime: RuntimeConfig,
    pub bind: String,
    pub web_control: Option<WebControlConfig>,
    /// None loads the owner's versioned composition or preserves an existing assistant.
    pub composition: Option<pot_runtime::host::Composition>,
}
pub fn default_root() -> PathBuf {
    if let Some(root) = std::env::var_os("POT_LOCAL_ROOT") {
        return root.into();
    }
    let home = PathBuf::from(std::env::var_os("HOME").unwrap_or_default());
    if cfg!(target_os = "macos") {
        home.join("Library/Application Support/pot")
    } else {
        std::env::var_os("XDG_DATA_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join(".local/share"))
            .join("pot")
    }
}
impl ServerConfig {
    pub fn from_env() -> Self {
        Self {
            composition: None,
            web_control: None,
            runtime: RuntimeConfig {
                data: std::env::var("POT_DATA_DIR")
                    .map(PathBuf::from)
                    .unwrap_or_else(|_| default_root().join("product")),
                workspace: std::env::var("POT_WORKSPACE")
                    .map(PathBuf::from)
                    .unwrap_or_else(|_| default_root().join("agent-workspace")),
            },
            bind: std::env::var("POT_RUNTIME_BIND")
                .unwrap_or_else(|_| format!("127.0.0.1:{RUNTIME_PORT}")),
        }
    }
}

/// Explicit Web UI destination. The credential is never included in Debug output.
#[derive(Clone)]
pub struct WebControlConfig {
    pub url: String,
    pub token: String,
    pub ca_pem: Option<String>,
}
impl std::fmt::Debug for WebControlConfig {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("WebControlConfig")
            .field("url", &self.url)
            .finish_non_exhaustive()
    }
}
