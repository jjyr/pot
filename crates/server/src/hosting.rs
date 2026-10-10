use crate::{ServerConfig, router};
use anyhow::Result;
use pot_protocol::{RUNTIME_ENDPOINT_FILE, RuntimeEndpoint};
use pot_runtime::Backend;
use std::sync::Arc;
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
        config.composition = Some(pot_runtime::host::Composition::Assistant);
        Self::bind_mode(config, true).await
    }
    pub fn local_client(&self) -> crate::LocalClient {
        let mut routes = backend_router(&self.backend);
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
        let composition = match config.composition {
            Some(composition) => composition,
            None => pot_runtime::host::Composition::load(&config.runtime.data)?,
        };
        let ts_hostname = std::env::var("POT_TAILSCALE_HOSTNAME").ok();
        let use_ts = embedded || ts_hostname.is_some();
        let access_config = if composition != pot_runtime::host::Composition::Assistant {
            None
        } else if use_ts {
            Some(crate::access::AccessConfig {
                origin: url::Url::parse("http://localhost/")?,
                bind: "127.0.0.1:0".parse()?,
                certificate: Default::default(),
                private_key: Default::default(),
                assets: std::env::var_os("POT_ACCESS_ASSETS")
                    .map(std::path::PathBuf::from)
                    .unwrap_or_else(|| {
                        std::env::current_exe()
                            .unwrap()
                            .parent()
                            .unwrap()
                            .join("../Resources/runtime/mobile")
                    }),
            })
        } else {
            crate::access::AccessConfig::from_env()?
        };
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
        let providers: Vec<Arc<dyn pot_sandbox::Provider>> = if !composition.has_environments() {
            vec![]
        } else if cfg!(target_os = "linux") {
            vec![Arc::new(
                pot_sandbox_linux_podman::LinuxPodmanProvider::new()?,
            )]
        } else {
            vec![
                Arc::new(pot_sandbox_macos_apple_container::MacOSAppleContainerProvider::new()?),
                Arc::new(pot_sandbox_macos_apple_vm::MacOSAppleVMProvider::new(
                    macos_helper_path(),
                )),
            ]
        };
        let mut backend =
            pot_runtime::initialize_composition(config.runtime, composition, providers).await?;
        let endpoint = backend.endpoint(listener.local_addr()?.port());

        let access = if let Some(cfg) = access_config {
            let mut access = match crate::access::Access::new(
                &cfg,
                backend.data_dir().to_path_buf(),
                endpoint.token.clone(),
                backend_router(&backend),
            ) {
                Ok(access) => access,
                Err(error) => {
                    backend.shutdown().await;
                    return Err(error);
                }
            };
            access.notifications = backend.optional_state();
            let (listener, tls) = access_listener.unwrap();
            if use_ts {
                access.tailscale = Some(crate::access::tailscale::Tailscale::new(
                    access.data.clone(),
                    ts_hostname.unwrap_or_else(|| "pot".into()),
                    listener.local_addr()?.port(),
                ));
                let preferences = {
                    let mut inner = access.inner.lock().unwrap();
                    let preferences = inner.credentials.web.unwrap_or_else(|| {
                        if access.data.join("tailscale-enabled").exists() && inner.enabled {
                            crate::access::web::Preferences {
                                tailscale: true,
                                https: true,
                                lan: false,
                            }
                        } else {
                            Default::default()
                        }
                    });
                    inner.credentials.web = Some(preferences);
                    inner.credentials.disabled = !(preferences.lan || preferences.tailscale);
                    inner.enabled = !inner.credentials.disabled;
                    access.persist(&inner)?;
                    access.policy.send_replace(inner.enabled);
                    preferences
                };
                access.endpoint_enabled = Some(Arc::new(std::sync::atomic::AtomicBool::new(false)));
                access.web = Some(crate::access::web::Web::new(preferences));
            }
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
    pub fn composition(&self) -> pot_runtime::host::Composition {
        self.backend.host().composition()
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
        let mut local = backend_router(&self.backend);
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
        let mut router = backend_router(&self.backend);
        let (public_stop, stop_rx) = tokio::sync::watch::channel(false);
        let (public_failed, mut failure_rx) = tokio::sync::watch::channel(false);
        let mut public_task = None;
        let mut ts_node = None;
        let mut ts_restore = None;
        let mut web_manager = None;
        let mut push_task = None;
        if let Some((access, listener, tls)) = self.access {
            if let Some(web) = &access.web {
                web.restore(&access).await;
                web_manager = Some(web.clone());
            }
            if let Some(ts) = access.tailscale.clone() {
                ts_node = Some(ts.clone());
                let a = access.clone();
                ts_restore = Some(tokio::spawn(async move {
                    loop {
                        if let Some(web) = &a.web {
                            web.maintain_tailnet(&a).await;
                        }
                        tokio::time::sleep(std::time::Duration::from_secs(10)).await;
                    }
                }));
            }
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
                    if p.starts_with("/desktop/")
                        || p.starts_with("/desktop-assets/")
                        || p == "/api/v1/mcp"
                        || p == "/api/agent/compactions"
                        || p.starts_with("/gw/")
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

                if let Some(task) = ts_restore {
                    task.abort();
                }
                if let Some(web) = web_manager {
                    web.shutdown().await;
                }
                if let Some(ts) = ts_node {
                    let _ = ts.stop().await;
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

fn macos_helper_path() -> std::path::PathBuf {
    if let Some(path) = std::env::var_os("POT_MACOS_HELPER") {
        return path.into();
    }
    if let Some(resources) = std::env::var_os("POT_RUNTIME_RESOURCES") {
        return std::path::PathBuf::from(resources).join("PotMacOSHelper");
    }
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.join("PotMacOSHelper")))
        .unwrap_or_default()
}

fn backend_router(backend: &Backend) -> axum::Router {
    let routes = backend.host().router();
    match backend.optional_state() {
        Some(service) => {
            let hub = backend.assistants().unwrap();
            let integration = hub.clone();
            let product = router(service).layer(axum::middleware::from_fn(
                move |request: axum::extract::Request, next: axum::middleware::Next| {
                    let hub = integration.clone();
                    async move {
                        if let Some(service) = hub.service_for(request.headers()) {
                            if service.owner_id() != "main" {
                                use tower::ServiceExt;
                                return router(service).oneshot(request).await.unwrap();
                            }
                        }
                        next.run(request).await
                    }
                },
            ));
            routes
                .merge(product)
                .merge(crate::routes::assistant_routes(hub))
        }
        None => routes,
    }
}
