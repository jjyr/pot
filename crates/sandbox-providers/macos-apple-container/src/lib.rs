//! Apple Containerization lifecycle and authenticated Linux guest transport.
use anyhow::{Result, ensure};
use futures::future::BoxFuture;
use pot_sandbox::{
    Capabilities, MACOS_APPLE_CONTAINER_PROVIDER, Provider, ProviderContext, ProviderDescriptor,
};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    time::Duration,
};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};

pub struct MacOSAppleContainerProvider {
    client: reqwest::Client,
    helper: Option<PathBuf>,
    children: tokio::sync::Mutex<BTreeMap<PathBuf, tokio::process::Child>>,
}
impl MacOSAppleContainerProvider {
    pub fn new() -> Result<Self> {
        Ok(Self {
            helper: std::env::var_os("POT_CONTAINER_HELPER").map(PathBuf::from),
            children: Default::default(),
            client: reqwest::Client::builder()
                .no_proxy()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(Duration::from_secs(660))
                .build()?,
        })
    }
    async fn endpoint(&self, context: &ProviderContext) -> Result<(String, String)> {
        let state = self.lifecycle(context, "status").await?;
        let ip = state["ip"]
            .as_str()
            .ok_or_else(|| anyhow::anyhow!("Sandbox has no address"))?;
        let ip: std::net::IpAddr = ip.parse()?;
        let port = state["executor_port"].as_u64().unwrap_or(17701);
        ensure!((1..=65535).contains(&port), "Invalid executor port");
        let address = std::net::SocketAddr::new(ip, port as u16);
        let token = std::fs::read_to_string(
            context
                .root
                .join("sandboxes")
                .join(&context.sandbox_id)
                .join("executor-token"),
        )?;
        Ok((format!("http://{address}"), token.trim().into()))
    }
}
impl Provider for MacOSAppleContainerProvider {
    fn bind_resources(
        &self,
        context: &ProviderContext,
        resources: &pot_sandbox::PreparedResources,
    ) -> Result<()> {
        let dir = context.root.join("sandboxes").join(&context.sandbox_id);
        std::fs::create_dir_all(&dir)?;
        let path = dir.join("prepared-resources.json");
        if path.exists() {
            let old: pot_sandbox::PreparedResources =
                serde_json::from_slice(&std::fs::read(&path)?)?;
            ensure!(
                old.bundle_id == resources.bundle_id,
                "Sandbox bundle is pinned"
            );
        }
        pot_sandbox::private_json(&path, &serde_json::to_value(resources)?)
    }
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: MACOS_APPLE_CONTAINER_PROVIDER.into(),
            os: "linux".into(),
            host_os: "macos".into(),
            engine: "apple-containerization".into(),
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
                let state = self.lifecycle(context, "status").await?;
                let mut url = reqwest::Url::parse(
                    state["viewer"]
                        .as_str()
                        .ok_or_else(|| anyhow::anyhow!("Desktop unavailable"))?,
                )?;
                let token = url.fragment().unwrap_or("").to_string();
                url.set_fragment(None);
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
            let state = self.lifecycle(context, "status").await?;
            let mut url = reqwest::Url::parse(
                state["viewer"]
                    .as_str()
                    .ok_or_else(|| anyhow::anyhow!("Desktop unavailable"))?,
            )?;
            let token = url.fragment().unwrap_or("").to_string();
            url.set_fragment(None);
            url.set_path("/api/browser");
            self.client
                .post(url)
                .header("X-Pot-Token", token)
                .json(&json!({}))
                .send()
                .await?
                .error_for_status()?;
            Ok(())
        })
    }
    fn desktop_metrics<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            let state = self.lifecycle(context, "status").await?;
            let mut url = reqwest::Url::parse(
                state["viewer"]
                    .as_str()
                    .ok_or_else(|| anyhow::anyhow!("Desktop unavailable"))?,
            )?;
            let token = url.fragment().unwrap_or("").to_string();
            url.set_fragment(None);
            url.set_path("/api/metrics");
            Ok(self
                .client
                .get(url)
                .header("X-Pot-Token", token)
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
            let state = self.lifecycle(context, "status").await?;
            let viewer = reqwest::Url::parse(
                state["viewer"]
                    .as_str()
                    .ok_or_else(|| anyhow::anyhow!("Desktop not ready"))?,
            )?;
            let token = viewer
                .fragment()
                .ok_or_else(|| anyhow::anyhow!("Missing desktop credential"))?;
            let mut control = viewer.clone();
            control.set_fragment(None);
            control.set_path("/api/control");
            self.client
                .post(control)
                .timeout(Duration::from_secs(5))
                .header("X-Pot-Token", token)
                .json(&json!({"action":"take"}))
                .send()
                .await?
                .error_for_status()?;
            let mut url = viewer.clone();
            url.set_fragment(None);
            url.set_scheme("ws")
                .map_err(|_| anyhow::anyhow!("Invalid viewer URL"))?;
            url.set_path("/rfb");
            url.query_pairs_mut().append_pair("token", token);
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
        context: &'a ProviderContext,
        method: &'a str,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            ensure!(
                ["prepare", "start", "status", "stop"].contains(&method),
                "Unsupported lifecycle operation"
            );
            let socket_path = context.root.join("helper.sock");
            let mut children = self.children.lock().await;
            if let Some(child) = children.get_mut(&context.root) {
                ensure!(
                    child.try_wait()?.is_none(),
                    "Sandbox Helper exited; restart Runtime to reconcile"
                );
            } else if let Some(helper) = &self.helper {
                ensure!(
                    !socket_path.exists(),
                    "Unowned Sandbox Helper socket; reconciliation required"
                );
                if method == "status" || method == "stop" {
                    return Ok(
                        json!({"phase":if helper.is_file() {"stopped"} else {"unavailable"},"generation":0}),
                    );
                }
                ensure!(helper.is_file(), "PotHelper is not installed");
                std::fs::create_dir_all(&context.root)?;
                use std::os::unix::fs::OpenOptionsExt;
                let log = std::fs::OpenOptions::new()
                    .create(true)
                    .append(true)
                    .mode(0o600)
                    .open(context.root.join("helper.log"))?;
                let resources = std::env::var_os("POT_RUNTIME_RESOURCES")
                    .map(PathBuf::from)
                    .ok_or_else(|| anyhow::anyhow!("Runtime resources are unavailable"))?;
                let child = tokio::process::Command::new(helper)
                    .arg(&context.root)
                    .arg(resources)
                    .arg("--sandbox-only")
                    .stdin(std::process::Stdio::null())
                    .stdout(std::process::Stdio::from(log.try_clone()?))
                    .stderr(std::process::Stdio::from(log))
                    .kill_on_drop(true)
                    .spawn()?;
                children.insert(context.root.clone(), child);
                for _ in 0..100 {
                    ensure!(
                        children
                            .get_mut(&context.root)
                            .unwrap()
                            .try_wait()?
                            .is_none(),
                        "Sandbox Helper exited during startup"
                    );
                    if socket_path.exists() {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(50)).await;
                }
            } else if !socket_path.exists() {
                if method == "status" || method == "stop" {
                    return Ok(json!({"phase":"unavailable","generation":0}));
                }
                anyhow::bail!("PotHelper is not configured");
            }
            let mut socket = tokio::net::UnixStream::connect(&socket_path).await?;
            drop(children);
            socket.write_all(format!("{}\n",json!({"id":uuid::Uuid::new_v4().to_string(),"method":method,"sandbox_id":context.sandbox_id})).as_bytes()).await?;
            let mut line = String::new();
            let mut reader = BufReader::new(socket).take(65537);
            tokio::time::timeout(Duration::from_secs(30), reader.read_line(&mut line)).await??;
            ensure!(
                line.len() <= 65536 && line.ends_with('\n'),
                "Invalid Helper response"
            );
            let response: Value = serde_json::from_str(&line)?;
            ensure!(
                response.get("error").is_none(),
                "Sandbox Helper rejected operation"
            );
            Ok(response["result"].clone())
        })
    }
    fn shutdown<'a>(&'a self, root: &'a Path) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let mut children = self.children.lock().await;
            if let Some(mut child) = children.remove(root) {
                // Helper owns all instances. SIGTERM invokes its stop-all path before exit.
                if let Some(pid) = child.id() {
                    let status = tokio::process::Command::new("/bin/kill")
                        .arg("-TERM")
                        .arg(pid.to_string())
                        .status()
                        .await?;
                    ensure!(
                        status.success(),
                        "Sandbox Helper termination request failed"
                    );
                }
                match tokio::time::timeout(Duration::from_secs(240), child.wait()).await {
                    Ok(Ok(_)) => {}
                    Ok(Err(error)) => {
                        children.insert(root.to_path_buf(), child);
                        return Err(error.into());
                    }
                    Err(_) => {
                        children.insert(root.to_path_buf(), child);
                        anyhow::bail!("Sandbox Helper shutdown unconfirmed");
                    }
                }
                let socket = root.join("helper.sock");
                if socket.exists() {
                    std::fs::remove_file(socket)?;
                }
            }
            Ok(())
        })
    }
    fn health<'a>(&'a self, context: &'a ProviderContext) -> BoxFuture<'a, Result<bool>> {
        Box::pin(async move {
            let (url, token) = self.endpoint(context).await?;
            Ok(self
                .client
                .get(format!("{url}/health"))
                .bearer_auth(token)
                .timeout(Duration::from_secs(1))
                .send()
                .await
                .is_ok_and(|r| r.status().is_success()))
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
                [
                    "execute",
                    "finish",
                    "cancel",
                    "inspect",
                    "desktop",
                    "prepare",
                    "capabilities"
                ]
                .contains(&route),
                "Unsupported guest route"
            );
            let (url, token) = self.endpoint(context).await?;
            Ok(self
                .client
                .post(format!("{url}/{route}"))
                .bearer_auth(token)
                .json(body)
                .timeout(timeout)
                .send()
                .await?
                .error_for_status()?
                .json()
                .await?)
        })
    }
    fn validate_config(&self, config: &Value) -> Result<()> {
        ensure!(
            (1..=32).contains(&config["cpus"].as_u64().unwrap_or(0))
                && (1..=128).contains(&config["memory_gib"].as_u64().unwrap_or(0))
                && (4..=512).contains(&config["disk_gib"].as_u64().unwrap_or(0)),
            "Invalid Sandbox resources"
        );
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn optional_helper_is_lazy_and_missing_installation_is_explicit() {
        let dir = tempfile::tempdir().unwrap();
        let context = ProviderContext {
            root: dir.path().join("runtime"),
            sandbox_id: "default".into(),
        };
        let mut provider = MacOSAppleContainerProvider::new().unwrap();
        provider.helper = Some(dir.path().join("missing-helper"));
        assert_eq!(
            provider.lifecycle(&context, "status").await.unwrap()["phase"],
            "unavailable"
        );
        assert_eq!(
            provider.lifecycle(&context, "stop").await.unwrap()["phase"],
            "unavailable"
        );
        assert!(provider.children.lock().await.is_empty());
        assert!(!context.root.exists());
        assert!(
            provider
                .lifecycle(&context, "start")
                .await
                .unwrap_err()
                .to_string()
                .contains("not installed")
        );
        provider.shutdown(&context.root).await.unwrap();
    }
    #[tokio::test]
    async fn owned_helper_shutdown_waits_for_graceful_stop_all() {
        let dir = tempfile::tempdir().unwrap();
        let script = dir.path().join("helper.py");
        std::fs::write(
            &script,
            r#"
import signal, sys, time
from pathlib import Path
root=Path(sys.argv[1])
def stop(*_):
    (root/'stopped').write_text('all instances stopped')
    sys.exit(0)
signal.signal(signal.SIGTERM,stop)
(root/'ready').write_text('ready')
while True: time.sleep(0.05)
"#,
        )
        .unwrap();
        let child = tokio::process::Command::new("python")
            .arg(script)
            .arg(dir.path())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        let provider = MacOSAppleContainerProvider::new().unwrap();
        provider
            .children
            .lock()
            .await
            .insert(dir.path().into(), child);
        tokio::time::timeout(Duration::from_secs(5), async {
            while !dir.path().join("ready").exists() {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        provider.shutdown(dir.path()).await.unwrap();
        assert_eq!(
            std::fs::read_to_string(dir.path().join("stopped")).unwrap(),
            "all instances stopped"
        );
        assert!(provider.children.lock().await.is_empty());
    }
    #[tokio::test]
    async fn desktop_readiness_refreshes_credentials_published_after_start() {
        let dir = tempfile::tempdir().unwrap();
        let context = ProviderContext {
            root: dir.path().into(),
            sandbox_id: "default".into(),
        };
        let private = dir.path().join("sandboxes/default");
        std::fs::create_dir_all(&private).unwrap();
        std::fs::write(private.join("executor-token"), "executor").unwrap();
        let http = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = http.local_addr().unwrap().port();
        let server = tokio::spawn(async move {
            loop {
                let (mut socket, _) = http.accept().await.unwrap();
                let mut head = Vec::new();
                let mut byte = [0];
                while !head.ends_with(b"\r\n\r\n") {
                    socket.read_exact(&mut byte).await.unwrap();
                    head.push(byte[0]);
                }
                let head = String::from_utf8(head).unwrap().to_lowercase();
                let ok =
                    head.starts_with("post /desktop") || head.contains("x-pot-token: published");
                socket.write_all(format!("HTTP/1.1 {}\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{{}}", if ok {"200 OK"} else {"403 Forbidden"}).as_bytes()).await.unwrap();
            }
        });
        let helper = tokio::net::UnixListener::bind(dir.path().join("helper.sock")).unwrap();
        let ipc = tokio::spawn(async move {
            let mut count = 0;
            loop {
                let (socket, _) = helper.accept().await.unwrap();
                let mut reader = BufReader::new(socket);
                let mut line = String::new();
                reader.read_line(&mut line).await.unwrap();
                count += 1;
                let state = json!({"phase":"running","ip":"127.0.0.1","executor_port":port,"viewer":format!("http://127.0.0.1:{port}/#{}",if count>=4 {"published"}else{""})});
                reader
                    .get_mut()
                    .write_all(format!("{}\n", json!({"result":state})).as_bytes())
                    .await
                    .unwrap();
            }
        });
        tokio::time::timeout(
            Duration::from_secs(3),
            MacOSAppleContainerProvider::new()
                .unwrap()
                .prepare_desktop(&context),
        )
        .await
        .unwrap()
        .unwrap();
        ipc.abort();
        server.abort();
    }
}
