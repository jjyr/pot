//! Durable conversational owners. Each owner has a private Thread, task hub and event stream.
use crate::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

const KEY: &str = "assistants.v1";
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Definition {
    pub id: String,
    pub name: String,
    pub revision: u64,
    pub request_id: String,
}
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Book {
    schema_version: u32,
    definitions: Vec<Definition>,
}
struct Owner {
    service: Service,
    main: tokio::task::JoinHandle<()>,
    coordinator: tokio::task::JoinHandle<()>,
}
pub struct Hub {
    root: Service,
    book: Mutex<Book>,
    owners: Mutex<BTreeMap<String, Owner>>,
}
impl Hub {
    pub fn open(root: Service) -> Result<Self> {
        let value = root.conversation.inner.lock().unwrap().store.get(KEY)?;
        let book: Book = value
            .map(|v| serde_json::from_str(&v))
            .transpose()?
            .unwrap_or_else(|| Book {
                schema_version: 1,
                definitions: vec![Definition {
                    id: root.usage_scope.clone(),
                    name: "Pot".into(),
                    revision: 1,
                    request_id: "initial".into(),
                }],
            });
        anyhow::ensure!(book.schema_version == 1, "Unsupported assistant schema");
        let mut seen = std::collections::BTreeSet::new();
        for d in &book.definitions {
            validate_name(&d.name)?;
            anyhow::ensure!(
                d.revision > 0 && seen.insert(&d.id),
                "Invalid or duplicate assistant identity"
            );
            if d.id != root.usage_scope {
                uuid::Uuid::parse_str(&d.id)?;
            }
        }
        anyhow::ensure!(
            seen.contains(&root.usage_scope),
            "Initial assistant missing"
        );
        let hub = Self {
            root,
            book: Mutex::new(book),
            owners: Mutex::new(BTreeMap::new()),
        };
        hub.persist(&hub.book.lock().unwrap())?;
        let _ = crate::assistant_instructions::read(&hub.root)?;
        Ok(hub)
    }
    fn persist(&self, book: &Book) -> Result<()> {
        self.root
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(KEY, &serde_json::to_string(book)?)
    }
    pub fn authorize(&self, headers: &HeaderMap) -> bool {
        self.root.authorized(headers).is_ok()
    }
    pub fn operator_headers(&self, service: &Service, headers: &mut HeaderMap) {
        headers.insert(
            "authorization",
            format!("Bearer {}", service.token).parse().unwrap(),
        );
    }
    /// Hold all assistant assignment locks while removing a shared Environment identity.
    pub fn guard_sandbox_delete(&self, id: &str, delete: &dyn Fn() -> Result<()>) -> Result<()> {
        let owners = self.owners.lock().unwrap();
        let services = std::iter::once(&self.root)
            .chain(owners.values().map(|o| &o.service))
            .collect::<Vec<_>>();
        fn guard(services: &[&Service], id: &str, delete: &dyn Fn() -> Result<()>) -> Result<()> {
            if let Some((service, rest)) = services.split_first() {
                tasks::guard_sandbox_delete(service, id, || guard(rest, id, delete))
            } else {
                delete()
            }
        }
        guard(&services, id, delete)
    }
    pub fn notifications(
        &self,
        after: Option<&BTreeMap<String, i64>>,
    ) -> Result<pot_protocol::AssistantNotifications> {
        let services = std::iter::once(self.root.clone())
            .chain(
                self.owners
                    .lock()
                    .unwrap()
                    .values()
                    .map(|o| o.service.clone()),
            )
            .collect::<Vec<_>>();
        let mut feed = pot_protocol::AssistantNotifications::default();
        for service in services {
            let owner = service.owner_id().to_owned();
            feed.cursors
                .insert(owner.clone(), service.notification_sequence()?);
            if let Some(after) = after {
                for (sequence, message) in
                    service.notifications(*after.get(&owner).unwrap_or(&0))?
                {
                    feed.messages.push(pot_protocol::AssistantNotification {
                        assistant_id: owner.clone(),
                        sequence,
                        message,
                    });
                }
            }
        }
        feed.messages.sort_by(|a, b| {
            (a.message.created_at_ms, &a.assistant_id, a.sequence).cmp(&(
                b.message.created_at_ms,
                &b.assistant_id,
                b.sequence,
            ))
        });
        feed.messages.truncate(32);
        Ok(feed)
    }
    pub fn definitions(&self) -> Vec<Definition> {
        self.book.lock().unwrap().definitions.clone()
    }
    pub fn service(&self, owner: &str) -> Result<Service> {
        if owner == self.root.usage_scope {
            return Ok(self.root.clone());
        }
        self.owners
            .lock()
            .unwrap()
            .get(owner)
            .map(|o| o.service.clone())
            .ok_or_else(|| anyhow!("Assistant unavailable"))
    }
    pub fn service_for(&self, headers: &HeaderMap) -> Option<Service> {
        if tasks::execution_service(&self.root, headers).is_ok() {
            return Some(self.root.clone());
        }
        self.owners
            .lock()
            .unwrap()
            .values()
            .find(|o| tasks::execution_service(&o.service, headers).is_ok())
            .map(|o| o.service.clone())
    }
    /// Used only after a member request has been authenticated and bound to an assignment.
    pub fn task_service(&self, task: &str) -> Result<Service> {
        let mut services = Vec::new();
        if let Some(service) = tasks::permission_lane(&self.root, task) {
            services.push(service);
        }
        for owner in self.owners.lock().unwrap().values() {
            if let Some(service) = tasks::permission_lane(&owner.service, task) {
                services.push(service);
            }
        }
        anyhow::ensure!(services.len() == 1, "Task owner is missing or ambiguous");
        Ok(services.pop().unwrap())
    }
    pub fn activate(&self) -> Result<()> {
        for d in self.definitions() {
            if d.id != self.root.usage_scope {
                self.start(&d.id)?;
            }
        }
        Ok(())
    }
    fn start(&self, owner: &str) -> Result<()> {
        let (service, rx) = self.build(owner)?;
        orchestration::initialize(&service)?;
        tasks::restore(&service)?;
        let main = bootstrap::start_main(service.clone(), rx);
        let coordinator = tasks::start(service.clone());
        self.owners.lock().unwrap().insert(
            owner.into(),
            Owner {
                service,
                main,
                coordinator,
            },
        );
        Ok(())
    }
    fn build(&self, owner: &str) -> Result<(Service, mpsc::UnboundedReceiver<SendMessage>)> {
        let data = self.root.data.join("assistants").join(owner);
        std::fs::create_dir_all(&data)?;
        let mut store = upgrade::open_store(&data.join("pot-v2.sqlite"), &self.root.data, owner)?;
        if agent::current(&store)?.is_none() {
            let root_inner = self.root.conversation.inner.lock().unwrap();
            if let Some(profiles) = root_inner.store.get(agent::PROFILES)? {
                store.set(agent::PROFILES, &profiles)?;
            }
            if let Some(thread) = agent::current(&root_inner.store)? {
                agent::select_initial(&mut store, owner, thread.config)?;
            }
        }
        // Credentials/configuration belong to this owner's configuration, never its transcript.
        for name in ["providers.json", "execution-limits.json"] {
            let target = data.join(name);
            if !target.exists() && self.root.data.join(name).exists() {
                let bytes = std::fs::read(self.root.data.join(name))?;
                execution::private_json(&target, &serde_json::from_slice(&bytes)?)?;
            }
        }
        let endpoint: RuntimeEndpoint =
            serde_json::from_slice(&std::fs::read(self.root.data.join(RUNTIME_ENDPOINT_FILE))?)?;
        let token = id();
        execution::private_json(
            &data.join(RUNTIME_ENDPOINT_FILE),
            &json!(RuntimeEndpoint {
                token: token.clone(),
                ..endpoint
            }),
        )?;
        let (events, _) = broadcast::channel(256);
        let (commands, rx) = mpsc::unbounded_channel();
        let mut scope = self.root.scope.clone();
        scope.data = data;
        scope.remote_context = Default::default();
        scope.usage_scope = owner.into();
        scope.assistant_id = owner.into();
        scope.token = token;
        scope.workspace = self.root.workspace.join("assistants").join(owner);
        scope.execution = None;
        scope.cancel_notify = Arc::new(tokio::sync::Notify::new());
        std::fs::create_dir_all(&scope.workspace)?;
        let configured = agent::current(&store)?.is_some();
        let service = Service {
            scope,
            conversation: ConversationService {
                inner: Arc::new(Mutex::new(Inner {
                    store,
                    agent: AgentStatus {
                        model: String::new(),
                        phase: if configured {
                            AgentPhase::Ready
                        } else {
                            AgentPhase::NeedsConfiguration
                        },
                        detail: None,
                    },
                    turn: None,
                    model_requests: 0,
                })),
                events,
                commands,
            },
            agent_service: self.root.agent_service.clone(),
            environment_service: self.root.environment_service.clone(),
            tasks: TaskService {
                hub: Arc::new(Mutex::new(tasks::Hub::default())),
                capacity: self.root.tasks.capacity.clone(),
            },
        };
        let _ = crate::assistant_instructions::read(&service)?;
        Ok((service, rx))
    }
    pub fn create(&self, request: &str, name: &str) -> Result<Definition> {
        anyhow::ensure!(!self.root.stopping(), "Runtime is stopping");
        anyhow::ensure!(
            !request.is_empty() && request.len() <= 128,
            "Invalid request identity"
        );
        validate_name(name)?;
        let mut book = self.book.lock().unwrap();
        if let Some(old) = book.definitions.iter().find(|d| d.request_id == request) {
            anyhow::ensure!(old.name == name, "Conflicting assistant creation request");
            let definition = old.clone();
            drop(book);
            if self.service(&definition.id).is_err() {
                self.start(&definition.id)?;
            }
            return Ok(definition);
        }
        anyhow::ensure!(book.definitions.len() < 64, "Assistant limit reached");
        let d = Definition {
            id: id(),
            name: name.into(),
            revision: 1,
            request_id: request.into(),
        };
        book.definitions.push(d.clone());
        if let Err(e) = self.persist(&book) {
            book.definitions.pop();
            return Err(e);
        }
        drop(book);
        self.start(&d.id)?;
        Ok(d)
    }
    pub fn rename(&self, owner: &str, name: &str, revision: u64) -> Result<Definition> {
        validate_name(name)?;
        let mut book = self.book.lock().unwrap();
        let position = book
            .definitions
            .iter()
            .position(|d| d.id == owner)
            .ok_or_else(|| anyhow!("Assistant missing"))?;
        let old = book.definitions[position].clone();
        anyhow::ensure!(
            old.revision == revision,
            "Assistant changed; reload before saving"
        );
        let new = Definition {
            name: name.into(),
            revision: revision
                .checked_add(1)
                .ok_or_else(|| anyhow!("Revision overflow"))?,
            ..old.clone()
        };
        book.definitions[position] = new.clone();
        if let Err(e) = self.persist(&book) {
            book.definitions[position] = old;
            return Err(e);
        }
        Ok(new)
    }
    pub async fn shutdown(&self) {
        let owners = std::mem::take(&mut *self.owners.lock().unwrap());
        for (_, mut owner) in owners {
            tasks::request_shutdown(&owner.service);
            terminal::shutdown(&owner.service).await;
            for handle in [&mut owner.main, &mut owner.coordinator] {
                if tokio::time::timeout(std::time::Duration::from_secs(20), &mut *handle)
                    .await
                    .is_err()
                {
                    handle.abort();
                    let _ = handle.await;
                }
            }
        }
    }
}
impl Drop for Hub {
    fn drop(&mut self) {
        for o in self.owners.get_mut().unwrap().values() {
            o.main.abort();
            o.coordinator.abort();
        }
    }
}
fn validate_name(name: &str) -> Result<()> {
    anyhow::ensure!(
        !name.trim().is_empty()
            && name.chars().count() <= 80
            && !name.chars().any(char::is_control),
        "Invalid assistant name"
    );
    Ok(())
}

