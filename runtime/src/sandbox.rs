//! Host-owned Sandbox inventory and the only environment execution boundary.
use super::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Sandbox {
    pub id: String,
    pub name: String,
}
#[derive(Clone, Default, Serialize, Deserialize)]
struct Inventory {
    selected: String,
    items: BTreeMap<String, Sandbox>,
    #[serde(default)]
    retired: std::collections::BTreeSet<String>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Pending {
    target: String,
    owner: String,
    #[serde(skip)]
    active: bool,
}
#[derive(Clone)]
struct Scope {
    id: String,
    target: String,
    owner: String,
    turn: String,
    generation: u64,
}
/// In-process Agent calls use the same Runtime authorization as MCP calls.
pub struct Tools {
    pub service: Service,
    pub epoch: u64,
}
impl pot_core::ToolExecutor for Tools {
    fn execute<'a>(
        &'a self,
        _: &'a pot_core::ExecutionContext,
        policy: &'a pot_core::ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(pot_core::Update) -> Result<()> + Send),
    ) -> futures::future::BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            anyhow::ensure!(
                agent::binding(&self.service)?.epoch == self.epoch,
                "Execution binding changed"
            );
            let mut value = tool(&self.service, name, call, args).await?;
            if let Some(events) = value.get_mut("events").and_then(Value::as_array_mut) {
                let mut remaining = policy.max_output_bytes.saturating_mul(2);
                for event in events.drain(..) {
                    emit(guest_output(event, call, &mut remaining)?)?;
                }
            }
            Ok(value.get("result").cloned().unwrap_or(value))
        })
    }
    fn finish<'a>(
        &'a self,
        _: &'a pot_core::ExecutionContext,
    ) -> futures::future::BoxFuture<'a, Result<()>> {
        Box::pin(finish(&self.service))
    }
}
// Guest output cannot manufacture Agent lifecycle/control events. IDs are
// remapped from the execution receipt to the caller's model tool invocation.
fn guest_output(value: Value, call: &str, remaining: &mut usize) -> Result<pot_core::Update> {
    let pot_core::Update::ToolOutput { stream, text, .. } = serde_json::from_value(value)? else {
        bail!("Guest sent a non-output execution event");
    };
    anyhow::ensure!(
        matches!(stream.as_str(), "stdout" | "stderr") && text.len() <= *remaining,
        "Invalid guest output"
    );
    *remaining -= text.len();
    Ok(pot_core::Update::ToolOutput {
        id: call.into(),
        stream,
        text,
    })
}
struct Execution {
    manager: Arc<Manager>,
    owner: String,
}
impl Drop for Execution {
    fn drop(&mut self) {
        *self
            .manager
            .running
            .lock()
            .unwrap()
            .get_mut(&self.owner)
            .unwrap() -= 1;
    }
}
pub struct Manager {
    pub(crate) agent_home: PathBuf,
    pub(crate) global_instructions: Mutex<crate::instructions::GlobalInstructions>,
    pub(crate) skill_user: PathBuf,
    pub(crate) skill_builtin: PathBuf,
    // Per-fixture ephemeral HTTP listeners; production always uses guest port 17701.
    #[cfg(test)]
    pub(crate) test_executor_urls: Mutex<BTreeMap<String, String>>,
    root: PathBuf,
    data: PathBuf,
    pending: Mutex<BTreeMap<String, Pending>>,
    running: Mutex<BTreeMap<String, usize>>,
    book: Mutex<Inventory>,
    committed: Mutex<Inventory>,
    leases: Mutex<BTreeMap<String, usize>>,
    scopes: Mutex<BTreeMap<String, Scope>>,
    sessions: Mutex<BTreeMap<String, String>>,
    lifecycle: tokio::sync::Mutex<()>,
    client: reqwest::Client,
}
pub struct Lease {
    manager: Arc<Manager>,
    id: String,
}
impl Drop for Lease {
    fn drop(&mut self) {
        let mut leases = self.manager.leases.lock().unwrap();
        if let Some(n) = leases.get_mut(&self.id) {
            *n -= 1;
        }
    }
}
impl Manager {
    pub fn open(data: &std::path::Path) -> Result<Self> {
        let root = data.parent().unwrap_or(data).to_path_buf();
        let path = data.join("sandboxes.json");
        let book = if path.exists() {
            serde_json::from_slice(&std::fs::read(path)?)?
        } else {
            let item = Sandbox {
                id: "default".into(),
                name: "Linux".into(),
            };
            Inventory {
                selected: item.id.clone(),
                items: BTreeMap::from([(item.id.clone(), item)]),
                retired: Default::default(),
            }
        };
        #[cfg(test)]
        let agent_home = root.join("agent-home");
        #[cfg(not(test))]
        let agent_home =
            PathBuf::from(std::env::var_os("HOME").unwrap_or_default()).join(".pot/agent");
        #[cfg(test)]
        let skill_user = root.join("skills-user");
        #[cfg(not(test))]
        let skill_user = std::env::var_os("POT_AGENT_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| agent_home.join("skills"));
        let skill_builtin = std::env::var_os("POT_BUILTIN_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../skills"));
        Ok(Self {
            agent_home,
            global_instructions: Mutex::new(Default::default()),
            skill_user,
            skill_builtin,
            #[cfg(test)]
            test_executor_urls: Mutex::new(BTreeMap::new()),
            root,
            data: data.to_path_buf(),
            running: Mutex::new(BTreeMap::new()),
            pending: Mutex::new(if data.join("sandbox-operations.json").exists() {
                serde_json::from_slice(&std::fs::read(data.join("sandbox-operations.json"))?)?
            } else {
                BTreeMap::new()
            }),
            committed: Mutex::new(book.clone()),
            book: Mutex::new(book),
            leases: Mutex::new(BTreeMap::new()),
            scopes: Mutex::new(BTreeMap::new()),
            sessions: Mutex::new(BTreeMap::new()),
            lifecycle: tokio::sync::Mutex::new(()),
            client: reqwest::Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(std::time::Duration::from_secs(660))
                .build()?,
        })
    }
    pub(crate) fn workspace(&self, target: &str) -> PathBuf {
        self.root
            .join("sandboxes")
            .join(target)
            .join("workspace/workspace")
    }
    fn executor_url(&self, ip: &str) -> String {
        #[cfg(test)]
        if let Some(url) = self.test_executor_urls.lock().unwrap().get(ip) {
            return url.clone();
        }
        format!("http://{ip}:17701")
    }
    fn persist(&self, data: &std::path::Path) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        let path = data.join("sandboxes.json");
        if let Err(error) = execution::private_json(&path, &serde_json::to_value(&*book)?) {
            *book = self.committed.lock().unwrap().clone();
            return Err(error);
        }
        *self.committed.lock().unwrap() = book.clone();
        Ok(())
    }
    fn persist_operations(&self) -> Result<()> {
        execution::private_json(
            &self.data.join("sandbox-operations.json"),
            &serde_json::to_value(&*self.pending.lock().unwrap())?,
        )
    }
    fn scope(&self, target: &str, owner: &str, turn: &str, generation: u64) -> Result<Scope> {
        let mut scopes = self.scopes.lock().unwrap();
        if let Some(scope) = scopes
            .values()
            .find(|v| v.owner == owner && v.turn == turn && v.target == target)
        {
            anyhow::ensure!(
                scope.generation == generation,
                "Sandbox generation changed; session outcome unknown"
            );
            return Ok(scope.clone());
        }
        let scope = Scope {
            id: store::id(),
            target: target.into(),
            owner: owner.into(),
            turn: turn.into(),
            generation,
        };
        // Keep initialization serialized through persistence; concurrent calls
        // cannot dispatch into a scope before its durable receipt exists.
        self.pending.lock().unwrap().insert(
            scope.id.clone(),
            Pending {
                target: target.into(),
                owner: owner.into(),
                active: true,
            },
        );
        if let Err(error) = self.persist_operations() {
            self.pending.lock().unwrap().remove(&scope.id);
            return Err(error);
        }
        *self
            .leases
            .lock()
            .unwrap()
            .entry(target.into())
            .or_default() += 1;
        scopes.insert(scope.id.clone(), scope.clone());
        Ok(scope)
    }
    fn settle(&self, operation: &str) -> Result<()> {
        let old = self.pending.lock().unwrap().remove(operation);
        if let Err(error) = self.persist_operations() {
            if let Some(old) = old {
                self.pending.lock().unwrap().insert(operation.into(), old);
            }
            return Err(error);
        }
        Ok(())
    }
    pub async fn acquire(self: &Arc<Self>, id: &str) -> Result<Lease> {
        let _lock = self.lifecycle.lock().await;
        self.lease(id)
    }
    pub fn assignment(&self, requested: &str) -> Result<String> {
        let book = self.book.lock().unwrap();
        let id = if requested.is_empty() {
            &book.selected
        } else {
            requested
        };
        anyhow::ensure!(book.items.contains_key(id), "Unknown Sandbox");
        Ok(id.into())
    }
    pub fn lease(self: &Arc<Self>, id: &str) -> Result<Lease> {
        self.assignment(id)?;
        anyhow::ensure!(
            !self
                .pending
                .lock()
                .unwrap()
                .values()
                .any(|v| v.target == id && !v.active),
            "Sandbox has unsettled execution; recover before resuming"
        );
        *self.leases.lock().unwrap().entry(id.into()).or_default() += 1;
        Ok(Lease {
            manager: self.clone(),
            id: id.into(),
        })
    }
    async fn helper(&self, id: &str, method: &str) -> Result<Value> {
        let mut socket = tokio::net::UnixStream::connect(self.root.join("helper.sock")).await?;
        socket
            .write_all(
                format!(
                    "{}\n",
                    json!({"id":store::id(),"method":method,"sandbox_id":id})
                )
                .as_bytes(),
            )
            .await?;
        let mut line = String::new();
        tokio::time::timeout(
            std::time::Duration::from_secs(30),
            BufReader::new(socket).read_line(&mut line),
        )
        .await??;
        anyhow::ensure!(line.len() <= 65536, "Invalid Helper response");
        let response: Value = serde_json::from_str(&line)?;
        anyhow::ensure!(
            response.get("error").is_none(),
            "Sandbox Helper rejected operation"
        );
        Ok(response["result"].clone())
    }
    async fn ready(&self, id: &str, s: &Service) -> Result<(String, String)> {
        let _lock = self.lifecycle.lock().await;
        let mut state = self.helper(id, "start").await?;
        for _ in 0..1200 {
            anyhow::ensure!(
                !agent::cancelled(s) && !s.stopping(),
                "Sandbox preparation cancelled"
            );
            anyhow::ensure!(
                state["phase"] != "failed",
                "Sandbox startup failed; inspect environment logs"
            );
            if state["phase"] == "running" {
                let ip = state["ip"]
                    .as_str()
                    .ok_or_else(|| anyhow!("Sandbox has no address"))?;
                let token = std::fs::read_to_string(
                    self.root.join("sandboxes").join(id).join("executor-token"),
                )?;
                let url = self.executor_url(ip);
                if self
                    .client
                    .get(format!("{url}/health"))
                    .bearer_auth(token.trim())
                    .timeout(std::time::Duration::from_secs(1))
                    .send()
                    .await
                    .is_ok_and(|r| r.status().is_success())
                {
                    return Ok((url, token.trim().into()));
                }
            }
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
            state = self.helper(id, "status").await?;
        }
        bail!("Sandbox executor did not become ready")
    }
}

