//! Owner approval transport. Only current permanent grants are persisted;
//! pending/one-time decisions are memory-only. Policy lives in pot-agent.
use super::*;
use pot_agent::permissions::{Config, Grant, Policy, Request, Scope};
use serde::Deserialize;
use std::{
    collections::BTreeMap,
    io::Write,
    os::{fd::AsRawFd, unix::fs::OpenOptionsExt},
};

#[derive(Clone)]
struct Pending {
    id: String,
    request: Request,
    phase: String,
    decision: String,
}
#[derive(Clone, Default)]
struct Book {
    policy: Policy,
    requests: Vec<Pending>,
    grants: Vec<Grant>,
    disk: Option<Vec<u8>>,
}
pub(crate) struct Hub {
    book: Mutex<Book>,
    path: Option<PathBuf>,
    changed: tokio::sync::Notify,
    remotes: Mutex<BTreeMap<String, Arc<Hub>>>,
    decisions: tokio::sync::Mutex<()>,
}
fn read(path: &std::path::Path) -> Result<Option<Vec<u8>>> {
    match std::fs::read(path) {
        Ok(bytes) => Ok(Some(bytes)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.into()),
    }
}
impl Hub {
    #[cfg(test)]
    pub fn memory() -> Self {
        Self {
            book: Mutex::new(Book::default()),
            path: None,
            changed: Default::default(),
            remotes: Default::default(),
            decisions: Default::default(),
        }
    }
    pub fn open(path: &std::path::Path) -> Result<Self> {
        let disk = read(path)?;
        let config: Config = disk
            .as_ref()
            .map(|bytes| serde_json::from_slice(bytes))
            .transpose()?
            .unwrap_or_default();
        let policy = config.policy.unwrap_or_default();
        let grants = config.into_grants()?;
        let hub = Self {
            book: Mutex::new(Book {
                policy,
                grants,
                disk,
                ..Book::default()
            }),
            path: Some(path.into()),
            changed: Default::default(),
            remotes: Default::default(),
            decisions: Default::default(),
        };
        // Absence means no grants. Do not create or rewrite user configuration on startup.
        Ok(hub)
    }
    pub fn open_with_legacy(path: &std::path::Path, legacy: &std::path::Path) -> Result<Self> {
        // A manually supplied JSON file is authoritative. Import only when absent.
        if read(path)?.is_some() || !legacy.exists() {
            return Self::open(path);
        }
        use rusqlite::OptionalExtension;
        let connection = rusqlite::Connection::open_with_flags(
            legacy,
            rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY,
        )?;
        let value: Option<String> = connection
            .query_row(
                "SELECT value FROM metadata WHERE key='permissions.v1'",
                [],
                |r| r.get(0),
            )
            .optional()?;
        let mut grants: Vec<Grant> = vec![];
        if let Some(value) = value {
            let book: Value = serde_json::from_str(&value)?;
            let entries = book["grants"]
                .as_array()
                .ok_or_else(|| anyhow!("Invalid legacy permissions"))?;
            for entry in entries {
                let mut entry = entry.clone();
                entry
                    .as_object_mut()
                    .ok_or_else(|| anyhow!("Invalid legacy grant"))?
                    .remove("epoch");
                let grant: Grant = serde_json::from_value(entry)?;
                grants.retain(|g| g.owner != grant.owner || g.scope != grant.scope);
                grants.push(grant);
            }
        }
        drop(connection);
        let grants = Config::from_grants(&grants).into_grants()?;
        let hub = Self {
            book: Mutex::new(Book::default()),
            path: Some(path.into()),
            changed: Default::default(),
            remotes: Default::default(),
            decisions: Default::default(),
        };
        {
            let mut book = hub.book.lock().unwrap();
            book.disk = Some(hub.persist(&grants, Policy::default(), None)?);
            book.grants = grants;
        }
        // Successful migration discards the old request/history store.
        std::fs::remove_file(legacy)?;
        Ok(hub)
    }
    fn persist(
        &self,
        grants: &[Grant],
        policy: Policy,
        expected: Option<&[u8]>,
    ) -> Result<Vec<u8>> {
        let path = self
            .path
            .as_ref()
            .ok_or_else(|| anyhow!("Permission file unavailable"))?;
        let parent = path
            .parent()
            .ok_or_else(|| anyhow!("Permission directory unavailable"))?;
        std::fs::create_dir_all(parent)?;
        // Serialize writers sharing the global Agent home, then detect manual/stale edits.
        let lock = std::fs::OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .mode(0o600)
            .open(parent.join(".permissions.lock"))?;
        anyhow::ensure!(
            unsafe { libc::flock(lock.as_raw_fd(), libc::LOCK_EX) } == 0,
            "Permission file lock unavailable"
        );
        anyhow::ensure!(
            read(path)?.as_deref() == expected,
            "permissions.json changed externally; restart Server before editing permissions in the UI"
        );
        let mut config = Config::from_grants(grants);
        config.policy = Some(policy);
        let mut bytes = serde_json::to_vec_pretty(&config)?;
        bytes.push(b'\n');
        let temporary = parent.join(format!(".permissions-{}.tmp", id()));
        let result = (|| -> Result<()> {
            let mut file = std::fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .mode(0o600)
                .open(&temporary)?;
            file.write_all(&bytes)?;
            file.sync_all()?;
            std::fs::rename(&temporary, path)?;
            std::fs::File::open(parent)?.sync_all()?;
            Ok(())
        })();
        if result.is_err() {
            let _ = std::fs::remove_file(&temporary);
        }
        result?;
        Ok(bytes)
    }
    fn save(&self, mut next: Book, book: &mut Book) -> Result<()> {
        if next.grants != book.grants && self.path.is_some() {
            next.disk = Some(self.persist(&next.grants, next.policy, book.disk.as_deref())?);
        }
        *book = next;
        self.changed.notify_waiters();
        Ok(())
    }
    pub fn grants(&self, owner: &str) -> Value {
        json!(
            self.book
                .lock()
                .unwrap()
                .grants
                .iter()
                .filter(|g| g.owner == owner)
                .collect::<Vec<_>>()
        )
    }
    pub fn state(&self, owner: &str) -> Value {
        let book = self.book.lock().unwrap();
        json!({"policy":book.policy,"grants":book.grants.iter().filter(|g|g.owner==owner).collect::<Vec<_>>()})
    }
    pub(crate) fn enqueue(&self, request: Request) -> Result<String> {
        let mut book = self.book.lock().unwrap();
        if let Some(old) = book.requests.iter().find(|p| {
            p.request.owner == request.owner
                && p.request.generation == request.generation
                && p.request.turn == request.turn
                && p.request.call_id == request.call_id
        }) {
            anyhow::ensure!(old.request == request, "Conflicting permission request ID");
            return Ok(old.id.clone());
        }
        anyhow::ensure!(
            book.requests.len() < 1000,
            "Permission request limit reached"
        );
        let key = id();
        book.requests.push(Pending {
            id: key.clone(),
            request,
            phase: "pending".into(),
            decision: String::new(),
        });
        self.changed.notify_waiters();
        Ok(key)
    }
    fn cancel(&self, key: &str) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        book.requests.retain(|p| p.id != key);
        self.changed.notify_waiters();
        Ok(())
    }
    fn consume(&self, key: &str) -> Result<Option<bool>> {
        let mut book = self.book.lock().unwrap();
        let Some(index) = book.requests.iter().position(|p| p.id == key) else {
            return Ok(Some(false));
        };
        if book.requests[index].phase == "pending" {
            return Ok(None);
        }
        let approved = book.requests.remove(index).phase == "approved";
        self.changed.notify_waiters();
        Ok(Some(approved))
    }
    fn plan(book: &Book, key: &str, action: &str, scope: Option<Scope>) -> Result<Book> {
        let mut next = book.clone();
        if action == "revoke" {
            next.grants.retain(|g| g.id != key);
        } else {
            anyhow::ensure!(
                matches!(action, "allow_once" | "allow_persistent" | "deny"),
                "Unknown permission decision"
            );
            let index = next
                .requests
                .iter()
                .position(|p| p.id == key)
                .ok_or_else(|| anyhow!("Permission request is no longer pending"))?;
            let p = &mut next.requests[index];
            if p.decision == action && p.phase == "approved" {
                if action == "allow_persistent" {
                    let expected = scope.clone().unwrap_or(p.request.grant_scope()?);
                    anyhow::ensure!(
                        next.grants
                            .iter()
                            .any(|g| g.id == key && g.scope == expected),
                        "Conflicting repeated grant scope"
                    );
                }
                return Ok(next);
            }
            anyhow::ensure!(
                p.phase == "pending",
                "Permission request is no longer pending"
            );
            if action == "deny" {
                next.requests.remove(index);
            } else {
                p.decision = action.into();
                p.phase = "approved".into();
                if action == "allow_persistent" {
                    let scope = scope.unwrap_or(p.request.grant_scope()?);
                    anyhow::ensure!(
                        scope.covers(&p.request.operation_scope()?),
                        "Grant must cover the reviewed operation"
                    );
                    let grant = Grant {
                        id: p.id.clone(),
                        owner: p.request.owner.clone(),
                        scope,
                    };
                    let checked = Config::from_grants(std::slice::from_ref(&grant))
                        .into_grants()?
                        .remove(0);
                    anyhow::ensure!(checked == grant, "Permission scope changed after review");
                    // Replace the same scope and remove narrower grants now covered by it.
                    next.grants
                        .retain(|g| g.owner != grant.owner || !grant.scope.covers(&g.scope));
                    anyhow::ensure!(next.grants.len() < 1000, "Persistent grant limit reached");
                    next.grants.push(grant);
                }
            }
        }
        Ok(next)
    }
    fn decide(&self, key: &str, action: &str, scope: Option<Scope>) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        let next = Self::plan(&book, key, action, scope)?;
        self.save(next, &mut book)
    }
    #[cfg(test)]
    pub fn configured(&self) -> bool {
        self.path.is_some()
    }
    pub fn environment(self: &Arc<Self>, environment: &str) -> Result<Arc<Self>> {
        if environment == "host" {
            return Ok(self.clone());
        }
        self.remotes
            .lock()
            .unwrap()
            .get(environment)
            .cloned()
            .ok_or_else(|| anyhow!("Execution environment has not been prepared"))
    }
    fn all(self: &Arc<Self>) -> Vec<(String, Arc<Self>)> {
        let mut all = vec![("host".into(), self.clone())];
        all.extend(
            self.remotes
                .lock()
                .unwrap()
                .iter()
                .map(|(env, hub)| (env.clone(), hub.clone())),
        );
        all
    }
    fn remote_hub(self: &Arc<Self>, environment: &str) -> Result<Arc<Self>> {
        if let Some(hub) = self.remotes.lock().unwrap().get(environment).cloned() {
            return Ok(hub);
        }
        use sha2::{Digest, Sha256};
        let digest = Sha256::digest(environment.as_bytes());
        let name = digest
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        let path = self
            .path
            .as_ref()
            .ok_or_else(|| anyhow!("Remote owner approval storage is unavailable"))?
            .parent()
            .unwrap()
            .join("remote-permissions")
            .join(format!("{name}.json"));
        let hub = Arc::new(Self::open(&path)?);
        let mut remotes = self.remotes.lock().unwrap();
        Ok(remotes.entry(environment.into()).or_insert(hub).clone())
    }
    fn guest_hub(&self, environment: &str) -> Arc<Hub> {
        self.remotes
            .lock()
            .unwrap()
            .entry(environment.into())
            .or_insert_with(|| {
                Arc::new(Self {
                    book: Mutex::new(Book::default()),
                    path: None,
                    changed: Default::default(),
                    remotes: Default::default(),
                    decisions: Default::default(),
                })
            })
            .clone()
    }
    fn load_guest(&self, config: Value) -> Result<()> {
        let parsed = serde_json::from_value::<Config>(config.clone())?;
        let policy = parsed.policy.unwrap_or_else(Policy::sandbox);
        let grants = parsed.into_grants()?;
        let mut book = self.book.lock().unwrap();
        book.grants = grants;
        book.policy = policy;
        book.disk = Some(serde_json::to_vec(&config)?);
        Ok(())
    }
}
fn guest_body(s: &Service, target: &str, owner: &str) -> Result<Value> {
    let base = s.environment_service.sandboxes.guest_workspace(target)?;
    let cwd = if owner == "main" {
        base
    } else {
        format!("{base}/tasks/{owner}")
    };
    Ok(json!({"cwd":cwd,"role":if owner == "main" {"main"} else {"task"}}))
}
async fn execution_environment(
    s: &Service,
    generation: u64,
    plugin: bool,
    args: &Value,
) -> Result<String> {
    anyhow::ensure!(
        args.get("environment").is_none() || args.get("sandbox_id").is_none(),
        "Use environment or sandbox_id"
    );
    let requested = if let Some(value) = args.get("environment") {
        Some(crate::environment::resolve(s, value)?)
    } else if let Some(value) = args.get("sandbox_id") {
        let id = value
            .as_str()
            .ok_or_else(|| anyhow!("Invalid sandbox_id"))?;
        Some(pot_core::EnvironmentRef::sandbox(&s.runtime_id, id))
    } else {
        None
    };
    let session_environment = if plugin {
        if let Some(session) = args["session_id"].as_str() {
            if s.environment_service
                .host
                .host_plugin_session(&s.usage_scope, generation, session)
                .await
            {
                Some("host".to_owned())
            } else {
                Some(format!(
                    "sandbox:{}",
                    s.environment_service.sandboxes.session_target(
                        &s.usage_scope,
                        generation,
                        session
                    )?
                ))
            }
        } else {
            None
        }
    } else {
        None
    };
    let reference = if let Some(reference) = requested {
        reference
    } else if let Some(value) = &session_environment {
        crate::environment::resolve(s, &json!(value))?
    } else if s.is_worker {
        s.environment_ref
            .clone()
            .ok_or_else(|| anyhow!("Worker Environment binding missing"))?
    } else {
        pot_core::EnvironmentRef::host(&s.runtime_id)
    };
    crate::environment::check_binding(s, &reference)?;
    let environment = if reference.is_local_to(&s.runtime_id) {
        crate::environment::local_key(s, &reference)?
    } else {
        crate::remote_environment::port(s)?.validate(&reference)?;
        crate::remote_environment::key(&reference)?
    };
    if let Some(original) = &session_environment {
        anyhow::ensure!(
            &environment == original,
            "Session operation cannot switch execution environment"
        );
    }
    Ok(environment)
}
pub(crate) async fn prepare(s: &Service, name: &str, mut args: Value) -> Result<Value> {
    let thread = agent::thread(s)?;
    let plugin = thread.config.params.agent.plugin_for_tool(name)?;
    let environment = execution_environment(s, thread.generation, plugin.is_some(), &args).await?;
    let object = args
        .as_object_mut()
        .ok_or_else(|| anyhow!("Arguments must be an object"))?;
    object.remove("environment");
    object.remove("sandbox_id");
    if let Some(plugin) = &plugin {
        plugin.validate_input(name, &args)?;
    } else {
        pot_core::environment_tools::validate(name, &args)?;
    }
    if environment.starts_with("remote:") {
        anyhow::ensure!(
            plugin.is_none(),
            "Remote destination does not support this installed plugin"
        );
        let reference = crate::remote_environment::from_key(&environment)?;
        let binding = crate::remote_environment::binding(s, reference)?;
        crate::remote_environment::open(s, binding.clone()).await?;
        args = crate::remote_environment::port(s)?
            .prepare(binding, name.into(), args)
            .await?;
        s.environment_service.permissions.remote_hub(&environment)?;
        args["environment"] = json!(environment);
        return Ok(args);
    }
    if environment == "host" {
        let workspace = if s.is_worker {
            s.workspace.clone()
        } else {
            if s.environment_service.sandboxes.assignment("").is_err() {
                s.workspace.clone()
            } else {
                s.data.join("agent-workspace")
            }
        };
        std::fs::create_dir_all(&workspace)?;
        if let Some(plugin) = &plugin {
            anyhow::ensure!(
                plugin.manifest.execution == "environment",
                "This plugin supports Sandbox execution only"
            );
        } else {
            pot_tool_executor::tools::normalize(
                name,
                &mut args,
                &workspace,
                &thread.config.params.agent.shell,
            )?;
        }
    } else {
        let target = environment
            .strip_prefix("sandbox:")
            .filter(|s| !s.is_empty())
            .ok_or_else(|| anyhow!("Use host or sandbox:ID"))?;
        let target = s.environment_service.sandboxes.assignment(target)?;
        anyhow::ensure!(
            !s.is_worker || s.sandbox_id.as_deref() == Some(target.as_str()),
            "Workers cannot switch Sandboxes"
        );
        if name == "write_stdin" {
            // Existing session handles are resolved and checked by the dispatcher.
            // Polling/input must not boot a guest or reload permission state.
            args["environment"] = json!(environment);
            return Ok(args);
        }
        let turn = {
            let inner = s.conversation.inner.lock().unwrap();
            let turn = inner
                .turn
                .as_ref()
                .ok_or_else(|| anyhow!("Active turn required"))?;
            inner
                .store
                .message(&turn.user_message_id)?
                .and_then(|m| m.request_id)
                .ok_or_else(|| anyhow!("Turn identity missing"))?
        };
        s.environment_service
            .sandboxes
            .prepare(&target, &s.usage_scope, Some(&turn), &|| {
                agent::cancelled(s) || s.stopping()
            })
            .await?;
        let hub = s.environment_service.permissions.guest_hub(&environment);
        let _decision = hub.decisions.lock().await;
        let mut body = guest_body(s, &target, &s.usage_scope)?;
        body["action"] = json!("prepare");
        body["tool"] = json!(name);
        body["args"] = args;
        body["policy"] = json!(thread.config.params.agent.shell);
        let prepared = s
            .environment_service
            .sandboxes
            .permissions(&target, &body)
            .await?;
        hub.load_guest(prepared["config"].clone())?;
        args = prepared["args"].clone();
        if let Some(plugin) = &plugin {
            plugin.validate_input(name, &args)?;
        } else {
            pot_core::environment_tools::validate(name, &args)?;
        }
        // Canonicalization occurs in the guest. Host paths are never used to normalize it.
    }
    args["environment"] = json!(environment);
    Ok(args)
}