impl Hub {
    pub fn rules(&self, owner: &str) -> Result<Value> {
        crate::assistant_instructions::read(&self.service(owner)?)
    }
    pub fn save_rules(&self, owner: &str, text: &str, revision: &str) -> Result<Value> {
        crate::assistant_instructions::save(&self.service(owner)?, text, revision)
    }
}

impl Hub {
    /// Operator projection across assistants. Model tool queries remain owner-scoped.
    pub fn tasks_page(&self, query: &TaskQuery) -> Result<TaskPage> {
        let mut combined = TaskPage::default();
        for definition in self.definitions() {
            let service = self.service(&definition.id)?;
            let mut q = query.clone();
            q.offset = 0;
            q.limit = 50;
            loop {
                let page = tasks::page(&service, &q).map_err(|e| anyhow!(e.1.0.message))?;
                if q.offset == 0 {
                    combined.counts.active += page.counts.active;
                    combined.counts.attention += page.counts.attention;
                    combined.counts.archived += page.counts.archived;
                    if page.selected.is_some() {
                        combined.selected = page.selected.clone();
                    }
                }
                combined.items.extend(page.items);
                match page.next_offset {
                    Some(next) => q.offset = next,
                    None => break,
                }
            }
        }
        anyhow::ensure!((1..=50).contains(&query.limit), "Invalid page size");
        combined.total = combined.items.len();
        combined.offset = query
            .offset
            .min(combined.total.saturating_sub(1) / query.limit * query.limit);
        let end = (combined.offset + query.limit).min(combined.total);
        combined.next_offset = (end < combined.total).then_some(end);
        combined.items = combined.items[combined.offset..end].to_vec();
        Ok(combined)
    }
}

