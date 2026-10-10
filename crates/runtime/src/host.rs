//! Capability hosting without implicit conversation, Agent or Task initialization.
use crate::*;
use axum::{
    Router,
    routing::{get, post},
};
use serde::{Deserialize, Serialize};
use std::{
    os::{
        fd::AsRawFd,
        unix::fs::{OpenOptionsExt, PermissionsExt},
    },
    path::Path,
};
#[cfg(test)]
mod tests;

#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Composition {
    #[default]
    Bare,
    Environment,
    Assistant,
}
impl Composition {
    pub fn load(data: &Path) -> Result<Self> {
        if let Ok(value) = std::env::var("POT_RUNTIME_COMPOSITION") {
            return value.parse();
        }
        let path = data.join("runtime-composition.json");
        if path.exists() {
            #[derive(Deserialize)]
            #[serde(deny_unknown_fields)]
            struct Config {
                schema_version: u32,
                preset: Composition,
            }
            let config: Config = serde_json::from_slice(&std::fs::read(path)?)?;
            anyhow::ensure!(
                config.schema_version == 1,
                "Unsupported Runtime composition schema"
            );
            return Ok(config.preset);
        }
        // An existing product root keeps its behavior; fresh standalone roots are idle.
        Ok(if data.join("pot-v2.sqlite").exists() {
            Self::Assistant
        } else {
            Self::Bare
        })
    }
    pub fn has_environments(self) -> bool {
        self != Self::Bare
    }
    pub fn capabilities(self) -> &'static [&'static str] {
        match self {
            Self::Bare => &["runtime"],
            Self::Environment => &[
                "runtime",
                "sandbox_execution",
                "sandbox_management",
                "resources",
            ],
            Self::Assistant => &[
                "runtime",
                "conversation",
                "tasks",
                "agent",
                "model",
                "accounting",
                "schedules",
                "sandbox_execution",
                "sandbox_management",
                "resources",
                "host_approval",
            ],
        }
    }
}
impl std::str::FromStr for Composition {
    type Err = anyhow::Error;
    fn from_str(value: &str) -> Result<Self> {
        match value {
            "bare" => Ok(Self::Bare),
            "environment" => Ok(Self::Environment),
            "assistant" => Ok(Self::Assistant),
            _ => bail!("Unknown Runtime composition: expected bare, environment or assistant"),
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Identity {
    schema_version: u32,
    runtime_id: String,
}

/// Process authority and identity exist independently of optional product stores.
#[derive(Clone)]
pub struct Host {
    pub(crate) data: PathBuf,
    pub(crate) token: String,
    pub(crate) boot: String,
    pub(crate) lock: Arc<std::fs::File>,
    runtime_id: String,
    composition: Composition,
    stop: tokio::sync::watch::Sender<bool>,
    environment: Option<Arc<sandbox::Manager>>,
}
impl Host {
    pub(crate) fn open(data: &Path, composition: Composition) -> Result<Self> {
        std::fs::create_dir_all(data)?;
        std::fs::set_permissions(data, std::fs::Permissions::from_mode(0o700))?;
        let file = std::fs::OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .mode(0o600)
            .open(data.join("instance.lock"))?;
        anyhow::ensure!(
            unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } == 0,
            "This data directory is already owned by another Pot instance"
        );
        let path = data.join("runtime-identity.json");
        let identity: Identity = if path.exists() {
            serde_json::from_slice(&std::fs::read(&path)?)?
        } else {
            let identity = Identity {
                schema_version: 1,
                runtime_id: id(),
            };
            execution::private_json(&path, &serde_json::to_value(&identity)?)?;
            identity
        };
        anyhow::ensure!(
            identity.schema_version == 1,
            "Unsupported Runtime identity schema"
        );
        uuid::Uuid::parse_str(&identity.runtime_id)?;
        Ok(Self {
            data: data.into(),
            token: id(),
            boot: id(),
            lock: Arc::new(file),
            runtime_id: identity.runtime_id,
            composition,
            stop: tokio::sync::watch::channel(false).0,
            environment: None,
        })
    }
    pub(crate) fn attach_environment(&mut self, manager: Arc<sandbox::Manager>) {
        self.environment = Some(manager);
    }
    pub fn composition(&self) -> Composition {
        self.composition
    }
    pub fn runtime_id(&self) -> &str {
        &self.runtime_id
    }
    pub fn describe(&self) -> Value {
        json!({"schema_version":1,"runtime_id":self.runtime_id,"boot_id":self.boot,"composition":self.composition,"capabilities":self.composition.capabilities()})
    }
    pub(crate) fn endpoint(&self, port: u16) -> RuntimeEndpoint {
        RuntimeEndpoint {
            api_version: API_VERSION,
            port,
            token: self.token.clone(),
            boot_id: self.boot.clone(),
        }
    }
    fn authorize(&self, h: &HeaderMap) -> ApiResult<()> {
        if h.contains_key("origin")
            || h.get("authorization").and_then(|h| h.to_str().ok())
                != Some(&format!("Bearer {}", self.token))
        {
            return Err(error(
                StatusCode::UNAUTHORIZED,
                "runtime_authority",
                "Native Runtime authority required",
            ));
        }
        Ok(())
    }
    fn environment(&self) -> ApiResult<&Arc<sandbox::Manager>> {
        self.environment.as_ref().ok_or_else(|| {
            error(
                StatusCode::CONFLICT,
                "capability_unavailable",
                "Environment service is disabled",
            )
        })
    }
    pub fn router(&self) -> Router {
        let mut routes = Router::new().route("/api/v1/runtime", get(describe));
        if self.composition == Composition::Environment {
            routes = routes
                .route("/api/v1/sandboxes", get(inventory).post(manage))
                .route(
                    "/api/v1/sandbox-resources",
                    get(resources).post(resource_command),
                )
                .route("/api/v1/execution", post(execute))
                .route("/api/v1/environment/cancel", post(cancel));
        }
        routes.with_state(self.clone())
    }
    pub(crate) async fn shutdown(&self) -> Result<()> {
        self.stop.send_replace(true);
        if let Some(environment) = &self.environment {
            environment.shutdown().await?;
        }
        Ok(())
    }
    pub(crate) fn request_stop(&self) {
        self.stop.send_replace(true);
    }
}

async fn describe(State(host): State<Host>, h: HeaderMap) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    Ok(Json(host.describe()))
}
async fn inventory(State(host): State<Host>, h: HeaderMap) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    Ok(Json(host.environment()?.inventory().await))
}
async fn manage(
    State(host): State<Host>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    let manager = host.environment()?;
    // Host mapping approval needs an explicit owner approval service, absent here.
    if args["action"] == "set_mounts" {
        return Err(error(
            StatusCode::FORBIDDEN,
            "approval_unavailable",
            "Host mappings require an owner approval service",
        ));
    }
    if matches!(args["action"].as_str(), Some("start" | "desktop")) {
        let target = manager
            .assignment(args["sandbox_id"].as_str().unwrap_or(""))
            .map_err(internal)?;
        if let Some(hub) = &manager.resources
            && let Some(request) = hub
                .inspect(manager, &target, "operator", None)
                .await
                .map_err(internal)?
        {
            return Ok(Json(
                json!({"sandbox_id":target,"resource_request_id":request,"phase":"awaiting-resources"}),
            ));
        }
    }
    let result = manager
        .management(&args, false, &|| *host.stop.borrow(), &|_, action| action())
        .await
        .map_err(|e| {
            error(
                StatusCode::CONFLICT,
                "environment_management",
                &e.to_string(),
            )
        })?;
    if args["action"] == "delete"
        && let Some(hub) = &manager.resources
    {
        hub.forget(args["sandbox_id"].as_str().unwrap_or(""))
            .map_err(internal)?;
    }
    Ok(Json(result))
}
async fn resources(State(host): State<Host>, h: HeaderMap) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    let hub = host.environment()?.resources.as_ref();
    Ok(Json(
        json!({"requests":hub.map(|h|h.requests()),"cache":hub.map(|h|h.cache()).transpose().map_err(internal)?}),
    ))
}
async fn resource_command(
    State(host): State<Host>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    let hub = host.environment()?.resources.as_ref().ok_or_else(|| {
        error(
            StatusCode::CONFLICT,
            "resources",
            "Resource service unavailable",
        )
    })?;
    let result = if args["action"] == "prune" {
        hub.prune(args["resource_id"].as_str().unwrap_or(""))
            .map(|_| json!({"removed":true}))
    } else {
        hub.control(
            args["request_id"].as_str().unwrap_or(""),
            args["action"].as_str().unwrap_or(""),
        )
    };
    Ok(Json(result.map_err(|e| {
        error(StatusCode::CONFLICT, "resources", &e.to_string())
    })?))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Operation {
    run_id: String,
    call_id: String,
    sandbox_id: String,
    name: String,
    args: Value,
}
async fn execute(
    State(host): State<Host>,
    h: HeaderMap,
    Json(request): Json<Operation>,
) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    uuid::Uuid::parse_str(&request.run_id).map_err(|_| {
        error(
            StatusCode::BAD_REQUEST,
            "run_id",
            "A UUID run identity is required",
        )
    })?;
    if request.call_id.is_empty() || request.call_id.len() > 256 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "call_id",
            "Invalid execution identity",
        ));
    }
    if !["exec_command", "write_stdin", "apply_patch"].contains(&request.name.as_str()) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "operation",
            "Unsupported Environment operation",
        ));
    }
    let manager = host.environment()?;
    let target = manager.assignment(&request.sandbox_id).map_err(internal)?;
    if request.args.get("sandbox_id").is_some() {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "scope",
            "Target belongs in sandbox_id, not args",
        ));
    }
    let owner = format!("operator:{}", request.run_id);
    let cancellation_path = host
        .data
        .join("environment-cancellations")
        .join(&request.run_id);
    let cancelled = || *host.stop.borrow() || cancellation_path.exists();
    if cancelled() {
        return Err(error(
            StatusCode::CONFLICT,
            "run_cancelled",
            "Run has been cancelled",
        ));
    }
    pot_core::environment_tools::validate(&request.name, &request.args)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "operation", &e.to_string()))?;
    if request.name != "write_stdin" {
        manager
            .prepare(&target, &owner, Some(&request.run_id), &cancelled)
            .await
            .map_err(internal)?;
    }
    let mut args = request.args;
    args["sandbox_id"] = json!(target);
    let grant = pot_sandbox::AccessGrant {
        owner,
        turn: request.run_id,
        thread_generation: 1,
        target,
        worker: true,
        policy: pot_core::ShellSettings::default(),
    };
    let result = manager
        .execute(
            &grant,
            &request.name,
            &request.call_id,
            &args,
            &cancelled,
            None,
        )
        .await
        .map_err(|e| {
            error(
                StatusCode::CONFLICT,
                "environment_execution",
                &e.to_string(),
            )
        })?;
    Ok(Json(result))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Cancel {
    run_id: String,
}
async fn cancel(
    State(host): State<Host>,
    h: HeaderMap,
    Json(request): Json<Cancel>,
) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    uuid::Uuid::parse_str(&request.run_id).map_err(|_| {
        error(
            StatusCode::BAD_REQUEST,
            "run_id",
            "A UUID run identity is required",
        )
    })?;
    let manager = host.environment()?;
    let owner = format!("operator:{}", request.run_id);
    let directory = host.data.join("environment-cancellations");
    std::fs::create_dir_all(&directory).map_err(|e| internal(e.into()))?;
    execution::private_json(
        &directory.join(&request.run_id),
        &json!({"run_id":request.run_id,"cancel_requested":true}),
    )
    .map_err(internal)?;
    manager.finish(&owner).await.map_err(internal)?;
    manager.drain(&owner).await.map_err(internal)?;
    Ok(Json(json!({"run_id":request.run_id,"cleanup":"confirmed"})))
}
