//! Apple Containerization lifecycle and authenticated Linux guest transport.
use anyhow::{Result, ensure};
use futures::future::BoxFuture;
use pot_sandbox::{
    Capabilities, MACOS_APPLE_CONTAINER_PROVIDER, Provider, ProviderContext, ProviderDescriptor,
};
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    time::Duration,
};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};

pub struct MacOSAppleContainerProvider {
    client: reqwest::Client,
}
impl MacOSAppleContainerProvider {
    pub fn new() -> Result<Self> {
        Ok(Self {
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
            let mut socket =
                tokio::net::UnixStream::connect(context.root.join("helper.sock")).await?;
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
                ["execute", "finish", "cancel", "inspect", "desktop"].contains(&route),
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
