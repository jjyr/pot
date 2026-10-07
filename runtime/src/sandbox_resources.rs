//! Product-owned approval, preparation and persistence. Providers receive only paths.
use super::*;
use pot_sandbox_images::{Images, PreparedBundle};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};

#[derive(Clone, Serialize, Deserialize)]
pub struct Request {
    pub id: String,
    #[serde(default)]
    pub created_at_ms: i64,
    pub sandbox_id: String,
    pub bundle_id: String,
    pub owner: String,
    pub turn: Option<String>,
    pub phase: String,
    pub missing_bytes: u64,
    pub resource: String,
    pub progress: Value,
    pub error: String,
}
#[derive(Default, Serialize, Deserialize)]
struct Book {
    pins: BTreeMap<String, String>,
    requests: BTreeMap<String, Request>,
}
pub struct Hub {
    images: Arc<Images>,
    path: PathBuf,
    book: Mutex<Book>,
    jobs: Mutex<BTreeSet<String>>,
    bound: Mutex<BTreeSet<String>>,
}
impl Hub {
    pub fn open(data: &std::path::Path) -> Result<Arc<Self>> {
        let path = data.join("sandbox-resources.json");
        let mut book: Book = if path.exists() {
            serde_json::from_slice(&std::fs::read(&path)?)?
        } else {
            Default::default()
        };
        for r in book.requests.values_mut() {
            if !matches!(
                r.phase.as_str(),
                "awaiting-authorization" | "ready" | "cancelled" | "failed"
            ) {
                r.phase = "interrupted".into();
            }
        }
        let hub = Arc::new(Self {
            images: Arc::new(Images::open(
                data.parent().unwrap_or(data).join("resource-cache"),
            )?),
            path,
            book: Mutex::new(book),
            jobs: Default::default(),
            bound: Default::default(),
        });
        hub.save(&hub.book.lock().unwrap())?;
        Ok(hub)
    }
    fn save(&self, book: &Book) -> Result<()> {
        pot_sandbox::private_json(&self.path, &serde_json::to_value(book)?)
    }
    fn pin(&self, id: &str) -> Result<String> {
        let mut book = self.book.lock().unwrap();
        let bundle = book
            .pins
            .entry(id.into())
            .or_insert_with(|| self.images.catalog.default_linux_bundle.clone())
            .clone();
        self.images.catalog.bundle(&bundle)?;
        self.save(&book)?;
        Ok(bundle)
    }
    pub fn requests(&self) -> Value {
        json!(
            self.book
                .lock()
                .unwrap()
                .requests
                .values()
                .collect::<Vec<_>>()
        )
    }
    pub fn pending(&self, id: &str) -> Value {
        let book = self.book.lock().unwrap();
        json!(
            book.requests
                .values()
                .filter(|r| r.sandbox_id == id && r.phase != "cancelled")
                .max_by_key(|r| (r.phase != "ready", r.created_at_ms))
        )
    }
    fn bind(&self, manager: &pot_sandbox::Manager, id: &str, b: PreparedBundle) -> Result<()> {
        manager.bind_resources(
            id,
            &pot_sandbox::PreparedResources {
                bundle_id: b.id,
                kernel: b.kernel.path().join("payload"),
                boot: b.boot.path().into(),
                image: b.image.path().into(),
                image_reference: b.image_reference,
                boot_reference: b.boot_reference,
            },
        )?;
        self.bound.lock().unwrap().insert(id.into());
        Ok(())
    }
    /// Inspection and cached preparation never access the network.
    pub async fn inspect(
        &self,
        manager: &pot_sandbox::Manager,
        id: &str,
        owner: &str,
        turn: Option<&str>,
    ) -> Result<Option<String>> {
        if manager.describe(id)?.os != "linux" || self.bound.lock().unwrap().contains(id) {
            return Ok(None);
        }
        let bundle = self.pin(id)?;
        let all_present = self.images.catalog.resource_ids(&bundle)?.iter().all(|id| {
            self.images
                .store
                .root()
                .join(&self.images.catalog.resources[*id].sha256)
                .join("complete.json")
                .exists()
        });
        let images = self.images.clone();
        let key = bundle.clone();
        let prepared = if all_present {
            tokio::task::spawn_blocking(move || images.prepared(&key)).await??
        } else {
            None
        };
        if let Some(prepared) = prepared {
            self.bind(manager, id, prepared)?;
            return Ok(None);
        }
        let missing = self
            .images
            .catalog
            .resource_ids(&bundle)?
            .iter()
            .map(|k| {
                let r = &self.images.catalog.resources[*k];
                if self
                    .images
                    .store
                    .root()
                    .join(&r.sha256)
                    .join("complete.json")
                    .exists()
                {
                    0
                } else {
                    r.size_bytes
                }
            })
            .sum();
        let mut book = self.book.lock().unwrap();
        if let Some(r) = book.requests.values_mut().find(|r| {
            r.sandbox_id == id
                && r.owner == owner
                && (r.turn.as_deref() == turn || r.turn.is_none())
                && r.phase != "cancelled"
                && r.bundle_id == bundle
        }) {
            r.turn = turn.map(str::to_owned);
            let key = r.id.clone();
            self.save(&book)?;
            return Ok(Some(key));
        }
        let request = Request {
            id: uuid::Uuid::new_v4().to_string(),
            created_at_ms: now(),
            sandbox_id: id.into(),
            bundle_id: bundle,
            owner: owner.into(),
            turn: turn.map(str::to_owned),
            phase: "awaiting-authorization".into(),
            missing_bytes: missing,
            resource: String::new(),
            progress: json!({}),
            error: String::new(),
        };
        let key = request.id.clone();
        book.requests.insert(key.clone(), request);
        self.save(&book)?;
        Ok(Some(key))
    }
    pub async fn wait(
        &self,
        manager: &pot_sandbox::Manager,
        id: &str,
        owner: &str,
        turn: Option<&str>,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
    ) -> Result<()> {
        let Some(key) = self.inspect(manager, id, owner, turn).await? else {
            return Ok(());
        };
        loop {
            if cancelled() {
                self.cancel(&key)?;
                bail!("Resource preparation cancelled");
            }
            let request = self.book.lock().unwrap().requests[&key].clone();
            match request.phase.as_str() {
                "ready" => {
                    let images = self.images.clone();
                    let bundle = request.bundle_id;
                    let prepared = tokio::task::spawn_blocking(move || images.prepared(&bundle))
                        .await??
                        .ok_or_else(|| anyhow!("Prepared bundle unavailable"))?;
                    self.bind(manager, id, prepared)?;
                    return Ok(());
                }
                "cancelled" => bail!("Sandbox resource download declined or cancelled"),
                _ => {}
            }
            tokio::time::sleep(std::time::Duration::from_millis(200)).await;
        }
    }
    fn cancel(&self, key: &str) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        let r = book
            .requests
            .get_mut(key)
            .ok_or_else(|| anyhow!("Unknown resource request"))?;
        r.phase = "cancelled".into();
        self.save(&book)
    }
    pub fn control(self: &Arc<Self>, key: &str, action: &str) -> Result<Value> {
        if action == "cancel" {
            self.cancel(key)?;
            return Ok(json!({"cancelled":true}));
        }
        anyhow::ensure!(
            matches!(action, "authorize" | "retry" | "continue"),
            "Unknown resource action"
        );
        let bundle = {
            let mut book = self.book.lock().unwrap();
            let r = book
                .requests
                .get_mut(key)
                .ok_or_else(|| anyhow!("Unknown resource request"))?;
            anyhow::ensure!(
                matches!(
                    r.phase.as_str(),
                    "awaiting-authorization" | "failed" | "interrupted"
                ),
                "Request cannot be authorized in current state"
            );
            r.phase = "downloading".into();
            r.error.clear();
            let bundle = r.bundle_id.clone();
            self.save(&book)?;
            bundle
        };
        let mut jobs = self.jobs.lock().unwrap();
        if jobs.insert(bundle.clone()) {
            let hub = self.clone();
            tokio::spawn(async move {
                hub.download(bundle).await;
            });
        }
        Ok(json!({"authorized":true}))
    }
    async fn download(self: Arc<Self>, bundle: String) {
        let result = self
            .images
            .prepare(
                &bundle,
                &|| {
                    let book = self.book.lock().unwrap();
                    !book.requests.values().any(|r| {
                        r.bundle_id == bundle
                            && matches!(
                                r.phase.as_str(),
                                "downloading" | "verifying" | "extracting"
                            )
                    })
                },
                &|resource, progress| {
                    let mut book = self.book.lock().unwrap();
                    for r in book.requests.values_mut().filter(|r| {
                        r.bundle_id == bundle
                            && matches!(
                                r.phase.as_str(),
                                "downloading" | "verifying" | "extracting"
                            )
                    }) {
                        r.phase = progress.phase.clone();
                        r.resource = resource.into();
                        r.progress = json!(progress);
                    }
                    // Download byte progress is transient; durable checkpoints avoid excessive fsync.
                },
            )
            .await;
        // Lock ordering makes authorization during job completion either join this
        // completion or start a fresh job. Cancelling one waiter keeps others alive.
        let mut jobs = self.jobs.lock().unwrap();
        let mut book = self.book.lock().unwrap();
        for r in book.requests.values_mut().filter(|r| {
            r.bundle_id == bundle
                && (if result.is_ok() {
                    r.phase != "cancelled"
                } else {
                    matches!(r.phase.as_str(), "downloading" | "verifying" | "extracting")
                })
        }) {
            match &result {
                Ok(_) => {
                    r.phase = "ready".into();
                    r.error.clear();
                }
                Err(e) => {
                    r.phase = "failed".into();
                    r.error = e.to_string();
                }
            }
        }
        let _ = self.save(&book);
        jobs.remove(&bundle);
    }
    pub fn cache(&self) -> Result<Value> {
        let book = self.book.lock().unwrap();
        let mut rows = vec![];
        for (id, r) in &self.images.catalog.resources {
            let dir = self.images.store.root().join(&r.sha256);
            let references = book
                .pins
                .iter()
                .filter(|(_, bundle)| {
                    self.images
                        .catalog
                        .resource_ids(bundle)
                        .is_ok_and(|ids| ids.contains(&id.as_str()))
                })
                .map(|(id, _)| id)
                .collect::<Vec<_>>();
            rows.push(json!({"id":id,"sha256":r.sha256,"present":dir.join("complete.json").exists(),"disk_bytes":if dir.exists(){r.size_bytes+r.unpacked_bytes}else{0},"references":references}));
        }
        Ok(json!({"items":rows}))
    }
    pub fn forget(&self, id: &str) -> Result<()> {
        let mut book = self.book.lock().unwrap();
        book.pins.remove(id);
        for r in book.requests.values_mut().filter(|r| r.sandbox_id == id) {
            r.phase = "cancelled".into();
        }
        self.bound.lock().unwrap().remove(id);
        self.save(&book)
    }
    pub fn prune(&self, key: &str) -> Result<()> {
        let jobs = self.jobs.lock().unwrap();
        anyhow::ensure!(jobs.is_empty(), "Resources are being prepared");
        let book = self.book.lock().unwrap();
        let r = self
            .images
            .catalog
            .resources
            .get(key)
            .ok_or_else(|| anyhow!("Unknown resource"))?;
        anyhow::ensure!(
            !book.pins.values().any(|id| self
                .images
                .catalog
                .resource_ids(id)
                .is_ok_and(|ids| ids.contains(&key))),
            "Resource is referenced by a Sandbox"
        );
        let dir = self.images.store.root().join(&r.sha256);
        if dir.exists() {
            std::fs::remove_dir_all(dir)?;
        }
        Ok(())
    }
}
pub async fn status(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(
        json!({"requests":s.sandboxes.resources.as_ref().map(|hub|hub.requests()),"cache":s.sandboxes.resources.as_ref().map(|hub|hub.cache()).transpose().map_err(internal)?}),
    ))
}
pub async fn command(
    State(s): State<Service>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let hub = s.sandboxes.resources.as_ref().ok_or_else(|| {
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

#[cfg(test)]
mod tests {
    use super::*;
    fn fixture() -> Service {
        let mut s = tasks::tests::fixture();
        s.sandboxes = Arc::new(
            sandbox::Manager::with_providers(
                &s.data,
                vec![Arc::new(pot_sandbox_linux::LinuxProvider::new().unwrap())],
            )
            .unwrap(),
        );
        s
    }
    #[tokio::test]
    async fn create_and_agent_supplied_consent_never_start_a_download() {
        let s = fixture();
        let response=sandbox::management(&s,&json!({"action":"create","sandbox_id":"approval-test","download":true,"authorize":true})).await.unwrap();
        assert_eq!(response["phase"], "awaiting-resources");
        let hub = s.sandboxes.resources.as_ref().unwrap();
        assert!(hub.jobs.lock().unwrap().is_empty());
        assert_eq!(hub.requests()[0]["phase"], "awaiting-authorization");
        assert!(!s.data.parent().unwrap().join("helper.sock").exists());
        assert!(
            hub.prune(
                &hub.images
                    .catalog
                    .bundle(&hub.images.catalog.default_linux_bundle)
                    .unwrap()
                    .kernel
            )
            .is_err()
        );
        let key = response["resource_request_id"].as_str().unwrap();
        hub.control(key, "cancel").unwrap();
        assert_eq!(hub.requests()[0]["phase"], "cancelled");
        hub.forget("approval-test").unwrap();
        assert!(hub.book.lock().unwrap().pins.is_empty());
    }
    #[tokio::test]
    async fn waiting_execution_stays_before_dispatch_and_decline_unblocks_same_call() {
        let s = fixture();
        s.save(Message {
            id: "input".into(),
            conversation_id: s.usage_scope.clone(),
            role: Role::User,
            text: "fixture".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some("resource-turn".into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "input".into(),
            cancelling: false,
        }));
        let caller = s.clone();
        let call = tokio::spawn(async move {
            sandbox::tool(
                &caller,
                "exec_command",
                "original-call",
                &json!({"cmd":"true"}),
            )
            .await
        });
        let hub = s.sandboxes.resources.as_ref().unwrap();
        tokio::time::timeout(std::time::Duration::from_secs(3), async {
            while hub.requests().as_array().unwrap().is_empty() {
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert!(!s.data.join("sandbox-receipts").exists());
        assert!(hub.jobs.lock().unwrap().is_empty());
        assert!(!call.is_finished());
        let key = hub.requests()[0]["id"].as_str().unwrap().to_string();
        hub.control(&key, "cancel").unwrap();
        assert!(
            call.await
                .unwrap()
                .unwrap_err()
                .to_string()
                .contains("declined")
        );
        assert!(!s.data.join("sandbox-receipts").exists());
    }
    #[tokio::test]
    async fn restart_marks_inflight_interrupted_without_reauthorization_or_replay() {
        let s = fixture();
        let hub = s.sandboxes.resources.as_ref().unwrap();
        let key = hub
            .inspect(&s.sandboxes, "default", "owner", None)
            .await
            .unwrap()
            .unwrap();
        {
            let mut book = hub.book.lock().unwrap();
            book.requests.get_mut(&key).unwrap().phase = "verifying".into();
            hub.save(&book).unwrap();
        }
        let reopened = Hub::open(&s.data).unwrap();
        assert_eq!(reopened.requests()[0]["phase"], "interrupted");
        assert!(reopened.jobs.lock().unwrap().is_empty());
        assert_eq!(
            reopened.book.lock().unwrap().pins["default"],
            hub.images.catalog.default_linux_bundle
        );
    }
    #[tokio::test]
    async fn cancelling_one_bundle_waiter_does_not_cancel_other_authorized_waiters() {
        let s = fixture();
        let hub = s.sandboxes.resources.as_ref().unwrap();
        let a = hub
            .inspect(&s.sandboxes, "default", "owner", Some("a"))
            .await
            .unwrap()
            .unwrap();
        let b = hub
            .inspect(&s.sandboxes, "default", "owner", Some("b"))
            .await
            .unwrap()
            .unwrap();
        {
            let mut book = hub.book.lock().unwrap();
            book.requests.get_mut(&a).unwrap().phase = "downloading".into();
            book.requests.get_mut(&b).unwrap().phase = "downloading".into();
        }
        hub.cancel(&a).unwrap();
        assert_eq!(hub.book.lock().unwrap().requests[&b].phase, "downloading");
        assert!(hub.jobs.lock().unwrap().is_empty());
    }
}
