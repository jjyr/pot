//! Control-owned assistant-to-machine capability authorization.
use super::*;
use pot_agent::permissions::{Capability, Config, DefaultDecision, Policy, Request, Scope};
use serde::Deserialize;
use std::{
    collections::BTreeMap,
    io::Write,
    os::{fd::AsRawFd, unix::fs::OpenOptionsExt},
};

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
struct WorkKey {
    assistant: String,
    work: String,
    scope: Scope,
}
#[derive(Clone)]
struct Pending {
    id: String,
    request: Request,
    assistant: String,
    work: String,
    scope: Option<Scope>,
    waiters: Vec<Request>,
    decision: Option<bool>,
}
#[derive(Default)]
struct Book {
    policy: Policy,
    requests: Vec<Pending>,
    temporary: BTreeMap<WorkKey, bool>,
    disk: Option<Vec<u8>>,
}
pub(crate) struct Hub {
    pub(crate) ui_changed: pot_core::changes::Signal,
    book: Mutex<Book>,
    path: Option<PathBuf>,
    changed: tokio::sync::Notify,
    pub(crate) sandbox_mutations: tokio::sync::Mutex<()>,
}
fn read(path: &std::path::Path) -> Result<Option<Vec<u8>>> {
    match std::fs::read(path) {
        Ok(b) => Ok(Some(b)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.into()),
    }
}
impl Hub {
    pub(crate) fn machine_policy(&self, machine: &str) -> pot_agent::permissions::MachinePolicy {
        self.book
            .lock()
            .unwrap()
            .policy
            .machines
            .get(machine)
            .cloned()
            .unwrap_or_default()
    }
    #[cfg(test)]
    pub(crate) fn set_machine_policy(
        &self,
        machine: &str,
        mode: pot_agent::permissions::ExecutionMode,
        access: DefaultDecision,
    ) {
        self.book.lock().unwrap().policy.machines.insert(
            machine.into(),
            pot_agent::permissions::MachinePolicy {
                execution_mode: mode,
                host_access: access,
            },
        );
    }
    fn continuous_host(&self, assistant: &str, machine: &str) -> bool {
        Self::allowed(
            &self.book.lock().unwrap(),
            &WorkKey {
                assistant: assistant.into(),
                work: String::new(),
                scope: Scope {
                    machine_id: machine.into(),
                    capability: Capability::Host,
                },
            },
        ) == Some(true)
    }
    #[cfg(test)]
    pub fn memory() -> Self {
        Self {
            book: Mutex::new(Book::default()),
            path: None,
            changed: Default::default(),
            ui_changed: Default::default(),
            sandbox_mutations: Default::default(),
        }
    }
    pub fn open(path: &std::path::Path) -> Result<Self> {
        let disk = read(path)?;
        let config: Config = disk
            .as_ref()
            .map(|b| serde_json::from_slice(b))
            .transpose()?
            .unwrap_or_default();
        let policy = config.policy.clone();
        config.validate()?;
        Ok(Self {
            book: Mutex::new(Book {
                policy,
                disk,
                ..Book::default()
            }),
            path: Some(path.into()),
            changed: Default::default(),
            ui_changed: Default::default(),
            sandbox_mutations: Default::default(),
        })
    }
    fn persist(&self, policy: Policy, expected: Option<&[u8]>) -> Result<Vec<u8>> {
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
        let config = Config { version: 3, policy };
        config.validate()?;
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

    #[cfg(test)]
    pub fn configured(&self) -> bool {
        self.path.is_some()
    }
    pub(crate) fn retain_work(&self, assistant: &str, active: &std::collections::BTreeSet<String>) {
        self.book.lock().unwrap().temporary.retain(|key, _| {
            key.assistant != assistant
                || active
                    .iter()
                    .any(|work| key.work == *work || key.work.starts_with(&format!("{work}:")))
        });
    }
    fn sync_machine_epoch(&self, machine: &str, epoch: &str) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        if book
            .policy
            .machine_epochs
            .get(machine)
            .is_some_and(|old| old == epoch)
        {
            return Ok(());
        }
        let changed = book.policy.machine_epochs.contains_key(machine);
        let mut policy = book.policy.clone();
        policy.machine_epochs.insert(machine.into(), epoch.into());
        if changed {
            policy.machines.remove(machine);
        }
        if self.path.is_some() {
            book.disk = Some(self.persist(policy.clone(), book.disk.as_deref())?);
        }
        book.policy = policy;
        if changed {
            book.temporary
                .retain(|key, _| key.scope.machine_id != machine);
            book.requests.retain(|p| {
                p.scope
                    .as_ref()
                    .is_none_or(|scope| scope.machine_id != machine)
            });
        }
        self.changed.notify_waiters();
        self.ui_changed.notify();
        Ok(())
    }
    fn save_policy(&self, book: &mut Book, policy: Policy) -> Result<()> {
        Config {
            version: 3,
            policy: policy.clone(),
        }
        .validate()?;
        if self.path.is_some() {
            book.disk = Some(self.persist(policy.clone(), book.disk.as_deref())?);
        }
        book.policy = policy;
        self.changed.notify_waiters();
        self.ui_changed.notify();
        Ok(())
    }
    fn allowed(book: &Book, key: &WorkKey) -> Option<bool> {
        if key.scope.capability == Capability::Sandbox {
            return Some(true);
        }
        let rule = book
            .policy
            .machines
            .get(&key.scope.machine_id)
            .cloned()
            .unwrap_or_default()
            .host_access;
        if rule == DefaultDecision::Deny {
            return Some(false);
        }
        if let Some(decision) = book.temporary.get(key) {
            return Some(*decision);
        }
        if rule == DefaultDecision::Allow {
            return Some(true);
        }
        None
    }
    pub(crate) fn join(
        &self,
        request: Request,
        assistant: &str,
        work: &str,
        scope: Option<Scope>,
    ) -> Result<String> {
        let mut book = self.book.lock().unwrap();
        if let Some(p) = book.requests.iter_mut().find(|p| {
            p.assistant == assistant
                && p.work == work
                && p.scope == scope
                && (scope
                    .as_ref()
                    .is_some_and(|s| s.capability == Capability::Sandbox)
                    || p.request == request)
        }) {
            p.waiters.push(request);
            return Ok(p.id.clone());
        }
        anyhow::ensure!(
            book.requests.len() < 1000,
            "Pending permission limit reached"
        );
        let id = id();
        book.requests.push(Pending {
            id: id.clone(),
            request: request.clone(),
            assistant: assistant.into(),
            work: work.into(),
            scope,
            waiters: vec![request],
            decision: None,
        });
        self.changed.notify_waiters();
        self.ui_changed.notify();
        Ok(id)
    }
    fn leave_request(&self, id: &str, request: &Request) {
        let mut book = self.book.lock().unwrap();
        if let Some(p) = book.requests.iter_mut().find(|p| p.id == id) {
            if let Some(index) = p.waiters.iter().position(|r| r == request) {
                p.waiters.remove(index);
            }
            if let Some(first) = p.waiters.first() {
                p.request = first.clone();
            }
        }
        book.requests.retain(|p| !p.waiters.is_empty());
    }
    fn decision(&self, id: &str) -> Option<bool> {
        let book = self.book.lock().unwrap();
        let Some(p) = book.requests.iter().find(|p| p.id == id) else {
            return Some(false);
        };
        if p.decision == Some(false) {
            return Some(false);
        }
        if let Some(scope) = &p.scope {
            Self::allowed(
                &book,
                &WorkKey {
                    assistant: p.assistant.clone(),
                    work: p.work.clone(),
                    scope: scope.clone(),
                },
            )
        } else {
            p.decision
        }
    }
    fn decide(&self, assistant: &str, key: &str, action: &str) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        anyhow::ensure!(
            matches!(action, "allow_once" | "deny"),
            "Only a single operation can be approved here; edit machine settings for persistent access"
        );
        let pending = book
            .requests
            .iter()
            .find(|p| p.id == key && p.assistant == assistant)
            .cloned()
            .ok_or_else(|| anyhow!("Request no longer pending"))?;
        anyhow::ensure!(pending.decision.is_none(), "Request already decided");
        if let Some(scope) = pending.scope {
            book.temporary.insert(
                WorkKey {
                    assistant: assistant.into(),
                    work: pending.work,
                    scope,
                },
                action == "allow_once",
            );
        }
        book.requests
            .iter_mut()
            .find(|p| p.id == key)
            .unwrap()
            .decision = Some(action == "allow_once");
        self.changed.notify_waiters();
        self.ui_changed.notify();
        Ok(())
    }
}
fn guest_body(s: &Service, target: &str, owner: &str) -> Result<Value> {
    let base = s.execution_service.sandboxes.guest_workspace(target)?;
    let cwd = if owner == "main" {
        base
    } else {
        format!("{base}/tasks/{owner}")
    };
    Ok(json!({"cwd":cwd,"role":if owner=="main" {"main"} else {"task"}}))
}
async fn execution_execution_target(
    s: &Service,
    generation: u64,
    plugin: bool,
    args: &Value,
) -> Result<String> {
    anyhow::ensure!(
        args.get("execution_target").is_none() || args.get("sandbox_id").is_none(),
        "Use execution_target or sandbox_id"
    );
    let requested = if let Some(value) = args.get("execution_target") {
        Some(crate::execution_target::resolve(s, value)?)
    } else if let Some(value) = args.get("sandbox_id") {
        let id = value
            .as_str()
            .ok_or_else(|| anyhow!("Invalid sandbox_id"))?;
        Some(pot_core::ExecutionTarget::sandbox(&s.runtime_id, id))
    } else {
        None
    };
    // Remote sessions are verified by the bound destination executor, not local registries.
    let remote_session = requested
        .as_ref()
        .is_some_and(|r| !r.is_local_to(&s.runtime_id))
        || crate::remote_execution::is_remote(s);
    let session_execution_target = if plugin && !remote_session {
        if let Some(session) = args["session_id"].as_str() {
            if s.execution_service
                .host
                .host_session(&s.usage_scope, generation, session)
                .await
            {
                Some("host".to_owned())
            } else {
                Some(format!(
                    "sandbox:{}",
                    s.execution_service.sandboxes.session_target(
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
    } else if let Some(value) = &session_execution_target {
        crate::execution_target::resolve(s, &json!(value))?
    } else if s.execution_target.is_some() {
        s.execution_target
            .clone()
            .ok_or_else(|| anyhow!("Worker Execution target binding missing"))?
    } else {
        pot_core::ExecutionTarget::host(&s.runtime_id)
    };
    crate::execution_target::check_binding(s, &reference)?;
    let execution_target = if reference.is_local_to(&s.runtime_id) {
        crate::execution_target::local_key(s, &reference)?
    } else {
        crate::remote_execution::port(s)?.validate(&reference)?;
        crate::remote_execution::key(&reference)?
    };
    if let Some(original) = &session_execution_target {
        anyhow::ensure!(
            &execution_target == original,
            "Session operation cannot switch execution target"
        );
    }
    Ok(execution_target)
}
pub(crate) async fn resolve_operation(s: &Service, name: &str, mut args: Value) -> Result<Value> {
    let thread = agent::thread(s)?;
    let plugin = thread.config.params.agent.plugin_for_tool(name)?.is_some();
    let env =
        execution_execution_target(s, thread.generation, plugin || name == "write_stdin", &args)
            .await?;
    args.as_object_mut()
        .ok_or_else(|| anyhow!("Arguments must be an object"))?
        .remove("sandbox_id");
    args["execution_target"] = json!(env);
    if let Some(target) = env.strip_prefix("sandbox:")
        && pot_sandbox::Manager::is_managed(target)
    {
        s.execution_service.sandboxes.ensure_managed(target)?;
    }
    if env == "host" {
        return prepare(s, name, args).await;
    }
    Ok(args)
}
pub(crate) async fn prepare(s: &Service, name: &str, mut args: Value) -> Result<Value> {
    let thread = agent::thread(s)?;
    let plugin = thread.config.params.agent.plugin_for_tool(name)?;
    let execution_target = execution_execution_target(
        s,
        thread.generation,
        plugin.is_some() || name == "write_stdin",
        &args,
    )
    .await?;
    let object = args
        .as_object_mut()
        .ok_or_else(|| anyhow!("Arguments must be an object"))?;
    object.remove("execution_target");
    object.remove("sandbox_id");
    if let Some(plugin) = &plugin {
        plugin.validate_input(name, &args)?;
    } else {
        pot_core::execution_tools::validate(name, &args)?;
    }
    if execution_target.starts_with("remote:") {
        let reference = crate::remote_execution::from_key(&execution_target)?;
        let binding = crate::remote_execution::binding(s, reference)?;
        crate::remote_execution::open(s, binding.clone()).await?;
        args = crate::remote_execution::port(s)?
            .prepare(binding, name.into(), args)
            .await?;
        args["execution_target"] = json!(execution_target);
        return Ok(args);
    }
    if execution_target == "host" {
        let workspace = if s.is_worker {
            s.workspace.clone()
        } else {
            if s.execution_service.sandboxes.assignment("").is_err() {
                s.workspace.clone()
            } else {
                s.data.join("agent-workspace")
            }
        };
        std::fs::create_dir_all(&workspace)?;
        if let Some(plugin) = &plugin {
            anyhow::ensure!(
                plugin.manifest.supports_host(),
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
        let target = execution_target
            .strip_prefix("sandbox:")
            .filter(|s| !s.is_empty())
            .ok_or_else(|| anyhow!("Use host or sandbox:ID"))?;
        let target = s.execution_service.sandboxes.assignment(target)?;
        anyhow::ensure!(
            !s.is_worker || s.sandbox_id.as_deref() == Some(target.as_str()),
            "Workers cannot switch Sandboxes"
        );
        if name == "write_stdin" {
            // Existing session handles are resolved and checked by the dispatcher.
            // Polling/input must not boot a guest or reload permission state.
            args["execution_target"] = json!(execution_target);
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
        s.execution_service
            .sandboxes
            .prepare(&target, &s.usage_scope, Some(&turn), &|| {
                agent::cancelled(s) || s.stopping()
            })
            .await?;
        let mut body = guest_body(s, &target, &s.usage_scope)?;
        body["action"] = json!("prepare");
        body["tool"] = json!(name);
        body["args"] = args;
        body["policy"] = json!(thread.config.params.agent.shell);
        let prepared = s
            .execution_service
            .sandboxes
            .prepare_operation(&target, &body)
            .await?;
        args = prepared["args"].clone();
        if let Some(plugin) = &plugin {
            plugin.validate_input(name, &args)?;
        } else {
            pot_core::execution_tools::validate(name, &args)?;
        }
        // Canonicalization occurs in the guest. Host paths are never used to normalize it.
    }
    args["execution_target"] = json!(execution_target);
    Ok(args)
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
    request: Request,
}
impl Drop for WaitingRequest<'_> {
    fn drop(&mut self) {
        self.hub.leave_request(&self.key, &self.request);
    }
}
/// Work identity is trusted conversation state, never supplied by a model.
pub(crate) fn work_scope(s: &Service) -> Result<String> {
    let inner = s.conversation.inner.lock().unwrap();
    if s.is_worker
        && let Some(work) = inner.store.get("permissions.work")?
    {
        return Ok(work);
    }
    let turn = inner
        .turn
        .as_ref()
        .ok_or_else(|| anyhow!("Active work required"))?;
    Ok(format!(
        "{}:{}:{}",
        s.assistant_id, s.usage_scope, turn.user_message_id
    ))
}
fn capability(reference: &pot_core::ExecutionTarget) -> Scope {
    Scope {
        machine_id: reference.runtime_id.clone(),
        capability: match reference.target {
            pot_core::ExecutionTargetKind::Host {} => Capability::Host,
            pot_core::ExecutionTargetKind::Sandbox { .. } => Capability::Sandbox,
        },
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
        !request.call_id.is_empty()
            && request.call_id.len() <= 256
            && serde_json::to_vec(&request)?.len() <= 300000,
        "Invalid permission request"
    );
    let scope = if request.tool == "update_agent_instructions" {
        anyhow::ensure!(
            !s.is_worker,
            "Only the owning assistant can edit instructions"
        );
        None
    } else {
        let plugin = agent::thread(s)?
            .config
            .params
            .agent
            .plugin_for_tool(&request.tool)?
            .is_some();
        anyhow::ensure!(
            pot_agent::permissions::is_execution_tool(&request.tool) || plugin,
            "Unsupported execution permission"
        );
        let mut args = request.args.clone();
        if !request.execution_target.is_empty() {
            args["execution_target"] = json!(request.execution_target);
        }
        let env = execution_execution_target(
            s,
            generation,
            plugin || request.tool == "write_stdin",
            &args,
        )
        .await?;
        let reference = if env.starts_with("remote:") {
            crate::remote_execution::from_key(&env)?
        } else {
            crate::execution_target::resolve(s, &json!(env))?
        };
        Some(capability(&reference))
    };
    if let Some(scope) = &scope
        && scope.machine_id != s.runtime_id
    {
        let epoch = crate::remote_execution::port(s)?.machine_epoch(&scope.machine_id)?;
        s.execution_service
            .permissions
            .sync_machine_epoch(&scope.machine_id, &epoch)?;
    }
    let mut work = work_scope(s)?;
    if scope
        .as_ref()
        .is_some_and(|s| s.capability == Capability::Host)
    {
        use sha2::{Digest, Sha256};
        let digest = Sha256::digest(serde_json::to_vec(&request)?);
        work.push(':');
        work.push_str(
            &digest
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>(),
        );
    }
    let hub = &s.execution_service.permissions;
    if let Some(scope) = &scope
        && scope.capability == Capability::Host
        && !hub.continuous_host(&s.assistant_id, &scope.machine_id)
    {
        anyhow::ensure!(
            request.args["tty"] != true,
            "Interactive Host terminals require continuous Host permission"
        );
        if request.tool == "write_stdin" {
            anyhow::ensure!(
                request.args["chars"].as_str().unwrap_or("").is_empty()
                    && request.args["close_stdin"] != true
                    && request.args.get("rows").is_none()
                    && request.args.get("cols").is_none(),
                "Single-operation Host permission allows output polling and cancellation only"
            );
            // The executor still validates session ownership and its original target.
            let book = hub.book.lock().unwrap();
            let denied = book
                .policy
                .machines
                .get(&scope.machine_id)
                .is_some_and(|p| p.host_access == DefaultDecision::Deny);
            return Ok(json!({"request":request,"approved":!denied}));
        }
    }
    if let Some(scope) = &scope {
        let key = WorkKey {
            assistant: s.assistant_id.clone(),
            work: work.clone(),
            scope: scope.clone(),
        };
        if let Some(approved) = Hub::allowed(&hub.book.lock().unwrap(), &key) {
            return Ok(json!({"request":request,"approved":approved}));
        }
    }
    let key = hub.join(request.clone(), &s.assistant_id, &work, scope)?;
    let _waiting = WaitingRequest {
        hub,
        key: key.clone(),
        request: request.clone(),
    };
    loop {
        let changed = hub.changed.notified();
        anyhow::ensure!(live(s, &request), "Permission request cancelled");
        if let Some(approved) = hub.decision(&key) {
            return Ok(json!({"request":request,"approved":approved}));
        }
        tokio::select! { _=changed=>{}, _=s.cancel_notify.notified()=>{}, _=s.stop_requested()=>{}, _=tokio::time::sleep(std::time::Duration::from_millis(200))=>{} }
    }
}
pub(crate) async fn authorize_task(s: &Service) -> Result<()> {
    if let Some(target) = &s.execution_target
        && matches!(target.target, pot_core::ExecutionTargetKind::Host {})
    {
        anyhow::ensure!(
            s.execution_service
                .permissions
                .machine_policy(&target.runtime_id)
                .host_access
                != DefaultDecision::Deny,
            "Host execution is forbidden on this machine"
        );
        // Starting the Agent grants no Host commands. Every operation is reviewed.
        return Ok(());
    }

    Ok(())
}
pub async fn inspect(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if s.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_only",
            "Owner administration required",
        ));
    }
    let book = s.execution_service.permissions.book.lock().unwrap();
    let requests:Vec<_>=book.requests.iter().filter(|p|p.assistant==s.assistant_id && p.decision.is_none()).map(|p|json!({"id":p.id,"request":p.request,"assistant_id":p.assistant,"work_scope_id":p.work,"scope":p.scope})).collect();
    Ok(Json(
        json!({"assistant_id":s.assistant_id,"requests":requests,"policy":book.policy}),
    ))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Decision {
    #[serde(default)]
    id: String,
    action: String,
    #[serde(default)]
    machine_policy: Option<pot_agent::permissions::MachinePolicy>,
    #[serde(default)]
    machine_id: Option<String>,
    #[serde(default)]
    sandbox_limits: Option<pot_agent::permissions::SandboxLimits>,
}
pub async fn decide(
    State(s): State<Service>,
    h: HeaderMap,
    Json(body): Json<Decision>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if s.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_only",
            "Owner administration required",
        ));
    }
    if body.action == "configure" {
        let hub = &s.execution_service.permissions;
        let _mutation = hub.sandbox_mutations.lock().await;
        let machine = body.machine_id.filter(|v| !v.is_empty()).ok_or_else(|| {
            error(
                StatusCode::BAD_REQUEST,
                "machine",
                "Machine identity required",
            )
        })?;
        if machine != s.runtime_id {
            let epoch = crate::remote_execution::port(&s)
                .and_then(|p| p.machine_epoch(&machine))
                .map_err(internal)?;
            hub.sync_machine_epoch(&machine, &epoch).map_err(internal)?;
        }
        if let Some(limits) = &body.sandbox_limits {
            let budget = json!(limits);
            if machine == s.runtime_id {
                s.execution_service
                    .sandboxes
                    .configure_budget(&budget)
                    .map_err(internal)?;
            } else {
                crate::remote_execution::port(&s)
                    .map_err(internal)?
                    .configure_sandbox_budget(machine.clone(), budget)
                    .await
                    .map_err(internal)?;
            }
        }
        let mut book = hub.book.lock().unwrap();
        let mut policy = book.policy.clone();
        if let Some(machine_policy) = body.machine_policy {
            policy.machines.insert(machine.clone(), machine_policy);
            book.temporary.retain(|k, _| k.scope.machine_id != machine);
            for pending in &mut book.requests {
                if pending
                    .scope
                    .as_ref()
                    .is_some_and(|scope| scope.machine_id == machine)
                {
                    pending.decision = Some(false);
                }
            }
        }
        if let Some(limits) = body.sandbox_limits {
            policy.sandbox_limits.insert(machine.clone(), limits);
        }
        hub.save_policy(&mut book, policy).map_err(internal)?;
        return Ok(Json(json!({"ok":true})));
    }
    {
        let requests = s
            .execution_service
            .permissions
            .book
            .lock()
            .unwrap()
            .requests
            .iter()
            .find(|p| p.id == body.id && p.assistant == s.assistant_id)
            .map(|p| p.waiters.clone())
            .unwrap_or_default();
        let valid = requests.iter().any(|request| {
            tasks::owner(&s, &request.owner)
                .ok()
                .is_some_and(|lane| live(&lane, request))
        });
        if !valid {
            return Err(error(
                StatusCode::CONFLICT,
                "permission_decision",
                "Work is no longer active",
            ));
        }
    }
    s.execution_service
        .permissions
        .decide(&s.assistant_id, &body.id, &body.action)
        .map_err(|e| error(StatusCode::CONFLICT, "permission_decision", &e.to_string()))?;
    Ok(Json(json!({"ok":true})))
}

#[cfg(test)]
#[path = "permissions_tests.rs"]
mod tests;
