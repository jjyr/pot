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
mod control_execution;
mod host_execution;
pub use control_execution::ControlOperation;

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
    executions: pot_runtime_execution::Manager,
    assistant_enabled: bool,
    pub(crate) host_enabled: bool,
    stop: tokio::sync::watch::Sender<bool>,
    sandboxes: Option<Arc<sandbox::Manager>>,
    host_operations: Arc<tokio::sync::Mutex<()>>,
    pub(crate) host_environment: Option<Arc<environment::HostEnvironment>>,
}
impl Host {
    pub fn enable_execution(&mut self) {
        self.host_enabled = true;
        if self.host_environment.is_none() {
            self.attach_host_environment();
        }
    }
    pub fn advertise_control(&mut self) {
        self.resource_capabilities(true);
    }
    pub fn environment_manager(&self) -> Option<Arc<sandbox::Manager>> {
        self.sandbox_manager()
    }
    pub fn register_providers(
        &mut self,
        providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    ) -> Result<Arc<sandbox::Manager>> {
        let manager = match self.sandbox_manager() {
            Some(manager) => manager,
            None => {
                let manager = Arc::new(sandbox::Manager::empty(&self.data, vec![])?);
                self.attach_sandboxes(manager.clone());
                manager
            }
        };
        for provider in providers {
            manager.register_provider(provider);
        }
        Ok(manager)
    }
    pub fn open(data: &Path) -> Result<Self> {
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
        let boot = id();
        let executions = pot_runtime_execution::Manager::open(
            data,
            &identity.runtime_id,
            &boot,
            pot_runtime_execution::Limits::load(data)?,
        )?;
        Ok(Self {
            executions,
            data: data.into(),
            token: id(),
            boot,
            lock: Arc::new(file),
            runtime_id: identity.runtime_id,
            assistant_enabled: false,
            host_enabled: false,
            stop: tokio::sync::watch::channel(false).0,
            sandboxes: None,
            host_operations: Arc::new(tokio::sync::Mutex::new(())),
            host_environment: None,
        })
    }
    pub(crate) fn attach_host_environment(&mut self) {
        self.host_environment = Some(Arc::new(environment::HostEnvironment::default()));
    }
    pub(crate) fn attach_sandboxes(&mut self, manager: Arc<sandbox::Manager>) {
        self.sandboxes = Some(manager);
    }
    pub(crate) fn sandbox_manager(&self) -> Option<Arc<sandbox::Manager>> {
        self.sandboxes.clone()
    }
    pub(crate) fn resource_capabilities(&mut self, assistant: bool) {
        self.assistant_enabled = assistant;
    }
    pub fn instance_lock(&self) -> Arc<std::fs::File> {
        self.lock.clone()
    }
    pub fn data_dir(&self) -> &Path {
        &self.data
    }

    pub fn runtime_id(&self) -> &str {
        &self.runtime_id
    }
    pub fn describe(&self) -> Value {
        let mut capabilities = vec!["runtime"];
        if self.host_enabled {
            capabilities.push("host_execution");
        }
        if self.sandboxes.is_some() {
            capabilities.extend(["sandbox_execution", "sandbox_management", "resources"]);
        }
        if self.assistant_enabled {
            capabilities.extend([
                "conversation",
                "tasks",
                "agent",
                "model",
                "accounting",
                "schedules",
            ]);
            if self.host_enabled {
                capabilities.push("host_approval");
            }
        }
        json!({"schema_version":1,"runtime_id":self.runtime_id,"boot_id":self.boot,"capabilities":capabilities})
    }

