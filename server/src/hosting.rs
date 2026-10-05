use crate::{ServerConfig, router};
use anyhow::Result;
use pot_protocol::{RUNTIME_ENDPOINT_FILE, RuntimeEndpoint};
use pot_runtime::Backend;
pub struct Server {
    backend: Backend,
    listener: tokio::net::TcpListener,
}
impl Server {
    pub async fn bind(config: ServerConfig) -> Result<Self> {
        // Bind first: listener conflicts must not initialize or recover product data.
        let listener = tokio::net::TcpListener::bind(&config.bind).await?;
        let backend = pot_runtime::initialize(config.runtime).await?;
        let endpoint = backend.endpoint(listener.local_addr()?.port());
        publish_endpoint(backend.data_dir(), &endpoint)?;
        backend.activate()?;
        Ok(Self { backend, listener })
    }
    pub fn local_addr(&self) -> Result<std::net::SocketAddr> {
        Ok(self.listener.local_addr()?)
    }
    pub fn endpoint(&self) -> Result<RuntimeEndpoint> {
        Ok(self.backend.endpoint(self.local_addr()?.port()))
    }
    pub async fn serve(
        self,
        shutdown: impl std::future::Future<Output = ()> + Send + 'static,
    ) -> Result<()> {
        let router = router(self.backend.state());
        let mut backend = self.backend;
        axum::serve(self.listener, router)
            .with_graceful_shutdown(async move {
                shutdown.await;
                backend.shutdown().await;
            })
            .await?;
        Ok(())
    }
}
fn publish_endpoint(data: &std::path::Path, endpoint: &RuntimeEndpoint) -> Result<()> {
    use std::{io::Write, os::unix::fs::OpenOptionsExt};
    let path = data.join(RUNTIME_ENDPOINT_FILE);
    let temp = path.with_extension("tmp");
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(&serde_json::to_vec(endpoint)?)?;
    file.sync_all()?;
    std::fs::rename(temp, path)?;
    Ok(())
}
