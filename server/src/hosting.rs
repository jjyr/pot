use crate::{ServerConfig, router};
use anyhow::Result;
use pot_protocol::{RUNTIME_ENDPOINT_FILE, RuntimeEndpoint};
use pot_runtime::Backend;
pub struct Server {
    backend: Backend,
    embedded: bool,
    listener: tokio::net::TcpListener,
    access: Option<(
        crate::access::Access,
        std::net::TcpListener,
        Option<axum_server::tls_rustls::RustlsConfig>,
    )>,
}
impl Server {
    pub async fn bind(config: ServerConfig) -> Result<Self> {
        Self::bind_mode(config, false).await
    }
    /// App requests dispatch in memory. TCP is restricted to subprocess model/MCP integration.
    pub async fn embedded(mut config: ServerConfig) -> Result<Self> {
        config.bind = "127.0.0.1:0".into();
        Self::bind_mode(config, true).await
    }
    pub fn local_client(&self) -> crate::LocalClient {
        let mut routes = router(self.backend.state());
        if let Some((a, _, _)) = &self.access {
            routes = routes.merge(crate::access::admin_router(a.clone()));
        }
        crate::LocalClient::new(
            routes,
            self.backend.endpoint(0).token,
            self.backend.instance_lock(),
        )
    }
    async fn bind_mode(config: ServerConfig, embedded: bool) -> Result<Self> {
        // Bind first: listener conflicts must not initialize or recover product data.
        let listener = tokio::net::TcpListener::bind(&config.bind).await?;
        let access_config = crate::access::AccessConfig::from_env()?;
        anyhow::ensure!(
            access_config.is_none() || listener.local_addr()?.ip().is_loopback(),
            "Mobile access administration requires a loopback native listener"
        );
        let access_listener = if let Some(ref cfg) = access_config {
            let tls = if cfg.origin.scheme() == "https" {
                use std::os::unix::fs::PermissionsExt;
                anyhow::ensure!(
                    std::fs::metadata(&cfg.private_key)?.permissions().mode() & 0o077 == 0,
                    "TLS private key must not be accessible to group/others"
                );
                let _ = rustls::crypto::ring::default_provider().install_default();
                Some(
                    axum_server::tls_rustls::RustlsConfig::from_pem_file(
                        &cfg.certificate,
                        &cfg.private_key,
                    )
                    .await?,
                )
            } else {
                None
            };
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
            embedded,
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
    /// Standalone CLI entry point: management is protected by OS socket permissions.
    pub async fn serve_local(
        self,
        shutdown: impl std::future::Future<Output = ()> + Send + 'static,
    ) -> Result<()> {
        let _guard = self.backend.instance_lock();
        use std::os::unix::fs::PermissionsExt;
        let path = self.backend.data_dir().join("control.sock");
        // The Backend owns the directory lock; any previous socket is stale.
        if path.exists() {
            std::fs::remove_file(&path)?;
        }
        let listener = tokio::net::UnixListener::bind(&path)?;
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))?;
        let token = self.backend.endpoint(0).token;
        let mut local = router(self.backend.state());
        if let Some((a, _, _)) = &self.access {
            local = local.merge(crate::access::admin_router(a.clone()));
        }
        local = local.layer(axum::middleware::from_fn(
            move |mut req: axum::extract::Request, next: axum::middleware::Next| {
                let token = token.clone();
                async move {
                    if req.headers().contains_key("origin") {
                        return axum::response::IntoResponse::into_response(
                            axum::http::StatusCode::FORBIDDEN,
                        );
                    }
                    req.headers_mut()
                        .insert("authorization", format!("Bearer {token}").parse().unwrap());
                    next.run(req).await
                }
            },
        ));
        let (stop, rx) = tokio::sync::oneshot::channel();
        let task = tokio::spawn(async move {
            axum::serve(listener, local)
                .with_graceful_shutdown(async {
                    let _ = rx.await;
                })
                .await
        });
        let result = self.serve(shutdown).await;
        let _ = stop.send(());
        let socket_result = task.await?;
        let _ = std::fs::remove_file(path);
        result?;
        socket_result?;
        Ok(())
    }
    pub async fn serve(
        self,
        shutdown: impl std::future::Future<Output = ()> + Send + 'static,
    ) -> Result<()> {
        let _guard = self.backend.instance_lock();
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
        if self.embedded {
            router = router.layer(axum::middleware::from_fn(
                |req: axum::extract::Request, next: axum::middleware::Next| async move {
                    let p = req.uri().path();
                    if p == "/api/v1/mcp" || p == "/api/agent/compactions" || p.starts_with("/gw/")
                    {
                        next.run(req).await
                    } else {
                        axum::response::IntoResponse::into_response(
                            axum::http::StatusCode::FORBIDDEN,
                        )
                    }
                },
            ));
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
    tls: Option<axum_server::tls_rustls::RustlsConfig>,
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
        let serving_handle = handle.clone();
        let public = crate::access::public_router(access.clone());
        let serve = async {
            if let Some(tls) = tls.clone() {
                axum_server::from_tcp_rustls(listener, tls)?
                    .handle(serving_handle.clone())
                    .serve(public.into_make_service())
                    .await
            } else {
                axum_server::from_tcp(listener)?
                    .handle(serving_handle.clone())
                    .serve(public.into_make_service())
                    .await
            }
        };
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
