use pot_protocol::RUNTIME_PORT;
use pot_runtime::RuntimeConfig;
use std::path::PathBuf;
#[derive(Clone, Debug)]
pub struct ServerConfig {
    pub runtime: RuntimeConfig,
    pub bind: String,
}
impl ServerConfig {
    pub fn from_env() -> Self {
        Self {
            runtime: RuntimeConfig {
                data: PathBuf::from(
                    std::env::var("POT_DATA_DIR").unwrap_or_else(|_| "/data/product".into()),
                ),
                workspace: PathBuf::from(
                    std::env::var("POT_WORKSPACE").unwrap_or_else(|_| "/data/workspace".into()),
                ),
            },
            bind: std::env::var("POT_RUNTIME_BIND")
                .unwrap_or_else(|_| format!("0.0.0.0:{RUNTIME_PORT}")),
        }
    }
}
