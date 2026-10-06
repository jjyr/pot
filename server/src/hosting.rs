use crate::{ServerConfig, router};
use anyhow::Result;
use pot_protocol::{RUNTIME_ENDPOINT_FILE, RuntimeEndpoint};
use pot_runtime::Backend;
pub struct Server {
    backend: Backend,
    listener: tokio::net::TcpListener,
    access: Option<(
        crate::access::Access,
        std::net::TcpListener,
        axum_server::tls_rustls::RustlsConfig,
    )>,
}
impl Server {
    pub async fn bind(config: ServerConfig) -> Result<Self> {
        // Bind first: listener conflicts must not initialize or recover product data.
        let listener = tokio::net::TcpListener::bind(&config.bind).await?;
        let access_config = crate::access::AccessConfig::from_env()?;
        anyhow::ensure!(
            access_config.is_none() || listener.local_addr()?.ip().is_loopback(),
            "Mobile access administration requires a loopback native listener"
        );
        let access_listener = if let Some(ref cfg) = access_config {
            use std::os::unix::fs::PermissionsExt;
            anyhow::ensure!(
                std::fs::metadata(&cfg.private_key)?.permissions().mode() & 0o077 == 0,
                "TLS private key must not be accessible to group/others"
            );
            let _ = rustls::crypto::ring::default_provider().install_default();
            let tls = axum_server::tls_rustls::RustlsConfig::from_pem_file(
                &cfg.certificate,
                &cfg.private_key,
            )
            .await?;
            let listener = std::net::TcpListener::bind(cfg.bind)?;
            listener.set_nonblocking(true)?;
            Some((listener, tls))
        } else {
            None
        };
        let mut backend = pot_runtime::initialize(config.runtime).await?;
        let endpoint = backend.endpoint(listener.local_addr()?.port());

        let access = if let Some(cfg) = access_config {
            let mut access = match crate::access::Access::new(
                &cfg,
                backend.data_dir().to_path_buf(),
                endpoint.token.clone(),
                router(backend.state()),
            ) {
                Ok(access) => access,
                Err(error) => {
                    backend.shutdown().await;
                    return Err(error);
                }
            };
            access.notifications = Some(backend.state());
            let (listener, tls) = access_listener.unwrap();
            Some((access, listener, tls))
        } else {
            None
        };
        if let Err(error) =
            publish_endpoint(backend.data_dir(), &endpoint).and_then(|_| backend.activate())
        {
            backend.shutdown().await;
            return Err(error);
        }
        Ok(Self {
            backend,
            listener,
            access,
        })
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
        let mut router = router(self.backend.state());
        let (public_stop, stop_rx) = tokio::sync::watch::channel(false);
        let (public_failed, mut failure_rx) = tokio::sync::watch::channel(false);
        let mut public_task = None;
        let mut push_task = None;
        if let Some((access, listener, tls)) = self.access {
            router = router.merge(crate::access::admin_router(access.clone()));
            push_task = Some(tokio::spawn(crate::access::push::dispatch(access.clone())));
            let failure = public_failed.clone();
            public_task = Some(tokio::spawn(async move {
                let result = serve_public(access, listener, tls, stop_rx).await;
                failure.send_replace(true);
                result
            }));
        }
        let mut backend = self.backend;
        axum::serve(self.listener, router)
            .with_graceful_shutdown(async move {
                tokio::select! { _=shutdown=>{}, _=failure_rx.changed()=>{} }
                public_stop.send_replace(true);
                if let Some(task) = push_task {
                    task.abort();
                }

                backend.shutdown().await;
            })
            .await?;
        if let Some(task) = public_task {
            task.await??;
        }
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

async fn serve_public(
    access: crate::access::Access,
    listener: std::net::TcpListener,
    tls: axum_server::tls_rustls::RustlsConfig,
    mut stop: tokio::sync::watch::Receiver<bool>,
) -> Result<()> {
    let addr = listener.local_addr()?;
    let mut listener = Some(listener);
    let mut policy = access.policy.subscribe();
    loop {
        if *stop.borrow() {
            return Ok(());
        }
        if !*policy.borrow() {
            // Disabled means no accepting socket, including across restart.
            listener.take();
            tokio::select! { _=stop.wait_for(|stopped|*stopped)=>return Ok(()), _=policy.wait_for(|enabled|*enabled)=>{} }
        }
        let listener = match listener.take() {
            Some(l) => l,
            None => {
                let l = std::net::TcpListener::bind(addr)?;
                l.set_nonblocking(true)?;
                l
            }
        };
        let handle = axum_server::Handle::new();
        let public = crate::access::public_router(access.clone());
        let serve = axum_server::from_tcp_rustls(listener, tls.clone())?
            .handle(handle.clone())
            .serve(public.into_make_service());
        tokio::pin!(serve);
        tokio::select! {
            result=&mut serve=>return result.map_err(Into::into),
            _=stop.wait_for(|stopped|*stopped)=>{},
            _=policy.wait_for(|enabled|!*enabled)=>{},
        }
        handle.graceful_shutdown(Some(std::time::Duration::from_secs(2)));
        serve.await?;
    }
}
