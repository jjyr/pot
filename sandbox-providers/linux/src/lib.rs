//! Apple Containerization lifecycle and authenticated Linux guest transport.
use anyhow::{Result, ensure};
use futures::future::BoxFuture;
use pot_sandbox::{Capabilities, LINUX_PROVIDER, Provider, ProviderContext, ProviderDescriptor};
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    time::Duration,
};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};

pub struct LinuxProvider {
    client: reqwest::Client,
}
impl LinuxProvider {
    pub fn new() -> Result<Self> {
        Ok(Self {
            client: reqwest::Client::builder()
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
impl Provider for LinuxProvider {
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: LINUX_PROVIDER.into(),
            os: "linux".into(),
            capabilities: Capabilities {
                shell: true,
                files: true,
                desktop: true,
                shared_execution: true,
            },
        }
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
                ["execute", "finish", "cancel", "desktop"].contains(&route),
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