pub(crate) fn rules_edit_requires_review(s: &Service, tool: &str, args: &Value) -> bool {
    let _ = args;
    !s.is_worker && tool == "update_agent_instructions"
}

fn live(s: &Service, r: &Request) -> bool {
    if s.stopping() {
        return false;
    }
    let inner = s.conversation.inner.lock().unwrap();
    agent::current(&inner.store)
        .ok()
        .flatten()
        .is_some_and(|b| b.generation == r.generation && b.owner == r.owner)
        && inner
            .turn
            .as_ref()
            .filter(|t| !t.cancelling)
            .and_then(|t| inner.store.message(&t.user_message_id).ok().flatten())
            .is_some_and(|m| m.request_id.as_deref() == Some(&r.turn))
}
struct WaitingRequest<'a> {
    hub: &'a Hub,
    key: String,
}
impl Drop for WaitingRequest<'_> {
    fn drop(&mut self) {
        let _ = self.hub.cancel(&self.key);
    }
}
pub(crate) async fn request(
    s: &Service,
    generation: u64,
    turn: &str,
    value: Value,
) -> Result<Value> {
    let request: Request = serde_json::from_value(value)?;
    anyhow::ensure!(
        request.owner == s.usage_scope
            && request.generation == generation
            && request.turn == turn
            && live(s, &request),
        "Permission identity changed"
    );
    anyhow::ensure!(
        request.call_id.len() <= 256
            && !request.call_id.is_empty()
            && serde_json::to_vec(&request)?.len() <= 300000,
        "Invalid permission request"
    );
    anyhow::ensure!(
        request.proposed_grant.as_ref().is_none_or(|suggested| {
            request
                .operation_scope()
                .is_ok_and(|actual| suggested.covers(&actual))
        }),
        "Permission scope does not match operation"
    );
    let plugin = agent::thread(s)?
        .config
        .params
        .agent
        .plugin_for_tool(&request.tool)?;
    let expected_display = plugin
        .as_ref()
        .map(|plugin| plugin.approval_display(&request.tool))
        .transpose()?
        .flatten();
    anyhow::ensure!(
        request.display == expected_display,
        "Permission display does not match bound tool"
    );
    anyhow::ensure!(
        request.plugin == plugin.map(|plugin| plugin.identity()),
        "Permission plugin identity does not match bound package"
    );
    let hub = s
        .environment_service
        .permissions
        .environment(&request.environment)?;
    let key = hub.enqueue(request.clone())?;
    let _waiting = WaitingRequest {
        hub: &hub,
        key: key.clone(),
    };
    loop {
        let notification = hub.changed.notified();
        if !live(s, &request) {
            hub.cancel(&key)?;
            anyhow::bail!("Permission request cancelled");
        }
        if let Some(approved) = hub.consume(&key)? {
            return Ok(json!({"request":request,"approved":approved}));
        }
        tokio::select! {
            _ = notification => {},
            _ = s.cancel_notify.notified() => {},
            _ = s.stop_requested() => {},
            _ = tokio::time::sleep(std::time::Duration::from_millis(200)) => {},
        }
    }
}
fn lane(root: &Service, owner: &str) -> Option<Service> {
    if owner == root.usage_scope {
        Some(root.clone())
    } else {
        tasks::permission_lane(root, owner)
    }
}
fn refresh(root: &Service) -> Result<()> {
    for (_, hub) in root.environment_service.permissions.all() {
        let records = hub.book.lock().unwrap().requests.clone();
        for p in records {
            if lane(root, &p.request.owner).is_some_and(|s| !live(&s, &p.request)) {
                hub.cancel(&p.id)?;
            }
        }
    }
    Ok(())
}
pub async fn inspect(State(root): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    if root.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_only",
            "Owner permission administration required",
        ));
    }
    refresh(&root).map_err(internal)?;
    let mut requests = vec![];
    let mut grants = vec![];
    for (environment, hub) in root.environment_service.permissions.all() {
        let environment_name = environment
            .strip_prefix("sandbox:")
            .map(|target| {
                root.environment_service
                    .sandboxes
                    .display_name(target)
                    .unwrap_or_else(|_| target.into())
            })
            .unwrap_or_else(|| {
                if environment.starts_with("remote:") {
                    crate::remote_environment::from_key(&environment)
                        .map(|reference| {
                            let target = match reference.target {
                                pot_core::EnvironmentTarget::Host {} => "Host".to_owned(),
                                pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                                    format!("Sandbox {sandbox_id}")
                                }
                            };
                            format!("{} / {target}", reference.runtime_id)
                        })
                        .unwrap_or_else(|_| environment.clone())
                } else {
                    "Host".into()
                }
            });
        let book = hub.book.lock().unwrap();
        requests.extend(
            book.requests
                .iter()
                .filter(|p| p.phase == "pending" && lane(&root, &p.request.owner).is_some())
                .map(
                    |p| json!({"id":p.id,"request":p.request,"environment_name":environment_name}),
                ),
        );
        grants.extend(
            book.grants
                .iter()
                .filter(|g| lane(&root, &g.owner).is_some())
                .map(|g| {
                    let mut value = json!(g);
                    value["environment"] = json!(environment);
                    value
                }),
        );
    }
    Ok(Json(json!({"requests":requests,"grants":grants})))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Decision {
    id: String,
    action: String,
    #[serde(default)]
    scope: Option<Scope>,
}
pub async fn decide(
    State(root): State<Service>,
    h: HeaderMap,
    Json(body): Json<Decision>,
) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    if root.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_only",
            "Owner permission administration required",
        ));
    }
    refresh(&root).map_err(internal)?;
    let (environment, hub) = root
        .environment_service
        .permissions
        .all()
        .into_iter()
        .find(|(_, hub)| {
            let book = hub.book.lock().unwrap();
            book.requests
                .iter()
                .any(|p| p.id == body.id && lane(&root, &p.request.owner).is_some())
                || book
                    .grants
                    .iter()
                    .any(|g| g.id == body.id && lane(&root, &g.owner).is_some())
        })
        .ok_or_else(|| {
            error(
                StatusCode::CONFLICT,
                "permission_decision",
                "Permission is no longer active",
            )
        })?;
    let _decision = hub.decisions.lock().await;
    if environment == "host"
        || environment.starts_with("remote:")
        || !["allow_persistent", "revoke"].contains(&body.action.as_str())
    {
        hub.decide(&body.id, &body.action, body.scope)
            .map_err(|e| error(StatusCode::CONFLICT, "permission_decision", &e.to_string()))?;
    } else {
        let (next, expected) = {
            let book = hub.book.lock().unwrap();
            (
                Hub::plan(&book, &body.id, &body.action, body.scope).map_err(internal)?,
                serde_json::from_slice::<Value>(
                    book.disk
                        .as_ref()
                        .ok_or_else(|| internal(anyhow!("Guest config missing")))?,
                )
                .map_err(|e| internal(e.into()))?,
            )
        };
        let target = environment
            .strip_prefix("sandbox:")
            .ok_or_else(|| internal(anyhow!("Invalid environment")))?;
        let mut config = Config::from_grants(&next.grants);
        config.policy = Some(next.policy);
        let config = json!(config);
        let decision_id = body.id.clone();
        let body = guest_body(&root, target, "main").map_err(internal)?;
        let mut write = body;
        write["action"] = json!("write");
        write["expected"] = expected;
        write["config"] = config.clone();
        root.environment_service
            .sandboxes
            .permissions(target, &write)
            .await
            .map_err(internal)?;
        let mut book = hub.book.lock().unwrap();
        // Requests can be cancelled while publication is awaiting the guest. Never restore them.
        book.grants = next.grants;
        book.disk = Some(serde_json::to_vec(&config).map_err(|e| internal(e.into()))?);
        if let Some(planned) = next.requests.iter().find(|n| n.id == decision_id)
            && let Some(p) = book
                .requests
                .iter_mut()
                .find(|p| p.id == decision_id && p.phase == "pending")
        {
            *p = planned.clone();
        }
        hub.changed.notify_waiters();
    }
    Ok(Json(json!({"ok":true})))
}