pub async fn list(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(inventory(&s).await))
}
// Agent-facing state is not a desktop login interface. Native App discovers
// viewer credentials through its private Helper channel, never these tool/API replies.
fn display_state(state: &Value) -> Value {
    json!({"phase":state["phase"].as_str().unwrap_or("unknown"),"generation":state["generation"].as_u64(),
        "cpus":state["cpus"].as_u64(),"memory_gib":state["memory_gib"].as_u64(),"disk_gib":state["disk_gib"].as_u64()})
}
async fn inventory(s: &Service) -> Value {
    let (items, selected) = {
        let book = s.sandboxes.book.lock().unwrap();
        (
            book.items.values().cloned().collect::<Vec<_>>(),
            book.selected.clone(),
        )
    };
    let task_snapshot = tasks::sandbox_tasks(s);
    let mut rows = vec![];
    for item in items {
        let state = s
            .sandboxes
            .helper(&item.id, "status")
            .await
            .unwrap_or_else(|_| json!({"phase":"unavailable"}));
        let active = *s
            .sandboxes
            .leases
            .lock()
            .unwrap()
            .get(&item.id)
            .unwrap_or(&0);
        let assigned: Vec<_> = task_snapshot
            .iter()
            .filter(|task| task.sandbox_id == item.id)
            .collect();
        let config = std::fs::read(
            s.sandboxes
                .root
                .join("sandboxes")
                .join(&item.id)
                .join("sandbox.json"),
        )
        .ok()
        .and_then(|bytes| serde_json::from_slice::<Value>(&bytes).ok())
        .unwrap_or(json!({"cpus":4,"memory_gib":8,"disk_gib":32}));
        rows.push(json!({"running_tasks":assigned.iter().filter(|task| task.phase == TaskPhase::Running).count(),"tasks":assigned,"config":config,"referenced":tasks::uses_sandbox(s,&item.id),"id":item.id,"name":item.name,"os":"linux","active_leases":active,"unsettled_operations":s.sandboxes.pending.lock().unwrap().values().filter(|v| v.target == item.id).count(),"state":display_state(&state)}));
    }
    json!({"selected":selected,"items":rows})
}
pub async fn manage(
    State(s): State<Service>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(management(&s, &args).await.map_err(|e| {
        error(StatusCode::CONFLICT, "sandbox_management", &e.to_string())
    })?))
}
pub async fn management(s: &Service, args: &Value) -> Result<Value> {
    anyhow::ensure!(!s.is_worker, "Workers cannot manage Sandboxes");
    if args["action"] == "desktop" {
        let id = s
            .sandboxes
            .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
        let _lease = s.sandboxes.acquire(&id).await?;
        let (url, token) = s.sandboxes.ready(&id, s).await?;
        s.sandboxes
            .client
            .post(format!("{url}/desktop"))
            .bearer_auth(token)
            .json(&json!({}))
            .send()
            .await?
            .error_for_status()?;
        return Ok(
            json!({"sandbox_id":id,"state":display_state(&s.sandboxes.helper(&id,"status").await?)}),
        );
    }
    let action = args["action"].as_str().unwrap_or("");
    if action == "start" {
        let id = s
            .sandboxes
            .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
        let _lease = s.sandboxes.acquire(&id).await?;
        s.sandboxes.ready(&id, s).await?;
        return Ok(
            json!({"sandbox_id":id,"state":display_state(&s.sandboxes.helper(&id,"status").await?)}),
        );
    }
    let _lock = s.sandboxes.lifecycle.lock().await;
    if action == "create" {
        let id = args["sandbox_id"].as_str().unwrap_or("");
        anyhow::ensure!(
            !id.is_empty()
                && id.len() <= 48
                && id
                    .bytes()
                    .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-'),
            "Invalid Sandbox ID"
        );
        let name = args["name"].as_str().unwrap_or(id).trim();
        anyhow::ensure!(
            !name.is_empty() && name.chars().count() <= 80,
            "Name must contain 1–80 characters"
        );
        {
            let mut book = s.sandboxes.book.lock().unwrap();
            anyhow::ensure!(
                !book.retired.contains(id),
                "Deleted Sandbox IDs cannot be reused"
            );
            anyhow::ensure!(book.items.len() < 16, "Sandbox limit reached");
            if let Some(existing) = book.items.get(id) {
                anyhow::ensure!(existing.name == name, "Sandbox ID already exists");
            } else {
                book.items.insert(
                    id.into(),
                    Sandbox {
                        id: id.into(),
                        name: name.into(),
                    },
                );
            }
        }
        s.sandboxes.persist(&s.data)?;
        return Ok(json!({"sandbox_id":id,"phase":"stopped"}));
    }
    let id = s
        .sandboxes
        .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
    if action == "update" {
        let name = args["name"]
            .as_str()
            .ok_or_else(|| anyhow!("Name required"))?
            .trim();
        anyhow::ensure!(
            !name.is_empty() && name.chars().count() <= 80,
            "Name must contain 1–80 characters"
        );
        let config = &args["config"];
        let c = config["cpus"].as_u64().unwrap_or(0);
        let m = config["memory_gib"].as_u64().unwrap_or(0);
        let d = config["disk_gib"].as_u64().unwrap_or(0);
        anyhow::ensure!(
            (1..=32).contains(&c) && (1..=128).contains(&m) && (4..=512).contains(&d),
            "Invalid Sandbox resources"
        );
        anyhow::ensure!(
            *s.sandboxes.leases.lock().unwrap().get(&id).unwrap_or(&0) == 0
                && !s
                    .sandboxes
                    .pending
                    .lock()
                    .unwrap()
                    .values()
                    .any(|v| v.target == id),
            "Sandbox busy; finish or recover operations first"
        );
        let state = s.sandboxes.helper(&id, "status").await?;
        anyhow::ensure!(
            state["phase"] == "stopped" || state["phase"] == "failed",
            "Stop the environment before changing settings"
        );
        let path = s.sandboxes.root.join("sandboxes").join(&id);
        std::fs::create_dir_all(&path)?;
        execution::private_json(
            &path.join("sandbox.json"),
            &json!({"cpus":c,"memory_gib":m,"disk_gib":d}),
        )?;
        s.sandboxes
            .book
            .lock()
            .unwrap()
            .items
            .get_mut(&id)
            .unwrap()
            .name = name.into();
        s.sandboxes.persist(&s.data)?;
        return Ok(json!({"sandbox_id":id}));
    }
    if action == "recover" {
        anyhow::ensure!(
            *s.sandboxes.leases.lock().unwrap().get(&id).unwrap_or(&0) == 0,
            "Sandbox busy"
        );
        let state = s.sandboxes.helper(&id, "status").await?;
        let pending: Vec<_> = s
            .sandboxes
            .pending
            .lock()
            .unwrap()
            .iter()
            .filter(|(_, t)| t.target == id)
            .map(|(op, _)| op.clone())
            .collect();
        for op in &pending {
            if state["phase"] != "stopped" && state["phase"] != "failed" {
                let ip = state["ip"]
                    .as_str()
                    .ok_or_else(|| anyhow!("Executor unavailable; cleanup unconfirmed"))?;
                let token = std::fs::read_to_string(
                    s.sandboxes
                        .root
                        .join("sandboxes")
                        .join(&id)
                        .join("executor-token"),
                )?;
                let result: Value = s
                    .sandboxes
                    .client
                    .post(format!("{}/cancel", s.sandboxes.executor_url(ip)))
                    .bearer_auth(token.trim())
                    .json(&json!({"id":op}))
                    .timeout(std::time::Duration::from_secs(15))
                    .send()
                    .await?
                    .error_for_status()?
                    .json()
                    .await?;
                anyhow::ensure!(result["settled"] == true, "Executor cleanup unconfirmed");
            }
            s.sandboxes.settle(op)?;
        }
        return Ok(
            json!({"sandbox_id":id,"reconciled_operations":pending,"prior_results":"unknown; commands were not replayed"}),
        );
    }
    if action == "select" {
        s.sandboxes.book.lock().unwrap().selected = id.clone();
        s.sandboxes.persist(&s.data)?;
        return Ok(json!({"selected":id}));
    }
    anyhow::ensure!(
        ["start", "stop", "delete"].contains(&action),
        "Unknown Sandbox action"
    );
    if action != "start" {
        let active = *s.sandboxes.leases.lock().unwrap().get(&id).unwrap_or(&0);
        anyhow::ensure!(
            active == 0,
            "Sandbox busy: {active} active task/operation leases; inspect inventory before stopping"
        );
        anyhow::ensure!(
            !s.sandboxes
                .pending
                .lock()
                .unwrap()
                .values()
                .any(|v| v.target == id),
            "Sandbox has unsettled operations; recover first"
        );
    }
    if action == "delete" {
        anyhow::ensure!(id != "default", "The default Sandbox cannot be deleted");
        anyhow::ensure!(
            !tasks::uses_sandbox(s, &id),
            "Sandbox is assigned to a persisted task or schedule"
        );
    }
    let state = s
        .sandboxes
        .helper(&id, if action == "delete" { "stop" } else { action })
        .await?;
    if action == "delete" {
        tasks::guard_sandbox_delete(s, &id, || {
            let mut book = s.sandboxes.book.lock().unwrap();
            book.items.remove(&id);
            book.retired.insert(id.clone());
            if book.selected == id {
                book.selected = "default".into();
            }
            drop(book);
            s.sandboxes.persist(&s.data)
        })?;
    }
    Ok(json!({"sandbox_id":id,"state":display_state(&state)}))
}

