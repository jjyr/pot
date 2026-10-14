//! Durable background task lanes. Each lane owns a Pot session, transcript and
//! cancellation scope. The main conversation retains its own execution slot.
use super::*;
use axum::extract::Path;
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, VecDeque};

#[derive(Clone, Serialize, Deserialize)]
struct Job {
    work_scope_id: String,
    #[serde(default)]
    order: u64,
    request_id: String,
    text: String,
    message_id: String,
    created: i64,
}
#[derive(Clone, Serialize, Deserialize)]
struct Record {
    #[serde(default)]
    assignment: Option<pot_core::Assignment>,
    info: TaskInfo,
    create_request: CreateTask,
    #[serde(default)]
    message_skills: Option<Vec<String>>,
    #[serde(default)]
    route_source: agent::routing::Source,
    queue: VecDeque<Job>,
    active: Option<Job>,
    #[serde(default)]
    interrupt_requested: bool,
    /// Persistent request receipts survive completion and cancellation.
    receipts: Vec<Job>,
}
struct Lane {
    record: Record,
    service: Service,
}
#[derive(Default)]
pub(crate) struct Capacity {
    // Shared across all assistant hubs, held until the run finishes cleanup.
    active: BTreeMap<String, (agent::ResolvedSpec, Option<pot_core::ExecutionTarget>)>,
}
#[derive(Default)]
pub(crate) struct Hub {
    lanes: BTreeMap<String, Lane>,
    pub(crate) schedules: schedules::Book,
}

fn next_order(hub: &Hub) -> u64 {
    hub.lanes
        .values()
        .flat_map(|l| l.record.receipts.iter())
        .map(|j| j.order)
        .max()
        .unwrap_or(0)
        + 1
}
pub(crate) fn serialized(hub: &Hub) -> Result<String> {
    let records: Vec<_> = hub.lanes.values().map(|l| &l.record).collect();
    Ok(serde_json::to_string(
        &json!({"version":3,"tasks":records,"schedules":hub.schedules}),
    )?)
}
pub(crate) fn persist(root: &Service, hub: &Hub) -> Result<()> {
    let root = assistant_message::owner(root)?;
    let encoded = serialized(hub)?;
    let inner = root.conversation.inner.lock().unwrap();
    if inner.store.get("tasks.v2")?.as_deref() != Some(&encoded) {
        inner.store.set("tasks.v2", &encoded)?;
        root.invalidate("tasks");
        root.invalidate("schedules");
    }
    Ok(())
}
pub(crate) fn task_phase(hub: &Hub, task_id: &str) -> Option<TaskPhase> {
    hub.lanes.get(task_id).map(|l| l.record.info.phase.clone())
}
pub(crate) fn remove_uncommitted(hub: &mut Hub, task_id: &str) {
    hub.lanes.remove(task_id);
}
fn child(
    root: &Service,
    task_id: &str,
    execution_target: &pot_core::ExecutionTarget,
    skills: Option<Vec<String>>,
) -> Result<Service> {
    anyhow::ensure!(
        uuid::Uuid::parse_str(task_id).is_ok(),
        "Invalid persisted task ID"
    );
    execution_target.validate()?;

    let data = root.data.join("tasks").join(task_id);
    let (sandbox_id, workspace) = if !execution_target.is_local_to(&root.runtime_id) {
        (
            match &execution_target.target {
                pot_core::ExecutionTargetKind::Host {} => None,
                pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => Some(sandbox_id.clone()),
            },
            data.join("agent-cache"),
        )
    } else {
        match &execution_target.target {
            pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => {
                if pot_sandbox::Manager::is_managed(sandbox_id) {
                    root.execution_service
                        .sandboxes
                        .ensure_managed(sandbox_id)?;
                }
                root.execution_service.sandboxes.assignment(sandbox_id)?;
                (
                    Some(sandbox_id.clone()),
                    root.execution_service
                        .sandboxes
                        .workspace(sandbox_id)
                        .join("tasks")
                        .join(task_id),
                )
            }
            pot_core::ExecutionTargetKind::Host {} => (None, data.join("agent-workspace")),
        }
    };
    std::fs::create_dir_all(&data)?;
    std::fs::create_dir_all(&workspace)?;
    let token = id();
    let (events, _) = broadcast::channel(256);
    let (commands, _) = mpsc::unbounded_channel();
    let service = Service {
        scope: OwnerScope {
            runtime_id: root.runtime_id.clone(),
            assistant_id: root.assistant_id.clone(),
            skill_selection: skills,
            remote_context: Default::default(),
            #[cfg(test)]
            test_tool_executor: root.test_tool_executor.clone(),
            sandbox_id,
            execution_target: Some(execution_target.clone()),
            usage: root.usage.clone(),
            usage_scope: task_id.into(),
            token,
            boot: root.boot.clone(),
            workspace,
            api_budget: root.api_budget.clone(),
            execution: root.execution.clone(),
            #[cfg(test)]
            agent_upstream: root.agent_upstream.clone(),
            is_worker: true,
            data: data.clone(),
            control_config: root.control_config.clone(),
            cancel_notify: Arc::new(tokio::sync::Notify::new()),
            stop: root.stop.clone(),
        },
        conversation: ConversationService {
            inner: Arc::new(Mutex::new(Inner {
                store: Store::open(&data.join("pot-v2.sqlite"))?,
                agent: root.conversation.inner.lock().unwrap().agent.clone(),
                turn: None,
                model_requests: 0,
            })),
            events,
            commands,
        },
        agent_service: AgentService {
            executions: root.agent_service.executions.clone(),
            #[cfg(test)]
            extensions: root.agent_service.extensions.clone(),
            resources: root.agent_service.resources.clone(),
        },
        execution_service: ExecutionService {
            remote: root.execution_service.remote.clone(),
            host_enabled: root.execution_service.host_enabled.clone(),
            host: root.execution_service.host.clone(),
            permissions: root.execution_service.permissions.clone(),
            sandboxes: root.execution_service.sandboxes.clone(),
            terminals: root.execution_service.terminals.clone(),
        },
        tasks: TaskService {
            hub: Arc::new(Mutex::new(Hub::default())),
            capacity: root.tasks.capacity.clone(),
        },
    };
    agent::initialize(&service)?;
    Ok(service)
}
pub(crate) fn restore(root: &Service) -> Result<()> {
    let inner = root.conversation.inner.lock().unwrap();
    let current = inner.store.get("tasks.v2")?;
    drop(inner);
    let (records, book): (Vec<Record>, schedules::Book) = match current {
        Some(value) => {
            let value: Value = serde_json::from_str(&value)?;
            anyhow::ensure!(
                value["version"] == 3,
                "Unsupported task persistence schema; machine-policy Tasks require version 3"
            );
            (
                serde_json::from_value(value["tasks"].clone())?,
                serde_json::from_value(value["schedules"].clone())?,
            )
        }
        None => (vec![], schedules::Book::default()),
    };
    schedules::validate_bindings(root, &book)?;
    for record in &records {
        anyhow::ensure!(
            record.info.assistant_id == root.assistant_id,
            "Task assistant ownership mismatch"
        );
        let execution_target = record
            .info
            .execution_target
            .clone()
            .ok_or_else(|| anyhow!("Task Execution target missing"))?;
        let projected = match &execution_target.target {
            pot_core::ExecutionTargetKind::Host {} => "",
            pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => sandbox_id.as_str(),
        };
        anyhow::ensure!(
            record.info.sandbox_id == projected,
            "Task Execution target projection mismatch"
        );
        let assignment = record
            .assignment
            .as_ref()
            .ok_or_else(|| anyhow!("Task assignment missing"))?;
        assignment.validate_binding(&root.runtime_id, &record.info.id, &execution_target)?;
        if !record.create_request.machine_id.is_empty() {
            anyhow::ensure!(
                record.create_request.machine_id == execution_target.runtime_id,
                "Task request Execution target mismatch"
            );
        }
    }
    let mut current_hub = root.tasks.hub.lock().unwrap();
    {
        let mut capacity = root.tasks.capacity.lock().unwrap();
        for task in current_hub.lanes.keys() {
            capacity.active.remove(task);
        }
    }
    let mut hub = Hub {
        schedules: book,
        ..Hub::default()
    };
    for mut record in records {
        restore_title(&mut record);
        let service = child(
            root,
            &record.info.id,
            record
                .info
                .execution_target
                .as_ref()
                .expect("validated binding"),
            record
                .message_skills
                .clone()
                .or_else(|| record.create_request.skills.clone()),
        )?;
        if let Some(job) = record.active.take() {
            if !record.interrupt_requested && crate::remote_execution::is_remote(&service) {
                // Recover the original input identity. The remote launch journal
                // reconciles that run; this never starts a replacement worker.
                record.queue.push_front(job);
                record.info.phase = TaskPhase::Queued;
            } else if !record.interrupt_requested && usage::paused(&service)?.is_some() {
                record.queue.push_front(job);
                record.info.phase = TaskPhase::Waiting;
            } else {
                record.info.phase = TaskPhase::Interrupted;
            }
            record.info.updated_at_ms = now();
        } else if record.info.phase == TaskPhase::Running {
            record.info.phase = TaskPhase::Interrupted;
        }
        if record.info.phase == TaskPhase::Waiting && usage::paused(&service)?.is_none() {
            record.queue.pop_front();
            record.info.phase = TaskPhase::Interrupted;
        }
        if usage::paused(&service)?.is_none() {
            service.usage.lock().unwrap().clear_wait(&record.info.id)?;
        }
        hub.lanes
            .insert(record.info.id.clone(), Lane { record, service });
    }
    persist(root, &hub)?;
    *current_hub = hub;
    Ok(())
}
fn validate(text: &str, request: &str) -> ApiResult<()> {
    if text.trim().is_empty() || text.len() > 65536 || request.is_empty() || request.len() > 128 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_input",
            "消息为空或超过长度限制",
        ));
    }
    Ok(())
}
fn task_title(text: &str) -> String {
    text.lines().next().unwrap_or("任务").to_owned()
}

fn restore_title(record: &mut Record) {
    let full = task_title(&record.create_request.text);
    // Repair only the exact prefix produced by the old 48-character limit.
    if record.info.title.chars().count() == 48
        && full.chars().take(48).collect::<String>() == record.info.title
    {
        record.info.title = full;
    }
}

fn info(record: &Record) -> TaskInfo {
    let mut i = record.info.clone();
    i.pending = record
        .queue
        .len()
        .saturating_sub(usize::from(record.info.phase == TaskPhase::Waiting));
    i
}
fn lane_info(lane: &Lane) -> TaskInfo {
    let mut task = info(&lane.record);
    if lane.record.active.is_some()
        && task.phase == TaskPhase::Running
        && crate::remote_execution::waiting_reason(&lane.service).is_some()
    {
        task.phase = TaskPhase::Waiting;
        task.remote_waiting = true;
    }
    if let Ok(Some(thread)) = agent::current(&lane.service.conversation.inner.lock().unwrap().store)
    {
        let icon = crate::gateway::executor_icon(&lane.service, &thread.config);
        // Missing old provider configuration must not erase a known historical icon.
        if icon != "custom"
            || task.executor_icon.is_empty()
            || task.routing.as_ref().is_some_and(|r| {
                r.profile != thread.config.source_profile_id || r.provider != thread.config.provider
            })
        {
            task.executor_icon = icon;
        }
    }
    task
}
fn job(request_id: String, text: String, order: u64) -> Job {
    Job {
        work_scope_id: id(),
        order,
        request_id,
        text,
        message_id: id(),
        created: now(),
    }
}
fn user(job: &Job) -> Message {
    Message {
        id: job.message_id.clone(),
        conversation_id: "main".into(),
        role: Role::User,
        text: job.text.clone(),
        status: MessageStatus::Accepted,
        created_at_ms: job.created,
        request_id: Some(job.request_id.clone()),
    }
}
fn missing() -> (StatusCode, Json<ApiError>) {
    error(StatusCode::NOT_FOUND, "task_missing", "任务不存在")
}

fn counts(hub: &Hub) -> TaskCounts {
    let mut counts = TaskCounts::default();
    for lane in hub.lanes.values() {
        match lane.record.info.phase.scope() {
            "active" => counts.active += 1,
            "attention" => counts.attention += 1,
            _ => counts.archived += 1,
        }
    }
    counts
}
fn sort_tasks(items: &mut [TaskInfo]) {
    items.sort_by(|a, b| {
        b.updated_at_ms
            .max(b.created_at_ms)
            .cmp(&a.updated_at_ms.max(a.created_at_ms))
            .then_with(|| b.id.cmp(&a.id))
    });
}
pub(crate) fn page(root: &Service, query: &TaskQuery) -> ApiResult<TaskPage> {
    if !["active", "attention", "archived", "all"].contains(&query.scope.as_str())
        || !(1..=50).contains(&query.limit)
        || query.q.len() > 256
        || query.selected.len() > 128
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_task_query",
            "Invalid scope, query or page size (1–50)",
        ));
    }
    let execution_target: Option<pot_core::ExecutionTarget> = if query.execution_target.is_empty() {
        None
    } else {
        let reference: pot_core::ExecutionTarget = serde_json::from_str(&query.execution_target)
            .map_err(|_| {
                error(
                    StatusCode::BAD_REQUEST,
                    "invalid_task_query",
                    "Invalid execution target filter",
                )
            })?;
        reference.validate().map_err(|_| {
            error(
                StatusCode::BAD_REQUEST,
                "invalid_task_query",
                "Invalid execution target filter",
            )
        })?;
        Some(reference)
    };
    let hub = root.tasks.hub.lock().unwrap();
    let needle = query.q.trim().to_lowercase();
    let mut items: Vec<_> = hub
        .lanes
        .values()
        .filter(|l| {
            let i = lane_info(l);
            query
                .phase
                .as_ref()
                .map(|p| p == &i.phase)
                .unwrap_or(query.scope == "all" || i.phase.scope() == query.scope)
                && execution_target
                    .as_ref()
                    .is_none_or(|r| i.execution_target.as_ref() == Some(r))
                && (needle.is_empty()
                    || i.title.to_lowercase().contains(&needle)
                    || i.id.to_lowercase().contains(&needle))
        })
        .map(lane_info)
        .collect();
    sort_tasks(&mut items);
    let total = items.len();
    let offset = query
        .offset
        .min(total.saturating_sub(1) / query.limit * query.limit);
    let end = offset.saturating_add(query.limit).min(total);
    Ok(TaskPage {
        items: items[offset..end]
            .iter()
            .map(|item| lane_info(&hub.lanes[&item.id]))
            .collect(),
        total,
        offset,
        next_offset: (end < total).then_some(end),
        counts: counts(&hub),
        selected: hub.lanes.get(&query.selected).map(lane_info),
    })
}
pub async fn query(
    State(root): State<Service>,
    h: HeaderMap,
    Query(query): Query<TaskQuery>,
) -> ApiResult<Json<TaskPage>> {
    root.authorized(&h)?;
    Ok(Json(page(&root, &query)?))
}

