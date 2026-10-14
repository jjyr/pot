mod desktop;
type DeleteGuard<'a> = dyn Fn(&str, &dyn Fn() -> Result<()>) -> Result<()> + Send + Sync + 'a;
use crate::{Provider, ProviderContext, private_json};
use anyhow::{Result, anyhow, bail};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::PathBuf,
    sync::{Arc, Mutex},
    time::Duration,
};
fn id() -> String {
    uuid::Uuid::new_v4().to_string()
}
/// Product-authorized execution context, never accepted from model arguments.
pub struct AccessGrant {
    pub workspace_owner: Option<String>,
    pub owner: String,
    pub turn: String,
    pub thread_generation: u64,
    pub target: String,
    pub worker: bool,
    pub policy: pot_core::ShellSettings,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Sandbox {
    pub id: String,
    pub name: String,
    pub provider: String,
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
    thread_generation: u64,
    parked: bool,
    generation: u64,
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
    pub changed: pot_core::changes::Signal,
    desktop_io: tokio::sync::Mutex<()>,
    pub(crate) viewers: Mutex<BTreeMap<String, crate::desktop::Viewer>>,
    root: PathBuf,
    data: PathBuf,
    pending: Mutex<BTreeMap<String, Pending>>,
    running: Mutex<BTreeMap<String, usize>>,
    book: Mutex<Inventory>,
    committed: Mutex<Inventory>,
    leases: Mutex<BTreeMap<String, usize>>,
    scopes: Mutex<BTreeMap<String, Scope>>,
    gates: Mutex<BTreeMap<String, Arc<tokio::sync::RwLock<()>>>>,
    sessions: Mutex<BTreeMap<String, String>>,
    lifecycle: tokio::sync::Mutex<()>,
    providers: Mutex<BTreeMap<String, Arc<dyn Provider>>>,
    default_provider: String,
    call_locks: Mutex<BTreeMap<String, Arc<tokio::sync::Mutex<()>>>>,
    instance_locks: Mutex<BTreeMap<String, Arc<tokio::sync::Mutex<()>>>>,
    generations: Mutex<BTreeMap<String, (u64, Option<u64>)>>,
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
    pub fn data_dir(&self) -> &std::path::Path {
        &self.data
    }
    pub fn open(data: &std::path::Path, providers: Vec<Arc<dyn Provider>>) -> Result<Self> {
        Self::open_inventory(data, providers, true)
    }
    /// Resource-driven callers restore definitions but never invent a default instance.
    pub fn open_empty(data: &std::path::Path, providers: Vec<Arc<dyn Provider>>) -> Result<Self> {
        Self::open_inventory(data, providers, false)
    }
    fn open_inventory(
        data: &std::path::Path,
        providers: Vec<Arc<dyn Provider>>,
        seed_default: bool,
    ) -> Result<Self> {
        let default_provider = providers
            .first()
            .map(|p| p.descriptor().id)
            .unwrap_or_else(crate::provider::default_provider);
        let mut registered = BTreeMap::new();
        for provider in providers {
            let name = provider.descriptor().id;
            anyhow::ensure!(
                registered.insert(name, provider).is_none(),
                "Duplicate Sandbox Provider"
            );
        }
        std::fs::create_dir_all(data)?;
        let root = data.parent().unwrap_or(data).to_path_buf();
        let path = data.join("sandboxes.json");
        let book = if path.exists() {
            serde_json::from_slice(&std::fs::read(path)?)?
        } else if seed_default {
            let item = Sandbox {
                id: "default".into(),
                name: "Linux".into(),
                provider: default_provider.clone(),
            };
            Inventory {
                selected: item.id.clone(),
                items: BTreeMap::from([(item.id.clone(), item)]),
                retired: Default::default(),
            }
        } else {
            Inventory {
                selected: String::new(),
                items: BTreeMap::new(),
                retired: Default::default(),
            }
        };
        let generation_path = data.join("sandbox-generations.json");
        let mut generations: BTreeMap<String, (u64, Option<u64>)> = if generation_path.exists() {
            serde_json::from_slice(&std::fs::read(&generation_path)?)?
        } else {
            BTreeMap::new()
        };
        for (epoch, observed) in generations.values_mut() {
            *epoch = epoch
                .checked_add(1)
                .ok_or_else(|| anyhow!("Sandbox epoch exhausted"))?;
            *observed = None;
        }
        private_json(&generation_path, &serde_json::to_value(&generations)?)?;
        Ok(Self {
            changed: Default::default(),
            desktop_io: tokio::sync::Mutex::new(()),
            viewers: Mutex::new(BTreeMap::new()),
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
            gates: Mutex::new(BTreeMap::new()),
            sessions: Mutex::new(BTreeMap::new()),
            lifecycle: tokio::sync::Mutex::new(()),
            providers: Mutex::new(registered),
            default_provider,
            call_locks: Mutex::new(BTreeMap::new()),
            instance_locks: Mutex::new(BTreeMap::new()),
            generations: Mutex::new(generations),
        })
    }
    /// Register installed code independently from instance definitions.
    pub fn register_provider(&self, provider: Arc<dyn Provider>) {
        let name = provider.descriptor().id;
        self.providers
            .lock()
            .unwrap()
            .entry(name)
            .or_insert(provider);
    }
    fn provider(&self, target: &str) -> Result<Arc<dyn Provider>> {
        let book = self.book.lock().unwrap();
        let definition = book
            .items
            .get(target)
            .ok_or_else(|| anyhow!("Unknown Sandbox"))?;
        self.providers
            .lock()
            .unwrap()
            .get(&definition.provider)
            .cloned()
            .ok_or_else(|| anyhow!("Sandbox Provider unavailable"))
    }
    fn context(&self, target: &str) -> ProviderContext {
        ProviderContext {
            root: self.root.clone(),
            sandbox_id: target.into(),
        }
    }
    pub fn bind_resources(&self, target: &str, resources: &crate::PreparedResources) -> Result<()> {
        self.provider(target)?
            .bind_resources(&self.context(target), resources)
    }
    pub fn workspace(&self, target: &str) -> PathBuf {
        self.provider(target)
            .map(|p| p.workspace(&self.root, target))
            .unwrap_or_else(|_| {
                self.root
                    .join("sandboxes")
                    .join(target)
                    .join("workspace/workspace")
            })
    }
    /// Inspect creation defaults and inventory without starting a provider.
    pub fn creation_config(&self, provider: Option<&str>) -> Result<Value> {
        self.providers
            .lock()
            .unwrap()
            .get(provider.unwrap_or(&self.default_provider))
            .map(|provider| provider.default_config())
            .ok_or_else(|| anyhow!("Sandbox Provider unavailable"))
    }
    pub fn sandbox_count(&self) -> usize {
        self.book.lock().unwrap().items.len()
    }
    pub fn describe(&self, target: &str) -> Result<crate::ProviderDescriptor> {
        Ok(self.provider(target)?.descriptor())
    }
    fn persist(&self, data: &std::path::Path) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        let path = data.join("sandboxes.json");
        if let Err(error) = private_json(&path, &serde_json::to_value(&*book)?) {
            *book = self.committed.lock().unwrap().clone();
            return Err(error);
        }
        *self.committed.lock().unwrap() = book.clone();
        self.changed.notify();
        Ok(())
    }
    fn persist_operations(&self) -> Result<()> {
        private_json(
            &self.data.join("sandbox-operations.json"),
            &serde_json::to_value(&*self.pending.lock().unwrap())?,
        )
    }
    fn gate(&self, owner: &str) -> Arc<tokio::sync::RwLock<()>> {
        self.gates
            .lock()
            .unwrap()
            .entry(owner.into())
            .or_default()
            .clone()
    }
    fn scope(&self, target: &str, owner: &str, epoch: u64, generation: u64) -> Result<Scope> {
        let mut scopes = self.scopes.lock().unwrap();
        if let Some(scope) = scopes
            .values()
            .find(|v| v.owner == owner && v.thread_generation == epoch && v.target == target)
        {
            anyhow::ensure!(
                scope.generation == generation,
                "Sandbox generation changed; session outcome unknown"
            );
            let id = scope.id.clone();
            if scope.parked {
                self.pending.lock().unwrap().insert(
                    id.clone(),
                    Pending {
                        target: target.into(),
                        owner: owner.into(),
                        active: true,
                    },
                );
                if let Err(error) = self.persist_operations() {
                    self.pending.lock().unwrap().remove(&id);
                    return Err(error);
                }
                *self
                    .leases
                    .lock()
                    .unwrap()
                    .entry(target.into())
                    .or_default() += 1;
                scopes.get_mut(&id).unwrap().parked = false;
            }
            return Ok(scopes[&id].clone());
        }
        if !self.describe(target)?.capabilities.shared_execution {
            anyhow::ensure!(
                !scopes.values().any(|scope| scope.target == target),
                "Sandbox requires exclusive execution ownership"
            );
        }
        let scope = Scope {
            id: id(),
            target: target.into(),
            owner: owner.into(),
            thread_generation: epoch,
            parked: false,
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
    pub fn guest_workspace(&self, target: &str) -> Result<String> {
        Ok(self.provider(target)?.guest_workspace().into())
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
    async fn helper(&self, target: &str, method: &str) -> Result<Value> {
        let mut state = self
            .provider(target)?
            .lifecycle(&self.context(target), method)
            .await?;
        if let Some(observed) = state["generation"].as_u64() {
            let mut generations = self.generations.lock().unwrap();
            let prior = generations.clone();
            let entry = generations.entry(target.into()).or_insert((0, None));
            if entry.1 != Some(observed) {
                entry.0 = entry
                    .0
                    .checked_add(1)
                    .ok_or_else(|| anyhow!("Sandbox epoch exhausted"))?;
                entry.1 = Some(observed);
            }
            let epoch = entry.0;
            if *generations != prior
                && let Err(error) = private_json(
                    &self.data.join("sandbox-generations.json"),
                    &serde_json::to_value(&*generations)?,
                )
            {
                *generations = prior;
                return Err(error);
            }
            state["generation"] = json!(epoch);
        }
        if method != "status" {
            self.changed.notify();
        }
        Ok(state)
    }
    async fn request(
        &self,
        target: &str,
        route: &str,
        body: &Value,
        timeout: Duration,
    ) -> Result<Value> {
        self.provider(target)?
            .request(&self.context(target), route, body, timeout)
            .await
    }
    /// Explicit execution preparation; inventory/permission inspection never invokes this.
    pub async fn ready_for_execution(
        &self,
        target: &str,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
    ) -> Result<()> {
        self.ready(target, cancelled).await
    }
    async fn ready(
        &self,
        target: &str,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
    ) -> Result<()> {
        let lock = self
            .instance_locks
            .lock()
            .unwrap()
            .entry(target.into())
            .or_default()
            .clone();
        let _lock = lock.lock().await;
        anyhow::ensure!(!cancelled(), "Sandbox preparation cancelled");
        let mut state = self.helper(target, "start").await?;
        for _ in 0..1200 {
            anyhow::ensure!(!cancelled(), "Sandbox preparation cancelled");
            anyhow::ensure!(
                state["phase"] != "failed",
                "Sandbox startup failed; inspect Sandbox logs"
            );
            if state["phase"] == "running"
                && self
                    .provider(target)?
                    .health(&self.context(target))
                    .await
                    .unwrap_or(false)
            {
                return Ok(());
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
            state = self.helper(target, "status").await?;
        }
        bail!("Sandbox executor did not become ready")
    }
}
// Agent-facing state carries no display credentials. Owner-authorized desktop
// tickets are issued separately from tool and inventory replies.
fn display_state(state: &Value) -> Value {
    json!({"phase":state["phase"].as_str().unwrap_or("unknown"),"generation":state["generation"].as_u64(),
        "cpus":state["cpus"].as_u64(),"memory_gib":state["memory_gib"].as_u64(),"disk_gib":state["disk_gib"].as_u64()})
}
impl Manager {
    pub fn display_name(&self, target: &str) -> Result<String> {
        self.book
            .lock()
            .unwrap()
            .items
            .get(target)
            .map(|item| item.name.clone())
            .ok_or_else(|| anyhow!("Unknown Sandbox"))
    }
    pub async fn inventory(&self) -> Value {
        let (items, selected) = {
            let book = self.book.lock().unwrap();
            (
                book.items.values().cloned().collect::<Vec<_>>(),
                book.selected.clone(),
            )
        };
        let mut rows = vec![];
        for item in items {
            let state = self
                .helper(&item.id, "status")
                .await
                .unwrap_or_else(|_| json!({"phase":"unavailable"}));
            let active = *self.leases.lock().unwrap().get(&item.id).unwrap_or(&0);
            let config = std::fs::read(
                self.root
                    .join("sandboxes")
                    .join(&item.id)
                    .join("sandbox.json"),
            )
            .ok()
            .and_then(|bytes| serde_json::from_slice::<Value>(&bytes).ok())
            .unwrap_or_else(|| {
                self.provider(&item.id)
                    .map(|p| p.default_config())
                    .unwrap_or(Value::Null)
            });
            let descriptor = self.describe(&item.id).ok();
            rows.push(json!({"config":config,"id":item.id,"name":item.name,"provider":item.provider,"os":descriptor.as_ref().map(|d|d.os.as_str()),"host_os":descriptor.as_ref().map(|d|d.host_os.as_str()),"engine":descriptor.as_ref().map(|d|d.engine.as_str()),"capabilities":descriptor.map(|d|d.capabilities),"desktop_capabilities":self.provider(&item.id).map(|p|p.desktop_capabilities()).ok(),"scope_cleanup_policy":self.provider(&item.id).map(|p|p.scope_cleanup_policy()).ok(),"active_leases":active,"unsettled_operations":self.pending.lock().unwrap().values().filter(|v| v.target == item.id).count(),"state":display_state(&state)}));
        }
        json!({"selected":selected,"items":rows})
    }
}
impl Manager {
    pub async fn management(
        self: &Arc<Self>,
        args: &Value,
        worker: bool,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
        delete_guard: &DeleteGuard<'_>,
    ) -> Result<Value> {
        anyhow::ensure!(!worker, "Workers cannot manage Sandboxes");
        anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
        if args["action"] == "desktop" {
            let id = self.assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
            let _lease = self.acquire(&id).await?;
            self.ready(&id, cancelled).await?;
            anyhow::ensure!(
                self.describe(&id)?.capabilities.desktop,
                "Desktop capability unavailable"
            );
            self.provider(&id)?
                .prepare_desktop(&self.context(&id))
                .await?;
            return Ok(
                json!({"sandbox_id":id,"state":display_state(&self.helper(&id,"status").await?)}),
            );
        }
        let action = args["action"].as_str().unwrap_or("");
        anyhow::ensure!(
            args.get("provider").is_none() || action == "create",
            "Provider is immutable; specify it only when creating a Sandbox"
        );
        if action == "start" {
            let id = self.assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
            let _lease = self.acquire(&id).await?;
            self.ready(&id, cancelled).await?;
            return Ok(
                json!({"sandbox_id":id,"state":display_state(&self.helper(&id,"status").await?)}),
            );
        }
        let _lock = self.lifecycle.lock().await;
        anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
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
            let provider_id = args["provider"].as_str().unwrap_or(&self.default_provider);
            anyhow::ensure!(
                self.providers.lock().unwrap().contains_key(provider_id),
                "Sandbox Provider unavailable"
            );
            {
                let mut book = self.book.lock().unwrap();
                anyhow::ensure!(
                    !book.retired.contains(id),
                    "Deleted Sandbox IDs cannot be reused"
                );
                anyhow::ensure!(book.items.len() < 16, "Sandbox limit reached");
                if let Some(existing) = book.items.get(id) {
                    anyhow::ensure!(
                        existing.name == name && existing.provider == provider_id,
                        "Sandbox ID already exists"
                    );
                } else {
                    book.items.insert(
                        id.into(),
                        Sandbox {
                            id: id.into(),
                            name: name.into(),
                            provider: args["provider"]
                                .as_str()
                                .unwrap_or(&self.default_provider)
                                .into(),
                        },
                    );
                }
            }
            self.persist(&self.data)?;
            return Ok(json!({"sandbox_id":id,"phase":"stopped"}));
        }
        let id = self.assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
        if action == "set_mounts" {
            anyhow::ensure!(
                [
                    crate::MACOS_APPLE_CONTAINER_PROVIDER,
                    crate::LINUX_PODMAN_PROVIDER
                ]
                .contains(&self.describe(&id)?.id.as_str()),
                "Host mappings are supported only by Linux guest providers"
            );
            let path = self.root.join("sandboxes").join(&id).join("sandbox.json");
            let mut config: Value = if path.exists() {
                serde_json::from_slice(&std::fs::read(&path)?)?
            } else {
                self.provider(&id)?.default_config()
            };
            config["mappings"] = args
                .get("mappings")
                .cloned()
                .ok_or_else(|| anyhow!("Mappings required"))?;
            crate::host_mappings(&config)?;
            anyhow::ensure!(
                *self.leases.lock().unwrap().get(&id).unwrap_or(&0) == 0
                    && !self
                        .pending
                        .lock()
                        .unwrap()
                        .values()
                        .any(|v| v.target == id),
                "Sandbox busy; finish or recover operations first"
            );
            let state = self.helper(&id, "status").await?;
            anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
            anyhow::ensure!(
                state["phase"] == "stopped" || state["phase"] == "failed",
                "Stop the Sandbox before changing mappings"
            );
            std::fs::create_dir_all(path.parent().unwrap())?;
            private_json(&path, &config)?;
            return Ok(
                json!({"sandbox_id":id,"mappings":config["mappings"],"applies":"next-start","access":"all tasks in this Sandbox"}),
            );
        }
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
            self.provider(&id)?
                .validate_instance_config(&self.context(&id), config)?;
            anyhow::ensure!(
                *self.leases.lock().unwrap().get(&id).unwrap_or(&0) == 0
                    && !self
                        .pending
                        .lock()
                        .unwrap()
                        .values()
                        .any(|v| v.target == id),
                "Sandbox busy; finish or recover operations first"
            );
            let state = self.helper(&id, "status").await?;
            anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
            anyhow::ensure!(
                state["phase"] == "stopped" || state["phase"] == "failed",
                "Stop the Sandbox before changing settings"
            );
            let path = self.root.join("sandboxes").join(&id);
            std::fs::create_dir_all(&path)?;
            let config_path = path.join("sandbox.json");
            let old: Value = if config_path.exists() {
                serde_json::from_slice(&std::fs::read(&config_path)?)?
            } else {
                json!({})
            };
            private_json(
                &config_path,
                &json!({"cpus":c,"memory_gib":m,"disk_gib":d,"mappings":old.get("mappings").cloned().unwrap_or_else(||json!([]))}),
            )?;
            self.book.lock().unwrap().items.get_mut(&id).unwrap().name = name.into();
            self.persist(&self.data)?;
            return Ok(json!({"sandbox_id":id}));
        }
        if action == "recover" {
            anyhow::ensure!(
                *self.leases.lock().unwrap().get(&id).unwrap_or(&0) == 0,
                "Sandbox busy"
            );
            let state = self.helper(&id, "status").await?;
            anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
            let pending: Vec<_> = self
                .pending
                .lock()
                .unwrap()
                .iter()
                .filter(|(_, t)| t.target == id)
                .map(|(op, _)| op.clone())
                .collect();
            for op in &pending {
                anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
                if state["phase"] != "stopped" {
                    let result = self
                        .request(&id, "cancel", &json!({"id":op}), Duration::from_secs(15))
                        .await?;
                    anyhow::ensure!(
                        self.provider(&id)?
                            .scope_cleanup_policy()
                            .confirmed(&result),
                        "Executor cleanup unconfirmed"
                    );
                }
                self.settle(op)?;
            }
            return Ok(
                json!({"sandbox_id":id,"reconciled_operations":pending,"prior_results":"unknown; commands were not replayed"}),
            );
        }
        if action == "select" {
            self.book.lock().unwrap().selected = id.clone();
            self.persist(&self.data)?;
            return Ok(json!({"selected":id}));
        }
        anyhow::ensure!(
            ["start", "stop", "delete"].contains(&action),
            "Unknown Sandbox action"
        );
        if action != "start" {
            let active = *self.leases.lock().unwrap().get(&id).unwrap_or(&0);
            anyhow::ensure!(
                active == 0,
                "Sandbox busy: {active} active task/operation leases; inspect inventory before stopping"
            );
            anyhow::ensure!(
                !self
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
        }
        let _desktop_guard = self.desktop_serialization().await;
        if action != "start" {
            self.revoke_desktops(&id);
        }
        anyhow::ensure!(!cancelled(), "Sandbox management cancelled");
        let state = self
            .helper(&id, if action == "delete" { "stop" } else { action })
            .await?;
        if action == "delete" {
            delete_guard(&id, &|| {
                let mut book = self.book.lock().unwrap();
                book.items.remove(&id);
                book.retired.insert(id.clone());
                if book.selected == id {
                    book.selected = "default".into();
                }
                drop(book);
                self.persist(&self.data)
            })?;
        }
        Ok(json!({"sandbox_id":id,"state":display_state(&state)}))
    }
}
impl Manager {
    /// Normalize execution arguments inside the guest after Control authorization.
    pub async fn prepare_operation(&self, target: &str, body: &Value) -> Result<Value> {
        self.request(target, "prepare", body, Duration::from_secs(30))
            .await
    }
    /// Resolve a live session without booting or retargeting its Sandbox.
    pub fn session_target(&self, owner: &str, epoch: u64, session: &str) -> Result<String> {
        let id = self
            .sessions
            .lock()
            .unwrap()
            .get(session)
            .cloned()
            .ok_or_else(|| {
                anyhow!("Session expired or belongs to another thread execution generation")
            })?;
        let scope = self
            .scopes
            .lock()
            .unwrap()
            .get(&id)
            .cloned()
            .ok_or_else(|| anyhow!("Session scope expired"))?;
        anyhow::ensure!(
            scope.owner == owner && scope.thread_generation == epoch,
            "Session belongs to another thread execution generation"
        );
        Ok(scope.target)
    }
    pub async fn execute(
        self: &Arc<Self>,
        grant: &AccessGrant,
        name: &str,
        call_id: &str,
        args: &Value,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
    ) -> Result<Value> {
        anyhow::ensure!(
            !call_id.is_empty() && call_id.len() <= 256 && !grant.turn.is_empty(),
            "Invalid execution identity"
        );
        anyhow::ensure!(
            ["exec_command", "write_stdin", "apply_patch", "plugin_call"].contains(&name),
            "Unknown execution operation"
        );
        anyhow::ensure!(
            args.get("sandbox_id").is_none_or(Value::is_string),
            "Invalid Sandbox identity"
        );
        let mut validated = args.clone();
        if let Some(object) = validated.as_object_mut() {
            object.remove("sandbox_id");
        }
        pot_core::execution_tools::validate(name, &validated)?;
        let gate = self.gate(&grant.owner);
        let _gate = gate.read().await;
        let turn = &grant.turn;
        use sha2::{Digest, Sha256};
        let key: String =
            Sha256::digest(serde_json::to_vec(&(grant.owner.as_str(), turn, call_id))?)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect();
        let call_lock = self
            .call_locks
            .lock()
            .unwrap()
            .entry(key.clone())
            .or_default()
            .clone();
        let _call_lock = call_lock.lock().await;
        let directory = self.data.join("sandbox-receipts");
        std::fs::create_dir_all(&directory)?;
        let receipt = directory.join(format!("{key}.json"));
        if receipt.exists() {
            let old: Value = serde_json::from_slice(&std::fs::read(&receipt)?)?;
            anyhow::ensure!(
                old["name"] == name && old["args"] == *args,
                "Execution ID reused with different arguments"
            );
            if old["status"] == "complete" {
                return Ok(old["result"].clone());
            }
            bail!("Prior execution outcome unknown; inspect before retrying with a new call ID");
        }
        let requested = args["sandbox_id"].as_str().unwrap_or("");
        let prior_scope = if name == "write_stdin"
            || (name == "plugin_call" && args.get("session_id").is_some())
        {
            let session = args["session_id"]
                .as_str()
                .ok_or_else(|| anyhow!("Missing session ID"))?;
            let id = self
                .sessions
                .lock()
                .unwrap()
                .get(session)
                .cloned()
                .ok_or_else(|| {
                    anyhow!("Session expired or belongs to another thread execution generation")
                })?;
            let scope = self
                .scopes
                .lock()
                .unwrap()
                .get(&id)
                .cloned()
                .ok_or_else(|| anyhow!("Session scope expired"))?;
            anyhow::ensure!(
                scope.owner == grant.owner && scope.thread_generation == grant.thread_generation,
                "Session belongs to another thread execution generation"
            );
            anyhow::ensure!(
                requested.is_empty() || requested == scope.target,
                "write_stdin cannot switch Sandbox"
            );
            Some(scope)
        } else {
            None
        };
        let target = if let Some(scope) = &prior_scope {
            scope.target.clone()
        } else if grant.worker {
            let assigned = grant.target.as_str();
            anyhow::ensure!(
                requested.is_empty() || requested == assigned,
                "Worker cannot switch Sandbox"
            );
            assigned.to_owned()
        } else {
            self.assignment(requested)?
        };
        *self
            .running
            .lock()
            .unwrap()
            .entry(grant.owner.clone())
            .or_default() += 1;
        let _execution = Execution {
            manager: self.clone(),
            owner: grant.owner.clone(),
        };
        anyhow::ensure!(
            self.describe(&target)?.capabilities.shell,
            "Shell capability unavailable"
        );
        if name == "plugin_call" {
            anyhow::ensure!(
                self.describe(&target)?.os == "linux",
                "Node plugins require a qualified Linux Sandbox"
            );
        }
        let _preparation = self.acquire(&target).await?;
        self.ready(&target, cancelled).await?;
        if name == "plugin_call" {
            let capabilities = self
                .request(
                    &target,
                    "capabilities",
                    &json!({"protocol_version":1}),
                    Duration::from_secs(10),
                )
                .await?;
            let bundle = pot_core::plugins::validate_call(&validated)?;
            anyhow::ensure!(
                capabilities["protocol_version"] == 1
                    && capabilities["plugins"]["protocol_version"] == 1
                    && capabilities["plugins"]["runtime"] == bundle.manifest.runtime,
                "Plugin runtime unavailable; update the Linux Sandbox image"
            );
            let available = capabilities["plugins"]["requirements"]
                .as_array()
                .cloned()
                .unwrap_or_default();
            anyhow::ensure!(
                bundle
                    .manifest
                    .requirements
                    .iter()
                    .all(|r| available.iter().any(|v| v == r)),
                "Plugin dependency unavailable in Sandbox image"
            );
        }
        let generation = self.helper(&target, "status").await?["generation"]
            .as_u64()
            .unwrap_or(0);
        anyhow::ensure!(
            !cancelled(),
            "Execution turn ended during Sandbox preparation"
        );
        let scope = self.scope(&target, &grant.owner, grant.thread_generation, generation)?;
        let mut record =
            json!({"id":id(),"target":target,"name":name,"args":args,"status":"accepted"});
        private_json(&receipt, &record)?;
        let cwd = if grant.worker {
            format!(
                "{}/tasks/{}",
                self.provider(&target)?.guest_workspace(),
                grant.workspace_owner.as_deref().unwrap_or(&grant.owner)
            )
        } else {
            self.provider(&target)?.guest_workspace().into()
        };
        let mut tool_args = args.clone();
        if let Some(object) = tool_args.as_object_mut() {
            object.remove("sandbox_id");
        }
        let body = json!({"id":record["id"],"scope":scope.id,"operation":name,"args":tool_args,"cwd":cwd,"role":if grant.worker {"task"} else {"main"},"policy":grant.policy});
        let response = self.request(&target, "execute", &body, Duration::from_secs(660));
        tokio::pin!(response);
        let result = loop {
            tokio::select! {
                result=&mut response=>break result?,
                _=tokio::time::sleep(std::time::Duration::from_millis(100))=>{
                    if cancelled() {
                        self.cancel_scope(&scope).await?;
                        let result=json!({"result":{"sandbox_id":target,"cancelled":true},"events":[]});
                        record["status"]=json!("complete");record["result"]=result.clone();private_json(&receipt,&record)?;
                        return Ok(result);
                    }
                }
            }
        };
        let mut result = result;
        if let Some(session) = result["result"]["session_id"].as_str() {
            self.sessions
                .lock()
                .unwrap()
                .insert(session.to_owned(), scope.id.clone());
        }
        if matches!(name, "write_stdin" | "plugin_call")
            && args.get("session_id").is_some()
            && result["result"].get("session_id").is_none()
        {
            self.sessions
                .lock()
                .unwrap()
                .remove(args["session_id"].as_str().unwrap());
        }
        result["result"]["sandbox_id"] = json!(target);
        record["status"] = json!("complete");
        record["result"] = result.clone();
        private_json(&receipt, &record)?;
        Ok(result)
    }
}
impl Manager {
    async fn cancel_scope(&self, scope: &Scope) -> Result<()> {
        let state = self.helper(&scope.target, "status").await?;
        if state["phase"] != "stopped" && state["phase"] != "failed" {
            let response = self
                .request(
                    &scope.target,
                    "finish",
                    &json!({"id":scope.id}),
                    Duration::from_secs(15),
                )
                .await?;
            anyhow::ensure!(
                self.provider(&scope.target)?
                    .scope_cleanup_policy()
                    .confirmed(&response),
                "Executor cleanup stop unconfirmed"
            );
        }
        self.settle(&scope.id)?;
        release_scope(self, scope);
        Ok(())
    }
}
fn release_scope(manager: &Manager, scope: &Scope) {
    if manager
        .scopes
        .lock()
        .unwrap()
        .remove(&scope.id)
        .is_some_and(|v| !v.parked)
    {
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
impl Manager {
    pub async fn finish(&self, owner: &str) -> Result<()> {
        let mut errors = vec![];
        for _ in 0..200 {
            let scopes: Vec<_> = self
                .scopes
                .lock()
                .unwrap()
                .values()
                .filter(|v| v.owner == owner)
                .cloned()
                .collect();
            for scope in scopes {
                if let Err(error) = self.cancel_scope(&scope).await {
                    if let Some(pending) = self.pending.lock().unwrap().get_mut(&scope.id) {
                        pending.active = false;
                    }
                    // Durable unknown outcomes still quarantine the Sandbox;
                    // release transient leases so explicit recovery can settle it.
                    release_scope(self, &scope);
                    errors.push(error.to_string());
                }
            }
            self.persist_operations()?;
            if *self.running.lock().unwrap().get(owner).unwrap_or(&0) == 0 {
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
}
impl Manager {
    pub async fn drain(&self, owner: &str) -> Result<()> {
        for _ in 0..200 {
            if *self.running.lock().unwrap().get(owner).unwrap_or(&0) == 0 {
                anyhow::ensure!(
                    !self
                        .pending
                        .lock()
                        .unwrap()
                        .values()
                        .any(|v| v.owner == owner && !v.active),
                    "Sandbox execution outcome unknown; stop unconfirmed, recover Sandbox before continuing"
                );
                return Ok(());
            }
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        }
        bail!("Sandbox operation cleanup timed out; stop unconfirmed")
    }
}

impl Manager {
    pub async fn shutdown(&self) -> Result<()> {
        self.viewers.lock().unwrap().clear();
        let scopes: Vec<_> = self.scopes.lock().unwrap().values().cloned().collect();
        let mut errors = Vec::new();
        for scope in scopes {
            if let Err(error) = self.cancel_scope(&scope).await {
                errors.push(error.to_string());
            }
        }
        let providers: Vec<_> = self.providers.lock().unwrap().values().cloned().collect();
        for provider in providers {
            if let Err(error) = provider.shutdown(&self.root).await {
                errors.push(error.to_string());
            }
        }
        anyhow::ensure!(
            errors.is_empty(),
            "Sandbox shutdown unconfirmed: {}",
            errors.join("; ")
        );
        Ok(())
    }
}

impl Manager {
    pub async fn finish_turn(&self, owner: &str) -> Result<()> {
        let scopes: Vec<_> = self
            .scopes
            .lock()
            .unwrap()
            .values()
            .filter(|v| v.owner == owner)
            .cloned()
            .collect();
        for scope in scopes {
            if let Err(error) = self.inspect_scope(&scope).await {
                if let Some(pending) = self.pending.lock().unwrap().get_mut(&scope.id) {
                    pending.active = false;
                }
                release_scope(self, &scope);
                self.persist_operations()?;
                return Err(anyhow!("Executor inspection stop unconfirmed: {error}"));
            }
        }
        Ok(())
    }
    async fn inspect_scope(&self, scope: &Scope) -> Result<()> {
        let gate = self.gate(&scope.owner);
        let _gate = gate.write().await;
        if !self.scopes.lock().unwrap().contains_key(&scope.id) {
            return Ok(());
        }
        let state = self.helper(&scope.target, "status").await?;
        if matches!(state["phase"].as_str(), Some("stopped" | "failed"))
            || state["generation"].as_u64().unwrap_or(0) != scope.generation
        {
            // A changed generation proves the old executor cannot retain its processes.
            self.settle(&scope.id)?;
            release_scope(self, scope);
            return Ok(());
        }
        anyhow::ensure!(
            state["phase"] == "running",
            "Executor inspection stop unconfirmed: Sandbox transition in progress"
        );
        let reply = self
            .request(
                &scope.target,
                "inspect",
                &json!({"id":scope.id}),
                Duration::from_secs(15),
            )
            .await?;
        let sessions = reply["sessions"]
            .as_array()
            .ok_or_else(|| anyhow!("Invalid executor session inventory; stop unconfirmed"))?;
        anyhow::ensure!(
            reply["active"].is_boolean()
                && reply["ended"].is_boolean()
                && sessions.iter().all(Value::is_string),
            "Invalid executor session inventory; stop unconfirmed"
        );
        self.sessions.lock().unwrap().retain(|session, id| {
            id != &scope.id || sessions.iter().any(|v| v.as_str() == Some(session))
        });
        if reply["ended"] == true {
            self.settle(&scope.id)?;
            release_scope(self, scope);
        } else if reply["active"] == false && !scope.parked {
            self.settle(&scope.id)?;
            let mut scopes = self.scopes.lock().unwrap();
            if let Some(value) = scopes.get_mut(&scope.id)
                && !value.parked
            {
                value.parked = true;
                *self.leases.lock().unwrap().get_mut(&scope.target).unwrap() -= 1;
            }
        }
        Ok(())
    }
    pub async fn reap_idle(&self) -> Result<()> {
        let scopes: Vec<_> = self
            .scopes
            .lock()
            .unwrap()
            .values()
            .filter(|v| *self.running.lock().unwrap().get(&v.owner).unwrap_or(&0) == 0)
            .cloned()
            .collect();
        for scope in scopes {
            if let Err(error) = self.inspect_scope(&scope).await {
                if let Some(pending) = self.pending.lock().unwrap().get_mut(&scope.id) {
                    pending.active = false;
                }
                release_scope(self, &scope);
                self.persist_operations()?;
                return Err(anyhow!("Executor inspection stop unconfirmed: {error}"));
            }
        }
        Ok(())
    }
    pub async fn finish_stale(&self, owner: &str, epoch: u64) -> Result<()> {
        let scopes: Vec<_> = self
            .scopes
            .lock()
            .unwrap()
            .values()
            .filter(|v| v.owner == owner && v.thread_generation != epoch)
            .cloned()
            .collect();
        for scope in scopes {
            self.cancel_scope(&scope).await?;
        }
        Ok(())
    }
    pub async fn finish_idle(&self, owner: &str) -> Result<()> {
        let gate = self.gate(owner);
        let _gate = gate.write().await;
        self.finish(owner).await?;
        self.drain(owner).await
    }
}