pub async fn tool(s: &Service, name: &str, call_id: &str, args: &Value) -> Result<Value> {
    anyhow::ensure!(
        !call_id.is_empty() && call_id.len() <= 256,
        "Invalid execution call ID"
    );
    let turn = {
        let inner = s.inner.lock().unwrap();
        let turn = inner
            .turn
            .as_ref()
            .filter(|t| !t.cancelling)
            .ok_or_else(|| anyhow!("No active execution turn"))?;
        inner
            .store
            .message(&turn.user_message_id)?
            .and_then(|m| m.request_id)
            .ok_or_else(|| anyhow!("Turn identity missing"))?
    };
    anyhow::ensure!(
        !turn.starts_with(orchestration::RESULT_PREFIX),
        "Summary is read-only"
    );
    if name == "pot_list_sandboxes" {
        anyhow::ensure!(!s.is_worker, "Workers cannot list Sandbox inventory");
        return Ok(inventory(s).await);
    }
    if name == "pot_manage_sandbox" {
        return management(s, args).await;
    }
    anyhow::ensure!(
        ["exec_command", "write_stdin", "apply_patch"].contains(&name),
        "Unknown execution operation"
    );
    let binding = agent::binding(s)?;
    let settings = &binding.spec.params.agent;
    anyhow::ensure!(
        binding.spec.engine != agent::Engine::Pot
            || (settings.builtin_tools.iter().any(|n| n == name)
                && settings
                    .allowed_tools
                    .as_ref()
                    .is_none_or(|names| names.iter().any(|n| n == name))),
        "Tool disabled by execution policy"
    );
    let mut validated = args.clone();
    if let Some(object) = validated.as_object_mut() {
        object.remove("sandbox_id");
    }
    pot_core::environment_tools::validate(name, &validated)?;

    let receipt = format!("sandbox.operation:{turn}:{call_id}");
    if let Some(old) = s.inner.lock().unwrap().store.get(&receipt)? {
        let old: Value = serde_json::from_str(&old)?;
        anyhow::ensure!(
            old["name"] == name && old["args"] == *args,
            "Execution ID reused with different arguments"
        );
        if old["status"] == "complete" {
            return Ok(old["result"].clone());
        }
        bail!("Prior execution outcome unknown; inspect before retrying with a new call ID");
    }
    anyhow::ensure!(
        args.get("sandbox_id").is_none_or(Value::is_string),
        "Invalid Sandbox identity"
    );
    let requested = args["sandbox_id"].as_str().unwrap_or("");
    let prior_scope = if name == "write_stdin" {
        anyhow::ensure!(requested.is_empty(), "write_stdin cannot switch Sandbox");
        let session = args["session_id"]
            .as_str()
            .ok_or_else(|| anyhow!("Missing session ID"))?;
        let id = s
            .sandboxes
            .sessions
            .lock()
            .unwrap()
            .get(session)
            .cloned()
            .ok_or_else(|| anyhow!("Session expired or belongs to another turn"))?;
        let scope = s
            .sandboxes
            .scopes
            .lock()
            .unwrap()
            .get(&id)
            .cloned()
            .ok_or_else(|| anyhow!("Session scope expired"))?;
        anyhow::ensure!(
            scope.owner == s.usage_scope && scope.turn == turn,
            "Session belongs to another task/turn"
        );
        Some(scope)
    } else {
        None
    };
    let target = if let Some(scope) = &prior_scope {
        scope.target.clone()
    } else if s.is_worker {
        let assigned = s
            .sandbox_id
            .as_deref()
            .ok_or_else(|| anyhow!("Worker has no Sandbox assignment"))?;
        anyhow::ensure!(
            requested.is_empty() || requested == assigned,
            "Worker cannot switch Sandbox"
        );
        assigned.to_owned()
    } else {
        s.sandboxes.assignment(requested)?
    };
    *s.sandboxes
        .running
        .lock()
        .unwrap()
        .entry(s.usage_scope.clone())
        .or_default() += 1;
    let _execution = Execution {
        manager: s.sandboxes.clone(),
        owner: s.usage_scope.clone(),
    };
    let _preparation = s.sandboxes.acquire(&target).await?;
    let (url, token) = s.sandboxes.ready(&target, s).await?;
    let generation = s.sandboxes.helper(&target, "status").await?["generation"]
        .as_u64()
        .unwrap_or(0);
    anyhow::ensure!(
        turn_alive(s, &turn) && !s.stopping(),
        "Execution turn ended during Sandbox preparation"
    );
    let scope = s
        .sandboxes
        .scope(&target, &s.usage_scope, &turn, generation)?;
    let mut record =
        json!({"id":store::id(),"target":target,"name":name,"args":args,"status":"accepted"});
    s.inner
        .lock()
        .unwrap()
        .store
        .set(&receipt, &record.to_string())?;
    let cwd = if s.is_worker {
        format!("/data/workspace/tasks/{}", s.usage_scope)
    } else {
        "/data/workspace".into()
    };
    let mut tool_args = args.clone();
    if let Some(object) = tool_args.as_object_mut() {
        object.remove("sandbox_id");
    }
    let response=s.sandboxes.client.post(format!("{url}/execute")).bearer_auth(&token)
        .json(&json!({"id":record["id"],"scope":scope.id,"operation":name,"args":tool_args,"cwd":cwd,"role":if s.is_worker {"task"} else {"main"},"policy":agent::binding(s)?.spec.params.agent.shell})).send();
    tokio::pin!(response);
    let result = loop {
        tokio::select! {
            result=&mut response=>break result?,
            _=tokio::time::sleep(std::time::Duration::from_millis(100))=>{
                if !turn_alive(s,&turn) || s.stopping() {
                    cancel_scope(s,&scope).await?;
                    let result=json!({"result":{"sandbox_id":target,"cancelled":true},"events":[]});
                    record["status"]=json!("complete");record["result"]=result.clone();s.inner.lock().unwrap().store.set(&receipt,&record.to_string())?;
                    return Ok(result);
                }
            }
        }
    };
    anyhow::ensure!(
        result.status().is_success(),
        "Sandbox execution request failed"
    );
    let mut result: Value = result.json().await?;
    if let Some(session) = result["result"]["session_id"].as_str() {
        s.sandboxes
            .sessions
            .lock()
            .unwrap()
            .insert(session.to_owned(), scope.id.clone());
    }
    result["result"]["sandbox_id"] = json!(target);
    record["status"] = json!("complete");
    record["result"] = result.clone();
    s.inner
        .lock()
        .unwrap()
        .store
        .set(&receipt, &record.to_string())?;
    Ok(result)
}
async fn cancel_scope(s: &Service, scope: &Scope) -> Result<()> {
    let state = s.sandboxes.helper(&scope.target, "status").await?;
    if state["phase"] != "stopped" && state["phase"] != "failed" {
        let ip = state["ip"]
            .as_str()
            .ok_or_else(|| anyhow!("Executor cleanup stop unconfirmed"))?;
        let token = std::fs::read_to_string(
            s.sandboxes
                .root
                .join("sandboxes")
                .join(&scope.target)
                .join("executor-token"),
        )?;
        let response: Value = s
            .sandboxes
            .client
            .post(format!("{}/finish", s.sandboxes.executor_url(ip)))
            .bearer_auth(token.trim())
            .json(&json!({"id":scope.id}))
            .timeout(std::time::Duration::from_secs(15))
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;
        anyhow::ensure!(
            response["settled"] == true,
            "Executor cleanup stop unconfirmed"
        );
    }
    s.sandboxes.settle(&scope.id)?;
    release_scope(&s.sandboxes, scope);
    Ok(())
}
fn release_scope(manager: &Manager, scope: &Scope) {
    if manager.scopes.lock().unwrap().remove(&scope.id).is_some() {
        *manager
            .leases
            .lock()
            .unwrap()
            .get_mut(&scope.target)
            .unwrap() -= 1;
    }
    manager
        .sessions
        .lock()
        .unwrap()
        .retain(|_, id| id != &scope.id);
}
pub async fn finish(s: &Service) -> Result<()> {
    let mut errors = vec![];
    for _ in 0..200 {
        let scopes: Vec<_> = s
            .sandboxes
            .scopes
            .lock()
            .unwrap()
            .values()
            .filter(|v| v.owner == s.usage_scope)
            .cloned()
            .collect();
        for scope in scopes {
            if let Err(error) = cancel_scope(s, &scope).await {
                if let Some(pending) = s.sandboxes.pending.lock().unwrap().get_mut(&scope.id) {
                    pending.active = false;
                }
                // Durable unknown outcomes still quarantine the environment;
                // release transient leases so explicit recovery can settle it.
                release_scope(&s.sandboxes, &scope);
                errors.push(error.to_string());
            }
        }
        s.sandboxes.persist_operations()?;
        if *s
            .sandboxes
            .running
            .lock()
            .unwrap()
            .get(&s.usage_scope)
            .unwrap_or(&0)
            == 0
        {
            anyhow::ensure!(
                errors.is_empty(),
                "Sandbox cleanup stop unconfirmed: {}",
                errors.join("; ")
            );
            return Ok(());
        }
        // Preparation is counted too. Re-snapshot after it settles so a late
        // scope cannot escape the cleanup barrier at cancellation/turn end.
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    bail!("Sandbox execution cleanup timed out; stop unconfirmed")
}
pub async fn execute(
    State(root): State<Service>,
    h: HeaderMap,
    Json(call): Json<Value>,
) -> ApiResult<Json<Value>> {
    let s = tasks::execution_service(&root, &h)?;
    let operation = tokio::spawn(async move {
        tool(
            &s,
            call["name"].as_str().unwrap_or(""),
            call["call_id"].as_str().unwrap_or(""),
            &call["args"],
        )
        .await
    });
    Ok(Json(
        operation
            .await
            .map_err(|e| internal(e.into()))?
            .map_err(|e| error(StatusCode::CONFLICT, "sandbox_execution", &e.to_string()))?,
    ))
}

fn turn_alive(s: &Service, request: &str) -> bool {
    let inner = s.inner.lock().unwrap();
    inner
        .turn
        .as_ref()
        .filter(|t| !t.cancelling)
        .and_then(|t| inner.store.message(&t.user_message_id).ok().flatten())
        .is_some_and(|m| m.request_id.as_deref() == Some(request))
}
pub async fn drain(s: &Service) -> Result<()> {
    for _ in 0..200 {
        if *s
            .sandboxes
            .running
            .lock()
            .unwrap()
            .get(&s.usage_scope)
            .unwrap_or(&0)
            == 0
        {
            anyhow::ensure!(
                !s.sandboxes
                    .pending
                    .lock()
                    .unwrap()
                    .values()
                    .any(|v| v.owner == s.usage_scope),
                "Sandbox execution outcome unknown; stop unconfirmed, recover environment before continuing"
            );
            return Ok(());
        }
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    }
    bail!("Sandbox operation cleanup timed out; stop unconfirmed")
}

#[cfg(test)]
mod tests {
    use super::*;
    fn active(s: &Service) {
        s.save(Message {
            id: "input".into(),
            conversation_id: s.usage_scope.clone(),
            role: Role::User,
            text: "fixture".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some("turn".into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "input".into(),
            cancelling: false,
        }));
    }
    #[tokio::test]
    async fn main_inventory_selection_persists_workers_cannot_manage_or_retarget() {
        let s = tasks::tests::fixture();
        management(
            &s,
            &json!({"action":"create","sandbox_id":"linux-b","name":"Second Linux"}),
        )
        .await
        .unwrap();
        management(&s, &json!({"action":"select","sandbox_id":"linux-b"}))
            .await
            .unwrap();
        assert_eq!(
            Manager::open(&s.data).unwrap().assignment("").unwrap(),
            "linux-b"
        );
        assert!(
            management(&s, &json!({"action":"create","sandbox_id":"Linux-B"}))
                .await
                .is_err()
        );
        assert!(!s.data.parent().unwrap().join("helper.sock").exists());
        let mut worker = s.clone();
        worker.is_worker = true;
        worker.sandbox_id = Some("default".into());
        active(&worker);
        assert!(
            management(&worker, &json!({"action":"create","sandbox_id":"escape"}))
                .await
                .is_err()
        );
        assert!(
            tool(&worker, "pot_list_sandboxes", "list", &json!({}))
                .await
                .is_err()
        );
        assert!(
            tool(
                &worker,
                "exec_command",
                "switch",
                &json!({"cmd":"true","sandbox_id":"linux-b"})
            )
            .await
            .unwrap_err()
            .to_string()
            .contains("cannot switch")
        );
        let tools = tool_bridge::schemas(false, true);
        assert_eq!(tools.len(), 3);
        assert!(
            tools
                .iter()
                .all(|t| t["name"].as_str().unwrap() == "exec_command"
                    || t["name"] == "write_stdin"
                    || t["name"] == "apply_patch")
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn settings_validate_before_io_and_refuse_busy_environment() {
        let s = tasks::tests::fixture();
        let args = json!({"action":"update","sandbox_id":"default","name":"Linux","config":{"cpus":2,"memory_gib":2,"disk_gib":8}});
        let mut invalid = args.clone();
        invalid["config"]["cpus"] = json!(0);
        assert!(
            management(&s, &invalid)
                .await
                .unwrap_err()
                .to_string()
                .contains("resources")
        );
        invalid = args.clone();
        invalid["name"] = json!("   ");
        assert!(
            management(&s, &invalid)
                .await
                .unwrap_err()
                .to_string()
                .contains("Name")
        );
        let lease = s.sandboxes.acquire("default").await.unwrap();
        assert!(
            management(&s, &args)
                .await
                .unwrap_err()
                .to_string()
                .contains("busy")
        );
        assert!(
            !s.sandboxes
                .root
                .join("sandboxes/default/sandbox.json")
                .exists()
        );
        let mut worker = s.clone();
        worker.is_worker = true;
        assert!(
            management(&worker, &args)
                .await
                .unwrap_err()
                .to_string()
                .contains("Workers")
        );
        drop(lease);
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn no_host_fallback_and_unknown_outcomes_block_stop_and_survive_restart() {
        let s = tasks::tests::fixture();
        active(&s);
        let result = tool(
            &s,
            "apply_patch",
            "write",
            &json!({"patch":"*** Begin Patch\n*** Add File: host-sentinel\n+bad\n*** End Patch"}),
        )
        .await;
        assert!(result.is_err());
        assert!(!s.workspace.join("host-sentinel").exists());
        s.sandboxes.pending.lock().unwrap().insert(
            "unknown-operation".into(),
            Pending {
                target: "default".into(),
                owner: "main".into(),
                active: false,
            },
        );
        s.sandboxes.persist_operations().unwrap();
        let reopened = Arc::new(Manager::open(&s.data).unwrap());
        assert!(reopened.acquire("default").await.is_err());
        assert!(
            management(&s, &json!({"action":"stop","sandbox_id":"default"}))
                .await
                .unwrap_err()
                .to_string()
                .contains("unsettled")
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn unknown_remote_operation_blocks_even_existing_worker_lease_and_failed_save_rolls_back()
    {
        let s = tasks::tests::fixture();
        let lease = s.sandboxes.acquire("default").await.unwrap();
        s.sandboxes.pending.lock().unwrap().insert(
            "op".into(),
            Pending {
                target: "default".into(),
                owner: "worker".into(),
                active: true,
            },
        );

        assert!(s.sandboxes.acquire("default").await.is_ok());
        s.sandboxes
            .pending
            .lock()
            .unwrap()
            .get_mut("op")
            .unwrap()
            .active = false;
        assert!(s.sandboxes.acquire("default").await.is_err());
        drop(lease);
        s.sandboxes.pending.lock().unwrap().clear();
        std::fs::create_dir(s.data.join("sandboxes.json")).unwrap();
        assert!(
            management(&s, &json!({"action":"create","sandbox_id":"unsaved"}))
                .await
                .is_err()
        );
        assert!(s.sandboxes.assignment("unsaved").is_err());
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn command_sessions_reject_foreign_owners_turns_overrides_and_expired_ids() {
        let s = tasks::tests::fixture();
        active(&s);
        let scope = Scope {
            id: "scope".into(),
            target: "default".into(),
            owner: "other-task".into(),
            turn: "turn".into(),
            generation: 1,
        };
        s.sandboxes
            .scopes
            .lock()
            .unwrap()
            .insert(scope.id.clone(), scope.clone());
        s.sandboxes
            .sessions
            .lock()
            .unwrap()
            .insert("session".into(), scope.id.clone());
        for args in [
            json!({"session_id":"session"}),
            json!({"session_id":"session","sandbox_id":"default"}),
            json!({"session_id":"expired"}),
        ] {
            let error = tool(&s, "write_stdin", "call", &args)
                .await
                .unwrap_err()
                .to_string();
            assert!(
                error.contains("another task/turn")
                    || error.contains("cannot switch")
                    || error.contains("expired"),
                "{error}"
            );
        }
        let mut prior = scope.clone();
        prior.owner = s.usage_scope.clone();
        prior.turn = "prior-turn".into();
        s.sandboxes
            .scopes
            .lock()
            .unwrap()
            .insert(prior.id.clone(), prior);
        assert!(
            tool(&s, "write_stdin", "call", &json!({"session_id":"session"}))
                .await
                .unwrap_err()
                .to_string()
                .contains("another task/turn")
        );
        assert!(!s.data.parent().unwrap().join("helper.sock").exists());
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
    #[test]
    fn guest_events_cannot_forge_agent_lifecycle_and_use_model_call_identity() {
        let mut remaining = 8;
        assert!(guest_output(json!({"TurnFinished":"Success"}), "model", &mut remaining).is_err());
        let output = guest_output(
            serde_json::to_value(pot_core::Update::ToolOutput {
                id: "guest-receipt".into(),
                stream: "stdout".into(),
                text: "hello".into(),
            })
            .unwrap(),
            "model",
            &mut remaining,
        )
        .unwrap();
        assert!(matches!(output,pot_core::Update::ToolOutput{id,..} if id=="model"));
        assert_eq!(remaining, 3);
        assert!(
            guest_output(
                serde_json::to_value(pot_core::Update::ToolOutput {
                    id: "guest".into(),
                    stream: "stdout".into(),
                    text: "oversized".into()
                })
                .unwrap(),
                "model",
                &mut remaining
            )
            .is_err()
        );
    }
    #[test]
    fn concurrent_calls_share_one_durably_initialized_scope_and_generation() {
        let s = tasks::tests::fixture();
        let barrier = Arc::new(std::sync::Barrier::new(8));
        let threads: Vec<_> = (0..8)
            .map(|_| {
                let manager = s.sandboxes.clone();
                let barrier = barrier.clone();
                std::thread::spawn(move || {
                    barrier.wait();
                    manager.scope("default", "main", "turn", 1).unwrap().id
                })
            })
            .collect();
        let ids: std::collections::BTreeSet<_> =
            threads.into_iter().map(|t| t.join().unwrap()).collect();
        assert_eq!(ids.len(), 1);
        assert_eq!(s.sandboxes.pending.lock().unwrap().len(), 1);
        assert_eq!(s.sandboxes.leases.lock().unwrap()["default"], 1);
        assert!(s.sandboxes.scope("default", "main", "turn", 2).is_err());
        assert_eq!(
            Manager::open(&s.data)
                .unwrap()
                .pending
                .lock()
                .unwrap()
                .len(),
            1
        );
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
}