pub async fn list(State(root): State<Service>, h: HeaderMap) -> ApiResult<Json<Vec<TaskInfo>>> {
    root.authorized(&h)?;
    let hub = root.tasks.hub.lock().unwrap();
    let mut list: Vec<_> = hub.lanes.values().map(lane_info).collect();
    list.sort_by_key(|i| i.created_at_ms);
    Ok(Json(list))
}
pub async fn create(
    State(root): State<Service>,
    h: HeaderMap,
    Json(req): Json<CreateTask>,
) -> ApiResult<Json<TaskInfo>> {
    create_from(root, h, req, agent::routing::Source::User).await
}
pub(crate) async fn create_from(
    root: Service,
    h: HeaderMap,
    req: CreateTask,
    source: agent::routing::Source,
) -> ApiResult<Json<TaskInfo>> {
    root.authorized(&h)?;
    let mut hub = root.tasks.hub.lock().unwrap();
    let existing: std::collections::BTreeSet<_> = hub.lanes.keys().cloned().collect();
    let result = enqueue_from(&root, &mut hub, req, source)?;
    if let Err(e) = persist(&root, &hub) {
        if !existing.contains(&result.id) {
            hub.lanes.remove(&result.id);
        }
        return Err(internal(e));
    }
    Ok(Json(result))
}
pub(crate) fn enqueue(root: &Service, hub: &mut Hub, req: CreateTask) -> ApiResult<TaskInfo> {
    enqueue_from(root, hub, req, agent::routing::Source::Schedule)
}
fn enqueue_from(
    root: &Service,
    hub: &mut Hub,
    req: CreateTask,
    source: agent::routing::Source,
) -> ApiResult<TaskInfo> {
    validate(&req.text, &req.request_id)?;
    if req.skills.as_ref().is_some_and(|skills| {
        skills.len() > 128
            || skills.iter().any(|name| {
                name.is_empty()
                    || name.len() > 64
                    || !name
                        .bytes()
                        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
            })
    }) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "task_skills",
            "Invalid skill selection",
        ));
    }

    if !agent::configured(root) {
        return Err(error(
            StatusCode::NOT_IMPLEMENTED,
            "agent_required",
            "后台任务需要主 Agent 配置",
        ));
    }
    if !["", "shared"].contains(&req.resource.as_str()) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_resource",
            "只支持独立目录或 shared 资源",
        ));
    }
    if let Some(lane) = hub
        .lanes
        .values()
        .find(|l| l.record.create_request.request_id == req.request_id)
    {
        if lane.record.create_request != req || lane.record.route_source != source {
            return Err(error(
                StatusCode::CONFLICT,
                "request_conflict",
                "请求编号已用于其他内容",
            ));
        }
        return Ok(lane_info(lane));
    }
    if hub.lanes.len() >= 1000 {
        return Err(error(
            StatusCode::CONFLICT,
            "task_limit",
            "任务数量达到上限",
        ));
    }
    if source != agent::routing::Source::Tool && req.selection_reason.is_some() {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "selection_source",
            "Selection metadata requires assistant delegation",
        ));
    }
    if req
        .selection_reason
        .as_ref()
        .is_some_and(|v| v.len() > 2048 || v.chars().count() > 512)
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "selection_reason",
            "Selection reason exceeds 512 characters",
        ));
    }
    let (spec, mut routing) =
        agent::routing::resolve(root, req.profile.as_deref(), source, &req.requirements)
            .map_err(|e| error(StatusCode::BAD_REQUEST, "task_profile", &e.to_string()))?;
    routing.selection_reason = req.selection_reason.clone();
    let executor_icon = crate::gateway::executor_icon(root, &spec);
    let refs = task_references(root, &req, source)?;
    let explicit =
        (!req.machine_id.is_empty()).then(|| pot_core::ExecutionTarget::host(&req.machine_id));
    let constrained = refs.machine.is_some() || !refs.tags.is_empty();
    let target = if explicit.is_none() || constrained {
        let machines = crate::remote_execution::port(root)
            .map(|p| p.machines())
            .unwrap_or_default();
        let machines = if machines.is_empty() {
            vec![pot_protocol::MachineRoute {
                runtime_id: root.runtime_id.clone(),
                tags: pot_protocol::references::default_labels(true, std::env::consts::OS),
            }]
        } else {
            machines
        };
        let runtime = pot_protocol::references::select_machine(&machines, &root.runtime_id, &refs)
            .map_err(|e| error(StatusCode::BAD_REQUEST, "task_machine", e))?;
        if let Some(execution_target) = &explicit {
            // A label selector constrains membership, not which matching machine the model chooses.
            let mut fixed = refs.clone();
            fixed.machine = Some(execution_target.runtime_id.clone());
            if refs
                .machine
                .as_ref()
                .is_some_and(|id| id != &execution_target.runtime_id)
                || pot_protocol::references::select_machine(&machines, &root.runtime_id, &fixed)
                    .is_err()
            {
                return Err(error(
                    StatusCode::BAD_REQUEST,
                    "task_machine",
                    "Task target conflicts with the current message references",
                ));
            }
            explicit.clone()
        } else {
            Some(pot_core::ExecutionTarget::sandbox(runtime, "auto"))
        }
    } else {
        explicit
    };
    let task_id = id();
    let machine = target
        .as_ref()
        .map(|t| t.runtime_id.as_str())
        .unwrap_or(&root.runtime_id);
    let execution_target = machine_execution_target(root, machine, &task_id)?;
    let message_skills = (!refs.skills.is_empty()).then_some(refs.skills);
    let selected_skills = message_skills.clone().or_else(|| req.skills.clone());
    let sandbox_id = match &execution_target.target {
        pot_core::ExecutionTargetKind::Host {} => String::new(),
        pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => sandbox_id.clone(),
    };
    let mut first = job(req.request_id.clone(), req.text.clone(), next_order(hub));
    if matches!(source, agent::routing::Source::Tool)
        && let Ok(work) = crate::permissions::work_scope(root)
    {
        first.work_scope_id = work;
    }
    let record = Record {
        assignment: Some(pot_core::Assignment::on_execution_target(
            &root.runtime_id,
            &task_id,
            execution_target.clone(),
        )),
        info: TaskInfo {
            remote_waiting: false,
            execution_target: Some(execution_target.clone()),
            assistant_id: root.assistant_id.clone(),
            sandbox_id: sandbox_id.clone(),
            id: task_id.clone(),
            title: task_title(&req.text),
            phase: TaskPhase::Queued,
            pending: 1,
            created_at_ms: now(),
            updated_at_ms: now(),
            resource: req.resource.clone(),
            routing: Some(routing),
            executor_icon,
        },
        message_skills,
        create_request: req,
        route_source: source,
        queue: VecDeque::from([first.clone()]),
        active: None,
        interrupt_requested: false,
        receipts: vec![first],
    };
    let service = child(root, &task_id, &execution_target, selected_skills).map_err(internal)?;
    {
        let mut inner = service.conversation.inner.lock().unwrap();
        if agent::current(&inner.store).map_err(internal)?.is_none() {
            agent::select_initial(&mut inner.store, &task_id, spec).map_err(internal)?;
        } else {
            agent::assign_new(&mut inner.store, spec).map_err(internal)?;
        }
    }
    let result = info(&record);
    hub.lanes.insert(task_id.clone(), Lane { record, service });
    Ok(result)
}
pub async fn snapshot(
    State(root): State<Service>,
    h: HeaderMap,
    Path(task_id): Path<String>,
) -> ApiResult<Json<Snapshot>> {
    root.authorized(&h)?;
    let hub = root.tasks.hub.lock().unwrap();
    let lane = hub.lanes.get(&task_id).ok_or_else(missing)?;
    let mut snapshot = lane.service.snapshot().map_err(internal)?;
    for job in &lane.record.queue {
        if !snapshot.messages.iter().any(|m| m.id == job.message_id) {
            snapshot.messages.push(user(job));
        }
    }
    snapshot.conversation_id = task_id.clone();
    for m in &mut snapshot.messages {
        m.conversation_id = task_id.clone();
    }
    Ok(Json(snapshot))
}
pub async fn input(
    State(root): State<Service>,
    h: HeaderMap,
    Path(task_id): Path<String>,
    Json(req): Json<TaskInput>,
) -> ApiResult<Json<SendMessageAccepted>> {
    root.authorized(&h)?;
    validate(&req.text, &req.request_id)?;
    let mut hub = root.tasks.hub.lock().unwrap();
    let order = next_order(&hub);
    let lane = hub.lanes.get_mut(&task_id).ok_or_else(missing)?;
    if let Some(old) = lane
        .record
        .receipts
        .iter()
        .find(|j| j.request_id == req.request_id)
    {
        if old.text != req.text {
            return Err(error(
                StatusCode::CONFLICT,
                "request_conflict",
                "请求编号已用于其他内容",
            ));
        }
        return Ok(Json(SendMessageAccepted {
            message_id: old.message_id.clone(),
            duplicate: true,
        }));
    }
    if matches!(
        lane.record.info.phase,
        TaskPhase::Complete | TaskPhase::Failed | TaskPhase::Cancelled | TaskPhase::Interrupted
    ) {
        return Err(error(
            StatusCode::CONFLICT,
            "task_finished",
            "This task has ended. Create a new task to run again with the machine's current execution policy.",
        ));
    }
    if lane.service.conversation.inner.lock().unwrap().agent.phase == AgentPhase::Failed {
        return Err(error(
            StatusCode::CONFLICT,
            "engine_uncertain",
            "Pot 停止状态未确认，请重启 Sandbox 后继续",
        ));
    }
    if lane.record.info.phase == TaskPhase::Running
        && (lane.record.interrupt_requested
            || lane.record.active.is_none()
            || lane
                .service
                .conversation
                .inner
                .lock()
                .unwrap()
                .turn
                .as_ref()
                .is_some_and(|t| t.cancelling))
    {
        return Err(error(
            StatusCode::CONFLICT,
            "task_stopping",
            "Wait for task shutdown cleanup before resuming",
        ));
    }
    if lane.record.queue.len() >= 100 {
        return Err(error(
            StatusCode::CONFLICT,
            "queue_full",
            "待处理指导已达上限",
        ));
    }
    let before = lane.record.clone();
    let mut job = job(req.request_id, req.text, order);
    if let Some(active) = &lane.record.active {
        job.work_scope_id = active.work_scope_id.clone();
    } else if matches!(
        lane.record.info.phase,
        TaskPhase::Waiting | TaskPhase::Queued
    ) {
        if let Some(queued) = lane.record.queue.front() {
            job.work_scope_id = queued.work_scope_id.clone();
        }
    } else {
        for queued in &mut lane.record.queue {
            queued.work_scope_id = job.work_scope_id.clone();
        }
    }
    let result = SendMessageAccepted {
        message_id: job.message_id.clone(),
        duplicate: false,
    };
    lane.record.info.updated_at_ms = now();
    lane.record.queue.push_back(job.clone());
    lane.record.receipts.push(job);
    // An explicit new instruction resumes a stopped lane. Old unconsumed inputs
    // remain ordered ahead of it; interrupted executing input is never replayed.
    if lane.record.active.is_none() && lane.record.info.phase != TaskPhase::Waiting {
        lane.record.info.phase = TaskPhase::Queued;
        lane.record.interrupt_requested = false;
    }
    if let Err(e) = persist(&root, &hub) {
        hub.lanes.get_mut(&task_id).unwrap().record = before;
        return Err(internal(e));
    }
    Ok(Json(result))
}
/// Abandon a task and discard pending guidance; preserve completed results.
pub async fn cancel(
    state: State<Service>,
    headers: HeaderMap,
    task: Path<String>,
) -> ApiResult<Json<Value>> {
    stop_task(state, headers, task, false).await
}
/// Interrupt retains pending guidance and pauses dispatch until explicit new guidance.
pub async fn interrupt(
    state: State<Service>,
    headers: HeaderMap,
    task: Path<String>,
) -> ApiResult<Json<Value>> {
    stop_task(state, headers, task, true).await
}
async fn stop_task(
    State(root): State<Service>,
    h: HeaderMap,
    Path(task_id): Path<String>,
    preserve: bool,
) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    let idle_service = {
        let hub = root.tasks.hub.lock().unwrap();
        let lane = hub.lanes.get(&task_id).ok_or_else(missing)?;
        (lane.record.active.is_none()
            && lane.record.queue.is_empty()
            && (preserve
                || matches!(
                    lane.record.info.phase,
                    TaskPhase::Complete | TaskPhase::Cancelled
                )))
        .then(|| (lane.service.clone(), lane.record.info.phase.clone()))
    };
    if let Some((service, phase)) = idle_service {
        // A completed task can still own background sessions. Preserve its result
        // while explicitly closing those resources instead of silently doing nothing.
        execution_target::finish_idle(&service)
            .await
            .map_err(internal)?;
        return Ok(Json(
            json!({"stopping":false,"phase":phase,"sessions_stopped":true}),
        ));
    }
    let cleanup = {
        let mut hub = root.tasks.hub.lock().unwrap();
        let lane = hub.lanes.get_mut(&task_id).ok_or_else(missing)?;
        if lane.record.active.is_none()
            && lane.record.queue.is_empty()
            && (preserve
                || matches!(
                    lane.record.info.phase,
                    TaskPhase::Complete | TaskPhase::Cancelled
                ))
        {
            return Ok(Json(json!({"stopping":false})));
        }
        if !preserve {
            for j in &lane.record.queue {
                let mut m = user(j);
                m.status = MessageStatus::Cancelled;
                lane.service.save(m).map_err(internal)?;
            }
        } else if lane.record.info.phase == TaskPhase::Waiting {
            // The front entry is the suspended executing input, not fresh guidance.
            // Do not replay its possibly completed effects after explicit interruption.
            if let Some(j) = lane.record.queue.pop_front() {
                let mut m = user(&j);
                m.status = MessageStatus::Cancelled;
                lane.service.save(m).map_err(internal)?;
            }
        }
        lane.record.info.updated_at_ms = now();
        if !preserve {
            lane.record.queue.clear();
        }
        lane.record.interrupt_requested = preserve;
        lane.service
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set("execution.paused", "")
            .map_err(internal)?;
        lane.service
            .usage
            .lock()
            .unwrap()
            .clear_wait(&task_id)
            .map_err(internal)?;
        if lane.record.active.is_some() {
            let mut inner = lane.service.conversation.inner.lock().unwrap();
            if let Some(t) = inner.turn.as_mut() {
                t.cancelling = true;
            }
            lane.service.cancel_notify.notify_one();
        } else {
            // Block dispatch and new guidance until idle resource cleanup is confirmed.
            lane.record.info.phase = TaskPhase::Running;
        }
        let cleanup = lane.record.active.is_none().then(|| lane.service.clone());
        persist(&root, &hub).map_err(internal)?;
        cleanup
    };
    if let Some(service) = cleanup {
        let result = execution_target::finish_idle(&service).await;
        {
            let mut hub = root.tasks.hub.lock().unwrap();
            let lane = hub.lanes.get_mut(&task_id).ok_or_else(missing)?;
            lane.record.info.phase = if result.is_ok() {
                if preserve {
                    TaskPhase::Interrupted
                } else {
                    TaskPhase::Cancelled
                }
            } else {
                TaskPhase::Failed
            };
            lane.record.info.updated_at_ms = now();
            if let Err(error) = &result {
                service.phase(AgentPhase::Failed, Some(error.to_string()));
            }
            persist(&root, &hub).map_err(internal)?;
        }
        result.map_err(internal)?;
        return Ok(Json(
            json!({"stopping":false,"phase":if preserve {"interrupted"} else {"cancelled"},"sessions_stopped":true}),
        ));
    }
    Ok(Json(json!({"stopping":true})))
}

pub(crate) fn send_reference(
    root: &Service,
    task_id: &str,
    request_id: &str,
    text: &str,
) -> ApiResult<Value> {
    let service = control_service(root, task_id)?;
    let reply = crate::turn_control::send_reference(&service, "main", request_id, text)?;
    let phase = task_phase(&root.tasks.hub.lock().unwrap(), task_id).ok_or_else(missing)?;
    Ok(
        json!({"message_id":reply.message_id,"duplicate":reply.duplicate,"task_id":task_id,"phase":phase,"delivery":"model_boundary_or_next_guidance"}),
    )
}