impl Hub {
    pub fn edit(
        &self,
        owner: &str,
        name: &str,
        revision: u64,
        text: &str,
        rules_revision: &str,
    ) -> Result<Value> {
        validate_name(name)?;
        let mut book = self.book.lock().unwrap();
        let position = book
            .definitions
            .iter()
            .position(|d| d.id == owner)
            .ok_or_else(|| anyhow!("Assistant missing"))?;
        let old = book.definitions[position].clone();
        anyhow::ensure!(
            old.revision == revision,
            "Assistant changed; reload before saving"
        );
        let previous = self.rules(owner)?;
        anyhow::ensure!(
            previous["revision"] == rules_revision,
            "AGENTS.md changed; reload before saving"
        );
        let rules = self.save_rules(owner, text, rules_revision)?;
        let definition = Definition {
            name: name.into(),
            revision: revision
                .checked_add(1)
                .ok_or_else(|| anyhow!("Revision overflow"))?,
            ..old.clone()
        };
        book.definitions[position] = definition.clone();
        if let Err(e) = self.persist(&book) {
            book.definitions[position] = old;
            self.save_rules(
                owner,
                previous["text"].as_str().unwrap(),
                rules["revision"].as_str().unwrap(),
            )?;
            return Err(e);
        }
        Ok(json!({"definition":definition,"rules":rules}))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn headers(service: &Service) -> HeaderMap {
        let mut h = HeaderMap::new();
        h.insert(
            "authorization",
            format!("Bearer {}", service.token).parse().unwrap(),
        );
        h
    }
    #[tokio::test]
    async fn tasks_and_approvals_are_scoped_and_capacity_is_shared() {
        let root = tasks::tests::fixture();
        agent::initialize(&root).unwrap();
        let hub = Hub::open(root.clone()).unwrap();
        let a = hub.create("task-owner-a", "A").unwrap();
        let b = hub.create("task-owner-b", "B").unwrap();
        // Stop background loops to exercise deterministic dispatch.
        {
            let owners = hub.owners.lock().unwrap();
            for owner in owners.values() {
                owner.main.abort();
                owner.coordinator.abort();
            }
        }
        let sa = hub.service(&a.id).unwrap();
        let sb = hub.service(&b.id).unwrap();
        let ta = tasks::create(
            State(sa.clone()),
            headers(&sa),
            Json(CreateTask {
                request_id: id(),
                text: "A job".into(),
                resource: "shared".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let tb = tasks::create(
            State(sb.clone()),
            headers(&sb),
            Json(CreateTask {
                request_id: id(),
                text: "B job".into(),
                resource: "shared".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let removed = std::sync::atomic::AtomicBool::new(false);
        let delete = || {
            removed.store(true, std::sync::atomic::Ordering::SeqCst);
            Ok(())
        };
        assert!(hub.guard_sandbox_delete("default", &delete).is_err());
        assert!(!removed.load(std::sync::atomic::Ordering::SeqCst));
        hub.guard_sandbox_delete("unassigned", &delete).unwrap();
        assert!(removed.load(std::sync::atomic::Ordering::SeqCst));
        assert_eq!(ta.assistant_id, a.id);
        assert_eq!(tb.assistant_id, b.id);
        assert!(tasks::control_service(&sa, &tb.id).is_err());
        assert!(tasks::control_service(&sb, &ta.id).is_err());
        let page = hub.tasks_page(&TaskQuery::default()).unwrap();
        assert_eq!(page.total, 2);
        assert_eq!(
            page.items
                .iter()
                .map(|t| t.assistant_id.clone())
                .collect::<Vec<_>>(),
            vec![a.id.clone(), b.id.clone()]
        );
        let (_, worker, request) = tasks::claim(&sa).unwrap().unwrap();
        let approval = pot_agent::permissions::Request {
            owner: ta.id.clone(),
            environment: "host".into(),
            generation: agent::thread(&worker).unwrap().generation,
            turn: request.request_id,
            call_id: "isolated-approval".into(),
            tool: "exec_command".into(),
            args: json!({"cmd":"true","workdir":"/"}),
            proposed_grant: None,
            display: None,
            plugin: None,
        };
        let approval_id = sa
            .environment_service
            .permissions
            .enqueue(approval)
            .unwrap();
        let Json(own) = permissions::inspect(State(sa.clone()), headers(&sa))
            .await
            .unwrap();
        let Json(other) = permissions::inspect(State(sb.clone()), headers(&sb))
            .await
            .unwrap();
        assert_eq!(own["requests"].as_array().unwrap().len(), 1);
        assert!(other["requests"].as_array().unwrap().is_empty());
        let decision =
            || serde_json::from_value(json!({"id":approval_id,"action":"deny"})).unwrap();
        assert!(
            permissions::decide(State(sb.clone()), headers(&sb), Json(decision()))
                .await
                .is_err()
        );
        let _ = permissions::decide(State(sa.clone()), headers(&sa), Json(decision()))
            .await
            .unwrap();
        assert!(
            tasks::claim(&sb).unwrap().is_none(),
            "shared environment cannot be concurrently claimed across assistants"
        );
        tasks::finish(&sa, &ta.id, Err(anyhow!("test finished"))).unwrap();
        assert!(tasks::claim(&sb).unwrap().is_some());
        tasks::finish(&sb, &tb.id, Err(anyhow!("test finished"))).unwrap();
        hub.shutdown().await;
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn notifications_cover_all_assistants_once_and_exclude_worker_and_synthetic_reports() {
        let root = tasks::tests::fixture();
        agent::initialize(&root).unwrap();
        let hub = Hub::open(root.clone()).unwrap();
        let a = hub.create("notification-a", "A").unwrap();
        let b = hub.create("notification-b", "B").unwrap();
        for owner in hub.owners.lock().unwrap().values() {
            owner.main.abort();
            owner.coordinator.abort();
        }
        let sa = hub.service(&a.id).unwrap();
        let sb = hub.service(&b.id).unwrap();
        let initial = hub.notifications(None).unwrap();
        assert!(initial.messages.is_empty());
        for service in [&sa, &sb] {
            let message = Message {
                id: id(),
                conversation_id: service.conversation_id(),
                role: Role::Assistant,
                text: "Actual assistant reply".into(),
                status: MessageStatus::Complete,
                created_at_ms: now(),
                request_id: None,
            };
            service.save(message.clone()).unwrap();
            service.save(message.clone()).unwrap();
            let mut synthetic = message.clone();
            synthetic.id = format!("task-report-{}", id());
            service.save(synthetic).unwrap();
            let mut streaming = message;
            streaming.id = id();
            streaming.status = MessageStatus::Streaming;
            service.save(streaming).unwrap();
        }
        let task = tasks::enqueue(
            &sa,
            &mut sa.tasks.hub.lock().unwrap(),
            CreateTask {
                request_id: "notification-worker".into(),
                text: "worker".into(),
                ..Default::default()
            },
        )
        .unwrap();
        let worker = tasks::owner(&sa, &task.id).unwrap();
        worker
            .save(Message {
                id: id(),
                conversation_id: "main".into(),
                role: Role::Assistant,
                text: "Worker result".into(),
                status: MessageStatus::Complete,
                created_at_ms: now(),
                request_id: None,
            })
            .unwrap();
        let feed = hub.notifications(Some(&initial.cursors)).unwrap();
        assert_eq!(feed.messages.len(), 2);
        assert!(
            feed.messages
                .iter()
                .all(|n| n.assistant_id == a.id || n.assistant_id == b.id)
        );
        assert!(
            hub.notifications(Some(&feed.cursors))
                .unwrap()
                .messages
                .is_empty()
        );
        hub.shutdown().await;
        let reopened = Hub::open(root.clone()).unwrap();
        reopened.activate().unwrap();
        for owner in reopened.owners.lock().unwrap().values() {
            owner.main.abort();
            owner.coordinator.abort();
        }

        assert_eq!(
            reopened
                .notifications(Some(&initial.cursors))
                .unwrap()
                .messages
                .len(),
            2
        );
        reopened.shutdown().await;
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn owners_isolate_context_tasks_permissions_and_rules_and_restore_definitions() {
        let root = tasks::tests::fixture();
        agent::initialize(&root).unwrap();
        let hub = Hub::open(root.clone()).unwrap();
        let a = hub.create("create-a", "A").unwrap();
        let b = hub.create("create-b", "B").unwrap();
        assert_eq!(hub.create("create-a", "A").unwrap().id, a.id);
        assert!(hub.create("create-a", "different").is_err());
        let sa = hub.service(&a.id).unwrap();
        let sb = hub.service(&b.id).unwrap();
        assert_eq!(agent::thread(&sa).unwrap().owner, a.id);
        assert_eq!(agent::thread(&sb).unwrap().owner, b.id);
        sa.save(Message {
            id: id(),
            conversation_id: a.id.clone(),
            role: Role::User,
            text: "private A".into(),
            status: MessageStatus::Complete,
            created_at_ms: now(),
            request_id: None,
        })
        .unwrap();
        assert!(sb.snapshot().unwrap().messages.is_empty());
        assert!(root.snapshot().unwrap().messages.is_empty());
        assert_eq!(sa.snapshot().unwrap().conversation_id, a.id);
        assert_eq!(sb.snapshot().unwrap().conversation_id, b.id);
        assert!(!Arc::ptr_eq(&sa.tasks.hub, &sb.tasks.hub));
        let rules = hub.rules(&a.id).unwrap();
        hub.save_rules(
            &a.id,
            "Route coding to A",
            rules["revision"].as_str().unwrap(),
        )
        .unwrap();
        assert!(
            hub.save_rules(
                &a.id,
                "stale overwrite",
                rules["revision"].as_str().unwrap()
            )
            .is_err()
        );
        assert_eq!(hub.rules(&b.id).unwrap()["text"], "");
        assert!(
            instructions::load(&sa)
                .unwrap()
                .text
                .contains("Route coding to A")
        );
        assert!(
            !instructions::load(&sb)
                .unwrap()
                .text
                .contains("Route coding to A")
        );
        assert!(hub.rename(&a.id, "Renamed", 0).is_err());
        hub.rename(&a.id, "Renamed", 1).unwrap();
        root.stop.send_replace(true);
        hub.shutdown().await;
        drop(hub);
        let restored = Hub::open(root.clone()).unwrap();
        assert_eq!(restored.definitions().len(), 3);
        assert_eq!(
            restored
                .definitions()
                .iter()
                .find(|d| d.id == a.id)
                .unwrap()
                .name,
            "Renamed"
        );
        restored.activate().unwrap();
        assert_eq!(
            restored
                .service(&a.id)
                .unwrap()
                .snapshot()
                .unwrap()
                .messages[0]
                .text,
            "private A"
        );
        restored.shutdown().await;
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
}
