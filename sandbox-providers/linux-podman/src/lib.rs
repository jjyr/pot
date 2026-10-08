//! Linux-host, rootless Podman lifecycle. No Podman API service or Host tools.
use anyhow::{Context, Result, bail, ensure};
use futures::future::BoxFuture;
use pot_sandbox::{
    Capabilities, LINUX_PODMAN_PROVIDER, Provider, ProviderContext, ProviderDescriptor,
    private_json,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    os::unix::fs::PermissionsExt,
    path::{Path, PathBuf},
    time::Duration,
};

use tokio::io::{AsyncReadExt, AsyncWriteExt};

pub struct LinuxPodmanProvider {
    client: reqwest::Client,
    image: String,
}
#[derive(Serialize, Deserialize)]
struct Instance {
    name: String,
    image: String,
    generation: u64,
}
impl LinuxPodmanProvider {
    pub fn new() -> Result<Self> {
        Ok(Self {
            client: reqwest::Client::builder()
                .no_proxy()
                .redirect(reqwest::redirect::Policy::none())
                .build()?,
            image: std::env::var("POT_PODMAN_IMAGE")
                .unwrap_or_else(|_| "localhost/pot-sandbox:0.1.0".into()),
        })
    }
    fn directory(c: &ProviderContext) -> PathBuf {
        c.root.join("sandboxes").join(&c.sandbox_id)
    }
    async fn command(args: &[String]) -> Result<std::process::Output> {
        let mut cmd = tokio::process::Command::new("podman");
        cmd.arg("--remote=false")
            .args(args)
            .env_remove("CONTAINER_HOST")
            .env_remove("CONTAINER_CONNECTION")
            .kill_on_drop(true);
        tokio::time::timeout(Duration::from_secs(60), cmd.output())
            .await
            .context("Podman operation timed out; inspect environment before retrying")?
            .context("Podman unavailable; install Podman on the Linux Server")
    }
    async fn checked(args: &[String]) -> Result<Value> {
        let out = Self::command(args).await?;
        // Do not forward runtime diagnostics: inspect/run may include credentials.
        ensure!(
            out.status.success(),
            "Podman {} failed; inspect Podman locally",
            args[0]
        );
        Ok(if out.stdout.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&out.stdout).context("Invalid Podman JSON response")?
        })
    }
    fn load(c: &ProviderContext) -> Result<Option<Instance>> {
        let path = Self::directory(c).join("podman.json");
        if !path.exists() {
            return Ok(None);
        }
        Ok(Some(serde_json::from_slice(&std::fs::read(path)?)?))
    }
    fn save(c: &ProviderContext, i: &Instance) -> Result<()> {
        private_json(&Self::directory(c).join("podman.json"), &json!(i))
    }
    async fn inspect(i: &Instance) -> Result<Option<Value>> {
        let exists = Self::command(&["container".into(), "exists".into(), i.name.clone()]).await?;
        if exists.status.code() == Some(1) {
            return Ok(None);
        }
        ensure!(
            exists.status.success(),
            "Cannot inspect Podman container existence"
        );
        let value = Self::checked(&["container".into(), "inspect".into(), i.name.clone()]).await?;
        let row = &value[0];
        ensure!(
            row["Config"]["Labels"]["dev.pot.instance"] == i.name && row["Image"] == i.image,
            "Podman container identity mismatch"
        );
        Ok(Some(row.clone()))
    }
    async fn prepare(&self, c: &ProviderContext) -> Result<Instance> {
        ensure!(
            cfg!(target_os = "linux"),
            "Podman Provider requires a Linux host"
        );
        let info = Self::checked(&["info".into(), "--format=json".into()]).await?;
        ensure!(
            info["host"]["security"]["rootless"] == true
                && info["host"]["cgroupVersion"] == "v2"
                && info["host"]["cgroupManager"] == "systemd",
            "Rootless Podman with cgroup v2 and systemd is required"
        );
        if let Some(i) = Self::load(c)? {
            return Ok(i);
        }
        let image = Self::checked(&["image".into(), "inspect".into(), self.image.clone()]).await?;
        ensure!(
            image[0]["Labels"]["dev.pot.bootstrap"] == "pot-executor-v2",
            "Build/install the Pot OCI image before starting a Sandbox"
        );
        let id = image[0]["Id"]
            .as_str()
            .context("Podman image identity missing")?
            .to_string();
        let dir = Self::directory(c);
        std::fs::create_dir_all(&dir)?;
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700))?;
        let i = Instance {
            name: format!("pot-{}", uuid::Uuid::new_v4()),
            image: id,
            generation: 0,
        };
        Self::save(c, &i)?;
        Ok(i)
    }
    async fn state(c: &ProviderContext) -> Result<Value> {
        let Some(i) = Self::load(c)? else {
            return Ok(json!({"phase":"stopped", "generation":0}));
        };
        let row = Self::inspect(&i).await?;
        let running = row.as_ref().is_some_and(|r| r["State"]["Running"] == true);
        Ok(
            json!({"phase":if running {"running"} else if row.is_some() {"failed"} else {"stopped"}, "generation":row.as_ref().map(boot_generation).unwrap_or(i.generation)}),
        )
    }
    async fn endpoint(c: &ProviderContext) -> Result<(String, String)> {
        let i = Self::load(c)?.context("Podman environment not prepared")?;
        let row = Self::inspect(&i)
            .await?
            .context("Podman environment stopped")?;
        ensure!(
            row["State"]["Running"] == true,
            "Podman environment stopped"
        );
        let port = &row["NetworkSettings"]["Ports"]["17701/tcp"][0];
        ensure!(
            port["HostIp"] == "127.0.0.1",
            "Executor must bind to loopback"
        );
        let port: u16 = port["HostPort"]
            .as_str()
            .context("Executor port missing")?
            .parse()?;
        let token = std::fs::read_to_string(Self::directory(c).join("executor-token"))?;
        Ok((format!("http://127.0.0.1:{port}"), token))
    }
    async fn desktop_endpoint(&self, c: &ProviderContext) -> Result<(reqwest::Url, String)> {
        let result = self
            .request(c, "desktop", &json!({}), Duration::from_secs(30))
            .await?;
        let token = result["token"]
            .as_str()
            .context("Desktop credential missing")?
            .to_owned();
        let i = Self::load(c)?.context("Podman environment not prepared")?;
        let row = Self::inspect(&i)
            .await?
            .context("Podman environment stopped")?;
        let port = &row["NetworkSettings"]["Ports"]["17681/tcp"][0];
        ensure!(
            port["HostIp"] == "127.0.0.1",
            "Desktop must bind to loopback"
        );
        let port: u16 = port["HostPort"]
            .as_str()
            .context("Desktop port missing")?
            .parse()?;
        Ok((
            reqwest::Url::parse(&format!("http://127.0.0.1:{port}"))?,
            token,
        ))
    }
    async fn start(&self, c: &ProviderContext) -> Result<Value> {
        let mut i = self.prepare(c).await?;
        if let Some(row) = Self::inspect(&i).await? {
            if row["State"]["Running"] == true {
                return Self::state(c).await;
            }
            self.remove(&i).await?;
        }
        let dir = Self::directory(c);
        let config_path = dir.join("sandbox.json");
        let config = if config_path.exists() {
            serde_json::from_slice(&std::fs::read(config_path)?)?
        } else {
            self.default_config()
        };
        self.validate_config(&config)?;
        let work = dir.join("workspace");
        std::fs::create_dir_all(work.join("workspace"))?;
        let home = PathBuf::from(std::env::var_os("HOME").context("HOME unavailable")?);
        let user = std::env::var_os("POT_AGENT_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join(".pot/agent/skills"));
        std::fs::create_dir_all(&user)?;
        let builtin = std::env::var_os("POT_BUILTIN_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                let source = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../skills");
                if source.is_dir() {
                    source
                } else {
                    dir.join("builtin-skills")
                }
            });
        std::fs::create_dir_all(&builtin)?;
        let token = format!(
            "{}{}",
            uuid::Uuid::new_v4().simple(),
            uuid::Uuid::new_v4().simple()
        );
        // Never mount control files in /data or pass credentials in argv.
        private_text(&dir.join("executor-token"), &token)?;
        let env_path = dir.join("podman.env");
        private_text(
            &env_path,
            &format!("POT_EXECUTOR_TOKEN={token}\nPOT_CONTAINER=podman\n"),
        )?;
        i.generation = i
            .generation
            .checked_add(1)
            .context("Podman generation exhausted")?;
        Self::save(c, &i)?;
        let mut args = vec![
            "run".into(),
            "--detach".into(),
            "--pull=never".into(),
            "--name".into(),
            i.name.clone(),
            "--label".into(),
            format!("dev.pot.instance={}", i.name),
            "--userns=keep-id:uid=1000,gid=1000".into(),
            "--user=0:0".into(),
            "--cgroupns=private".into(),
            "--systemd=always".into(),
            "--stop-signal=SIGTERM".into(),
            "--cap-add=SYS_ADMIN".into(),
            "--security-opt=no-new-privileges".into(),
            "--network=pasta:--no-map-gw".into(),
            "--no-hosts".into(),
            "--publish=127.0.0.1::17701".into(),
            "--publish=127.0.0.1::17681".into(),
            format!("--cpus={}", config["cpus"]),
            format!("--memory={}g", config["memory_gib"]),
            "--pids-limit=2048".into(),
            "--env-file".into(),
            env_path.to_string_lossy().into_owned(),
        ];
        for (src, dst, ro) in [
            (work, "/data", false),
            (user, "/run/pot-skill-sources/user", false),
            (builtin, "/run/pot-skill-sources/builtin", true),
        ] {
            let src = src.canonicalize()?;
            ensure!(
                !src.to_string_lossy().contains([':', ',']),
                "Unsupported mount path"
            );
            args.push("--volume".into());
            args.push(format!(
                "{}:{dst}:{}",
                src.display(),
                if ro { "ro" } else { "rw" }
            ));
        }
        for mapping in pot_sandbox::host_mappings(&config)? {
            args.push("--volume".into());
            args.push(format!(
                "{}:{}:{}",
                mapping.source.display(),
                mapping.destination.display(),
                if mapping.read_only { "ro" } else { "rw" }
            ));
        }
        args.extend([
            i.image.clone(),
            // Podman's --init conflicts with --systemd=always (needed for the
            // writable delegated cgroup mount). Use the image's pinned init.
            "/sbin/tini".into(),
            "--".into(),
            "/bin/sh".into(),
            "/opt/pot/sandbox.sh".into(),
        ]);
        let result = Self::command(&args).await;
        let _ = std::fs::remove_file(env_path);
        ensure!(
            result?.status.success(),
            "Podman run failed; inspect environment locally before retrying"
        );
        Self::state(c).await
    }
    async fn remove(&self, i: &Instance) -> Result<()> {
        let out = Self::command(&["rm".into(), "--force".into(), i.name.clone()]).await?;
        ensure!(
            out.status.success(),
            "Podman removal failed; process cleanup unconfirmed"
        );
        ensure!(
            Self::inspect(i).await?.is_none(),
            "Podman container still exists"
        );
        Ok(())
    }
}
impl Provider for LinuxPodmanProvider {
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: LINUX_PODMAN_PROVIDER.into(),
            host_os: "linux".into(),
            engine: "podman".into(),
            os: "linux".into(),
            capabilities: Capabilities {
                shell: true,
                files: true,
                desktop: true,
                shared_execution: true,
            },
        }
    }
    fn desktop_capabilities(&self) -> pot_sandbox::DesktopCapabilities {
        pot_sandbox::DesktopCapabilities {
            viewing: true,
            input: true,
            clipboard: true,
            browser: true,
            protocol: Some(pot_sandbox::DesktopProtocol::Rfb38),
        }
    }
    fn prepare_desktop<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            self.request(context, "desktop", &json!({}), Duration::from_secs(30))
                .await?;
            for _ in 0..100 {
                let (mut url, token) = self.desktop_endpoint(context).await?;
                url.set_path("/api/status");
                if self
                    .client
                    .get(url.clone())
                    .header("X-Pot-Token", &token)
                    .timeout(Duration::from_secs(1))
                    .send()
                    .await
                    .is_ok_and(|r| r.status().is_success())
                {
                    return Ok(());
                }
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
            anyhow::bail!("Desktop service did not become ready")
        })
    }
    fn desktop_browser<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let (mut url, token) = self.desktop_endpoint(context).await?;
            url.set_path("/api/browser");
            self.client
                .post(url)
                .header("X-Pot-Token", &token)
                .json(&json!({}))
                .send()
                .await?
                .error_for_status()?;
            Ok(())
        })
    }
    fn desktop_metrics<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            let (mut url, token) = self.desktop_endpoint(context).await?;
            url.set_path("/api/metrics");
            Ok(self
                .client
                .get(url)
                .header("X-Pot-Token", &token)
                .timeout(Duration::from_secs(3))
                .send()
                .await?
                .error_for_status()?
                .json()
                .await?)
        })
    }
    fn open_desktop<'a>(
        &'a self,
        context: &'a ProviderContext,
    ) -> BoxFuture<'a, Result<pot_sandbox::DesktopStream>> {
        Box::pin(async move {
            use futures::{SinkExt, StreamExt};
            self.request(context, "desktop", &json!({}), Duration::from_secs(30))
                .await?;
            let (viewer, token) = self.desktop_endpoint(context).await?;
            let mut control = viewer.clone();
            control.set_fragment(None);
            control.set_path("/api/control");
            self.client
                .post(control)
                .timeout(Duration::from_secs(5))
                .header("X-Pot-Token", &token)
                .json(&json!({"action":"take"}))
                .send()
                .await?
                .error_for_status()?;
            let mut url = viewer.clone();
            url.set_fragment(None);
            url.set_scheme("ws")
                .map_err(|_| anyhow::anyhow!("Invalid viewer URL"))?;
            url.set_path("/rfb");
            url.query_pairs_mut().append_pair("token", &token);
            let (ws, _) = tokio_tungstenite::connect_async(url.as_str())
                .await
                .map_err(|_| anyhow::anyhow!("Private desktop transport failed"))?;
            let (client, bridge) = tokio::io::duplex(65536);
            tokio::spawn(async move {
                let (mut tx, mut rx) = ws.split();
                let (mut rd, mut wr) = tokio::io::split(bridge);
                let up = async {
                    let mut buf = [0; 65536];
                    loop {
                        let n = rd.read(&mut buf).await?;
                        if n == 0 {
                            break;
                        }
                        tx.send(tokio_tungstenite::tungstenite::Message::Binary(
                            buf[..n].to_vec().into(),
                        ))
                        .await?;
                    }
                    anyhow::Ok(())
                };
                let down = async {
                    while let Some(msg) = rx.next().await {
                        match msg? {
                            tokio_tungstenite::tungstenite::Message::Binary(data) => {
                                wr.write_all(&data).await?
                            }
                            tokio_tungstenite::tungstenite::Message::Close(_) => break,
                            _ => {}
                        }
                    }
                    anyhow::Ok(())
                };
                tokio::select! { _=up=>{}, _=down=>{} }
            });
            Ok(Box::pin(client) as pot_sandbox::DesktopStream)
        })
    }
    fn workspace(&self, root: &Path, id: &str) -> PathBuf {
        root.join("sandboxes").join(id).join("workspace/workspace")
    }
    fn guest_workspace(&self) -> &str {
        "/data/workspace"
    }
    fn lifecycle<'a>(
        &'a self,
        c: &'a ProviderContext,
        method: &'a str,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            match method {
                "status" => Self::state(c).await,
                "prepare" => {
                    self.prepare(c).await?;
                    Self::state(c).await
                }
                "start" => self.start(c).await,
                "stop" => {
                    if let Some(i) = Self::load(c)?
                        && Self::inspect(&i).await?.is_some()
                    {
                        self.remove(&i).await?;
                    }
                    Self::state(c).await
                }
                _ => bail!("Unsupported Podman lifecycle operation"),
            }
        })
    }
    fn health<'a>(&'a self, c: &'a ProviderContext) -> BoxFuture<'a, Result<bool>> {
        Box::pin(async move {
            let (url, token) = Self::endpoint(c).await?;
            let response = self
                .client
                .get(format!("{url}/health"))
                .bearer_auth(token)
                .timeout(Duration::from_secs(2))
                .send()
                .await?;
            Ok(response.status().is_success())
        })
    }
    fn request<'a>(
        &'a self,
        c: &'a ProviderContext,
        route: &'a str,
        body: &'a Value,
        timeout: Duration,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            ensure!(
                [
                    "execute",
                    "finish",
                    "cancel",
                    "inspect",
                    "desktop",
                    "permissions",
                    "capabilities"
                ]
                .contains(&route),
                "Unsupported executor route"
            );
            let (url, token) = Self::endpoint(c).await?;
            let response = self
                .client
                .post(format!("{url}/{route}"))
                .bearer_auth(token)
                .json(body)
                .timeout(timeout)
                .send()
                .await?;
            let success = response.status().is_success();
            let value: Value = response.json().await?;
            ensure!(
                success,
                "Sandbox executor rejected request: {}",
                value["error"].as_str().unwrap_or("unknown")
            );
            Ok(value)
        })
    }
    fn shutdown<'a>(&'a self, root: &'a Path) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let directory = root.join("sandboxes");
            if !directory.exists() {
                return Ok(());
            }
            for entry in std::fs::read_dir(directory)? {
                let entry = entry?;
                if entry.file_type()?.is_dir() && entry.path().join("podman.json").is_file() {
                    let context = ProviderContext {
                        root: root.into(),
                        sandbox_id: entry.file_name().to_string_lossy().into_owned(),
                    };
                    self.lifecycle(&context, "stop").await?;
                }
            }
            Ok(())
        })
    }
    fn default_config(&self) -> Value {
        json!({"cpus":2,"memory_gib":2,"disk_gib":0})
    }
    fn validate_config(&self, c: &Value) -> Result<()> {
        ensure!(
            (1..=32).contains(&c["cpus"].as_u64().unwrap_or(0))
                && (1..=128).contains(&c["memory_gib"].as_u64().unwrap_or(0)),
            "Invalid container CPU/memory limits"
        );
        ensure!(
            c["disk_gib"] == 0,
            "Podman disk quotas are unavailable; disk_gib must be 0"
        );
        Ok(())
    }
}

fn private_text(path: &Path, contents: &str) -> Result<()> {
    use std::{io::Write, os::unix::fs::OpenOptionsExt};
    let temp = path.with_extension(format!("{}.tmp", uuid::Uuid::new_v4()));
    let mut file = std::fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(contents.as_bytes())?;
    file.sync_all()?;
    std::fs::rename(temp, path)?;
    Ok(())
}
fn boot_generation(row: &Value) -> u64 {
    // Podman restart changes StartedAt even when the container ID is retained.
    let identity = format!("{}:{}", row["Id"], row["State"]["StartedAt"]);
    identity.bytes().fold(0xcbf29ce484222325u64, |hash, b| {
        (hash ^ u64::from(b)).wrapping_mul(0x100000001b3)
    })
}