/// A bounded task-state wait. Cursors are fingerprints of current metadata, not history.
pub(crate) async fn wait_tasks(root: &Service, args: &Value) -> ApiResult<Value> {
    use sha2::{Digest, Sha256};
    let object = args
        .as_object()
        .ok_or_else(|| error(StatusCode::BAD_REQUEST, "invalid_wait", "Expected object"))?;
    if object
        .keys()
        .any(|k| !["task_ids", "after", "timeout_ms"].contains(&k.as_str()))
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_wait",
            "Unknown wait argument",
        ));
    }
    let ids: Vec<String> = serde_json::from_value(args["task_ids"].clone())
        .map_err(|_| error(StatusCode::BAD_REQUEST, "invalid_wait", "task_ids required"))?;
    if ids.is_empty()
        || ids.len() > 8
        || ids.iter().any(|id| id.is_empty())
        || ids.iter().collect::<std::collections::BTreeSet<_>>().len() != ids.len()
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_wait",
            "Supply 1-8 distinct task IDs",
        ));
    }
    let after: BTreeMap<String, String> = args
        .get("after")
        .map(|v| serde_json::from_value(v.clone()))
        .transpose()
        .map_err(|_| {
            error(
                StatusCode::BAD_REQUEST,
                "invalid_wait",
                "Invalid after cursors",
            )
        })?
        .unwrap_or_default();
    if after.keys().any(|id| !ids.contains(id))
        || after
            .values()
            .any(|v| v.len() != 64 || !v.bytes().all(|b| b.is_ascii_hexdigit()))
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_wait",
            "Invalid after cursors",
        ));
    }
    let timeout = args
        .get("timeout_ms")
        .map(|v| {
            v.as_u64().filter(|n| *n <= 60000).ok_or_else(|| {
                error(
                    StatusCode::BAD_REQUEST,
                    "invalid_wait",
                    "timeout_ms must be 0-60000",
                )
            })
        })
        .transpose()?
        .unwrap_or(10000);
    let caller_turn = root
        .conversation
        .inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .filter(|t| !t.cancelling)
        .map(|t| t.user_message_id.clone())
        .ok_or_else(|| {
            error(
                StatusCode::CONFLICT,
                "no_turn",
                "Active caller turn required",
            )
        })?;
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_millis(timeout);
    loop {
        if root.stopping()
            || agent::cancelled(root)
            || root
                .conversation
                .inner
                .lock()
                .unwrap()
                .turn
                .as_ref()
                .is_none_or(|t| t.user_message_id != caller_turn)
        {
            return Err(error(
                StatusCode::CONFLICT,
                "wait_cancelled",
                "Caller ended or is stopping",
            ));
        }
        let (tasks, cursors, ready) = {
            let hub = root.tasks.hub.lock().unwrap();
            let mut tasks = vec![];
            let mut cursors = BTreeMap::new();
            let mut ready = false;
            for id in &ids {
                let lane = hub.lanes.get(id).ok_or_else(missing)?;
                let info = lane_info(lane);
                let cursor: String =
                    Sha256::digest(serde_json::to_vec(&info).map_err(|e| internal(e.into()))?)
                        .iter()
                        .map(|b| format!("{b:02x}"))
                        .collect();
                let settled = !matches!(info.phase, TaskPhase::Queued | TaskPhase::Running);
                let changed = after.get(id) != Some(&cursor);
                let observed = settled && changed;
                ready |= observed;
                cursors.insert(id.clone(), cursor.clone());
                tasks.push(json!({"task":info,"cursor":cursor,"ready":observed}));
            }
            (tasks, cursors, ready)
        };
        let timed_out = tokio::time::Instant::now() >= deadline;
        if ready || timed_out {
            return Ok(json!({"tasks":tasks,"cursors":cursors,"timed_out":!ready && timed_out}));
        }
        tokio::select! {
            _=tokio::time::sleep_until(deadline.min(tokio::time::Instant::now()+std::time::Duration::from_millis(50))) => {},
            _=root.stop_requested()=>{},
            _=root.cancel_notify.notified()=>{},
        }
    }
}

// Token chooses exactly one live lane. Main and workers have distinct bearer
// tokens; a late request after cancellation cannot spend another lane's budget.
pub async fn gateway(
    State(root): State<Service>,
    h: HeaderMap,
    body: Json<Value>,
) -> ApiResult<axum::response::Response> {
    crate::gateway::forward(State(execution_service(&root, &h)?), h, body).await
}

pub(crate) fn control_service(root: &Service, task_id: &str) -> ApiResult<Service> {
    root.tasks
        .hub
        .lock()
        .unwrap()
        .lanes
        .get(task_id)
        .map(|lane| lane.service.clone())
        .ok_or_else(missing)
}
pub(crate) fn execution_service(root: &Service, h: &HeaderMap) -> ApiResult<Service> {
    if root.authorized(h).is_ok() {
        Ok(root.clone())
    } else {
        root.tasks
            .hub
            .lock()
            .unwrap()
            .lanes
            .values()
            .find(|l| l.service.authorized(h).is_ok())
            .map(|l| l.service.clone())
            .ok_or_else(|| {
                error(
                    StatusCode::UNAUTHORIZED,
                    "unauthorized",
                    "Runtime token required",
                )
            })
    }
}

/// Claim durably before launching; configured background concurrency and one worker
/// per lane. FIFO among eligible lanes. A shared-resource task holds an
/// exclusive cooperative lease against other shared-resource tasks.
pub(crate) fn claim(root: &Service) -> Result<Option<(String, Service, SendMessage)>> {
    let mut hub = root.tasks.hub.lock().unwrap();
    let mut capacity = root.tasks.capacity.lock().unwrap();
    for lane in hub.lanes.values().filter(|l| l.record.active.is_none()) {
        agent::settings::apply_pending(&lane.service, false)?;
    }
    if capacity.active.len()
        >= execution::execution_config(root.execution.clone().unwrap_or_default(), &root.data)?
            .max_parallel_tasks
    {
        return Ok(None);
    }
    let policy = agent::routing::policy(root)?;
    let active_specs: Vec<_> = capacity
        .active
        .values()
        .map(|(spec, _)| spec.clone())
        .collect();
    let candidate = hub
        .lanes
        .iter()
        .filter(|(_, l)| {
            l.record.active.is_none()
                && (l.record.info.phase == TaskPhase::Queued
                    || (l.record.info.phase == TaskPhase::Waiting
                        && root
                            .usage
                            .lock()
                            .unwrap()
                            .ready(&l.record.info.id, now())
                            .unwrap_or(false)))
                && !l.record.queue.is_empty()
                // Let the normal execution path report a corrupt thread visibly.
                && agent::thread(&l.service)
                    .map(|b| policy.admits(&b.config, &active_specs)).unwrap_or(true)
                && !(l.record.info.resource == "shared" && capacity.active.values().any(|(_, target)| target.as_ref().map(|t| &t.runtime_id) == l.record.info.execution_target.as_ref().map(|t| &t.runtime_id)))
        })
        .min_by_key(|(_, l)| l.record.queue.front().unwrap().order)
        .map(|(id, _)| id.clone());
    let Some(task_id) = candidate else {
        return Ok(None);
    };
    let lane = hub.lanes.get_mut(&task_id).unwrap();
    let before = lane.record.clone();
    let before_service = lane.service.clone();
    let machine = lane
        .record
        .info
        .execution_target
        .as_ref()
        .ok_or_else(|| anyhow!("Task machine missing"))?
        .runtime_id
        .clone();
    let target = machine_execution_target(root, &machine, &task_id)
        .map_err(|e| anyhow!("Cannot prepare task execution: {e:?}"))?;
    lane.record.info.execution_target = Some(target.clone());
    lane.record.info.sandbox_id = match &target.target {
        pot_core::ExecutionTargetKind::Host {} => String::new(),
        pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => sandbox_id.clone(),
    };
    lane.record.assignment = Some(pot_core::Assignment::on_execution_target(
        &root.runtime_id,
        &task_id,
        target.clone(),
    ));
    lane.service.execution_target = Some(target);
    lane.service.sandbox_id =
        (!lane.record.info.sandbox_id.is_empty()).then(|| lane.record.info.sandbox_id.clone());
    lane.service.workspace = match &lane.service.sandbox_id {
        Some(id) if machine == root.runtime_id => root
            .execution_service
            .sandboxes
            .workspace(id)
            .join("tasks")
            .join(&task_id),
        _ => lane.service.data.join("agent-workspace"),
    };
    std::fs::create_dir_all(&lane.service.workspace)?;
    let job = lane.record.queue.pop_front().unwrap();
    lane.record.active = Some(job.clone());
    lane.record.info.updated_at_ms = now();
    lane.record.info.phase = TaskPhase::Running;
    if let Err(e) = persist(root, &hub) {
        let lane = hub.lanes.get_mut(&task_id).unwrap();
        lane.record = before;
        lane.service = before_service;
        return Err(e);
    }
    let lane = hub.lanes.get_mut(&task_id).unwrap();
    lane.service.save(user(&job))?;
    {
        let mut inner = lane.service.conversation.inner.lock().unwrap();
        inner.model_requests = 0;
        inner.store.set("permissions.work", &job.work_scope_id)?;
        inner.turn = Some(TurnState {
            user_message_id: job.message_id.clone(),
            cancelling: false,
        });
    }
    capacity.active.insert(
        task_id.clone(),
        (
            agent::thread(&lane.service)?.config,
            (lane.record.info.resource == "shared")
                .then(|| lane.record.info.execution_target.clone())
                .flatten(),
        ),
    );
    Ok(Some((
        task_id,
        lane.service.clone(),
        SendMessage {
            request_id: job.request_id,
            conversation_id: "main".into(),
            text: job.text,
        },
    )))
}
fn machine_execution_target(
    root: &Service,
    machine: &str,
    task: &str,
) -> ApiResult<pot_core::ExecutionTarget> {
    let policy = root.execution_service.permissions.machine_policy(machine);
    let target = match policy.execution_mode {
        pot_agent::permissions::ExecutionMode::Host => pot_core::ExecutionTarget::host(machine),
        pot_agent::permissions::ExecutionMode::Sandbox => {
            let id = pot_sandbox::Manager::managed_id(task);
            if machine == root.runtime_id {
                root.execution_service
                    .sandboxes
                    .ensure_managed(&id)
                    .map_err(internal)?;
            }
            pot_core::ExecutionTarget::sandbox(machine, &id)
        }
    };
    resolve_execution_target(root, Some(target), "")
}
pub(crate) fn resolve_execution_target(
    root: &Service,
    execution_target: Option<pot_core::ExecutionTarget>,
    sandbox_id: &str,
) -> ApiResult<pot_core::ExecutionTarget> {
    let execution_target = if let Some(execution_target) = &execution_target {
        if !sandbox_id.is_empty() {
            return Err(error(
                StatusCode::BAD_REQUEST,
                "execution_target_conflict",
                "Use execution_target or sandbox_id",
            ));
        }
        execution_target
            .validate()
            .map_err(|e| error(StatusCode::BAD_REQUEST, "execution_target", &e.to_string()))?;
        if !execution_target.is_local_to(&root.runtime_id) {
            crate::remote_execution::port(root)
                .and_then(|port| port.validate(execution_target))
                .map_err(|e| {
                    error(
                        StatusCode::CONFLICT,
                        "execution_target_unavailable",
                        &e.to_string(),
                    )
                })?;
        }
        execution_target.clone()
    } else if sandbox_id.is_empty()
        && root.execution_service.sandboxes.assignment("").is_err()
        && root
            .execution_service
            .host_enabled
            .load(std::sync::atomic::Ordering::SeqCst)
    {
        pot_core::ExecutionTarget::host(&root.runtime_id)
    } else {
        pot_core::ExecutionTarget::sandbox(
            &root.runtime_id,
            &root
                .execution_service
                .sandboxes
                .assignment(sandbox_id)
                .map_err(internal)?,
        )
    };
    match &execution_target.target {
        pot_core::ExecutionTargetKind::Host {}
            if execution_target.is_local_to(&root.runtime_id) =>
        {
            if !root
                .execution_service
                .host_enabled
                .load(std::sync::atomic::Ordering::SeqCst)
            {
                return Err(error(
                    StatusCode::CONFLICT,
                    "execution_target_unavailable",
                    "Host execution resource is disabled",
                ));
            }
        }
        pot_core::ExecutionTargetKind::Sandbox { sandbox_id }
            if execution_target.is_local_to(&root.runtime_id) =>
        {
            root.execution_service
                .sandboxes
                .assignment(sandbox_id)
                .map_err(internal)?;
        }
        _ => {}
    }
    Ok(execution_target)
}
#[cfg(test)]
pub(crate) fn finish(root: &Service, task_id: &str, result: Result<()>) -> Result<()> {
    finish_with_result(root, task_id, result, None)
}
async fn finish_remote(root: &Service, task_id: &str, result: Result<()>) -> Result<()> {
    let scheduled = {
        let hub = root.tasks.hub.lock().unwrap();
        schedules::contains_task(&hub.schedules, task_id)
    };
    let service = control_service(root, task_id).map_err(|e| anyhow!(e.1.0.message))?;
    let remote = service
        .execution_target
        .as_ref()
        .is_some_and(|e| !e.is_local_to(&root.runtime_id));
    let structured = if scheduled && remote && result.is_ok() {
        Some(
            async {
                use base64::Engine;
                let chunk = crate::files::task_chunk(
                    root,
                    task_id,
                    &pot_protocol::FileQuery {
                        path: ".pot-schedule-result.json".into(),
                        ..Default::default()
                    },
                )
                .await?;
                anyhow::ensure!(
                    chunk.size <= 16384 && chunk.offset == 0,
                    "Result exceeds 16 KiB"
                );
                let bytes = base64::engine::general_purpose::STANDARD.decode(chunk.data)?;
                anyhow::ensure!(
                    bytes.len() as u64 == chunk.size,
                    "Incomplete schedule result"
                );
                schedules::parse_result(&bytes)
            }
            .await,
        )
    } else {
        None
    };
    finish_with_result(root, task_id, result, structured)
}
fn finish_with_result(
    root: &Service,
    task_id: &str,
    result: Result<()>,
    structured: Option<Result<schedules::ResultFile>>,
) -> Result<()> {
    let mut hub = root.tasks.hub.lock().unwrap();
    let lane = hub
        .lanes
        .get_mut(task_id)
        .ok_or_else(|| anyhow!("Task missing"))?;
    let job = lane
        .record
        .active
        .as_ref()
        .ok_or_else(|| anyhow!("No active task"))?
        .clone();
    if usage::paused(&lane.service)?.is_some()
        && !lane
            .service
            .conversation
            .inner
            .lock()
            .unwrap()
            .turn
            .as_ref()
            .is_some_and(|t| t.cancelling)
    {
        lane.service.turn(None);
        lane.record.active = None;
        lane.record.queue.push_front(job);
        lane.record.info.phase = TaskPhase::Waiting;
        lane.record.info.updated_at_ms = now();
        persist(root, &hub)?;
        root.tasks.capacity.lock().unwrap().active.remove(task_id);
        return Ok(());
    }
    if let Err(ref e) = result {
        let message = lane
            .service
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .duplicate(&job.request_id)?;
        if let Some(mut m) = message {
            m.status = MessageStatus::Failed;
            lane.service.save(m)?;
        }
        lane.service.save(Message {
            id: id(),
            conversation_id: "main".into(),
            role: Role::System,
            text: format!("任务执行失败：{e}"),
            status: MessageStatus::Failed,
            created_at_ms: now(),
            request_id: None,
        })?;
    }
    let snap = lane.service.snapshot()?;
    // Unconfirmed resource shutdown keeps every scheduling lease until Runtime
    // restart. A cancellation request alone must never release capacity.
    if snap.agent.phase == AgentPhase::Failed {
        lane.record.info.phase = TaskPhase::Failed;
        lane.record.info.updated_at_ms = now();
        return persist(root, &hub);
    }
    let remote_interrupted = lane
        .service
        .conversation
        .inner
        .lock()
        .unwrap()
        .store
        .get("execution.remote_interrupted")?
        .is_some_and(|v| v == "true");
    if remote_interrupted {
        lane.record.interrupt_requested = true;
    }
    let stopped = remote_interrupted || snap.turn.as_ref().is_some_and(|t| t.cancelling);
    let message = snap.messages.iter().find(|m| m.id == job.message_id);
    let mut status = if stopped {
        if lane.record.interrupt_requested {
            TaskPhase::Interrupted
        } else {
            TaskPhase::Cancelled
        }
    } else if result.is_err() || message.is_none_or(|m| m.status != MessageStatus::Complete) {
        TaskPhase::Failed
    } else {
        TaskPhase::Complete
    };
    // Once stopping is requested, queued follow-ups are never auto-started.
    if stopped {
        lane.service
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set("execution.paused", "")?;
        lane.service.usage.lock().unwrap().clear_wait(task_id)?;
        if !lane.record.interrupt_requested {
            for j in lane.record.queue.drain(..) {
                let mut m = user(&j);
                m.status = MessageStatus::Cancelled;
                lane.service.save(m)?;
            }
        }
    }
    let after = snap
        .messages
        .iter()
        .position(|m| m.id == job.message_id)
        .map(|n| n + 1)
        .unwrap_or(snap.messages.len());
    let answer = snap.messages[after..]
        .iter()
        .rev()
        .find(|m| matches!(m.role, Role::Assistant | Role::System))
        .map(|m| m.text.chars().take(1200).collect::<String>())
        .unwrap_or_default();
    let workspace = lane.service.workspace.clone();
    let notice = schedules::complete(
        &mut hub.schedules,
        task_id,
        &mut status,
        &workspace,
        &answer,
        now(),
        structured,
    );
    let label = match status {
        TaskPhase::Complete => "已完成",
        TaskPhase::Cancelled => "已停止",
        TaskPhase::Interrupted => "已中断",
        _ => "未完成",
    };
    let lane = hub.lanes.get_mut(task_id).unwrap();
    // Stable report ID makes recovery/repeated event delivery idempotent.
    let report = Message {
        id: format!("task-report-{}", job.message_id),
        conversation_id: root.conversation_id(),
        role: Role::Assistant,
        text: format!(
            "任务「{}」{}\n{}\n任务编号：{}",
            lane.record.info.title, label, answer, task_id
        ),
        status: MessageStatus::Complete,
        created_at_ms: now(),
        request_id: None,
    };
    lane.service.turn(None);
    lane.record.active = None;
    lane.record.info.updated_at_ms = now();
    lane.record.info.phase = if !lane.record.queue.is_empty() && status == TaskPhase::Complete {
        TaskPhase::Queued
    } else {
        status
    };
    let serialized = serialized(&hub)?;
    if notice.as_ref().is_some_and(|v| v.is_none()) {
        root.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set("tasks.v2", &serialized)?;
        root.tasks.capacity.lock().unwrap().active.remove(task_id);
        return Ok(());
    }
    let mut report = report;
    if let Some(Some(text)) = notice {
        report.text = format!("{}\n任务编号：{}", text, task_id);
    }
    let event = root
        .conversation
        .inner
        .lock()
        .unwrap()
        .store
        .save_with_metadata(report, Some(("tasks.v2", &serialized)))?;
    root.publish(event);
    root.tasks.capacity.lock().unwrap().active.remove(task_id);
    Ok(())
}
pub(crate) fn owner(root: &Service, owner: &str) -> ApiResult<Service> {
    if owner == root.usage_scope {
        return Ok(root.clone());
    }
    if let Ok(hub) = assistant_message::hub(root)
        && let Some(session) = hub.mail_sessions.lock().unwrap().get(owner)
        && session.assistant_id == root.assistant_id
    {
        return Ok(session.clone());
    }
    root.tasks
        .hub
        .lock()
        .unwrap()
        .lanes
        .get(owner)
        .map(|lane| lane.service.clone())
        .ok_or_else(|| error(StatusCode::NOT_FOUND, "owner_missing", "Owner not found"))
}
pub(crate) fn validate_configuration(
    root: &Service,
    owner: &str,
    spec: &agent::ResolvedSpec,
) -> Result<()> {
    if owner == root.usage_scope {
        return Ok(());
    }
    let hub = root.tasks.hub.lock().unwrap();
    let lane = hub
        .lanes
        .get(owner)
        .ok_or_else(|| anyhow!("Task missing"))?;
    agent::routing::check_requirements(spec, &lane.record.create_request.requirements)
}
fn reap_work_permissions(root: &Service) {
    let hub = root.tasks.hub.lock().unwrap();
    let inner = root.conversation.inner.lock().unwrap();
    let mut active: std::collections::BTreeSet<String> = hub
        .lanes
        .values()
        .flat_map(|lane| lane.record.active.iter().chain(lane.record.queue.iter()))
        .map(|job| job.work_scope_id.clone())
        .collect();
    if let Some(turn) = &inner.turn {
        active.insert(format!(
            "{}:{}:{}",
            root.assistant_id, root.usage_scope, turn.user_message_id
        ));
    }
    active.extend(
        root.agent_service
            .resources
            .mail_work
            .lock()
            .unwrap()
            .iter()
            .cloned(),
    );
    root.execution_service
        .permissions
        .retain_work(&root.assistant_id, &active);
}
pub(crate) fn start(root: Service) -> tokio::task::JoinHandle<()> {
    tokio::spawn(async move {
        let mut workers = tokio::task::JoinSet::new();
        let mut ticks = 0u8;
        let mut executor_reaper: Option<tokio::task::JoinHandle<()>> = None;
        let mut next_executor_reap =
            tokio::time::Instant::now() + std::time::Duration::from_secs(30);
        loop {
            if root.stopping() {
                break;
            }
            while workers.try_join_next().is_some() {}
            if tokio::time::Instant::now() >= next_executor_reap {
                reap_work_permissions(&root);
                if executor_reaper
                    .as_ref()
                    .is_none_or(|task| task.is_finished())
                {
                    if let Some(task) = executor_reaper.take() {
                        let _ = task.await;
                    }
                    let service = root.clone();
                    executor_reaper = Some(tokio::spawn(async move {
                        if let Err(error) = execution_target::reap_idle(&service).await {
                            eprintln!("{error}");
                        }
                    }));
                }
                next_executor_reap =
                    tokio::time::Instant::now() + std::time::Duration::from_secs(30);
            }
            if ticks == 0
                && let Err(e) = schedules::tick(&root, now())
            {
                eprintln!("schedule tick: {e}");
            }
            ticks = (ticks + 1) % 10;
            if let Err(e) = agent::settings::apply_pending(&root, false) {
                eprintln!("thread configuration: {e}");
            }
            if let Err(e) = usage::resume_main(&root) {
                eprintln!("usage resume: {e}");
            }
            if let Err(e) = orchestration::deliver(&root) {
                eprintln!("task result delivery: {e}");
            }
            match claim(&root) {
                Ok(Some((task_id, service, req))) => {
                    let root = root.clone();
                    workers.spawn(async move {
                        let result = agent::run(&service, req).await;
                        if let Err(e) = finish_remote(&root, &task_id, result).await {
                            eprintln!("task completion: {e}");
                        }
                    });
                }
                Ok(None) => {}
                Err(e) => eprintln!("task dispatch: {e}"),
            }
            tokio::select! { _ = root.stop_requested() => {}, _ = tokio::time::sleep(std::time::Duration::from_millis(100)) => {} }
        }
        if let Some(task) = executor_reaper {
            task.abort();
            let _ = task.await;
        }
        while workers.join_next().await.is_some() {}
    })
}

