use anyhow::Result;
use futures::future::BoxFuture;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    path::{Path, PathBuf},
    time::Duration,
};

pub const LINUX_PROVIDER: &str = "linux.apple-containerization";
pub fn default_provider() -> String {
    LINUX_PROVIDER.into()
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Capabilities {
    pub shell: bool,
    pub files: bool,
    pub desktop: bool,
    pub shared_execution: bool,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ProviderDescriptor {
    pub id: String,
    pub os: String,
    pub capabilities: Capabilities,
}
/// Trusted resolved input; constructed by Manager, never deserialized from tools.
#[derive(Clone)]
pub struct ProviderContext {
    pub root: PathBuf,
    pub sandbox_id: String,
}
/// Object-safe provider contract. Credentials and concrete transports stay private.
pub trait Provider: Send + Sync {
    fn descriptor(&self) -> ProviderDescriptor;
    fn workspace(&self, root: &Path, sandbox_id: &str) -> PathBuf;
    fn guest_workspace(&self) -> &str;
    fn lifecycle<'a>(
        &'a self,
        context: &'a ProviderContext,
        method: &'a str,
    ) -> BoxFuture<'a, Result<Value>>;
    fn health<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<bool>>;
    fn request<'a>(
        &'a self,
        context: &'a ProviderContext,
        route: &'a str,
        body: &'a Value,
        timeout: Duration,
    ) -> BoxFuture<'a, Result<Value>>;
    fn validate_config(&self, config: &Value) -> Result<()>;
}