#[cfg(test)]
mod tests {
    #[test]
    fn own_instruction_edits_require_review_in_host_environment() {
        let s = crate::tasks::tests::fixture();
        assert!(rules_edit_requires_review(
            &s,
            "update_agent_instructions",
            &json!({"text":"rules"})
        ));
        assert!(!rules_edit_requires_review(
            &s,
            "read_agent_instructions",
            &json!({})
        ));
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }

    use super::*;
    use std::os::unix::fs::PermissionsExt;
    fn operation() -> Request {
        Request {
            owner: "main".into(),
            environment: "host".into(),
            generation: 1,
            turn: "turn".into(),
            call_id: "call".into(),
            tool: "exec_command".into(),
            args: json!({"cmd":"true","workdir":"/","timeout_ms":60000,"shell":"/bin/bash","login":true,"tty":false}),
            proposed_grant: None,
            display: None,
            plugin: None,
        }
    }
    fn directory() -> PathBuf {
        let path = std::env::temp_dir().join(format!("pot-permissions-{}", id()));
        std::fs::create_dir(&path).unwrap();
        path
    }
    #[test]
    fn optional_scope_uses_reviewed_operation_for_permanent_grants() {
        for explicit in [false, true] {
            let hub = Hub::memory();
            let mut request = operation();
            if explicit {
                request.proposed_grant = Some(request.operation_scope().unwrap());
            }
            let serialized = serde_json::to_value(&request).unwrap();
            assert_eq!(serialized.get("proposed_grant").is_some(), explicit);
            let key = hub.enqueue(request.clone()).unwrap();
            hub.decide(&key, "allow_persistent", None).unwrap();
            hub.decide(&key, "allow_persistent", None).unwrap();
            assert_eq!(
                hub.grants("main")[0]["scope"],
                serde_json::to_value(request.operation_scope().unwrap()).unwrap()
            );
        }
    }
    #[tokio::test]
    async fn plugin_environment_defaults_and_session_ownership_match_execution_tools() {
        let s = tasks::tests::fixture();
        assert_eq!(
            execution_environment(&s, 1, false, &json!({}))
                .await
                .unwrap(),
            "host"
        );
        assert_eq!(
            execution_environment(&s, 1, true, &json!({}))
                .await
                .unwrap(),
            "host"
        );
        assert_eq!(
            execution_environment(&s, 1, true, &json!({"sandbox_id":"default"}))
                .await
                .unwrap(),
            "sandbox:default"
        );
        assert_eq!(
            execution_environment(&s, 1, true, &json!({"environment":"sandbox:default"}))
                .await
                .unwrap(),
            "sandbox:default"
        );
        assert!(
            execution_environment(&s, 1, true, &json!({"session_id":"lost"}))
                .await
                .is_err()
        );
        let mut worker = s.clone();
        worker.is_worker = true;
        worker.sandbox_id = Some("default".into());
        worker.environment_ref = Some(pot_core::EnvironmentRef::sandbox(
            &worker.runtime_id,
            "default",
        ));
        assert_eq!(
            execution_environment(&worker, 1, true, &json!({}))
                .await
                .unwrap(),
            "sandbox:default"
        );
        assert!(
            execution_environment(&worker, 1, true, &json!({"environment":"host"}))
                .await
                .is_err()
        );
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
    #[test]
    fn host_and_guest_defaults_and_explicit_policies_survive_grant_changes() {
        let root = directory();
        let path = root.join("permissions.json");
        let host = Hub::open(&path).unwrap();
        assert_eq!(host.state("main")["policy"]["default"], "ask");
        let guest = Hub::memory();
        guest.load_guest(json!({"version":1,"grants":[]})).unwrap();
        assert_eq!(guest.state("main")["policy"]["default"], "allow");
        guest
            .load_guest(json!({"version":1,"policy":{"default":"ask"},"grants":[]}))
            .unwrap();
        assert_eq!(guest.state("main")["policy"]["default"], "ask");
        std::fs::write(
            &path,
            r#"{"version":1,"policy":{"default":"allow"},"grants":[]}"#,
        )
        .unwrap();
        let host = Hub::open(&path).unwrap();
        assert_eq!(host.state("main")["policy"]["default"], "allow");
        let key = host.enqueue(operation()).unwrap();
        host.decide(&key, "allow_persistent", None).unwrap();
        host.decide(&key, "revoke", None).unwrap();
        assert_eq!(
            Hub::open(&path).unwrap().state("main")["policy"]["default"],
            "allow"
        );
        std::fs::write(
            &path,
            r#"{"version":1,"policy":{"default":"anything"},"grants":[]}"#,
        )
        .unwrap();
        assert!(Hub::open(&path).is_err());
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn once_is_consumed_once_and_only_current_grants_survive_restart() {
        let root = directory();
        let path = root.join("permissions.json");
        let hub = Hub::open(&path).unwrap();
        let initial = read(&path).unwrap();
        let r = operation();
        let key = hub.enqueue(r.clone()).unwrap();
        assert_eq!(hub.enqueue(r.clone()).unwrap(), key);
        let mut conflict = r.clone();
        conflict.args["cmd"] = json!("false");
        assert!(hub.enqueue(conflict).is_err());
        assert!(hub.consume(&key).unwrap().is_none());
        hub.decide(&key, "allow_once", None).unwrap();
        assert_eq!(hub.consume(&key).unwrap(), Some(true));
        assert_eq!(hub.consume(&key).unwrap(), Some(false));
        assert!(hub.book.lock().unwrap().requests.is_empty());
        assert_eq!(read(&path).unwrap(), initial);
        let mut next = r;
        next.call_id = "saved".into();
        let saved = hub.enqueue(next.clone()).unwrap();
        hub.decide(&saved, "allow_persistent", None).unwrap();
        hub.decide(&saved, "allow_persistent", None).unwrap();
        // The same Agent/scope replaces its previous state rather than appending history.
        next.call_id = "replacement".into();
        next.generation = 99;
        let replacement = hub.enqueue(next.clone()).unwrap();
        hub.decide(&replacement, "allow_persistent", None).unwrap();
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        next.call_id = "pending".into();
        let pending = hub.enqueue(next).unwrap();
        drop(hub);
        let hub = Hub::open(&path).unwrap();
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        assert!(hub.grants("other").as_array().unwrap().is_empty());
        assert_eq!(hub.consume(&pending).unwrap(), Some(false));
        assert_eq!(hub.consume(&saved).unwrap(), Some(false));
        assert_eq!(hub.consume(&replacement).unwrap(), Some(false));
        let file: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(file["version"], 1);
        assert_eq!(file["grants"][0]["agent"], "main");
        assert_eq!(file["grants"][0]["permission"]["type"], "operation");
        assert!(file.get("requests").is_none());
        assert!(file["grants"][0].get("generation").is_none());
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        hub.decide(&replacement, "revoke", None).unwrap();
        assert!(hub.grants("main").as_array().unwrap().is_empty());
        drop(hub);
        assert!(
            Hub::open(&path)
                .unwrap()
                .grants("main")
                .as_array()
                .unwrap()
                .is_empty()
        );
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn settled_requests_do_not_accumulate_or_touch_the_file() {
        let root = directory();
        let path = root.join("permissions.json");
        let hub = Hub::open(&path).unwrap();
        let bytes = read(&path).unwrap();
        for n in 0..1100 {
            let mut request = operation();
            request.call_id = n.to_string();
            let key = hub.enqueue(request).unwrap();
            if n % 2 == 0 {
                hub.decide(&key, "deny", None).unwrap();
            } else {
                hub.cancel(&key).unwrap();
            }
            assert_eq!(hub.consume(&key).unwrap(), Some(false));
            assert!(hub.book.lock().unwrap().requests.is_empty());
        }
        let key = hub.enqueue(operation()).unwrap();
        drop(WaitingRequest {
            hub: &hub,
            key: key.clone(),
        });
        assert_eq!(hub.consume(&key).unwrap(), Some(false));
        assert!(hub.book.lock().unwrap().requests.is_empty());
        assert_eq!(read(&path).unwrap(), bytes);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn manual_edits_take_effect_on_restart_and_are_not_overwritten_by_stale_writers() {
        let root = directory();
        let path = root.join("permissions.json");
        let hub = Hub::open(&path).unwrap();
        let stale = Hub::open(&path).unwrap();
        let key = hub.enqueue(operation()).unwrap();
        hub.decide(&key, "allow_persistent", None).unwrap();
        let edited = std::fs::read(&path).unwrap();
        let key = stale.enqueue(operation()).unwrap();
        assert!(stale.decide(&key, "allow_persistent", None).is_err());
        assert!(stale.grants("main").as_array().unwrap().is_empty());
        assert_eq!(stale.consume(&key).unwrap(), None);
        assert_eq!(std::fs::read(&path).unwrap(), edited);
        std::fs::write(&path, "{\"version\":1,\"grants\":[]}").unwrap();
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        assert!(
            Hub::open(&path)
                .unwrap()
                .grants("main")
                .as_array()
                .unwrap()
                .is_empty()
        );
        std::fs::write(&path, "{bad").unwrap();
        assert!(Hub::open(&path).is_err());
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "{bad");
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn legacy_migration_keeps_latest_permanent_grants_and_discards_history() {
        let root = directory();
        let legacy = root.join("permissions-v1.sqlite");
        let path = root.join("permissions.json");
        let store = Store::open(&legacy).unwrap();
        let r = operation();
        store
            .set(
                "permissions.v1",
                &json!({"requests":[{"request":r}], "grants":[
                    {"id":"old","owner":"main","epoch":1,"scope":r.operation_scope().unwrap()},
                    {"id":"latest","owner":"main","epoch":2,"scope":r.operation_scope().unwrap()}
                ]})
                .to_string(),
            )
            .unwrap();
        drop(store);
        let hub = Hub::open_with_legacy(&path, &legacy).unwrap();
        assert!(!legacy.exists());
        assert!(hub.book.lock().unwrap().requests.is_empty());
        assert_eq!(hub.grants("main")[0]["id"], "latest");
        assert_eq!(hub.grants("main").as_array().unwrap().len(), 1);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[tokio::test]
    async fn owner_only_decisions_and_cancelled_turns_cannot_approve() {
        let mut s = tasks::tests::fixture();
        s.is_worker = true;
        assert_eq!(
            inspect(State(s.clone()), tasks::tests::headers())
                .await
                .unwrap_err()
                .0,
            StatusCode::FORBIDDEN
        );
        s.is_worker = false;
        let mut r = operation();
        r.generation = agent::thread(&s).unwrap().generation;
        let key = s.environment_service.permissions.enqueue(r).unwrap();
        let body = serde_json::from_value(json!({"id":key,"action":"allow_once"})).unwrap();
        assert_eq!(
            decide(State(s.clone()), tasks::tests::headers(), Json(body))
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        assert_eq!(
            s.environment_service.permissions.consume(&key).unwrap(),
            Some(false)
        );
        assert!(
            s.environment_service
                .permissions
                .book
                .lock()
                .unwrap()
                .requests
                .is_empty()
        );
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
}
