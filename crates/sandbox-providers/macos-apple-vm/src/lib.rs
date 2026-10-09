//! Owner-enrolled macOS VM lifecycle and pinned, authenticated guest transport.
use anyhow::{Result, ensure};
use futures::future::BoxFuture;
use pot_sandbox::{Capabilities, Provider, ProviderContext, ProviderDescriptor};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    net::Ipv4Addr,
    path::{Path, PathBuf},
    time::Duration,
};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
pub const PROVIDER_ID: &str = "macos.apple-virtualization";
const LIMIT: usize = 8 * 1024 * 1024;
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Manifest {
    pub schema_version: u32,
    pub provider: String,
    pub guest_build: String,
    pub xcode_version: String,
    pub bootstrap: String,
    pub guest_ip: Ipv4Addr,
    pub mac_address: String,
}
impl Manifest {
    pub fn load(bundle: &Path) -> Result<Self> {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        let meta = std::fs::symlink_metadata(bundle)?;
        ensure!(
            meta.is_dir() && meta.permissions().mode() & 0o077 == 0,
            "macOS bundle must be a private directory"
        );
        let owner = meta.uid();
        for name in [
            "manifest.json",
            "hardware-model",
            "machine-identifier",
            "auxiliary-storage",
            "disk.img",
            "guest-ca.pem",
            "executor-token",
        ] {
            let meta = std::fs::symlink_metadata(bundle.join(name))?;
            ensure!(
                meta.is_file() && meta.uid() == owner && meta.permissions().mode() & 0o077 == 0,
                "Invalid or non-private macOS bundle file: {name}"
            );
        }
        ensure!(
            std::fs::metadata(bundle.join("manifest.json"))?.len() <= 65536,
            "Manifest too large"
        );
        let m: Self = serde_json::from_slice(&std::fs::read(bundle.join("manifest.json"))?)?;
        ensure!(
            m.schema_version == 1
                && m.provider == PROVIDER_ID
                && !m.guest_build.is_empty()
                && !m.xcode_version.is_empty(),
            "Incompatible macOS bundle"
        );
        ensure!(
            m.bootstrap == "pot-macos-executor-v3",
            "Update the macOS guest bootstrap to pot-macos-executor-v3 and re-enroll the prepared VM"
        );
        ensure!(
            m.guest_ip.is_private(),
            "Guest must use a private VM network address"
        );
        let parts: Vec<_> = m.mac_address.split(':').collect();
        ensure!(
            parts.len() == 6
                && parts
                    .iter()
                    .all(|p| p.len() == 2 && u8::from_str_radix(p, 16).is_ok()),
            "Invalid VM MAC address"
        );
        let first = u8::from_str_radix(parts[0], 16)?;
        ensure!(
            first & 1 == 0 && first & 2 != 0,
            "VM MAC must be locally administered unicast"
        );
        Ok(m)
    }
}
pub struct MacOSAppleVMProvider {
    helper: PathBuf,
    children: tokio::sync::Mutex<HashMap<PathBuf, tokio::process::Child>>,
}
impl MacOSAppleVMProvider {
    /// Trusted composition configuration; helper path is never a model tool argument.
    pub fn new(helper: PathBuf) -> Self {
        Self {
            helper,
            children: Default::default(),
        }
    }
    fn directory(context: &ProviderContext) -> PathBuf {
        context.root.join("sandboxes").join(&context.sandbox_id)
    }
    async fn rpc(path: &Path, method: &str) -> Result<Value> {
        tokio::time::timeout(Duration::from_secs(60), async {
            let mut stream = tokio::net::UnixStream::connect(path).await?;
            let id = uuid::Uuid::new_v4().to_string();
            stream
                .write_all(format!("{}\n", json!({"version":1,"id":id,"method":method})).as_bytes())
                .await?;
            let mut line = String::new();
            BufReader::new(stream)
                .take(65537)
                .read_line(&mut line)
                .await?;
            ensure!(
                line.len() <= 65536 && line.ends_with('\n'),
                "Invalid macOS Helper response"
            );
            let response: Value = serde_json::from_str(&line)?;
            ensure!(
                response["version"] == 1 && response["id"] == id,
                "macOS Helper protocol mismatch"
            );
            ensure!(
                response.get("error").is_none(),
                "macOS Helper: {}",
                response["error"].as_str().unwrap_or("operation failed")
            );
            Ok(response["result"].clone())
        })
        .await?
    }
    async fn transport(
        &self,
        context: &ProviderContext,
    ) -> Result<(reqwest::Client, String, String)> {
        let bundle = Self::directory(context).join("macos");
        let manifest = Manifest::load(&bundle)?;
        ensure!(
            self.lifecycle(context, "status").await?["phase"] == "running",
            "macOS VM is not running"
        );
        let certificate =
            reqwest::Certificate::from_pem(&std::fs::read(bundle.join("guest-ca.pem"))?)?;
        let client = reqwest::Client::builder()
            .tls_certs_only([certificate])
            .redirect(reqwest::redirect::Policy::none())
            .no_proxy()
            .build()?;
        let token = std::fs::read_to_string(bundle.join("executor-token"))?
            .trim()
            .to_string();
        ensure!(
            token.len() >= 32
                && token.len() <= 256
                && token
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_'),
            "Invalid enrolled credential"
        );
        Ok((
            client,
            format!("https://{}:17701", manifest.guest_ip),
            token,
        ))
    }
}
impl Provider for MacOSAppleVMProvider {
    fn scope_cleanup_policy(&self) -> pot_sandbox::ScopeCleanupPolicy {
        pot_sandbox::ScopeCleanupPolicy::SessionClosure
    }
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: PROVIDER_ID.into(),
            os: "macos".into(),
            host_os: "macos".into(),
            engine: "apple-virtualization".into(),
            capabilities: Capabilities {
                shell: true,
                files: true,
                desktop: true,
                shared_execution: false,
            },
        }
    }
    fn desktop_capabilities(&self) -> pot_sandbox::DesktopCapabilities {
        pot_sandbox::DesktopCapabilities {
            viewing: true,
            input: true,
            clipboard: false,
            browser: false,
            protocol: Some(pot_sandbox::DesktopProtocol::Rfb38),
        }
    }
    fn prepare_desktop<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let (client, url, token) = self.transport(context).await?;
            client
                .get(format!("{url}/desktop-ready"))
                .bearer_auth(token)
                .timeout(Duration::from_secs(5))
                .send()
                .await?
                .error_for_status()
                .map_err(|_| {
                    anyhow::anyhow!(
                        "Guest desktop unavailable: configure a loopback RFB 3.8 service first"
                    )
                })?;
            Ok(())
        })
    }
    fn open_desktop<'a>(
        &'a self,
        context: &'a ProviderContext,
    ) -> BoxFuture<'a, Result<pot_sandbox::DesktopStream>> {
        Box::pin(async move {
            let (client, url, token) = self.transport(context).await?;
            let response = client
                .get(format!("{url}/desktop-stream"))
                .bearer_auth(token)
                .header("Connection", "Upgrade")
                .header("Upgrade", "pot-rfb")
                .timeout(Duration::from_secs(10))
                .send()
                .await?;
            ensure!(
                response.status() == reqwest::StatusCode::SWITCHING_PROTOCOLS,
                "Guest desktop unavailable: configure a loopback RFB 3.8 service first"
            );
            Ok(Box::pin(response.upgrade().await?) as pot_sandbox::DesktopStream)
        })
    }
    fn workspace(&self, root: &Path, id: &str) -> PathBuf {
        root.join("sandboxes").join(id).join("workspace/workspace")
    }
    fn guest_workspace(&self) -> &str {
        "/Volumes/PotWorkspace/workspace"
    }
    fn lifecycle<'a>(
        &'a self,
        context: &'a ProviderContext,
        method: &'a str,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            ensure!(
                ["prepare", "start", "status", "stop"].contains(&method),
                "Unsupported macOS lifecycle method"
            );
            let directory = Self::directory(context);
            let socket = directory.join("macos-helper.sock");
            let mut children = self.children.lock().await;
            let exited = if let Some(child) = children.get_mut(&directory) {
                child.try_wait()?.is_some()
            } else {
                false
            };
            if exited {
                children.remove(&directory);
            }
            if !children.contains_key(&directory) {
                if method == "status" || method == "stop" {
                    // A stale socket could belong to another helper. Never adopt it or
                    // declare it stopped without exclusive process/VM ownership.
                    ensure!(
                        !socket.exists(),
                        "Unowned macOS Helper socket; owner reconciliation required"
                    );
                    if method == "status"
                        && (!cfg!(all(target_os = "macos", target_arch = "aarch64"))
                            || !self.helper.is_file()
                            || Manifest::load(&directory.join("macos")).is_err())
                    {
                        return Ok(json!({"phase":"unavailable","generation":0}));
                    }
                    return Ok(json!({"phase":"stopped","generation":0}));
                }
                ensure!(
                    cfg!(target_os = "macos") && cfg!(target_arch = "aarch64"),
                    "macOS VM requires Apple silicon macOS"
                );
                Manifest::load(&directory.join("macos"))?;
                ensure!(self.helper.is_file(), "PotMacOSHelper not installed");
                ensure!(
                    !socket.exists(),
                    "Unowned macOS Helper socket; owner reconciliation required"
                );
                std::fs::create_dir_all(directory.join("workspace/workspace"))?;
                snapshot_skills(&directory.join("macos-skills"))?;
                use std::os::unix::fs::OpenOptionsExt;
                let log = std::fs::OpenOptions::new()
                    .create(true)
                    .append(true)
                    .mode(0o600)
                    .open(directory.join("macos-helper.log"))?;
                let child = tokio::process::Command::new(&self.helper)
                    .arg(&directory)
                    .stdin(std::process::Stdio::null())
                    .stdout(std::process::Stdio::from(log.try_clone()?))
                    .stderr(std::process::Stdio::from(log))
                    .kill_on_drop(true)
                    .spawn()?;
                children.insert(directory.clone(), child);
                for _ in 0..100 {
                    ensure!(
                        children.get_mut(&directory).unwrap().try_wait()?.is_none(),
                        "macOS Helper exited; inspect private log"
                    );
                    if socket.exists() {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(50)).await;
                }
            }
            let result = Self::rpc(&socket, method).await;
            if method == "stop" && result.as_ref().is_ok_and(|v| v["phase"] == "stopped") {
                if let Some(mut child) = children.remove(&directory) {
                    child.kill().await?;
                    child.wait().await?;
                }
                let _ = std::fs::remove_file(&socket);
            }
            result
        })
    }
    fn health<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<bool>> {
        Box::pin(async move {
            let (client, url, token) = self.transport(context).await?;
            let mut response = client
                .get(format!("{url}/health"))
                .bearer_auth(token)
                .timeout(Duration::from_secs(2))
                .send()
                .await?
                .error_for_status()?;
            let mut bytes = Vec::new();
            while let Some(chunk) = response.chunk().await? {
                ensure!(bytes.len() + chunk.len() <= 65536, "Oversized health reply");
                bytes.extend_from_slice(&chunk);
            }
            let value: Value = serde_json::from_slice(&bytes)?;
            Ok(value["ready"] == true && value["protocol"] == "pot-macos-executor-v3")
        })
    }
    fn request<'a>(
        &'a self,
        context: &'a ProviderContext,
        route: &'a str,
        body: &'a Value,
        timeout: Duration,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            ensure!(
                ["execute", "finish", "cancel", "inspect", "permissions"].contains(&route),
                "Unsupported macOS guest capability"
            );
            let (client, url, token) = self.transport(context).await?;
            let mut response = client
                .post(format!("{url}/{route}"))
                .bearer_auth(token)
                .json(body)
                .timeout(timeout)
                .send()
                .await?
                .error_for_status()?;
            let mut bytes = Vec::new();
            while let Some(chunk) = response.chunk().await? {
                ensure!(
                    bytes.len() + chunk.len() <= LIMIT,
                    "Oversized guest response"
                );
                bytes.extend_from_slice(&chunk);
            }
            Ok(serde_json::from_slice(&bytes)?)
        })
    }
    fn shutdown<'a>(&'a self, root: &'a Path) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let directories: Vec<_> = self.children.lock().await.keys().cloned().collect();
            let mut errors = Vec::new();
            for directory in directories {
                let Some(id) = directory.file_name().and_then(|s| s.to_str()) else {
                    continue;
                };
                let context = ProviderContext {
                    root: root.into(),
                    sandbox_id: id.into(),
                };
                if let Err(error) = self.lifecycle(&context, "stop").await {
                    errors.push(error.to_string());
                }
            }
            ensure!(
                errors.is_empty(),
                "macOS shutdown unconfirmed: {}",
                errors.join("; ")
            );
            Ok(())
        })
    }
    fn default_config(&self) -> Value {
        json!({"cpus":4,"memory_gib":8,"disk_gib":64})
    }
    fn validate_instance_config(&self, context: &ProviderContext, config: &Value) -> Result<()> {
        self.validate_config(config)?;
        let bytes = std::fs::metadata(Self::directory(context).join("macos/disk.img"))?.len();
        ensure!(
            bytes == config["disk_gib"].as_u64().unwrap_or(0) * 1024 * 1024 * 1024,
            "macOS disk resizing is not supported; retain the prepared disk size"
        );
        Ok(())
    }
    fn validate_config(&self, config: &Value) -> Result<()> {
        ensure!(
            (2..=32).contains(&config["cpus"].as_u64().unwrap_or(0))
                && (4..=128).contains(&config["memory_gib"].as_u64().unwrap_or(0))
                && (64..=512).contains(&config["disk_gib"].as_u64().unwrap_or(0)),
            "macOS requires 2+ CPUs, 4+ GiB RAM and a prepared 64+ GiB disk"
        );
        Ok(())
    }
}
fn snapshot_skills(destination: &Path) -> Result<()> {
    // Prepared content only: skip symlinks and special files. The guest sees
    // a readonly share; changes to source directories apply on the next boot.
    fn copy(source: &Path, target: &Path, depth: usize) -> Result<()> {
        ensure!(depth < 32, "Skill tree too deep");
        let meta = std::fs::symlink_metadata(source)?;
        if meta.is_dir() {
            std::fs::create_dir_all(target)?;
            for entry in std::fs::read_dir(source)? {
                let entry = entry?;
                copy(&entry.path(), &target.join(entry.file_name()), depth + 1)?;
            }
        } else if meta.is_file() {
            ensure!(meta.len() <= 16 * 1024 * 1024, "Skill resource too large");
            std::fs::copy(source, target)?;
        }
        Ok(())
    }
    if destination.exists() {
        std::fs::remove_dir_all(destination)?;
    }
    std::fs::create_dir_all(destination)?;
    let builtin = std::env::var_os("POT_BUILTIN_SKILLS_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../skills"));
    let user = std::env::var_os("POT_AGENT_SKILLS_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            PathBuf::from(std::env::var_os("HOME").unwrap_or_default()).join(".pot/agent/skills")
        });
    for source in [builtin, user] {
        if !source.is_dir() {
            continue;
        }
        for entry in std::fs::read_dir(source)? {
            let entry = entry?;
            let target = destination.join(entry.file_name());
            if !target.exists() {
                copy(&entry.path(), &target, 0)?;
            }
        }
    }
    Ok(())
}
#[cfg(test)]
mod tests;