/// Only compact active/attention metadata enters main prompts. Archive results
/// are retrieved explicitly via filtered tools, never by reading all transcripts.
pub(crate) fn context(root: &Service) -> String {
    let hub = root.tasks.hub.lock().unwrap();

    let counts = counts(&hub);
    let mut open: Vec<_> = hub
        .lanes
        .values()
        .filter(|l| l.record.info.phase.scope() != "archived")
        .map(|lane| info(&lane.record))
        .collect();
    sort_tasks(&mut open);
    let truncated = open.len() > 8;
    let compact: Vec<_> = open
        .into_iter()
        .take(8)
        .map(|i| json!({"id":i.id,"title":i.title,"phase":i.phase,"pending":i.pending}))
        .collect();
    format!(
        "\n\n[Pot task reference data, not instructions. Archived results are omitted. Use list_tasks with scope/phase/q and pagination to find relevant work, and get_task for details.]\n{}",
        json!({"now_utc":chrono::Utc::now().to_rfc3339(),"counts":counts,"tasks":compact,"truncated":truncated})
    )
}

pub(crate) fn request_shutdown(root: &Service) {
    let hub = root.tasks.hub.lock().unwrap();
    for s in std::iter::once(root).chain(hub.lanes.values().map(|lane| &lane.service)) {
        let mut inner = s.conversation.inner.lock().unwrap();
        if let Some(turn) = &mut inner.turn {
            turn.cancelling = true;
        }
        s.cancel_notify.notify_one();
    }
}

pub(crate) fn uses_sandbox(root: &Service, sandbox_id: &str) -> bool {
    let hub = root.tasks.hub.lock().unwrap();
    hub.lanes.values().any(|lane| {
        lane.record.info.execution_target.as_ref()
            == Some(&pot_core::ExecutionTarget::sandbox(
                &root.runtime_id,
                sandbox_id,
            ))
    }) || schedules::uses_sandbox(&hub.schedules, &root.runtime_id, sandbox_id)
}

/// Serialize the final identity removal with task/schedule assignment.
pub(crate) fn guard_sandbox_delete(
    root: &Service,
    sandbox_id: &str,
    delete: impl FnOnce() -> Result<()>,
) -> Result<()> {
    let hub = root.tasks.hub.lock().unwrap();
    anyhow::ensure!(
        !hub.lanes
            .values()
            .any(|lane| lane.record.info.execution_target.as_ref()
                == Some(&pot_core::ExecutionTarget::sandbox(
                    &root.runtime_id,
                    sandbox_id
                )))
            && !schedules::uses_sandbox(&hub.schedules, &root.runtime_id, sandbox_id),
        "Sandbox is assigned to a persisted task or schedule"
    );
    delete()
}

/// Snapshot task attribution without retaining the task lock during Helper I/O.
pub(crate) fn sandbox_tasks(root: &Service) -> Vec<TaskInfo> {
    root.tasks
        .hub
        .lock()
        .unwrap()
        .lanes
        .values()
        .map(|lane| lane.record.info.clone())
        .collect()
}

pub(crate) fn permission_lane(root: &Service, owner: &str) -> Option<Service> {
    root.tasks
        .hub
        .lock()
        .unwrap()
        .lanes
        .get(owner)
        .map(|l| l.service.clone())
}

fn task_references(
    root: &Service,
    req: &CreateTask,
    source: agent::routing::Source,
) -> ApiResult<pot_protocol::references::References> {
    if source == agent::routing::Source::Schedule {
        return Ok(Default::default());
    }
    let text = if source == agent::routing::Source::Tool {
        let inner = root.conversation.inner.lock().unwrap();
        inner
            .turn
            .as_ref()
            .map(|turn| inner.store.message(&turn.user_message_id))
            .transpose()
            .map_err(internal)?
            .flatten()
            .filter(|m| m.role == Role::User)
            .map(|m| m.text)
            .unwrap_or_default()
    } else {
        req.text.clone()
    };
    pot_protocol::references::References::parse(&text)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "message_references", e))
}

/// Task subscriptions use the same public conversation identity as task snapshots.
pub async fn events(
    State(root): State<Service>,
    headers: HeaderMap,
    Path(task_id): Path<String>,
) -> ApiResult<impl IntoResponse> {
    root.authorized(&headers)?;
    use futures::StreamExt;
    let mut changes = root.changes().subscribe();
    let mut versions = changes.next().await.unwrap_or_default();
    let (mut rx, initial, service) = event_snapshot(&root, &task_id)?;
    let stream = async_stream::stream! {
        yield Ok::<_,Infallible>(SseEvent::default().event("snapshot").json_data(initial).unwrap());
        loop { tokio::select! {
            event = rx.recv() => match event {
                Ok(mut event) => {
                    if let EventKind::Message { message } = &mut event.kind { message.conversation_id = task_id.clone(); }
                    yield Ok(SseEvent::default().json_data(event).unwrap());
                },
                Err(_) => break,
            },
            next = changes.next() => {
                let Some(next) = next else { break };
                let topic = format!("tasks/{}", root.assistant_id);
                if next.get(&topic) != versions.get(&topic) {
                    match event_snapshot(&root, &task_id) {
                        Ok((receiver, current, _)) => { rx = receiver; yield Ok(SseEvent::default().event("snapshot").json_data(current).unwrap()); },
                        Err(_) => break,
                    }
                }
                versions = next;
            },
            _ = service.stop_requested() => break,
        }}
    };
    Ok(Sse::new(stream)
        .keep_alive(KeepAlive::default().interval(std::time::Duration::from_secs(5))))
}

