//! Caller-created resources. Creation restores definitions, never model calls or VM starts.
mod machine_monitor;
use anyhow::{Result, ensure};
use axum::{
    Json, Router,
    extract::State,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::get,
};
use pot_runtime::{Backend, RuntimeConfig};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    io::Write,
    os::unix::fs::OpenOptionsExt,
    path::Path,
    sync::{Arc, RwLock},
};
use tower::ServiceExt;

#[derive(Clone, Serialize, Deserialize, PartialEq)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
enum Resource {
    Host,
    Assistant {
        name: String,
    },
    Sandbox {
        sandbox_id: String,
        name: String,
        provider: String,
    },
}
#[derive(Clone, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct Create {
    request_id: String,
    resource: Resource,
}
impl Create {
    fn validate(&self) -> Result<()> {
        ensure!(
            !self.request_id.is_empty() && self.request_id.len() <= 128,
            "Invalid request identity"
        );
        match &self.resource {
            Resource::Host => {}
            Resource::Assistant { name } => ensure!(
                !name.trim().is_empty()
                    && name.chars().count() <= 80
                    && !name.chars().any(char::is_control),
                "Name must contain 1–80 characters"
            ),
            Resource::Sandbox {
                sandbox_id,
                name,
                provider,
            } => {
                ensure!(
                    !sandbox_id.is_empty()
                        && sandbox_id.len() <= 48
                        && sandbox_id
                            .bytes()
                            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-'),
                    "Invalid Sandbox ID"
                );
                ensure!(
                    !name.trim().is_empty() && name.chars().count() <= 80 && !provider.is_empty(),
                    "Sandbox name and Provider are required"
                );
            }
        }
        Ok(())
    }
}
#[derive(Clone, Serialize, Deserialize)]
struct Entry {
    request: Create,
    result: Option<Value>,
    error: Option<String>,
}
#[derive(Default, Serialize, Deserialize)]
struct Book {
    schema_version: u32,
    entries: Vec<Entry>,
}
struct Inner {
    backend: Backend,
    book: Book,
}
pub(crate) struct Resources {
    changes: pot_core::changes::Changes,
    monitor: std::sync::Mutex<machine_monitor::Monitor>,
    inner: tokio::sync::Mutex<Inner>,
    routes: RwLock<Router>,
    config: RuntimeConfig,
    token: String,
    pub(super) control: Arc<crate::control::Authority>,
}
fn persist(path: &Path, book: &Book) -> Result<()> {
    let temp = path.with_extension("tmp");
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(&serde_json::to_vec_pretty(book)?)?;
    file.sync_all()?;
    std::fs::rename(temp, path)?;
    std::fs::File::open(path.parent().unwrap())?.sync_all()?;
    Ok(())
}
impl Resources {
    pub(super) async fn join_control(&self, request: crate::control::Join) -> Result<Value> {
        // Serialize role changes with resource creation and restoration.
        let inner = self.inner.lock().await;
        ensure!(
            inner.backend.assistants().is_none(),
            "A local Control with assistants cannot join as an execution Runner; use a separate data directory"
        );
        self.control.join(request).await
    }
    #[cfg(test)]
    pub async fn open(backend: Backend, config: RuntimeConfig) -> Result<Arc<Self>> {
        Self::open_policy(backend, config, true).await
    }
    pub async fn open_policy(
        mut backend: Backend,
        config: RuntimeConfig,
        control_allowed: bool,
    ) -> Result<Arc<Self>> {
        let path = config.data.join("runtime-resources.json");
        let book = if path.exists() {
            let book: Book = serde_json::from_slice(&std::fs::read(&path)?)?;
            ensure!(
                book.schema_version == 1,
                "Unsupported Runtime resource schema"
            );
            book
        } else {
            Book {
                schema_version: 1,
                entries: vec![],
            }
        };
        // Shared environments are created by execution rather than resource-book
        // entries. Restore their Provider registration before lease reconciliation.
        if config.data.join("sandboxes.json").exists() {
            backend.register_sandbox_providers(crate::hosting::installed_providers()?)?;
        }
        let token = backend.endpoint(0).token;
        let routes = crate::hosting::backend_router(&backend);
        let control = crate::control::Authority::open_policy(
            &config.data,
            backend.host().runtime_id(),
            control_allowed,
        )?;
        if let Some(state) = backend.optional_state() {
            state.set_remote_execution(control.clone());
        }
        let changes = backend.host().changes.clone();
        control.ui_changed.bind(changes.clone(), "machines");
        let this = Arc::new(Self {
            monitor: Default::default(),
            changes,
            inner: tokio::sync::Mutex::new(Inner { backend, book }),
            routes: RwLock::new(routes),
            config,
            token,
            control,
        });
        {
            let mut inner = this.inner.lock().await;
            for index in 0..inner.book.entries.len() {
                if inner.book.entries[index].error.is_some() {
                    continue;
                }
                let entry = inner.book.entries[index].clone();
                let result = this.apply(&mut inner.backend, &entry).await?;
                inner.book.entries[index].result = Some(result);
            }
            if path.exists() {
                persist(&path, &inner.book)?;
            }
            *this.routes.write().unwrap() = crate::hosting::backend_router(&inner.backend);
        }
        this.control.restore_listener(this.clone()).await?;
        Ok(this)
    }
    async fn apply(&self, backend: &mut Backend, entry: &Entry) -> Result<Value> {
        entry.request.validate()?;
        match &entry.request.resource {
            Resource::Host => {
                backend.enable_host_resource();
                Ok(json!({"kind":"host", "runtime_id":backend.host().runtime_id()}))
            }
            Resource::Assistant { name } => {
                ensure!(
                    !self.control.is_member(),
                    "Execution Runner cannot own assistants; create them on Control"
                );
                let first = backend.assistants().is_none();
                let existing = self.config.data.join("pot-v2.sqlite").exists();
                backend.register_sandbox_providers(crate::hosting::installed_providers()?)?;
                backend
                    .enable_assistant_resources(self.config.clone())
                    .await?;
                backend.state()?.set_remote_execution(self.control.clone());
                let hub = backend.assistants().unwrap();
                if first {
                    if let Some(result) = &entry.result {
                        return Ok(result.clone());
                    }
                    let main = hub
                        .definitions()?
                        .into_iter()
                        .find(|d| d.id == "main")
                        .unwrap();
                    Ok(
                        json!({"kind":"assistant","definition":if existing { main } else { hub.rename("main", name, main.revision)? }}),
                    )
                } else if let Some(result) = &entry.result {
                    Ok(result.clone())
                } else {
                    Ok(
                        json!({"kind":"assistant","definition":hub.create(&entry.request.request_id, name)?}),
                    )
                }
            }
            Resource::Sandbox {
                sandbox_id,
                name,
                provider,
            } => {
                let providers = crate::hosting::installed_providers()?;
                ensure!(
                    providers.iter().any(|p| p.descriptor().id == *provider),
                    "Sandbox Provider unavailable"
                );
                let manager = backend.register_sandbox_providers(providers)?;
                // Ready creation receipts must not undo later rename/config/delete operations.
                // The current Sandbox inventory is authoritative after creation completes.
                if let Some(result) = &entry.result {
                    return Ok(result.clone());
                }
                manager.management(&json!({"action":"create","sandbox_id":sandbox_id,"name":name,"provider":provider}), false, &||false, &|_, action|action()).await
            }
        }
    }
    pub(super) fn authorize(&self, headers: &HeaderMap) -> bool {
        !headers.contains_key("origin")
            && headers.get("authorization").and_then(|h| h.to_str().ok())
                == Some(&format!("Bearer {}", self.token))
    }
    pub fn router(self: &Arc<Self>) -> Router {
        let current = self.clone();
        Router::new()
            .route("/api/v1/runtime/resources", get(list).post(create))
            .route("/api/v1/machines", get(machines))
            .route("/api/v1/machines/watch", get(machine_monitor::watch))
            .route(
                "/api/v1/machine-connections",
                get(crate::control::metadata).post(crate::control::connection_operator),
            )
            .route(
                "/api/v1/runtime/control",
                get(crate::control::metadata).post(crate::control::operator),
            )
            .fallback(move |req: axum::extract::Request| {
                let current = current.clone();
                async move {
                    let routes = current.routes.read().unwrap().clone();
                    routes.oneshot(req).await.unwrap()
                }
            })
            .with_state(self.clone())
            .layer(axum::middleware::from_fn({
                let changes = self.changes.clone();
                move |request: axum::extract::Request, next: axum::middleware::Next| {
                    let changes = changes.clone();
                    async move {
                        let path = request.uri().path().to_owned();
                        let write = request.method() == axum::http::Method::POST
                            && path.starts_with("/api/v1/")
                            && !path.ends_with("/providers/check");
                        let response = next.run(request).await;
                        if write && response.status().is_success() {
                            // Read-only POSTs (notification cursors, queries, membership
                            // heartbeats) must never trigger a refresh feedback loop.
                            let domain = if path.contains("/agent")
                                || path.contains("/providers")
                                || path.ends_with("/instructions")
                            {
                                Some("settings")
                            } else if path.contains("/usage/") {
                                Some("usage")
                            } else if path.contains("/permissions") {
                                Some("permissions")
                            } else if path.contains("/schedules") {
                                Some("schedules")
                            } else if path.contains("/sandbox")
                                || path.contains("/machine-connections")
                                || path == "/api/v1/runtime/resources"
                                || path == "/api/v1/runtime/control"
                            {
                                Some("machines")
                            } else {
                                None
                            };
                            if let Some(domain) = domain {
                                changes.notify(domain);
                            }
                        }
                        response
                    }
                }
            }))
    }
    pub async fn ensure_app_assistant(self: &Arc<Self>) -> Result<()> {
        if self.optional_state().await.is_some() {
            return Ok(());
        }
        let mut headers = HeaderMap::new();
        headers.insert("authorization", format!("Bearer {}", self.token).parse()?);
        let response = create(
            State(self.clone()),
            headers,
            Json(Create {
                request_id: "app-default-assistant".into(),
                resource: Resource::Assistant { name: "Pot".into() },
            }),
        )
        .await;
        ensure!(
            response.status().is_success(),
            "App assistant resource creation failed"
        );
        Ok(())
    }
    #[cfg(test)]
    pub(super) async fn register_test_provider(&self, provider: Arc<dyn pot_sandbox::Provider>) {
        self.inner
            .lock()
            .await
            .backend
            .register_sandbox_providers(vec![provider])
            .unwrap();
    }
    pub(super) async fn assistant_hub(&self) -> Option<Arc<pot_control::control::assistants::Hub>> {
        self.inner.lock().await.backend.assistants()
    }
    /// Only an authenticated shared-environment command may provision Providers.
    /// Registration creates no container and downloads no image.
    pub(super) async fn execution_host(&self, shared: bool) -> Result<pot_control::host::Host> {
        let mut inner = self.inner.lock().await;
        if shared {
            inner
                .backend
                .register_sandbox_providers(crate::hosting::installed_providers()?)?;
            *self.routes.write().unwrap() = crate::hosting::backend_router(&inner.backend);
        }
        Ok(inner.backend.host().clone())
    }
    pub(super) async fn host(&self) -> pot_control::host::Host {
        self.inner.lock().await.backend.host().clone()
    }
    pub async fn shutdown(&self) {
        if let Some(task) = self.monitor.lock().unwrap().task.take() {
            task.abort();
        }
        if let Err(error) = self.control.stop_listener(false).await {
            eprintln!("Control listener shutdown failed: {error}");
        }
        self.control.stop_operations().await;
        self.inner.lock().await.backend.shutdown().await;
    }
    pub async fn optional_state(&self) -> Option<pot_control::control::Service> {
        self.inner.lock().await.backend.optional_state()
    }
}
/// UI discovery is separate from native resource-management authority.
async fn machines(State(s): State<Arc<Resources>>, headers: HeaderMap) -> Response {
    if headers.get("authorization").and_then(|h| h.to_str().ok())
        != Some(&format!("Bearer {}", s.token))
    {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    let runtime = s.inner.lock().await.backend.host().describe();
    let routes = s.routes.read().unwrap().clone();
    let request = axum::http::Request::builder()
        .uri("/api/v1/execution_targets")
        .header("authorization", format!("Bearer {}", s.token))
        .body(axum::body::Body::empty())
        .unwrap();
    let response = routes.oneshot(request).await.unwrap();
    let execution_targets = if response.status() == StatusCode::NOT_FOUND {
        json!({"items":[]})
    } else if response.status().is_success() {
        let bytes = match axum::body::to_bytes(response.into_body(), 1024 * 1024).await {
            Ok(bytes) => bytes,
            Err(_) => return StatusCode::BAD_GATEWAY.into_response(),
        };
        match serde_json::from_slice::<Value>(&bytes) {
            Ok(value) => value,
            Err(_) => return StatusCode::BAD_GATEWAY.into_response(),
        }
    } else {
        return response;
    };
    // The tools inventory includes all connected targets. A machine card must
    // contain only execution targets owned by that exact Runtime.
    let local_execution_targets = execution_targets["items"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|item| item["execution_target"]["runtime_id"] == runtime["runtime_id"])
        .cloned()
        .collect::<Vec<_>>();
    let mut machines = vec![json!({
        "runtime_id":runtime["runtime_id"],"local":true,"connection":"online",
        "tags":s.control.local_tags(),"capabilities":runtime["capabilities"],"boot_id":runtime["boot_id"],
        "execution_targets":local_execution_targets,"default":execution_targets["selected"]
    })];
    machines.extend(s.control.machines().await);
    Json(json!({"schema_version":1,"machines":machines,"control_authority":s.control.metadata()}))
        .into_response()
}
async fn list(State(s): State<Arc<Resources>>, headers: HeaderMap) -> Response {
    if !s.authorize(&headers) {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    let inner = s.inner.lock().await;
    Json(json!({"schema_version":1,"resources":inner.book.entries,"runtime":inner.backend.host().describe()})).into_response()
}
async fn create(
    State(s): State<Arc<Resources>>,
    headers: HeaderMap,
    Json(request): Json<Create>,
) -> Response {
    if !s.authorize(&headers) {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    if let Err(error) = request.validate() {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error":error.to_string()})),
        )
            .into_response();
    }
    let mut inner = s.inner.lock().await;
    if let Some(entry) = inner
        .book
        .entries
        .iter()
        .find(|e| e.request.request_id == request.request_id)
    {
        if entry.request != request {
            return (
                StatusCode::CONFLICT,
                Json(json!({"error":"Conflicting resource creation request"})),
            )
                .into_response();
        }
        if let Some(result) = &entry.result {
            return Json(result.clone()).into_response();
        }
        if let Some(error) = &entry.error {
            return (StatusCode::CONFLICT, Json(json!({"error":error}))).into_response();
        }
    }
    if inner.book.entries.len() >= 128 {
        return (
            StatusCode::CONFLICT,
            Json(json!({"error":"Runtime resource limit reached"})),
        )
            .into_response();
    }
    let entry = Entry {
        request,
        result: None,
        error: None,
    };
    inner.book.entries.push(entry.clone());
    let index = inner.book.entries.len() - 1;
    let path = s.config.data.join("runtime-resources.json");
    if let Err(error) = persist(&path, &inner.book) {
        inner.book.entries.pop();
        return (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error":error.to_string()})),
        )
            .into_response();
    }
    let result = s.apply(&mut inner.backend, &entry).await;
    match &result {
        Ok(value) => inner.book.entries[index].result = Some(value.clone()),
        Err(error) => inner.book.entries[index].error = Some(error.to_string()),
    }
    // New routes become visible only after the resource definition is durably recorded.
    if let Err(error) = persist(&path, &inner.book) {
        return (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error":error.to_string()})),
        )
            .into_response();
    }
    *s.routes.write().unwrap() = crate::hosting::backend_router(&inner.backend);
    match result {
        Ok(value) => Json(value).into_response(),
        Err(error) => (
            StatusCode::CONFLICT,
            Json(json!({"error":error.to_string()})),
        )
            .into_response(),
    }
}

#[cfg(test)]
mod tests {
    #[tokio::test]
    async fn shared_environment_registers_lazily_and_restores_without_resource_creation() {
        let root =
            std::env::temp_dir().join(format!("pot-shared-registration-{}", uuid::Uuid::new_v4()));
        let config = RuntimeConfig {
            data: root.join("data"),
            workspace: root.join("workspace"),
        };
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let resources = Resources::open(backend, config.clone()).await.unwrap();
        assert!(resources.host().await.sandbox_manager().is_none());
        let host = resources.execution_host(true).await.unwrap();
        let manager = host.sandbox_manager().unwrap();
        let target = manager.ensure_shared().unwrap();
        assert!(manager.describe(&target).is_ok());
        assert!(resources.inner.lock().await.book.entries.is_empty());
        resources.shutdown().await;
        drop(manager);
        drop(host);
        drop(resources);
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let restored = Resources::open(backend, config).await.unwrap();
        let host = restored.host().await;
        let manager = host.sandbox_manager().unwrap();
        assert_eq!(manager.ensure_shared().unwrap(), target);
        assert!(manager.describe(&target).is_ok());
        assert!(restored.inner.lock().await.book.entries.is_empty());
        assert!(!root.join("helper.sock").exists());
        restored.shutdown().await;
        drop(manager);
        drop(host);
        drop(restored);
        std::fs::remove_dir_all(root).unwrap();
    }
    use super::*;
    #[tokio::test]
    async fn local_and_http_clients_receive_mutations_without_read_feedback() {
        use futures_util::StreamExt;
        let root = std::env::temp_dir().join(format!("pot-ui-transports-{}", uuid::Uuid::new_v4()));
        let config = RuntimeConfig {
            data: root.join("product"),
            workspace: root.join("workspace"),
        };
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let resources = Resources::open(backend, config).await.unwrap();
        assert!(resources.monitor.lock().unwrap().task.is_none());
        let host = resources.host().await;
        let local = crate::ClientTransport::Local(crate::LocalClient::new(
            resources.router(),
            resources.token.clone(),
            host.instance_lock(),
        ));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let remote = crate::ClientTransport::Remote(
            crate::RemoteClient::new(
                &format!("http://{}", listener.local_addr().unwrap()),
                resources.token.clone(),
            )
            .unwrap(),
        );
        let router = resources.router();
        let server = tokio::spawn(async move {
            axum::serve(listener, router).await.unwrap();
        });
        let mut a = local.events("/api/v1/changes").await.unwrap();
        let mut b = remote.events("/api/v1/changes").await.unwrap();
        assert_eq!(
            a.next().await.unwrap().unwrap(),
            b.next().await.unwrap().unwrap()
        );
        assert!(local.events("/api/v1/runtime").await.is_err());
        assert!(remote.events("/api/v1/runtime").await.is_err());
        local.request("/api/v1/runtime", None).await.unwrap();
        remote.request("/api/v1/machines", None).await.unwrap();
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), a.next())
                .await
                .is_err()
        );
        let reply = local
            .request(
                "/api/v1/runtime/resources",
                Some(json!({"request_id":"host","resource":{"kind":"host"}})),
            )
            .await
            .unwrap();
        assert!(reply.status.is_success());
        let a = tokio::time::timeout(std::time::Duration::from_secs(1), a.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        let b = tokio::time::timeout(std::time::Duration::from_secs(1), b.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        assert_eq!(a, b);
        assert!(
            serde_json::from_str::<Value>(&a.data).unwrap()["versions"]["machines"].is_number()
        );
        resources.shutdown().await;
        server.abort();
        let _ = server.await;
        drop(local);
        drop(remote);
        drop(host);
        drop(resources);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[tokio::test]
    async fn change_stream_authorizes_coalesces_and_closes_on_shutdown() {
        use futures_util::StreamExt;
        let root = std::env::temp_dir().join(format!("pot-ui-events-{}", uuid::Uuid::new_v4()));
        let config = RuntimeConfig {
            data: root.join("product"),
            workspace: root.join("workspace"),
        };
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let resources = Resources::open(backend, config).await.unwrap();
        // Ordinary change subscriptions do not activate machine checks.
        assert!(resources.monitor.lock().unwrap().task.is_none());
        let router = resources.router();
        let request = |token: &str| {
            axum::http::Request::builder()
                .uri("/api/v1/changes")
                .header("authorization", format!("Bearer {token}"))
                .body(axum::body::Body::empty())
                .unwrap()
        };
        assert_eq!(
            router
                .clone()
                .oneshot(request("wrong"))
                .await
                .unwrap()
                .status(),
            StatusCode::UNAUTHORIZED
        );
        let response = router
            .clone()
            .oneshot(request(&resources.token))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let mut stream = response.into_body().into_data_stream();
        let mut decoder = pot_protocol::sse::Decoder::default();
        let initial = decoder
            .push(&stream.next().await.unwrap().unwrap())
            .unwrap();
        assert_eq!(initial[0].event, "changes");
        assert!(
            !serde_json::from_str::<Value>(&initial[0].data).unwrap()["boot_id"]
                .as_str()
                .unwrap()
                .is_empty()
        );
        for _ in 0..1000 {
            resources.changes.notify("tasks/main");
        }
        resources.changes.notify("settings/main");
        let frames = decoder
            .push(&stream.next().await.unwrap().unwrap())
            .unwrap();
        let next: Value = serde_json::from_str(&frames[0].data).unwrap();
        assert!(
            next["versions"]["settings/main"].as_u64().unwrap()
                > next["versions"]["tasks/main"].as_u64().unwrap()
        );
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), stream.next())
                .await
                .is_err()
        );
        resources.shutdown().await;
        assert!(
            tokio::time::timeout(std::time::Duration::from_secs(1), stream.next())
                .await
                .unwrap()
                .is_none()
        );
        drop(stream);
        drop(router);
        drop(resources);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[tokio::test]
    async fn machine_discovery_is_authenticated_and_independent_from_assistants() {
        let root =
            std::env::temp_dir().join(format!("pot-machine-discovery-{}", uuid::Uuid::new_v4()));
        let config = RuntimeConfig {
            data: root.join("product"),
            workspace: root.join("workspace"),
        };
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        backend.activate().unwrap();
        let resources = Resources::open(backend, config.clone()).await.unwrap();
        let denied = machines(State(resources.clone()), HeaderMap::new()).await;
        assert_eq!(denied.status(), StatusCode::UNAUTHORIZED);
        let mut headers = HeaderMap::new();
        headers.insert(
            "authorization",
            format!("Bearer {}", resources.token).parse().unwrap(),
        );
        for command in [
            json!({"action":"disable_control"}),
            json!({"action":"call","runtime_id":"other","body":{"action":"inventory"}}),
        ] {
            // Raw operator actions must remain unavailable on the UI connection route.
            let operator = serde_json::from_value::<crate::control::Operator>(command).unwrap();
            assert_eq!(
                crate::control::connection_operator(
                    State(resources.clone()),
                    headers.clone(),
                    Json(operator)
                )
                .await
                .status(),
                StatusCode::FORBIDDEN
            );
        }
        let response = machines(State(resources.clone()), headers.clone()).await;
        let directory: Value = serde_json::from_slice(
            &axum::body::to_bytes(response.into_body(), 1024 * 1024)
                .await
                .unwrap(),
        )
        .unwrap();
        let identity = directory["machines"][0]["runtime_id"].clone();
        assert_eq!(directory["machines"].as_array().unwrap().len(), 1);
        assert_eq!(directory["machines"][0]["execution_targets"], json!([]));
        assert!(!config.data.join("pot-v2.sqlite").exists());
        assert!(
            create(
                State(resources.clone()),
                headers.clone(),
                Json(Create {
                    request_id: "host".into(),
                    resource: Resource::Host
                })
            )
            .await
            .status()
            .is_success()
        );
        let response = machines(State(resources.clone()), headers.clone()).await;
        let directory: Value = serde_json::from_slice(
            &axum::body::to_bytes(response.into_body(), 1024 * 1024)
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(directory["machines"][0]["runtime_id"], identity);
        assert_eq!(
            directory["machines"][0]["execution_targets"][0]["execution_target"],
            json!({"runtime_id":identity,"target":{"kind":"host"}})
        );
        assert!(!config.data.join("pot-v2.sqlite").exists());
        let local_id = identity.clone();
        *resources.routes.write().unwrap() = Router::new().route("/api/v1/execution_targets", axum::routing::get(move || {
            let id = local_id.clone();
            async move { Json(json!({"items":[{"execution_target":{"runtime_id":id,"target":{"kind":"host"}}},{"execution_target":{"runtime_id":"other-runtime","target":{"kind":"host"}}}]})) }
        }));
        let response = machines(State(resources.clone()), headers.clone()).await;
        let directory: Value = serde_json::from_slice(
            &axum::body::to_bytes(response.into_body(), 1024 * 1024)
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(
            directory["machines"][0]["execution_targets"]
                .as_array()
                .unwrap()
                .len(),
            1
        );
        assert_eq!(
            directory["machines"][0]["execution_targets"][0]["execution_target"]["runtime_id"],
            identity
        );
        // Discovery does not grant resource mutation to requests with an Origin.
        headers.insert("origin", "https://client.example".parse().unwrap());
        assert_eq!(
            create(
                State(resources.clone()),
                headers.clone(),
                Json(Create {
                    request_id: "assistant".into(),
                    resource: Resource::Assistant {
                        name: "Assistant".into()
                    }
                })
            )
            .await
            .status(),
            StatusCode::UNAUTHORIZED
        );
        assert!(
            machines(State(resources.clone()), headers)
                .await
                .status()
                .is_success()
        );
        resources.shutdown().await;
        drop(resources);
        std::fs::remove_dir_all(root).unwrap();
    }
}
