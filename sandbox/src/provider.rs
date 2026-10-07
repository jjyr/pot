use anyhow::Result;
use futures::future::BoxFuture;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    path::{Path, PathBuf},
    time::Duration,
};

pub const MACOS_APPLE_CONTAINER_PROVIDER: &str = "macos.apple-containerization";
pub const LINUX_PODMAN_PROVIDER: &str = "linux.podman";
pub fn default_provider() -> String {
    if cfg!(target_os = "linux") {
        LINUX_PODMAN_PROVIDER
    } else {
        MACOS_APPLE_CONTAINER_PROVIDER
    }
    .into()
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
    pub host_os: String,
    pub engine: String,
    pub capabilities: Capabilities,
}
/// Trusted resolved input; constructed by Manager, never deserialized from tools.
#[derive(Clone)]
pub struct ProviderContext {
    pub root: PathBuf,
    pub sandbox_id: String,
}
/// Scope closure is independent of VM/disk persistence.
#[derive(Clone, Copy, Debug, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ScopeCleanupPolicy {
    ProcessContainment,
    SessionClosure,
}
impl ScopeCleanupPolicy {
    pub fn confirmed(self, response: &Value) -> bool {
        match self {
            Self::ProcessContainment => response["settled"] == true,
            Self::SessionClosure => response["scope_closed"] == true,
        }
    }
}
/// Owner-resolved resources, independent of any catalog or downloader.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct PreparedResources {
    pub bundle_id: String,
    pub kernel: PathBuf,
    pub boot: PathBuf,
    pub image: PathBuf,
    pub image_reference: String,
    pub boot_reference: String,
}
/// Object-safe provider contract. Credentials and concrete transports stay private.
pub trait Provider: Send + Sync {
    fn bind_resources(
        &self,
        _context: &ProviderContext,
        _resources: &PreparedResources,
    ) -> Result<()> {
        anyhow::bail!("Provider does not accept prepared Linux resources")
    }
    fn descriptor(&self) -> ProviderDescriptor;
    fn desktop_capabilities(&self) -> crate::DesktopCapabilities {
        Default::default()
    }
    fn prepare_desktop<'a>(&'a self, _context: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { anyhow::bail!("Desktop capability unavailable") })
    }
    fn desktop_browser<'a>(&'a self, _context: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { anyhow::bail!("Desktop browser unavailable") })
    }
    fn desktop_metrics<'a>(
        &'a self,
        _context: &'a ProviderContext,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async { Ok(serde_json::json!({})) })
    }
    fn open_desktop<'a>(
        &'a self,
        _context: &'a ProviderContext,
    ) -> BoxFuture<'a, Result<crate::DesktopStream>> {
        Box::pin(async { anyhow::bail!("Desktop transport unavailable") })
    }
    fn scope_cleanup_policy(&self) -> ScopeCleanupPolicy {
        ScopeCleanupPolicy::ProcessContainment
    }
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
    fn shutdown<'a>(&'a self, _root: &'a Path) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { Ok(()) })
    }
    fn default_config(&self) -> Value {
        serde_json::json!({"cpus":4,"memory_gib":8,"disk_gib":32})
    }
    fn validate_config(&self, config: &Value) -> Result<()>;
    fn validate_instance_config(&self, _context: &ProviderContext, config: &Value) -> Result<()> {
        self.validate_config(config)
    }
}