fn event_snapshot(
    root: &Service,
    task_id: &str,
) -> ApiResult<(broadcast::Receiver<Event>, Snapshot, Service)> {
    let hub = root.tasks.hub.lock().unwrap();
    let lane = hub.lanes.get(task_id).ok_or_else(missing)?;
    let inner = lane.service.conversation.inner.lock().unwrap();
    let receiver = lane.service.conversation.events.subscribe();
    let mut snapshot = lane.service.snapshot_locked(&inner).map_err(internal)?;
    for job in &lane.record.queue {
        if !snapshot.messages.iter().any(|m| m.id == job.message_id) {
            snapshot.messages.push(user(job));
        }
    }
    snapshot.conversation_id = task_id.to_owned();
    for message in &mut snapshot.messages {
        message.conversation_id = task_id.to_owned();
    }
    Ok((receiver, snapshot, lane.service.clone()))
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    #[tokio::test]
    async fn rerun_creates_new_task_and_reads_current_machine_policy() {
        let s = fixture();
        let mid = id();
        s.save(Message {
            id: mid.clone(),
            conversation_id: s.conversation_id(),
            role: Role::User,
            text: "start".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some(id()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: mid,
            cancelling: false,
        }));
        let parent = crate::permissions::work_scope(&s).unwrap();
        let created = create_from(
            s.clone(),
            headers(),
            CreateTask {
                request_id: id(),
                text: "task".into(),
                ..Default::default()
            },
            agent::routing::Source::Tool,
        )
        .await
        .unwrap()
        .0;
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        assert_eq!(crate::permissions::work_scope(&worker).unwrap(), parent);
        s.turn(None);
        assert_eq!(crate::permissions::work_scope(&worker).unwrap(), parent);
        finish(&s, &created.id, Err(anyhow!("finished"))).unwrap();
        assert_finished(&s, &created.id).await;
        s.execution_service.permissions.set_machine_policy(
            &s.runtime_id,
            pot_agent::permissions::ExecutionMode::Host,
            pot_agent::permissions::DefaultDecision::Allow,
        );
        let rerun = add(&s, "new work", "").await;
        assert_ne!(rerun.id, created.id);
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        assert_ne!(crate::permissions::work_scope(&worker).unwrap(), parent);
        assert!(matches!(
            worker.execution_target.as_ref().unwrap().target,
            pot_core::ExecutionTargetKind::Host {}
        ));
        assert!(pot_sandbox::Manager::is_managed(&created.sandbox_id));
        finish(&s, &rerun.id, Err(anyhow!("done"))).unwrap();
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn queued_task_reads_policy_at_dispatch_and_active_context_stays_fixed() {
        let root = fixture();
        let first = add(&root, "queued before policy change", "").await;
        assert!(pot_sandbox::Manager::is_managed(&first.sandbox_id));
        root.execution_service.permissions.set_machine_policy(
            &root.runtime_id,
            pot_agent::permissions::ExecutionMode::Host,
            pot_agent::permissions::DefaultDecision::Allow,
        );
        let (_, worker, _) = claim(&root).unwrap().unwrap();
        let host = pot_core::ExecutionTarget::host(&root.runtime_id);
        assert_eq!(worker.execution_target, Some(host.clone()));
        root.execution_service.permissions.set_machine_policy(
            &root.runtime_id,
            pot_agent::permissions::ExecutionMode::Sandbox,
            pot_agent::permissions::DefaultDecision::Ask,
        );
        assert_eq!(
            owner(&root, &first.id).unwrap().execution_target,
            Some(host)
        );
        let second = add(&root, "independent execution", "").await;
        assert_ne!(first.sandbox_id, second.sandbox_id);
        let (_, second_worker, _) = claim(&root).unwrap().unwrap();
        assert!(
            second_worker
                .sandbox_id
                .as_ref()
                .is_some_and(|id| pot_sandbox::Manager::is_managed(id))
        );
        finish(&root, &first.id, Err(anyhow!("done"))).unwrap();
        finish(&root, &second.id, Err(anyhow!("done"))).unwrap();
    }
    #[tokio::test]
    async fn compaction_events_are_authorized_and_confined_to_the_active_lane() {
        let s = fixture();
        let op = id();
        let event = json!({"id":op,"action":"start","reason":"threshold","tokens_before":28000});
        assert_eq!(
            execution::compaction_event(State(s.clone()), HeaderMap::new(), Json(event.clone()))
                .await
                .unwrap_err()
                .0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(
            execution::compaction_event(State(s.clone()), headers(), Json(event.clone()))
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        let task = add(&s, "compact worker", "").await;
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        let mut h = HeaderMap::new();
        h.insert(
            "authorization",
            format!("Bearer {}", worker.token).parse().unwrap(),
        );
        let _ = execution::compaction_event(State(s.clone()), h.clone(), Json(event.clone()))
            .await
            .unwrap();
        main_turn(&s, "main-compaction");
        assert!(
            execution::compaction_event(State(s.clone()), headers(), Json(event.clone()))
                .await
                .is_err()
        );
        assert!(
            execution::compaction_event(
                State(s.clone()),
                headers(),
                Json(json!({"id":op,"action":"complete"}))
            )
            .await
            .is_err()
        );
        worker
            .conversation
            .inner
            .lock()
            .unwrap()
            .turn
            .as_mut()
            .unwrap()
            .cancelling = true;
        assert_eq!(
            execution::compaction_event(
                State(s.clone()),
                h.clone(),
                Json(json!({"id":id(),"action":"start","reason":"threshold"}))
            )
            .await
            .unwrap_err()
            .0,
            StatusCode::CONFLICT
        );
        let _ = execution::compaction_event(
            State(s.clone()),
            h,
            Json(json!({"id":op,"action":"cancelled"})),
        )
        .await
        .unwrap();
        let (scope, state): (String, String) = s
            .usage
            .lock()
            .unwrap()
            .0
            .query_row(
                "SELECT scope,state FROM compactions WHERE id=?",
                [op],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .unwrap();
        assert_eq!(scope, task.id);
        assert_eq!(state, "cancelled");
    }

    #[tokio::test]
    async fn full_task_title_survives_restore_and_repairs_legacy_prefix() {
        let s = fixture();
        let title = "获取网页完整标题及内容，保留所有中日韩文字和 URL https://example.com/a/long/path/that/should/not/be/truncated";
        let task = create(
            State(s.clone()),
            headers(),
            Json(CreateTask {
                request_id: "full-title".into(),
                text: format!("{title}\nAdditional instructions"),
                resource: "".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        assert_eq!(task.title, title);
        {
            let mut hub = s.tasks.hub.lock().unwrap();
            let record = &mut hub.lanes.get_mut(&task.id).unwrap().record;
            record.info.title = title.chars().take(48).collect();
        }
        persist(&s, &s.tasks.hub.lock().unwrap()).unwrap();
        restore(&s).unwrap();
        let mut hub = s.tasks.hub.lock().unwrap();
        let record = &mut hub.lanes.get_mut(&task.id).unwrap().record;
        assert_eq!(record.info.title, title);
        record.info.title = "用户自定义标题".into();
        restore_title(record);
        assert_eq!(record.info.title, "用户自定义标题");
    }

    async fn tool(s: &Service, name: &str, call_id: &str, args: Value) -> ApiResult<Json<Value>> {
        orchestration::invoke(
            State(s.clone()),
            headers(),
            Json(
                serde_json::from_value(json!({"name":name,"call_id":call_id,"args":args})).unwrap(),
            ),
        )
        .await
    }
    fn main_turn(s: &Service, request: &str) {
        let mid = id();
        s.save(Message {
            id: mid.clone(),
            conversation_id: "main".into(),
            role: Role::User,
            text: "Research companies".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some(request.into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: mid,
            cancelling: false,
        }));
    }
    #[tokio::test]
    async fn routing_tools_deduplicate_validate_and_isolate_workers() {
        let s = fixture();
        assert_eq!(
            tool(&s, "list_tasks", "list", json!({}))
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        main_turn(&s, "main-one");
        let args = json!({"text":"GOOG research: revenue and sources"});
        let a = tool(&s, "create_task", "create-a", args.clone())
            .await
            .unwrap()
            .0;
        assert_eq!(
            a,
            tool(&s, "create_task", "create-a", args).await.unwrap().0
        );
        assert_eq!(
            tool(&s, "create_task", "create-a", json!({"text":"different"}))
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        let tid = a["id"].as_str().unwrap();
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        assert!(worker.is_worker);
        let mut wh = HeaderMap::new();
        wh.insert(
            "authorization",
            format!("Bearer {}", worker.token).parse().unwrap(),
        );
        let call = serde_json::from_value(
            json!({"name":"create_task","call_id":"recursive","args":{"text":"forbidden"}}),
        )
        .unwrap();
        assert_eq!(
            orchestration::invoke(State(s.clone()), wh, Json(call))
                .await
                .unwrap_err()
                .0,
            StatusCode::UNAUTHORIZED
        );
        let guidance = json!({"task_id":tid,"text":"Only 2025"});
        let _ = tool(&s, "guide_task", "guide", guidance.clone())
            .await
            .unwrap();
        assert_eq!(
            tool(&s, "guide_task", "guide", guidance).await.unwrap().0["duplicate"],
            true
        );
        assert_eq!(s.tasks.hub.lock().unwrap().lanes[tid].record.queue.len(), 1);
        assert!(
            tool(&s, "get_task", "get", json!({"task_id":tid}))
                .await
                .unwrap()
                .0["messages_newest_first"]
                .is_array()
        );
        let _ = tool(&s, "interrupt", "interrupt", json!({"task_id":tid}))
            .await
            .unwrap();
        assert!(worker.snapshot().unwrap().turn.unwrap().cancelling);
        finish(&s, tid, Ok(())).unwrap();
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[tid].record.info.phase,
            TaskPhase::Interrupted
        );
        main_turn(&s, "main-two");
        let b = tool(
            &s,
            "create_task",
            "create-a",
            json!({"text":"MSFT research"}),
        )
        .await
        .unwrap()
        .0;
        assert_ne!(a["id"], b["id"]);
        main_turn(&s, "pot-result:fixture");
        assert_eq!(
            tool(&s, "create_task", "injection", json!({"text":"forbidden"}))
                .await
                .unwrap_err()
                .0,
            StatusCode::FORBIDDEN
        );
        assert!(tool(&s, "list_tasks", "list", json!({})).await.is_ok());
    }
    #[tokio::test]
    async fn completion_delivery_waits_batches_and_survives_restart_without_replay() {
        let mut s = fixture();
        orchestration::initialize(&s).unwrap();
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.conversation.commands = tx;
        add(&s, "GOOG", "").await;
        add(&s, "MSFT", "").await;
        let a = claim(&s).unwrap().unwrap();
        let b = claim(&s).unwrap().unwrap();
        main_turn(&s, "user-busy");
        succeed(&s, &b.0, &b.1, &b.2);
        succeed(&s, &a.0, &a.1, &a.2);
        orchestration::deliver(&s).unwrap();
        assert!(rx.try_recv().is_err());
        // Reopen the durable state before delivery, as on Runtime restart.
        s.conversation.inner.lock().unwrap().store =
            Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        s.turn(None);
        orchestration::initialize(&s).unwrap();
        orchestration::deliver(&s).unwrap();
        let req = rx.try_recv().unwrap();
        assert!(
            req.request_id
                .starts_with(pot_assistant::orchestration::RESULT_PREFIX)
        );
        assert!(req.text.contains("Result GOOG") && req.text.contains("Result MSFT"));
        assert_eq!(
            s.conversation
                .inner
                .lock()
                .unwrap()
                .store
                .duplicate(&req.request_id)
                .unwrap()
                .unwrap()
                .role,
            Role::System
        );
        orchestration::deliver(&s).unwrap();
        assert!(rx.try_recv().is_err());
        s.turn(None);
        s.conversation.inner.lock().unwrap().store =
            Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        orchestration::deliver(&s).unwrap();
        assert!(rx.try_recv().is_err());
        assert_eq!(
            s.conversation
                .inner
                .lock()
                .unwrap()
                .store
                .duplicate(&req.request_id)
                .unwrap()
                .unwrap()
                .status,
            MessageStatus::Failed
        );
    }
    #[tokio::test]
    async fn upgrade_does_not_summarize_old_reports_and_budget_defers_new_results() {
        let mut s = fixture();
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.conversation.commands = tx;
        add(&s, "historical", "").await;
        let a = claim(&s).unwrap().unwrap();
        succeed(&s, &a.0, &a.1, &a.2);
        orchestration::initialize(&s).unwrap();
        orchestration::deliver(&s).unwrap();
        assert!(rx.try_recv().is_err());
        add(&s, "new", "").await;
        let a = claim(&s).unwrap().unwrap();
        finish(&s, &a.0, Err(anyhow!("observed failure"))).unwrap();
        s.api_budget = Some(budget::BudgetSettings {
            namespace: "test-budget",
            data: s.data.clone(),
            limit: 0,
            max_output_tokens: 128,
        });
        orchestration::deliver(&s).unwrap();
        assert!(rx.try_recv().is_err());
        s.api_budget.as_mut().unwrap().limit = 1;
        orchestration::deliver(&s).unwrap();
        let req = rx.try_recv().unwrap();
        assert!(req.text.contains("observed failure"));
        assert!(!req.text.contains("Result historical"));
    }

    #[tokio::test]
    async fn task_file_download_checks_owner_and_streams_exact_bytes() {
        let s = fixture();
        let task = add(&s, "Download fixture", "").await;
        let worker = control_service(&s, &task.id).unwrap();
        let bytes = vec![0xab; crate::files::CHUNK_BYTES + 41];
        std::fs::write(worker.workspace.join("报告.bin"), &bytes).unwrap();
        let mut headers = HeaderMap::new();
        headers.insert(
            "authorization",
            format!("Bearer {}", s.token).parse().unwrap(),
        );
        let query = pot_protocol::FileQuery {
            path: "报告.bin".into(),
            ..Default::default()
        };
        let response = crate::files::download(
            State(s.clone()),
            headers.clone(),
            Path(task.id.clone()),
            Query(query.clone()),
        )
        .await
        .unwrap();
        assert_eq!(
            response.headers()["content-length"],
            bytes.len().to_string()
        );
        assert!(
            response.headers()["content-disposition"]
                .to_str()
                .unwrap()
                .starts_with("attachment; filename*=UTF-8''")
        );
        let body = axum::body::to_bytes(response.into_body(), bytes.len() + 1)
            .await
            .unwrap();
        assert_eq!(body.as_ref(), bytes.as_slice());
        let other = fixture();
        let mut other_headers = HeaderMap::new();
        other_headers.insert(
            "authorization",
            format!("Bearer {}", other.token).parse().unwrap(),
        );
        assert!(
            crate::files::chunk(
                State(other.clone()),
                other_headers,
                Path(task.id.clone()),
                Query(query.clone())
            )
            .await
            .is_err()
        );
        assert!(
            crate::files::chunk(
                State(s.clone()),
                HeaderMap::new(),
                Path(task.id.clone()),
                Query(query.clone())
            )
            .await
            .is_err()
        );
        let mut worker_headers = HeaderMap::new();
        worker_headers.insert(
            "authorization",
            format!("Bearer {}", worker.token).parse().unwrap(),
        );
        assert!(
            crate::files::chunk(State(worker), worker_headers, Path(task.id), Query(query))
                .await
                .is_err()
        );
        for root in [s.data.parent().unwrap(), other.data.parent().unwrap()] {
            std::fs::remove_dir_all(root).unwrap();
        }
    }

    #[tokio::test]
    async fn task_pages_match_exact_execution_target_before_pagination() {
        let s = fixture();
        let targets = [
            pot_core::ExecutionTarget::host("machine-a"),
            pot_core::ExecutionTarget::sandbox("machine-a", "build"),
            pot_core::ExecutionTarget::sandbox("machine-b", "build"),
        ];
        for n in 0..9 {
            let task = add(&s, &format!("Execution target task {n}"), "").await;
            s.tasks
                .hub
                .lock()
                .unwrap()
                .lanes
                .get_mut(&task.id)
                .unwrap()
                .record
                .info
                .execution_target = Some(targets[n % 3].clone());
        }
        for target in &targets {
            let query = TaskQuery {
                scope: "all".into(),
                execution_target: serde_json::to_string(target).unwrap(),
                limit: 2,
                ..Default::default()
            };
            let first = page(&s, &query).unwrap();
            assert_eq!(first.total, 3);
            assert_eq!(first.items.len(), 2);
            assert!(
                first
                    .items
                    .iter()
                    .all(|i| i.execution_target.as_ref() == Some(target))
            );
            let last = page(
                &s,
                &TaskQuery {
                    offset: first.next_offset.unwrap(),
                    ..query
                },
            )
            .unwrap();
            assert_eq!(last.items.len(), 1);
            assert_eq!(last.items[0].execution_target.as_ref(), Some(target));
            assert!(first.items.iter().all(|i| i.id != last.items[0].id));
            assert!(last.next_offset.is_none());
        }
        for invalid in ["broken", r#"{"runtime_id":"","target":{"kind":"host"}}"#] {
            assert!(
                page(
                    &s,
                    &TaskQuery {
                        execution_target: invalid.into(),
                        ..Default::default()
                    }
                )
                .is_err()
            );
        }
    }

    #[tokio::test]
    async fn task_pages_filter_all_phases_search_and_bound_results() {
        let s = fixture();
        for n in 0..27 {
            let task = add(&s, &format!("GOOG 财报 {n:02}"), "").await;
            let mut hub = s.tasks.hub.lock().unwrap();
            let i = &mut hub.lanes.get_mut(&task.id).unwrap().record.info;
            i.phase = match n % 6 {
                0 => TaskPhase::Queued,
                1 => TaskPhase::Running,
                2 => TaskPhase::Complete,
                3 => TaskPhase::Failed,
                4 => TaskPhase::Cancelled,
                _ => TaskPhase::Interrupted,
            };
            // Tie timestamps deliberately; ID must stabilize ordering.
            i.created_at_ms = 1;
            i.updated_at_ms = 1;
        }
        let first = page(&s, &TaskQuery::default()).unwrap();
        assert_eq!(first.total, 10);
        assert_eq!(first.items.len(), 10);
        assert_eq!(
            first.counts,
            TaskCounts {
                active: 10,
                attention: 8,
                archived: 9
            }
        );
        assert!(first.items.iter().all(|i| i.phase.scope() == "active"));
        let all = TaskQuery {
            scope: "all".into(),
            limit: 7,
            ..Default::default()
        };
        let mut ids = Vec::new();
        let mut offset = 0;
        loop {
            let p = page(
                &s,
                &TaskQuery {
                    offset,
                    ..all.clone()
                },
            )
            .unwrap();
            assert!(p.items.len() <= 7);
            ids.extend(p.items.into_iter().map(|i| i.id));
            match p.next_offset {
                Some(next) => offset = next,
                None => break,
            }
        }
        assert_eq!(ids.len(), 27);
        assert_eq!(
            ids.iter().collect::<std::collections::BTreeSet<_>>().len(),
            27
        );
        let archived = page(
            &s,
            &TaskQuery {
                scope: "archived".into(),
                ..Default::default()
            },
        )
        .unwrap();
        let selected = archived.items[0].id.clone();
        let active = page(
            &s,
            &TaskQuery {
                selected: selected.clone(),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(active.selected.unwrap().id, selected);
        assert!(active.items.iter().all(|i| i.id != selected));
        let exact = page(
            &s,
            &TaskQuery {
                phase: Some(TaskPhase::Complete),
                q: "goog 财报".into(),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(exact.total, 5);
        assert!(exact.items.iter().all(|i| i.phase == TaskPhase::Complete));
        assert_eq!(
            page(
                &s,
                &TaskQuery {
                    scope: "all".into(),
                    q: selected,
                    ..Default::default()
                }
            )
            .unwrap()
            .total,
            1
        );
        assert_eq!(
            page(
                &s,
                &TaskQuery {
                    q: "missing".into(),
                    ..Default::default()
                }
            )
            .unwrap()
            .total,
            0
        );
        let last = page(
            &s,
            &TaskQuery {
                offset: usize::MAX,
                ..all.clone()
            },
        )
        .unwrap();
        assert_eq!(last.offset, 21);
        assert_eq!(last.items.len(), 6);
        for limit in [0, 51, usize::MAX] {
            assert!(
                page(
                    &s,
                    &TaskQuery {
                        limit,
                        ..Default::default()
                    }
                )
                .is_err()
            );
        }
        assert!(
            page(
                &s,
                &TaskQuery {
                    scope: "unknown".into(),
                    ..Default::default()
                }
            )
            .is_err()
        );
    }
    #[tokio::test]
    async fn a_thousand_archived_tasks_do_not_expand_default_model_context() {
        let s = fixture();
        let initial = add(&s, "historical report", "").await;
        {
            let mut hub = s.tasks.hub.lock().unwrap();
            let lane = hub.lanes.get(&initial.id).unwrap();
            let template = lane.record.clone();
            let worker = lane.service.clone();
            hub.lanes.clear();
            for n in 0..1000 {
                let mut record = template.clone();
                record.info.id = id();
                record.info.title = format!("History {n}");
                record.info.phase = TaskPhase::Complete;
                hub.lanes.insert(
                    record.info.id.clone(),
                    Lane {
                        record,
                        service: worker.clone(),
                    },
                );
            }
        }
        let default = page(&s, &TaskQuery::default()).unwrap();
        assert!(default.items.is_empty());
        assert_eq!(default.counts.archived, 1000);
        let archive = page(
            &s,
            &TaskQuery {
                scope: "archived".into(),
                limit: 20,
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(archive.items.len(), 20);
        assert_eq!(archive.next_offset, Some(20));
        let prompt = context(&s);
        assert!(prompt.len() < 600);
        assert!(!prompt.contains("History"));
    }
    #[tokio::test]
    async fn archive_rejects_guidance_and_context_omits_history() {
        let s = fixture();
        let completed = add(&s, "ARCHIVED_TITLE", "").await;
        let a = claim(&s).unwrap().unwrap();
        succeed(&s, &a.0, &a.1, &a.2);
        let cancelled = add(&s, "CANCELLED_TITLE", "").await;
        let _ = cancel(State(s.clone()), headers(), Path(cancelled.id.clone()))
            .await
            .unwrap();
        let failed = add(&s, "NEEDS_ATTENTION", "").await;
        let a = claim(&s).unwrap().unwrap();
        finish(&s, &a.0, Err(anyhow!("failure"))).unwrap();
        let ctx = context(&s);
        assert!(!ctx.contains("ARCHIVED_TITLE") && !ctx.contains("CANCELLED_TITLE"));
        assert!(ctx.contains("NEEDS_ATTENTION"));
        assert_eq!(
            page(
                &s,
                &TaskQuery {
                    scope: "archived".into(),
                    ..Default::default()
                }
            )
            .unwrap()
            .total,
            2
        );
        assert_eq!(
            page(
                &s,
                &TaskQuery {
                    scope: "attention".into(),
                    ..Default::default()
                }
            )
            .unwrap()
            .items[0]
                .id,
            failed.id
        );
        assert_finished(&s, &completed.id).await;
        assert!(page(&s, &TaskQuery::default()).unwrap().items.is_empty());
        assert_eq!(
            page(
                &s,
                &TaskQuery {
                    scope: "archived".into(),
                    ..Default::default()
                }
            )
            .unwrap()
            .total,
            2
        );
        main_turn(&s, "filtered-tools");
        let output = tool(
            &s,
            "list_tasks",
            "archived",
            json!({"scope":"archived","q":"cancelled"}),
        )
        .await
        .unwrap()
        .0;
        assert_eq!(output["total"], 1);
        assert_eq!(output["items"][0]["phase"], "cancelled");
        assert!(!output.to_string().contains("messages"));
        let detail = tool(&s, "get_task", "detail", json!({"task_id":cancelled.id}))
            .await
            .unwrap()
            .0;
        assert_eq!(detail["task"]["phase"], "cancelled");
        // Persistence retains the actual execution phase, not an ambiguous archive flag.
        let mut reopened = s.clone();
        reopened.tasks.hub = Arc::new(Mutex::new(Hub::default()));
        restore(&reopened).unwrap();
        assert_eq!(
            page(
                &reopened,
                &TaskQuery {
                    scope: "archived".into(),
                    ..Default::default()
                }
            )
            .unwrap()
            .total,
            2
        );
        assert_eq!(page(&reopened, &TaskQuery::default()).unwrap().total, 0);
    }

    #[tokio::test]
    async fn allowance_wait_releases_slot_survives_restore_and_cancels() {
        let mut s = fixture();
        s.execution.as_mut().unwrap().max_parallel_tasks = 1;
        let a = add(&s, "waits", "").await;
        let (_, lane, req) = claim(&s).unwrap().unwrap();
        let mid = lane
            .conversation
            .inner
            .lock()
            .unwrap()
            .turn
            .as_ref()
            .unwrap()
            .user_message_id
            .clone();
        lane.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set("execution.paused", &serde_json::to_string(&req).unwrap())
            .unwrap();
        s.usage
            .lock()
            .unwrap()
            .set_policy(&usage::Policy {
                period: "day".into(),
                limit: 1,
                ..usage::Policy::default()
            })
            .unwrap();
        s.usage
            .lock()
            .unwrap()
            .wait(&a.id, &mid, now(), 100)
            .unwrap();
        finish(&s, &a.id, Ok(())).unwrap();
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &a.id),
            Some(TaskPhase::Waiting)
        );
        assert!(claim(&s).unwrap().is_none());
        let b = add(&s, "can use the freed slot", "").await;
        assert_eq!(claim(&s).unwrap().unwrap().0, b.id);
        s.tasks.hub = Arc::new(Mutex::new(Hub::default()));
        restore(&s).unwrap();
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &a.id),
            Some(TaskPhase::Waiting)
        );
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &b.id),
            Some(TaskPhase::Interrupted)
        );
        let _ = guide(&s, &a.id, "queued-guidance", "extra guidance").await;
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &a.id),
            Some(TaskPhase::Waiting)
        );
        let _ = cancel(State(s.clone()), headers(), Path(a.id.clone()))
            .await
            .unwrap();
        s.usage
            .lock()
            .unwrap()
            .set_policy(&usage::Policy::default())
            .unwrap();
        assert!(claim(&s).unwrap().is_none());
        assert!(s.usage.lock().unwrap().waiting(&a.id).unwrap().is_none());
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    pub(crate) fn headers() -> HeaderMap {
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        h
    }
    fn routing_profiles(s: &Service) {
        agent::initialize(s).unwrap();
        let base = agent::Profile::api(s.execution.as_ref().unwrap());
        let mut alternate = base.clone();
        alternate.id = "alternate".into();
        let mut other = base.clone();
        other.id = "other".into();
        other.provider = "other".into();
        other.auth_ref = "provider:other".into();
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&vec![base, alternate, other]).unwrap(),
            )
            .unwrap();
        crate::tests::providers(
            s,
            json!([{"id":"other","endpoint":"https://example.com/model","allowed_hosts":["example.com"],"api_key":"test-only","protocol":"unknown"}]),
        );
    }
    #[tokio::test]
    async fn executor_icon_follows_task_binding_without_following_global_profile_edits() {
        let s = fixture();
        let mut s = s;
        s.agent_upstream = "https://api.deepseek.com/chat/completions".into();
        let task = add(&s, "original task", "").await;
        assert_eq!(task.executor_icon, "deepseek");
        let mut replacement = agent::Profile::api(s.execution.as_ref().unwrap());
        replacement.provider = "openai".into();
        replacement.auth_ref = "provider:openai".into();
        crate::tests::providers(
            &s,
            json!([{"id":"openai","endpoint":"https://api.openai.com/v1/chat/completions","allowed_hosts":["api.openai.com"],"api_key":"test-only","protocol":"openai_chat"}]),
        );
        replacement.revision = 2;
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&vec![replacement.clone()]).unwrap(),
            )
            .unwrap();
        assert_eq!(
            page(&s, &TaskQuery::default()).unwrap().items[0].executor_icon,
            "deepseek"
        );
        let worker = s.tasks.hub.lock().unwrap().lanes[&task.id].service.clone();
        agent::assign_new(
            &mut worker.conversation.inner.lock().unwrap().store,
            replacement.resolve().unwrap(),
        )
        .unwrap();
        assert_eq!(
            page(&s, &TaskQuery::default()).unwrap().items[0].executor_icon,
            "openai"
        );
        persist(&s, &s.tasks.hub.lock().unwrap()).unwrap();
        restore(&s).unwrap();
        assert_eq!(
            page(&s, &TaskQuery::default()).unwrap().items[0].executor_icon,
            "openai"
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn profile_routes_are_pinned_authorized_and_idempotent() {
        let s = fixture();
        routing_profiles(&s);
        let request = CreateTask {
            request_id: "routed".into(),
            text: "test".into(),
            profile: Some("alternate".into()),
            ..Default::default()
        };
        assert!(
            create_from(
                s.clone(),
                headers(),
                request.clone(),
                agent::routing::Source::Tool
            )
            .await
            .is_err()
        );
        assert!(s.tasks.hub.lock().unwrap().lanes.is_empty());
        let policy = agent::routing::Policy {
            allowed_tool_profiles: ["alternate".into()].into(),
            ..Default::default()
        };
        assert!(
            agent::routing::configure(State(s.clone()), HeaderMap::new(), Json(policy.clone()))
                .await
                .is_err()
        );
        let _ = agent::routing::configure(State(s.clone()), headers(), Json(policy.clone()))
            .await
            .unwrap();
        assert!(
            agent::routing::configure(State(s.clone()), headers(), Json(policy))
                .await
                .is_err()
        );
        let task = create_from(
            s.clone(),
            headers(),
            request.clone(),
            agent::routing::Source::Tool,
        )
        .await
        .unwrap()
        .0;
        assert_eq!(task.routing.as_ref().unwrap().reason, "tool_hint");
        assert_eq!(
            create_from(
                s.clone(),
                headers(),
                request.clone(),
                agent::routing::Source::Tool
            )
            .await
            .unwrap()
            .0
            .id,
            task.id
        );
        let mut conflicting = request.clone();
        conflicting.profile = None;
        assert_eq!(
            create_from(
                s.clone(),
                headers(),
                conflicting,
                agent::routing::Source::Tool
            )
            .await
            .unwrap_err()
            .0,
            StatusCode::CONFLICT
        );
        let mut profiles = agent::profiles(&s.conversation.inner.lock().unwrap().store).unwrap();
        profiles[1].revision = 2;
        profiles[1].model = "new-model".into();
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&profiles).unwrap(),
            )
            .unwrap();
        restore(&s).unwrap();
        let worker = owner(&s, &task.id).unwrap();
        assert_eq!(
            agent::thread(&worker)
                .unwrap()
                .config
                .source_profile_revision,
            1
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&task.id]
                .record
                .info
                .routing,
            task.routing
        );
        for requirements in [TaskRequirements {
            desktop: true,
            ..Default::default()
        }] {
            assert!(
                create(
                    State(s.clone()),
                    headers(),
                    Json(CreateTask {
                        request_id: id(),
                        requirements,
                        ..request.clone()
                    })
                )
                .await
                .is_err()
            );
        }
        assert!(
            create(
                State(s.clone()),
                headers(),
                Json(CreateTask {
                    request_id: id(),
                    profile: Some("other".into()),
                    requirements: TaskRequirements {
                        exact_budget: true,
                        ..Default::default()
                    },
                    ..request
                })
            )
            .await
            .is_err()
        );
    }
    #[tokio::test]
    async fn profile_and_provider_caps_skip_blocked_lanes_and_hold_cancelling_slots() {
        let mut s = fixture();
        s.execution.as_mut().unwrap().max_parallel_tasks = 3;
        routing_profiles(&s);
        let policy = agent::routing::Policy {
            profile_limits: [("pot-default".into(), 1)].into(),
            provider_limits: [("deepseek".into(), 2)].into(),
            ..Default::default()
        };
        let _ = agent::routing::configure(State(s.clone()), headers(), Json(policy))
            .await
            .unwrap();
        let mut tasks = Vec::new();
        for profile in [
            "pot-default",
            "pot-default",
            "alternate",
            "alternate",
            "other",
        ] {
            tasks.push(
                create(
                    State(s.clone()),
                    headers(),
                    Json(CreateTask {
                        request_id: id(),
                        text: "job".into(),
                        profile: Some(profile.into()),
                        ..Default::default()
                    }),
                )
                .await
                .unwrap()
                .0,
            );
        }
        let a = claim(&s).unwrap().unwrap();
        assert_eq!(a.0, tasks[0].id);
        let b = claim(&s).unwrap().unwrap();
        assert_eq!(b.0, tasks[2].id);
        let c = claim(&s).unwrap().unwrap();
        assert_eq!(c.0, tasks[4].id);
        assert!(claim(&s).unwrap().is_none());
        let _ = cancel(State(s.clone()), headers(), Path(a.0.clone()))
            .await
            .unwrap();
        succeed(&s, &c.0, &c.1, &c.2);
        assert!(
            claim(&s).unwrap().is_none(),
            "cancelling lane still holds profile/provider capacity"
        );
        finish(&s, &a.0, Ok(())).unwrap();
        assert_eq!(claim(&s).unwrap().unwrap().0, tasks[1].id);
    }
    #[tokio::test]
    async fn schedule_resolves_each_occurrence_and_overrides_worker_default() {
        let s = fixture();
        routing_profiles(&s);
        let created = schedules::apply(&s, json!({"request_id":"schedule-profile","action":"create","spec":{"title":"plan","goal":"goal","kind":"interval","every_minutes":1,"profile":"alternate"}}), now()).unwrap();
        let sid = created["id"].as_str().unwrap();
        let first = schedules::apply(
            &s,
            json!({"request_id":"run-one","action":"run","schedule_id":sid}),
            now(),
        )
        .unwrap();
        let tid = first["task_id"].as_str().unwrap();
        assert_eq!(
            agent::thread(&owner(&s, tid).unwrap())
                .unwrap()
                .config
                .source_profile_id,
            "alternate"
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[tid]
                .record
                .info
                .routing
                .as_ref()
                .unwrap()
                .reason,
            "schedule"
        );
        let _ = cancel(State(s.clone()), headers(), Path(tid.into()))
            .await
            .unwrap();
        let mut profiles = agent::profiles(&s.conversation.inner.lock().unwrap().store).unwrap();
        profiles[1].revision = 2;
        profiles[1].model = "later".into();
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&profiles).unwrap(),
            )
            .unwrap();
        let second = schedules::apply(
            &s,
            json!({"request_id":"run-two","action":"run","schedule_id":sid}),
            now(),
        )
        .unwrap();
        let thread =
            agent::thread(&owner(&s, second["task_id"].as_str().unwrap()).unwrap()).unwrap();
        assert_eq!(thread.config.source_profile_revision, 2);
        assert_eq!(thread.config.model, "later");
        assert_eq!(
            agent::thread(&owner(&s, tid).unwrap())
                .unwrap()
                .config
                .source_profile_revision,
            1
        );
    }

    #[tokio::test]
    async fn unconfirmed_stop_holds_capacity_until_restart() {
        let mut s = fixture();
        s.execution.as_mut().unwrap().max_parallel_tasks = 1;
        let first = add(&s, "first", "").await;
        let second = add(&s, "second", "").await;
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        let _ = interrupt(State(s.clone()), headers(), Path(first.id.clone()))
            .await
            .unwrap();
        worker.phase(AgentPhase::Failed, Some("stop unconfirmed".into()));
        finish(&s, &first.id, Ok(())).unwrap();
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&first.id]
                .record
                .info
                .phase,
            TaskPhase::Failed
        );
        assert!(claim(&s).unwrap().is_none());
        restore(&s).unwrap();
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&first.id]
                .record
                .info
                .phase,
            TaskPhase::Interrupted
        );
        assert_eq!(claim(&s).unwrap().unwrap().0, second.id);
    }
    #[tokio::test]
    async fn message_references_bind_only_new_tasks_and_survive_restore_and_retry() {
        let s = fixture();
        let mid = id();
        s.save(Message {
            id: mid.clone(),
            conversation_id: s.conversation_id(),
            role: Role::User,
            text: format!(
                "/machine:{} /tag:desktop /skill:skill-creator Create task",
                s.runtime_id
            ),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some(id()),
        })
        .unwrap();
        s.conversation.inner.lock().unwrap().turn = Some(TurnState {
            user_message_id: mid,
            cancelling: false,
        });
        let req = CreateTask {
            request_id: id(),
            text: "Perform work".into(),
            ..Default::default()
        };
        let task = create_from(
            s.clone(),
            headers(),
            req.clone(),
            agent::routing::Source::Tool,
        )
        .await
        .unwrap()
        .0;
        let worker = owner(&s, &task.id).unwrap();
        assert_eq!(worker.skill_selection.as_ref().unwrap(), &["skill-creator"]);
        let report = id();
        s.save(Message {
            id: report.clone(),
            conversation_id: s.conversation_id(),
            role: Role::System,
            text: "/tag:missing /skill:other".into(),
            status: MessageStatus::Complete,
            created_at_ms: now(),
            request_id: Some(id()),
        })
        .unwrap();
        s.conversation.inner.lock().unwrap().turn = Some(TurnState {
            user_message_id: report,
            cancelling: false,
        });
        assert_eq!(
            task_references(&s, &req, agent::routing::Source::Tool).unwrap(),
            Default::default()
        );
        s.conversation.inner.lock().unwrap().turn = None;
        let retry = create_from(
            s.clone(),
            headers(),
            req.clone(),
            agent::routing::Source::Tool,
        )
        .await
        .unwrap()
        .0;
        assert_eq!(retry.id, task.id);
        restore(&s).unwrap();
        assert_eq!(
            owner(&s, &task.id)
                .unwrap()
                .skill_selection
                .as_ref()
                .unwrap(),
            &["skill-creator"]
        );
        let next = create_from(
            s.clone(),
            headers(),
            CreateTask {
                request_id: id(),
                ..req.clone()
            },
            agent::routing::Source::Tool,
        )
        .await
        .unwrap()
        .0;
        assert!(owner(&s, &next.id).unwrap().skill_selection.is_none());
        let configured = create_from(
            s.clone(),
            headers(),
            CreateTask {
                request_id: id(),
                skills: Some(vec!["skill-installer".into()]),
                ..req.clone()
            },
            agent::routing::Source::Tool,
        )
        .await
        .unwrap()
        .0;
        restore(&s).unwrap();
        assert_eq!(
            owner(&s, &configured.id)
                .unwrap()
                .skill_selection
                .as_ref()
                .unwrap(),
            &["skill-installer"]
        );
        let bad = create_from(
            s.clone(),
            headers(),
            CreateTask {
                request_id: id(),
                text: "/tag:missing task".into(),
                ..req
            },
            agent::routing::Source::User,
        )
        .await;
        assert!(bad.is_err());
        assert_eq!(
            owner(&s, &task.id).unwrap().execution_target,
            worker.execution_target
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    pub(crate) fn fixture() -> Service {
        let mut s = crate::tests::service();
        s.data = std::env::temp_dir()
            .join(format!("pot-tasks-{}", id()))
            .join("product");
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(s.data.clone()));
        s.execution_service.sandboxes = Arc::new(sandbox::Manager::open(&s.data).unwrap());
        s.agent_service.resources = Arc::new(AgentResources::paths(&s.data));
        s.workspace = s.data.join("workspace");
        std::fs::create_dir_all(&s.workspace).unwrap();
        s.conversation.inner.lock().unwrap().store =
            Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        s.execution = Some(execution::Config {
            api_key: "fixture".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 20,
            max_tokens: 128,
            max_requests_per_turn: 3,
            max_parallel_tasks: 2,
        });
        execution::private_json(
            &s.data.join(RUNTIME_ENDPOINT_FILE),
            &json!(RuntimeEndpoint {
                api_version: API_VERSION,
                port: 17700,
                token: s.token.clone(),
                boot_id: s.boot.clone()
            }),
        )
        .unwrap();
        s
    }
    #[tokio::test]
    async fn incomplete_task_assignments_are_rejected_without_implicit_upgrade() {
        let root = fixture();
        let task = add(&root, "persist this task", "").await;
        let worker = owner(&root, &task.id).unwrap();
        let context = json!({"type":"context","version":1,"messages":[],"marker":"preserved"});
        pot_assistant::threads::save_context(
            &worker.conversation.inner.lock().unwrap().store,
            &context,
        )
        .unwrap();
        let old = root
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .get("tasks.v2")
            .unwrap()
            .unwrap();
        let mut legacy: Value = serde_json::from_str(&old).unwrap();
        legacy["tasks"][0]
            .as_object_mut()
            .unwrap()
            .remove("assignment");
        legacy["tasks"][0]
            .as_object_mut()
            .unwrap()
            .remove("execution_target");
        root.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set("tasks.v2", &legacy.to_string())
            .unwrap();
        assert!(restore(&root).is_err());
        assert_eq!(
            pot_assistant::threads::load_context(&worker.conversation.inner.lock().unwrap().store)
                .unwrap(),
            Some(context)
        );
    }

    #[tokio::test]
    async fn control_restart_retains_original_remote_input_identity_for_reconciliation() {
        let root = fixture();
        let task = create(
            State(root.clone()),
            headers(),
            Json(CreateTask {
                text: "original input".into(),
                request_id: "original-request".into(),
                machine_id: root.runtime_id.clone(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let (_, _, request) = claim(&root).unwrap().unwrap();
        let destination = pot_core::ExecutionTarget::host(&id());
        {
            let mut hub = root.tasks.hub.lock().unwrap();
            let lane = hub.lanes.get_mut(&task.id).unwrap();
            lane.record.info.execution_target = Some(destination.clone());
            lane.record.create_request.machine_id = destination.runtime_id.clone();
            lane.record.info.sandbox_id.clear();
            lane.record.assignment = Some(pot_core::Assignment::on_execution_target(
                &root.runtime_id,
                &task.id,
                destination.clone(),
            ));
            persist(&root, &hub).unwrap();
        }
        restore(&root).unwrap();
        let hub = root.tasks.hub.lock().unwrap();
        let lane = &hub.lanes[&task.id];
        assert!(lane.record.active.is_none());
        assert_eq!(lane.record.info.phase, TaskPhase::Queued);
        assert_eq!(lane.record.queue.len(), 1);
        assert_eq!(lane.record.queue[0].request_id, request.request_id);
        assert_eq!(lane.record.queue[0].text, request.text);
        assert_eq!(lane.service.execution_target, Some(destination.clone()));
        assert_eq!(
            lane.record.assignment.as_ref().unwrap().agent_runtime_id,
            destination.runtime_id
        );
        drop(hub);
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn host_task_binding_restores_and_conflicts_fail_before_creation() {
        let root = fixture();
        root.execution_service.permissions.set_machine_policy(
            &root.runtime_id,
            pot_agent::permissions::ExecutionMode::Host,
            pot_agent::permissions::DefaultDecision::Allow,
        );
        let execution_target = pot_core::ExecutionTarget::host(&root.runtime_id);
        let request = CreateTask {
            machine_id: execution_target.runtime_id.clone(),
            request_id: id(),
            text: "Host task".into(),
            ..Default::default()
        };
        let task = create(State(root.clone()), headers(), Json(request.clone()))
            .await
            .unwrap()
            .0;
        assert!(task.sandbox_id.is_empty());
        assert_eq!(task.execution_target.as_ref(), Some(&execution_target));
        let worker = owner(&root, &task.id).unwrap();
        assert!(worker.sandbox_id.is_none());
        assert_eq!(worker.workspace, worker.data.join("agent-workspace"));
        assert!(!uses_sandbox(&root, "default"));
        std::fs::write(
            worker.workspace.join("AGENTS.md"),
            "Host task directory rules",
        )
        .unwrap();
        assert!(
            crate::instructions::load(&worker)
                .unwrap()
                .text
                .contains("Host task directory rules")
        );
        let _ = crate::skills::resources(&worker).unwrap();
        restore(&root).unwrap();
        assert_eq!(
            owner(&root, &task.id).unwrap().execution_target,
            Some(execution_target)
        );
        let mut conflict = request.clone();
        conflict.request_id = id();
        conflict.machine_id = "invalid/machine".into();
        assert!(
            create(State(root.clone()), headers(), Json(conflict))
                .await
                .is_err()
        );
        let mut remote = request;
        remote.request_id = id();
        remote.machine_id = "remote".into();
        assert!(
            create(State(root.clone()), headers(), Json(remote))
                .await
                .is_err()
        );
        assert_eq!(root.tasks.hub.lock().unwrap().lanes.len(), 1);
    }
    #[tokio::test]
    async fn shared_capacity_is_scoped_to_full_execution_target_target() {
        let root = fixture();
        let sandbox = add(&root, "Sandbox shared work", "shared").await;
        let host = create(
            State(root.clone()),
            headers(),
            Json(CreateTask {
                machine_id: root.runtime_id.clone(),
                request_id: id(),
                text: "Host shared work".into(),
                resource: "shared".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        assert_eq!(claim(&root).unwrap().unwrap().0, sandbox.id);
        assert!(claim(&root).unwrap().is_none());
        finish(&root, &sandbox.id, Err(anyhow!("done"))).unwrap();
        assert_eq!(claim(&root).unwrap().unwrap().0, host.id);
        assert!(claim(&root).unwrap().is_none());
    }
    async fn add(s: &Service, text: &str, resource: &str) -> TaskInfo {
        create(
            State(s.clone()),
            headers(),
            Json(CreateTask {
                request_id: text.into(),
                text: text.into(),
                resource: resource.into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0
    }
    #[tokio::test]
    async fn task_stream_pushes_queued_guidance_with_public_task_identity() {
        use futures::StreamExt;
        let root = fixture();
        let task = add(&root, "event task", "").await;
        let response = events(State(root.clone()), headers(), Path(task.id.clone()))
            .await
            .unwrap()
            .into_response();
        let mut stream = response.into_body().into_data_stream();
        let mut decoder = pot_protocol::sse::Decoder::default();
        let initial = decoder
            .push(&stream.next().await.unwrap().unwrap())
            .unwrap();
        let snapshot: Snapshot = serde_json::from_str(&initial[0].data).unwrap();
        assert_eq!(snapshot.conversation_id, task.id);
        assert!(
            snapshot
                .messages
                .iter()
                .all(|m| m.conversation_id == task.id)
        );
        guide(&root, &task.id, "guidance", "new guidance").await;
        let frame = tokio::time::timeout(std::time::Duration::from_secs(1), stream.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        let frames = decoder.push(&frame).unwrap();
        assert_eq!(frames[0].event, "snapshot");
        let snapshot: Snapshot = serde_json::from_str(&frames[0].data).unwrap();
        assert!(
            snapshot
                .messages
                .iter()
                .any(|m| m.text == "new guidance" && m.conversation_id == task.id)
        );
    }
    async fn assert_finished(s: &Service, task: &str) {
        let error = input(
            State(s.clone()),
            headers(),
            Path(task.into()),
            Json(TaskInput {
                request_id: id(),
                text: "resume".into(),
            }),
        )
        .await
        .unwrap_err();
        assert_eq!(error.0, StatusCode::CONFLICT);
        assert_eq!(error.1.0.code, "task_finished");
    }
    async fn guide(s: &Service, task: &str, key: &str, text: &str) -> SendMessageAccepted {
        input(
            State(s.clone()),
            headers(),
            Path(task.into()),
            Json(TaskInput {
                request_id: key.into(),
                text: text.into(),
            }),
        )
        .await
        .unwrap()
        .0
    }
    pub(crate) fn succeed(root: &Service, task: &str, child: &Service, req: &SendMessage) {
        let mut m = child
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .duplicate(&req.request_id)
            .unwrap()
            .unwrap();
        m.status = MessageStatus::Complete;
        child.save(m).unwrap();
        child
            .save(Message {
                id: id(),
                conversation_id: "main".into(),
                role: Role::Assistant,
                text: format!("Result {}", req.text),
                status: MessageStatus::Complete,
                created_at_ms: now(),
                request_id: None,
            })
            .unwrap();
        finish(root, task, Ok(())).unwrap();
    }
    #[tokio::test]
    async fn configured_concurrency_limits_and_releases_slots() {
        for limit in [1, 5, 8] {
            let mut s = fixture();
            s.execution.as_mut().unwrap().max_parallel_tasks = limit;
            let mut queued = Vec::new();
            for i in 0..limit + 1 {
                queued.push(add(&s, &format!("task-{i}"), "").await);
            }
            let mut claimed = Vec::new();
            for expected in queued.iter().take(limit) {
                let job = claim(&s).unwrap().unwrap();
                assert_eq!(job.0, expected.id);
                claimed.push(job);
            }
            assert!(claim(&s).unwrap().is_none());
            let (id, child, req) = &claimed[0];
            succeed(&s, id, child, req);
            assert_eq!(claim(&s).unwrap().unwrap().0, queued[limit].id);
            assert!(claim(&s).unwrap().is_none());
        }
    }

    #[tokio::test]
    async fn parallel_lanes_limit_fifo_and_private_contexts() {
        let s = fixture();
        let a = add(&s, "ALPHA", " ".trim()).await;
        let b = add(&s, "BETA", "").await;
        let c = add(&s, "GAMMA", "").await;
        let (aid, asvc, ar) = claim(&s).unwrap().unwrap();
        let (bid, bsvc, br) = claim(&s).unwrap().unwrap();
        assert_eq!(aid, a.id);
        assert_eq!(bid, b.id);
        assert_ne!(aid, bid);
        assert!(claim(&s).unwrap().is_none());
        assert_ne!(asvc.data, bsvc.data);
        assert_ne!(asvc.workspace, bsvc.workspace);
        assert_ne!(asvc.token, bsvc.token);
        assert_eq!(asvc.snapshot().unwrap().messages.len(), 1);
        assert!(
            !asvc
                .snapshot()
                .unwrap()
                .messages
                .iter()
                .any(|m| m.text == br.text)
        );
        // Main turn is independently available despite occupied background slots.
        assert!(s.snapshot().unwrap().turn.is_none());
        let mut front = s.clone();
        let (tx, mut rx) = mpsc::unbounded_channel();
        front.conversation.commands = tx;
        let accepted = super::super::send(
            State(front.clone()),
            headers(),
            Json(SendMessage {
                request_id: "foreground".into(),
                conversation_id: "main".into(),
                text: "hello while workers run".into(),
            }),
        )
        .await
        .unwrap()
        .0;
        assert!(!accepted.duplicate);
        assert_eq!(rx.try_recv().unwrap().text, "hello while workers run");
        assert!(front.snapshot().unwrap().turn.is_some());
        succeed(&s, &aid, &asvc, &ar);
        let (cid, _, _) = claim(&s).unwrap().unwrap();
        assert!([a.id, b.id, c.id].contains(&cid));
        assert_ne!(cid, aid);
        assert_ne!(cid, bid);
    }
    #[tokio::test]
    async fn guidance_is_durable_received_then_consumed_in_order() {
        let s = fixture();
        let a = add(&s, "report", "").await;
        let (tid, worker, req) = claim(&s).unwrap().unwrap();
        let ack = guide(&s, &a.id, "guide-1", "include cost").await;
        let snap = snapshot(State(s.clone()), headers(), Path(a.id.clone()))
            .await
            .unwrap()
            .0;
        assert_eq!(
            snap.turn.unwrap().user_message_id,
            worker.snapshot().unwrap().turn.unwrap().user_message_id
        );
        assert_eq!(snap.messages.last().unwrap().id, ack.message_id);
        assert_eq!(
            snap.messages.last().unwrap().status,
            MessageStatus::Accepted
        );
        assert!(claim(&s).unwrap().is_none());
        assert!(
            s.conversation
                .inner
                .lock()
                .unwrap()
                .store
                .get("tasks.v2")
                .unwrap()
                .unwrap()
                .contains("include cost")
        );
        succeed(&s, &tid, &worker, &req);
        let (next, _, req) = claim(&s).unwrap().unwrap();
        assert_eq!(next, tid);
        assert_eq!(req.text, "include cost");
        assert!(claim(&s).unwrap().is_none());
    }
    #[tokio::test]
    async fn duplicate_create_and_guidance_conflicts_and_report_dedup() {
        let s = fixture();
        let a = add(&s, "one", "").await;
        assert_eq!(a.id, add(&s, "one", "").await.id);
        let ack = guide(&s, &a.id, "g", "extra").await;
        assert!(guide(&s, &a.id, "g", "extra").await.duplicate);
        let err = input(
            State(s.clone()),
            headers(),
            Path(a.id.clone()),
            Json(TaskInput {
                request_id: "g".into(),
                text: "wrong".into(),
            }),
        )
        .await
        .unwrap_err();
        assert_eq!(err.0, StatusCode::CONFLICT);
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&a.id].record.queue.len(),
            2
        );
        assert!(!ack.duplicate);
        let (tid, worker, req) = claim(&s).unwrap().unwrap();
        succeed(&s, &tid, &worker, &req);
        assert!(finish(&s, &tid, Ok(())).is_err());
        assert_eq!(s.snapshot().unwrap().messages.len(), 1);
    }
    #[tokio::test]
    async fn cancel_one_lane_does_not_stop_other_and_clears_pending() {
        let s = fixture();
        let a = add(&s, "A", "").await;
        let _b = add(&s, "B", "").await;
        let x = claim(&s).unwrap().unwrap();
        let y = claim(&s).unwrap().unwrap();
        let (a_worker, b_worker) = if x.0 == a.id {
            (&x.1, &y.1)
        } else {
            (&y.1, &x.1)
        };
        guide(&s, &a.id, "later", "do not run this").await;
        let _ = cancel(State(s.clone()), headers(), Path(a.id.clone()))
            .await
            .unwrap();
        assert!(a_worker.snapshot().unwrap().turn.unwrap().cancelling);
        assert!(!b_worker.snapshot().unwrap().turn.unwrap().cancelling);
        finish(&s, &a.id, Ok(())).unwrap();
        assert!(
            s.tasks.hub.lock().unwrap().lanes[&a.id]
                .record
                .queue
                .is_empty()
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&a.id].record.info.phase,
            TaskPhase::Cancelled
        );
    }
    #[tokio::test]
    async fn restart_preserves_queue_but_never_replays_started_work() {
        let s = fixture();
        let a = add(&s, "write externally", "").await;
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        guide(&s, &a.id, "g", "later").await;
        let b = add(&s, "not started", "").await;
        let path = s.data.clone();
        let runtime_id = s.runtime_id.clone();
        drop(worker);
        drop(s);
        let mut next = crate::tests::service();
        next.data = path.clone();
        next.control_config = Arc::new(crate::model_config::ControlConfig::new(next.data.clone()));
        next.runtime_id = runtime_id;
        next.workspace = path.join("workspace");
        next.conversation.inner.lock().unwrap().store =
            Store::open(&path.join("pot-v2.sqlite")).unwrap();
        restore(&next).unwrap();
        assert_eq!(
            next.tasks.hub.lock().unwrap().lanes[&a.id]
                .record
                .info
                .phase,
            TaskPhase::Interrupted
        );
        let (id, _, req) = claim(&next).unwrap().unwrap();
        assert_eq!(id, b.id);
        assert_eq!(req.text, "not started");
        assert!(claim(&next).unwrap().is_none());
        assert_finished(&next, &a.id).await;
        assert!(claim(&next).unwrap().is_none());
    }
    #[tokio::test]
    async fn cooperative_shared_resource_is_serialized() {
        let s = fixture();
        add(&s, "shared A", "shared").await;
        add(&s, "shared B", "shared").await;
        let first = claim(&s).unwrap().unwrap();
        assert!(claim(&s).unwrap().is_none());
        add(&s, "private", "").await;
        let second = claim(&s).unwrap().unwrap();
        assert_ne!(first.0, second.0);
        succeed(&s, &first.0, &first.1, &first.2);
        assert!(claim(&s).unwrap().is_some());
    }
    #[tokio::test]
    async fn fake_workers_use_barriers_not_timing() {
        let s = fixture();
        add(&s, "first", "").await;
        add(&s, "second", "").await;
        let (started_tx, mut started_rx) = mpsc::unbounded_channel();
        let mut gates = vec![];
        let mut jobs = vec![];
        for _ in 0..2 {
            let (tid, svc, req) = claim(&s).unwrap().unwrap();
            let (release, wait) = tokio::sync::oneshot::channel();
            gates.push(release);
            let root = s.clone();
            let tx = started_tx.clone();
            jobs.push(tokio::spawn(async move {
                tx.send(tid.clone()).unwrap();
                wait.await.unwrap();
                succeed(&root, &tid, &svc, &req);
            }));
        }
        let first = tokio::time::timeout(std::time::Duration::from_secs(2), started_rx.recv())
            .await
            .unwrap()
            .unwrap();
        let second = tokio::time::timeout(std::time::Duration::from_secs(2), started_rx.recv())
            .await
            .unwrap()
            .unwrap();
        assert_ne!(first, second);
        assert!(claim(&s).unwrap().is_none());
        for gate in gates {
            gate.send(()).unwrap();
        }
        for job in jobs {
            job.await.unwrap();
        }
        assert_eq!(s.snapshot().unwrap().messages.len(), 2);
        let cancelled = cancel(State(s.clone()), headers(), Path(first.clone()))
            .await
            .unwrap()
            .0;
        assert_eq!(cancelled["stopping"], false);
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&first].record.info.phase,
            TaskPhase::Complete
        );
    }
    #[tokio::test]
    async fn failed_followup_does_not_report_previous_round_answer() {
        let s = fixture();
        let a = add(&s, "ORIGINAL", "").await;
        let (tid, worker, req) = claim(&s).unwrap().unwrap();
        guide(&s, &a.id, "followup", "new request").await;
        succeed(&s, &tid, &worker, &req);
        let (tid, _, _) = claim(&s).unwrap().unwrap();
        finish(&s, &tid, Err(anyhow!("fixture failure"))).unwrap();
        let snapshot = s.snapshot().unwrap();
        let report = snapshot.messages.last().unwrap();
        assert!(report.text.contains("fixture failure"));
        assert!(!report.text.contains("Result ORIGINAL"));
    }
    #[tokio::test]
    async fn worker_token_cannot_access_other_task_or_root_api() {
        let s = fixture();
        let a = add(&s, "A", "").await;
        let b = add(&s, "B", "").await;
        let token = s.tasks.hub.lock().unwrap().lanes[&a.id]
            .service
            .token
            .clone();
        let mut h = HeaderMap::new();
        h.insert("authorization", format!("Bearer {token}").parse().unwrap());
        assert_eq!(
            snapshot(State(s.clone()), h, Path(b.id))
                .await
                .unwrap_err()
                .0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(
            create(
                State(s),
                HeaderMap::new(),
                Json(CreateTask {
                    request_id: "x".into(),
                    text: "x".into(),
                    resource: "".into(),
                    ..Default::default()
                })
            )
            .await
            .unwrap_err()
            .0,
            StatusCode::UNAUTHORIZED
        );
    }

    #[tokio::test]
    async fn new_tasks_inherit_profile_changes_but_existing_and_restored_tasks_stay_pinned() {
        let s = fixture();
        agent::initialize(&s).unwrap();
        let old = add(&s, "old snapshot", "").await;
        let before = agent::thread(&owner(&s, &old.id).unwrap()).unwrap().config;
        let mut profile = agent::Profile::api(s.execution.as_ref().unwrap());
        profile.revision = 2;
        profile.params.max_output_tokens = 77;
        let settings=serde_json::from_value(json!({"profile":profile,"expected_revision":1,"request_id":"edit","follow_main":false})).unwrap();
        let _ = agent::settings::settings(State(s.clone()), headers(), Json(settings))
            .await
            .unwrap();
        let new = add(&s, "new snapshot", "").await;
        assert_eq!(
            agent::thread(&owner(&s, &old.id).unwrap()).unwrap().config,
            before
        );
        assert_eq!(
            agent::thread(&owner(&s, &new.id).unwrap())
                .unwrap()
                .config
                .params
                .max_output_tokens,
            77
        );
        restore(&s).unwrap();
        assert_eq!(
            agent::thread(&owner(&s, &old.id).unwrap()).unwrap().config,
            before
        );
        assert_eq!(
            agent::thread(&owner(&s, &new.id).unwrap())
                .unwrap()
                .config
                .params
                .max_output_tokens,
            77
        );
    }
    #[tokio::test]
    async fn interruption_retains_guidance_capacity_and_requires_explicit_resume() {
        let s = fixture();
        main_turn(&s, "interrupt-main");
        let task = add(&s, "original work", "").await;
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        guide(&s, &task.id, "follow-up", "pending guidance").await;
        let response = tool(&s, "interrupt", "stop", json!({"task_id":task.id}))
            .await
            .unwrap()
            .0;
        assert_eq!(response["stopping"], true);
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
            Some(TaskPhase::Running)
        );
        assert!(worker.snapshot().unwrap().turn.unwrap().cancelling);
        assert!(claim(&s).unwrap().is_none());
        assert_eq!(
            input(
                State(s.clone()),
                headers(),
                Path(task.id.clone()),
                Json(TaskInput {
                    request_id: "too-early".into(),
                    text: "resume".into()
                })
            )
            .await
            .unwrap_err()
            .0,
            StatusCode::CONFLICT
        );
        finish(&s, &task.id, Ok(())).unwrap();
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
            Some(TaskPhase::Interrupted)
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&task.id]
                .record
                .queue
                .len(),
            1
        );
        assert!(claim(&s).unwrap().is_none());
        restore(&s).unwrap();
        assert!(claim(&s).unwrap().is_none());
        assert_finished(&s, &task.id).await;
    }

    #[tokio::test]
    async fn cancelling_interrupted_tasks_with_or_without_guidance_archives_and_survives_restart() {
        for pending in [false, true] {
            let s = fixture();
            main_turn(&s, "cancel-main");
            let task = add(&s, "original", "").await;
            let (_, _, _) = claim(&s).unwrap().unwrap();
            if pending {
                guide(&s, &task.id, "pending", "discard this guidance").await;
            }
            let _ = interrupt(State(s.clone()), headers(), Path(task.id.clone()))
                .await
                .unwrap();
            finish(&s, &task.id, Ok(())).unwrap();
            let args = json!({"task_id":task.id});
            let response = tool(&s, "cancel_task", "abandon", args.clone())
                .await
                .unwrap()
                .0;
            assert_eq!(response["phase"], "cancelled");
            assert_eq!(response["stopping"], false);
            assert_eq!(
                tool(&s, "cancel_task", "abandon", args).await.unwrap().0,
                response
            );
            assert!(
                s.tasks.hub.lock().unwrap().lanes[&task.id]
                    .record
                    .queue
                    .is_empty()
            );
            assert_eq!(
                task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
                Some(TaskPhase::Cancelled)
            );
            assert_eq!(
                page(
                    &s,
                    &TaskQuery {
                        scope: "archived".into(),
                        ..Default::default()
                    }
                )
                .unwrap()
                .total,
                1
            );
            assert_eq!(
                wait_tasks(&s, &json!({"task_ids":[task.id],"timeout_ms":0}))
                    .await
                    .unwrap()["timed_out"],
                false
            );
            restore(&s).unwrap();
            assert!(claim(&s).unwrap().is_none());
            assert_eq!(
                task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
                Some(TaskPhase::Cancelled)
            );
            assert_finished(&s, &task.id).await;
        }
    }

    #[tokio::test]
    async fn cancel_tool_stops_running_work_and_rejects_new_guidance_during_cleanup() {
        let s = fixture();
        main_turn(&s, "cancel-running");
        let task = add(&s, "running work", "").await;
        let (_, worker, _) = claim(&s).unwrap().unwrap();
        guide(&s, &task.id, "pending", "discard").await;
        let response = tool(&s, "cancel_task", "abandon", json!({"task_id":task.id}))
            .await
            .unwrap()
            .0;
        assert_eq!(response["stopping"], true);
        assert!(worker.snapshot().unwrap().turn.unwrap().cancelling);
        assert!(
            input(
                State(s.clone()),
                headers(),
                Path(task.id.clone()),
                Json(TaskInput {
                    request_id: "too-soon".into(),
                    text: "continue".into()
                })
            )
            .await
            .is_err()
        );
        assert!(
            s.tasks.hub.lock().unwrap().lanes[&task.id]
                .record
                .queue
                .is_empty()
        );
        finish(&s, &task.id, Ok(())).unwrap();
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
            Some(TaskPhase::Cancelled)
        );
        assert!(claim(&s).unwrap().is_none());
    }

    #[tokio::test]
    async fn interrupt_allowance_wait_discards_suspended_input_but_keeps_guidance() {
        let s = fixture();
        let task = add(&s, "suspended input", "").await;
        let (_, worker, req) = claim(&s).unwrap().unwrap();
        let mid = worker.snapshot().unwrap().turn.unwrap().user_message_id;
        worker
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set("execution.paused", &serde_json::to_string(&req).unwrap())
            .unwrap();
        s.usage
            .lock()
            .unwrap()
            .set_policy(&usage::Policy {
                period: "day".into(),
                limit: 1,
                ..Default::default()
            })
            .unwrap();
        s.usage
            .lock()
            .unwrap()
            .wait(&task.id, &mid, now(), 100)
            .unwrap();
        finish(&s, &task.id, Ok(())).unwrap();
        guide(&s, &task.id, "pending", "fresh guidance").await;
        let _ = interrupt(State(s.clone()), headers(), Path(task.id.clone()))
            .await
            .unwrap();
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
            Some(TaskPhase::Interrupted)
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&task.id]
                .record
                .queue
                .len(),
            1
        );
        assert!(s.usage.lock().unwrap().waiting(&task.id).unwrap().is_none());
        s.usage
            .lock()
            .unwrap()
            .set_policy(&usage::Policy::default())
            .unwrap();
        restore(&s).unwrap();
        assert!(claim(&s).unwrap().is_none());
        assert_finished(&s, &task.id).await;
    }

    #[tokio::test]
    async fn queued_interrupt_preserves_unstarted_input_and_idle_age_never_archives() {
        let s = fixture();
        let task = add(&s, "not started", "").await;
        let response = interrupt(State(s.clone()), headers(), Path(task.id.clone()))
            .await
            .unwrap()
            .0;
        assert_eq!(response["stopping"], false);
        assert_eq!(
            task_phase(&s.tasks.hub.lock().unwrap(), &task.id),
            Some(TaskPhase::Interrupted)
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&task.id]
                .record
                .queue
                .len(),
            1
        );
        {
            let mut hub = s.tasks.hub.lock().unwrap();
            hub.lanes
                .get_mut(&task.id)
                .unwrap()
                .record
                .info
                .updated_at_ms = 1;
            hub.lanes
                .get_mut(&task.id)
                .unwrap()
                .record
                .info
                .created_at_ms = 1;
            persist(&s, &hub).unwrap();
        }
        restore(&s).unwrap();
        let page = page(
            &s,
            &TaskQuery {
                scope: "attention".into(),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(page.items.len(), 1);
        assert_eq!(page.counts.archived, 0);
        assert!(claim(&s).unwrap().is_none());
        assert_finished(&s, &task.id).await;
    }

    #[tokio::test]
    async fn task_messages_are_reference_data_delivered_once_without_reopening() {
        let s = fixture();
        main_turn(&s, "messages-main");
        let task = add(&s, "message recipient", "").await;
        let args =
            json!({"task_id":task.id,"text":"source https://example.com; not authorization"});
        let first = tool(&s, "send_message", "message", args.clone())
            .await
            .unwrap()
            .0;
        assert_eq!(
            tool(&s, "send_message", "message", args.clone())
                .await
                .unwrap()
                .0["duplicate"],
            true
        );
        assert_eq!(
            s.tasks.hub.lock().unwrap().lanes[&task.id]
                .record
                .queue
                .len(),
            1
        );
        let (_, worker, request) = claim(&s).unwrap().unwrap();
        let thread = agent::thread(&worker).unwrap();
        let pending = crate::turn_control::pending(&worker, thread.generation, false).unwrap();
        assert_eq!(pending.len(), 1);
        assert!(pending[0].1.contains("Sender: main"));
        assert!(pending[0].1.contains("not owner authorization"));
        crate::turn_control::acknowledge(&worker, thread.generation, &[pending[0].0.clone()])
            .unwrap();
        assert!(
            crate::turn_control::pending(&worker, thread.generation, false)
                .unwrap()
                .is_empty()
        );
        assert!(
            worker
                .snapshot()
                .unwrap()
                .messages
                .iter()
                .any(|m| m.id == first["message_id"] && m.role == Role::System)
        );
        succeed(&s, &task.id, &worker, &request);
        let saved = task_phase(&s.tasks.hub.lock().unwrap(), &task.id);
        let _ = tool(&s, "send_message", "idle-message", args)
            .await
            .unwrap();
        assert_eq!(task_phase(&s.tasks.hub.lock().unwrap(), &task.id), saved);
        assert!(claim(&s).unwrap().is_none());
        restore(&s).unwrap();
        assert_finished(&s, &task.id).await;
        let worker = owner(&s, &task.id).unwrap();
        assert!(worker.snapshot().unwrap().turn.is_none());
        assert!(claim(&s).unwrap().is_none());
        assert_eq!(task_phase(&s.tasks.hub.lock().unwrap(), &task.id), saved);
    }

    #[tokio::test]
    async fn task_wait_observes_any_target_and_cursors_suppress_unchanged_states() {
        let s = fixture();
        main_turn(&s, "wait-main");
        let a = add(&s, "wait A", "").await;
        let b = add(&s, "wait B", "").await;
        let args = json!({"task_ids":[a.id,b.id],"timeout_ms":0});
        let initial = tool(&s, "wait_task", "snapshot", args).await.unwrap().0;
        assert_eq!(initial["timed_out"], true);
        let wait_args = json!({"task_ids":[a.id,b.id],"timeout_ms":1000});
        let wait = wait_tasks(&s, &wait_args);
        let change = async {
            tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            let _ = interrupt(State(s.clone()), headers(), Path(b.id.clone()))
                .await
                .unwrap();
        };
        let (reply, ()) = tokio::join!(wait, change);
        let reply = reply.unwrap();
        assert_eq!(reply["timed_out"], false);
        assert!(
            reply["tasks"]
                .as_array()
                .unwrap()
                .iter()
                .any(|v| v["task"]["id"] == b.id && v["ready"] == true)
        );
        let repeated = wait_tasks(
            &s,
            &json!({"task_ids":[a.id,b.id],"timeout_ms":0,"after":reply["cursors"]}),
        )
        .await
        .unwrap();
        assert_eq!(repeated["timed_out"], true);
        for args in [
            json!({"task_ids":[]}),
            json!({"task_ids":[a.id,a.id]}),
            json!({"task_ids":["missing"]}),
            json!({"task_ids":[a.id],"timeout_ms":60001}),
        ] {
            assert!(wait_tasks(&s, &args).await.is_err());
        }
    }

    #[tokio::test]
    async fn task_wait_stops_when_its_main_turn_is_interrupted() {
        let s = fixture();
        main_turn(&s, "wait-cancel");
        let task = add(&s, "wait target", "").await;
        let stop = async {
            tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            s.conversation
                .inner
                .lock()
                .unwrap()
                .turn
                .as_mut()
                .unwrap()
                .cancelling = true;
            s.cancel_notify.notify_waiters();
        };
        let wait_args = json!({"task_ids":[task.id],"timeout_ms":60000});
        let wait = wait_tasks(&s, &wait_args);
        let (reply, ()) = tokio::join!(wait, stop);
        assert_eq!(reply.unwrap_err().0, StatusCode::CONFLICT);
    }
}