    pub fn endpoint(&self, port: u16) -> RuntimeEndpoint {
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
    fn sandboxes(&self) -> ApiResult<&Arc<sandbox::Manager>> {
        self.sandboxes.as_ref().ok_or_else(|| {
            error(
                StatusCode::CONFLICT,
                "capability_unavailable",
                "Sandbox service is disabled",
            )
        })
    }
    pub fn router(&self) -> Router {
        let mut routes = Router::new().route("/api/v1/runtime", get(describe));
        if !self.assistant_enabled && (self.host_enabled || self.sandboxes.is_some()) {
            routes = routes
                .route("/api/v1/environments", get(environment_inventory))
                .route("/api/v1/execution", post(execute))
                .route("/api/v1/environment/cancel", post(cancel))
                .route("/api/v1/environment/operations", get(host_execution::query));
        }
        if !self.assistant_enabled && self.sandboxes.is_some() {
            routes = routes
                .route("/api/v1/sandboxes", get(inventory).post(manage))
                .route(
                    "/api/v1/sandbox-resources",
                    get(resources).post(resource_command),
                );
        }
        routes.with_state(self.clone())
    }
    pub async fn shutdown(&self) -> Result<()> {
        self.request_stop();
        let agents =
            tokio::time::timeout(std::time::Duration::from_secs(20), self.executions.drain())
                .await
                .map_err(anyhow::Error::from)
                .and_then(|result| result);
        if let Some(host) = &self.host_environment {
            host.shutdown_host().await?;
        }
        if let Some(environment) = &self.sandboxes {
            environment.shutdown().await?;
        }
        agents
    }
    pub fn executions(&self) -> &pot_runtime_execution::Manager {
        &self.executions
    }
    pub fn request_stop(&self) {
        self.executions.stop();
        self.stop.send_replace(true);
    }
    pub(crate) fn stopping(&self) -> bool {
        *self.stop.borrow()
    }
}

async fn describe(State(host): State<Host>, h: HeaderMap) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    Ok(Json(host.describe()))
}
async fn inventory(State(host): State<Host>, h: HeaderMap) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    Ok(Json(host.sandboxes()?.inventory().await))
}
async fn environment_inventory(State(host): State<Host>, h: HeaderMap) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    let mut items = vec![];
    if host.host_enabled && host.host_environment.is_some() {
        items.push(json!({"environment_ref":pot_core::EnvironmentRef::host(&host.runtime_id),"kind":"host","management":false,"workspace":host.data.join("host-runs"),"execution_authority":"native_operator"}));
    }
    if let Some(manager) = &host.sandboxes {
        let inventory = manager.inventory().await;
        for mut item in inventory["items"].as_array().cloned().unwrap_or_default() {
            let target = item["id"].as_str().unwrap_or("");
            item["environment_ref"] =
                json!(pot_core::EnvironmentRef::sandbox(&host.runtime_id, target));
            items.push(item);
        }
    }
    Ok(Json(json!({"runtime_id":host.runtime_id,"items":items})))
}
async fn manage(
    State(host): State<Host>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    host.authorize(&h)?;
    let manager = host.sandboxes()?;
    // This is the authenticated machine-owner configuration channel.
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
    let hub = host.sandboxes()?.resources.as_ref();
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
    let hub = host.sandboxes()?.resources.as_ref().ok_or_else(|| {
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
    #[serde(default)]
    sandbox_id: String,
    #[serde(default)]
    environment_ref: Option<pot_core::EnvironmentRef>,
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
    if let Some(reference) = &request.environment_ref {
        reference
            .validate()
            .map_err(|e| error(StatusCode::BAD_REQUEST, "environment", &e.to_string()))?;
        if !reference.is_local_to(&host.runtime_id) || !request.sandbox_id.is_empty() {
            return Err(error(
                StatusCode::BAD_REQUEST,
                "environment",
                "Conflicting or nonlocal Environment target",
            ));
        }
        if matches!(reference.target, pot_core::EnvironmentTarget::Host {}) {
            return host_execution::execute(&host, request)
                .await
                .map(Json)
                .map_err(|e| error(StatusCode::CONFLICT, "host_execution", &e.to_string()));
        }
    }
    let sandbox_id = request
        .environment_ref
        .as_ref()
        .and_then(|reference| match &reference.target {
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => Some(sandbox_id.as_str()),
            _ => None,
        })
        .unwrap_or(&request.sandbox_id);
    let manager = host.sandboxes()?;
    let target = manager.assignment(sandbox_id).map_err(internal)?;
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
        workspace_owner: None,
        owner,
        turn: request.run_id,
        thread_generation: 1,
        target,
        worker: true,
        policy: pot_core::ShellSettings::default(),
    };
    let result = manager
        .execute(&grant, &request.name, &request.call_id, &args, &cancelled)
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
    let owner = format!("operator:{}", request.run_id);
    let directory = host.data.join("environment-cancellations");
    std::fs::create_dir_all(&directory).map_err(|e| internal(e.into()))?;
    execution::private_json(
        &directory.join(&request.run_id),
        &json!({"run_id":request.run_id,"cancel_requested":true}),
    )
    .map_err(internal)?;
    if let Some(executor) = &host.host_environment {
        executor.finish_host(&owner).await.map_err(internal)?;
    }
    if let Some(manager) = &host.sandboxes {
        manager.finish(&owner).await.map_err(internal)?;
        manager.drain(&owner).await.map_err(internal)?;
    }
    Ok(Json(json!({"run_id":request.run_id,"cleanup":"confirmed"})))
}

impl pot_runner::Destination for Host {
    fn executions(&self) -> &pot_runtime_execution::Manager {
        &self.executions
    }
    fn runtime_id(&self) -> &str {
        self.runtime_id()
    }
    fn boot_id(&self) -> &str {
        &self.boot
    }
    fn stopping(&self) -> bool {
        self.stopping()
    }
    fn install_resources<'a>(
        &'a self,
        target: &'a pot_core::EnvironmentTarget,
        workspace: &'a str,
        resources: pot_runner::resources::GlobalResources,
    ) -> futures::future::BoxFuture<'a, Result<Value>> {
        Box::pin(self.install_controlled_resources(target, workspace, resources))
    }
}
