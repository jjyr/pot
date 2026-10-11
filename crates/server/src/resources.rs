//! Caller-created resources. Creation restores definitions, never model calls or VM starts.
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
        let mut book = if path.exists() {
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
        // One-time adoption of product roots created before the resource manifest.
        if !path.exists()
            && config.data.join("pot-v2.sqlite").exists()
            && backend.assistants().is_none()
        {
            backend.enable_host_resource();
            let manager =
                backend.register_environment_providers(crate::hosting::installed_providers()?)?;
            if !config.data.join("sandboxes.json").exists() {
                manager
                    .management(
                        &json!({"action":"create","sandbox_id":"default","name":"Linux"}),
                        false,
                        &|| false,
                        &|_, action| action(),
                    )
                    .await?;
                manager
                    .management(
                        &json!({"action":"select","sandbox_id":"default"}),
                        false,
                        &|| false,
                        &|_, action| action(),
                    )
                    .await?;
            }
            backend.enable_assistant_resources(config.clone()).await?;
        }
        // Preserve the current product resource definitions when upgrading an existing root.
        // This does not import assistant instruction files or old preference records.
        if !path.exists() && backend.assistants().is_some() {
            let main = backend
                .assistants()
                .unwrap()
                .definitions()
                .into_iter()
                .find(|d| d.id == "main")
                .unwrap();
            book.entries.push(Entry {
                request: Create {
                    request_id: "upgrade-main-assistant".into(),
                    resource: Resource::Assistant {
                        name: main.name.clone(),
                    },
                },
                result: Some(json!({"kind":"assistant","definition":main})),
                error: None,
            });
            book.entries.push(Entry {
                request: Create {
                    request_id: "upgrade-host".into(),
                    resource: Resource::Host,
                },
                result: Some(json!({"kind":"host","runtime_id":backend.host().runtime_id()})),
                error: None,
            });
            if let Some(manager) = backend.environment_manager() {
                for item in manager.inventory().await["items"]
                    .as_array()
                    .cloned()
                    .unwrap_or_default()
                {
                    let sandbox_id = item["id"].as_str().unwrap().to_owned();
                    // Earlier roots could hold the default definition only in memory.
                    // Materialize the current inventory before recording ready adoption.
                    manager.management(&json!({"action":"create","sandbox_id":sandbox_id,"name":item["name"],"provider":item["provider"]}), false, &||false, &|_, action|action()).await?;
                    book.entries.push(Entry {
                        request: Create {
                            request_id: format!("upgrade-sandbox-{sandbox_id}"),
                            resource: Resource::Sandbox {
                                sandbox_id,
                                name: item["name"].as_str().unwrap().into(),
                                provider: item["provider"].as_str().unwrap().into(),
                            },
                        },
                        result: Some(json!({"sandbox_id":item["id"],"phase":"stopped"})),
                        error: None,
                    });
                }
            }
            persist(&path, &book)?;
        }
        let token = backend.endpoint(0).token;
        let routes = crate::hosting::backend_router(&backend);
        let control = crate::control::Authority::open_policy(
            &config.data,
            backend.host().runtime_id(),
            control_allowed,
        )?;
        if let Some(state) = backend.optional_state() {
            state.set_remote_environment(control.clone());
        }
        let this = Arc::new(Self {
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
                let first = backend.assistants().is_none();
                let existing = self.config.data.join("pot-v2.sqlite").exists();
                backend
                    .enable_assistant_resources(self.config.clone())
                    .await?;
                backend
                    .state()?
                    .set_remote_environment(self.control.clone());
                let hub = backend.assistants().unwrap();
                if first {
                    if let Some(result) = &entry.result {
                        return Ok(result.clone());
                    }
                    let main = hub
                        .definitions()
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
                let manager = backend.register_environment_providers(providers)?;
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
            .register_environment_providers(vec![provider])
            .unwrap();
    }
    pub(super) async fn assistant_hub(&self) -> Option<Arc<pot_runtime::assistants::Hub>> {
        self.inner.lock().await.backend.assistants()
    }
    pub(super) async fn host(&self) -> pot_runtime::host::Host {
        self.inner.lock().await.backend.host().clone()
    }
    pub async fn shutdown(&self) {
        if let Err(error) = self.control.stop_listener(false).await {
            eprintln!("Control listener shutdown failed: {error}");
        }
        self.control.stop_operations().await;
        self.inner.lock().await.backend.shutdown().await;
    }
    pub async fn optional_state(&self) -> Option<pot_runtime::Service> {
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
        .uri("/api/v1/environments")
        .header("authorization", format!("Bearer {}", s.token))
        .body(axum::body::Body::empty())
        .unwrap();
    let response = routes.oneshot(request).await.unwrap();
    let environments = if response.status() == StatusCode::NOT_FOUND {
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
    let mut machines = vec![json!({
        "runtime_id":runtime["runtime_id"],"local":true,"connection":"online",
        "capabilities":runtime["capabilities"],"boot_id":runtime["boot_id"],
        "environments":environments["items"],"default":environments["selected"]
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
    use super::*;
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
        assert_eq!(directory["machines"][0]["environments"], json!([]));
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
            directory["machines"][0]["environments"][0]["environment_ref"],
            json!({"runtime_id":identity,"target":{"kind":"host"}})
        );
        assert!(!config.data.join("pot-v2.sqlite").exists());
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