/// Copy a complete stopped instance into its fixed private location. Never clones
/// platform identity or overwrites an enrolled VM. Source must be owner-stopped.
pub fn enroll(root: &Path, id: &str, source: &Path) -> Result<()> {
    use std::os::unix::fs::{MetadataExt, PermissionsExt};
    ensure!(
        !id.is_empty()
            && id.len() <= 48
            && id
                .bytes()
                .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-'),
        "Invalid Sandbox ID"
    );
    Manifest::load(source)?;
    let target = root.join("sandboxes").join(id);
    std::fs::create_dir_all(&target)?;
    let metadata = std::fs::symlink_metadata(&target)?;
    ensure!(metadata.is_dir(), "Sandbox directory must not be a link");
    std::fs::set_permissions(&target, std::fs::Permissions::from_mode(0o700))?;
    ensure!(
        !target.join("macos").exists() && !target.join("macos-helper.sock").exists(),
        "macOS instance already enrolled or running"
    );
    let temp = target.join(format!("macos-enroll-{}", uuid::Uuid::new_v4()));
    std::fs::create_dir(&temp)?;
    std::fs::set_permissions(&temp, std::fs::Permissions::from_mode(0o700))?;
    let result = (|| {
        let owner = metadata.uid();
        for name in [
            "manifest.json",
            "hardware-model",
            "machine-identifier",
            "auxiliary-storage",
            "disk.img",
            "guest-ca.pem",
            "executor-token",
        ] {
            ensure!(
                std::fs::symlink_metadata(source.join(name))?.uid() == owner,
                "Bundle must belong to the instance owner"
            );
            std::fs::copy(source.join(name), temp.join(name))?;
            std::fs::set_permissions(temp.join(name), std::fs::Permissions::from_mode(0o600))?;
        }
        Manifest::load(&temp)?;
        std::fs::rename(&temp, target.join("macos"))?;
        let bytes = std::fs::metadata(target.join("macos/disk.img"))?.len();
        let gib = 1024 * 1024 * 1024;
        if bytes >= 64 * gib && bytes % gib == 0 {
            pot_sandbox::private_json(
                &target.join("sandbox.json"),
                &json!({"cpus":4,"memory_gib":8,"disk_gib":bytes/gib}),
            )?;
        }
        Ok(())
    })();
    if result.is_err() {
        let _ = std::fs::remove_dir_all(temp);
    }
    result
}
