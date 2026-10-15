use pot_protocol::*;
use serde_json::{Value, json};
use std::{
    collections::BTreeSet,
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    time::Duration,
};

/// Preserve server diagnostics; localize the fallback for invalid or empty error bodies.
fn rejection_detail(body: &mut crate::backend::Body, fallback_key: &str) -> String {
    body.read_json::<ApiError>()
        .map(|error| error.message)
        .unwrap_or_else(|_| crate::i18n::text(fallback_key).into())
}

use pot_ui::application::client::task_progress;

pub use pot_ui::application::client::{RunningTask, View};
#[derive(Clone)]
pub(crate) struct Refresh {
    pub(crate) sender: mpsc::SyncSender<()>,
    pub(crate) dirty: Arc<Mutex<Option<BTreeSet<String>>>>,
}
impl Refresh {
    pub(crate) fn try_send(&self, _: ()) -> Result<(), mpsc::TrySendError<()>> {
        *self.dirty.lock().unwrap() = None;
        self.sender.try_send(())
    }
    pub(crate) fn changed(&self, topics: BTreeSet<String>) {
        if topics.is_empty() {
            return;
        }
        if let Some(dirty) = self.dirty.lock().unwrap().as_mut() {
            dirty.extend(topics);
        }
        let _ = self.sender.try_send(());
    }
}
pub struct Client {
    pub view: Arc<Mutex<View>>,
    tx: mpsc::Sender<(String, SendMessage)>,
    running: Arc<AtomicBool>,
    refresh: Refresh,
}
impl Drop for Client {
    fn drop(&mut self) {
        self.running.store(false, Ordering::Relaxed);
        let _ = self.refresh.try_send(());
    }
}
pub fn root() -> PathBuf {
    std::env::var_os("POT_LOCAL_ROOT")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            PathBuf::from(std::env::var_os("HOME").unwrap()).join("Library/Application Support/pot")
        })
}
fn requests(view: &Arc<Mutex<View>>) -> crate::backend::Requests {
    let state = view.lock().unwrap();
    crate::backend::Requests::for_mode(&state.client_mode).scope(state.assistant_id())
}
fn receive(view: &Arc<Mutex<View>>, dirty: Option<&BTreeSet<String>>) -> Result<(), String> {
    let configuration_generation = crate::backend::configuration_generation();
    let before = view.lock().unwrap().clone();
    let changed = |domain: &str| {
        dirty.is_none_or(|topics| {
            topics
                .iter()
                .any(|t| t == domain || t.starts_with(&format!("{domain}/")))
        })
    };
    crate::backend::ready()?;
    let (selected, query, assistant_id) = {
        let v = view.lock().unwrap();
        (
            v.selected.clone(),
            v.task_query.clone(),
            v.assistant_id().to_owned(),
        )
    };
    let sq = {
        let v = view.lock().unwrap();
        (
            v.schedules_visible,
            v.schedule_selected.clone(),
            v.schedule_offset,
            v.runs_offset,
        )
    };
    let agent = requests(view)
        .timeout_global(Some(Duration::from_secs(3)))
        .build()
        .new_agent();
    let directory: Value = if changed("machines") {
        agent
            .get("/api/v1/runtime")
            .call()?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.runtime.clone()
    };
    let mut machines: Value = if changed("machines") {
        agent
            .get("/api/v1/machines")
            .call()?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.machines.clone()
    };
    if view.lock().unwrap().client_mode == "remote"
        && let Some(items) = machines["machines"].as_array_mut()
    {
        for machine in items {
            if machine["local"] == true {
                machine["name"] = serde_json::json!(crate::i18n::text("machines.control"));
            }
        }
    }

    if !directory["capabilities"]
        .as_array()
        .is_some_and(|caps| caps.iter().any(|c| c == "conversation"))
    {
        let mut v = view.lock().unwrap();
        if v.assistant_id() == assistant_id {
            v.set_runtime(directory);
            v.permissions = Value::Null;
            v.snapshot = None;
        }
        return Err("Runtime conversation capability unavailable".into());
    }
    let assistants: Value = if changed("assistants") || changed("conversation") || changed("tasks")
    {
        agent
            .get("/api/v1/assistants")
            .call()?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.assistants.clone()
    };
    let assistant_rules: Value = if changed("settings") {
        agent
            .get(&format!("/api/v1/assistants/{assistant_id}/instructions"))
            .call()?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.assistant_rules.clone()
    };
    let task_page: TaskPage = if changed("tasks") {
        agent
            .get("/api/v1/assistant-tasks")
            .query("scope", &query.scope)
            .query("q", &query.q)
            .query("offset", query.offset.to_string())
            .query("limit", query.limit.to_string())
            .query("selected", &selected)
            .query(
                "execution_target",
                serde_json::from_str::<Value>(&query.execution_target)
                    .ok()
                    .and_then(|v| v.get("execution_target").map(Value::to_string))
                    .unwrap_or_else(|| query.execution_target.clone()),
            )
            .call()
            .map_err(|e| e.to_string())?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        TaskPage::default()
    };
    let agent = agent.scope(&assistant_id);
    // Independent of the visible task filter and page. Only executing lanes
    // count as activity; queued and budget/user waits do not.
    let executing: TaskPage = if changed("tasks") {
        agent
            .get(&format!("{PATH_TASKS}/query"))
            .query("phase", "running")
            .query("limit", "3")
            .call()
            .map_err(|e| e.to_string())?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        TaskPage {
            total: before.running_total,
            ..Default::default()
        }
    };
    let mut running_tasks = if changed("tasks") {
        Vec::new()
    } else {
        before.running_tasks.clone()
    };
    let mut finished = 0;
    for task in &executing.items {
        let progress = agent
            .get(&format!("{PATH_TASKS}/{}", task.id))
            .call()
            .ok()
            .and_then(|mut r| r.body_mut().read_json::<Snapshot>().ok());
        // A finished snapshot removes the row; a transient fetch failure
        // retains the authoritative running lane with a neutral status.
        if progress.as_ref().is_some_and(|s| s.turn.is_none()) {
            finished += 1;
            continue;
        }
        running_tasks.push(RunningTask {
            id: task.id.clone(),
            title: task.title.clone(),
            executor_icon: task.executor_icon.clone(),
            progress: progress
                .as_ref()
                .map(task_progress)
                .unwrap_or_else(|| crate::i18n::text("client.working").into()),
        });
    }
    let schedules: Option<SchedulePage> = if sq.0 && changed("schedules") {
        Some(
            agent
                .get("/api/v1/schedules")
                .query("selected", &sq.1)
                .query("offset", sq.2.to_string())
                .query("runs_offset", sq.3.to_string())
                .call()
                .map_err(|e| e.to_string())?
                .body_mut()
                .read_json()
                .map_err(|e| e.to_string())?,
        )
    } else {
        None
    };
    let uq = {
        let v = view.lock().unwrap();
        v.usage_query.clone().filter(|q| {
            changed("usage") || v.usage_loaded.as_ref() != Some(q) || v.usage_fetched.is_none()
        })
    };
    if let Some(q) = uq {
        let result = (|| -> Result<Value, String> {
            agent
                .get("/api/v1/usage/summary")
                .query("scope", &q.0)
                .query("days", q.1.to_string())
                .query("offset", q.2.to_string())
                .query("timezone", &q.3)
                .call()
                .map_err(|e| e.to_string())?
                .body_mut()
                .read_json()
                .map_err(|e| e.to_string())
        })();
        let mut v = view.lock().unwrap();
        if v.assistant_id() == assistant_id
            && v.generation == before.generation
            && configuration_generation == crate::backend::configuration_generation()
            && v.usage_query.as_ref() == Some(&q)
        {
            v.usage_fetched = Some(std::time::Instant::now());
            v.usage_loaded = Some(q);
            match result {
                Ok(data) => {
                    v.usage = data;
                    v.usage_error.clear();
                }
                Err(e) => v.usage_error = e,
            }
        }
    }
    let completions: Value = if changed("settings") {
        agent
            .get("/api/v1/completions")
            .call()?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.completions.clone()
    };
    let permissions: Value = if changed("permissions") {
        agent
            .get("/api/v1/permissions")
            .call()
            .map_err(|e| e.to_string())?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.permissions.clone()
    };

    let main: Snapshot =
        if dirty.is_none() || changed("conversation") || before.main_snapshot.is_none() {
            agent
                .get(PATH_SNAPSHOT)
                .call()
                .map_err(|e| e.to_string())?
                .body_mut()
                .read_json()
                .map_err(|e| e.to_string())?
        } else {
            before.main_snapshot.clone().unwrap()
        };
    let path = if selected.is_empty() || selected == "new" {
        PATH_SNAPSHOT.to_string()
    } else {
        format!("{PATH_TASKS}/{selected}")
    };
    let snapshot: Snapshot = if selected.is_empty() || selected == "new" {
        main.clone()
    } else if dirty.is_none() || before.snapshot.is_none() {
        agent
            .get(&path)
            .call()
            .map_err(|e| e.to_string())?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    } else {
        before.snapshot.clone().unwrap()
    };
    let mut v = view.lock().unwrap();
    if v.assistant_id() != assistant_id
        || v.generation != before.generation
        || configuration_generation != crate::backend::configuration_generation()
    {
        return Ok(());
    }
    if v.runtime["runtime_id"] != directory["runtime_id"] {
        v.cached_tasks.clear();
    }
    v.machines = machines;
    v.set_runtime(directory);
    v.permissions = permissions;
    v.completions = completions;
    v.assistants = assistants;
    v.assistant_rules = assistant_rules;
    if main.api_version != API_VERSION || snapshot.api_version != API_VERSION {
        return Err(crate::i18n::text("client.incompatible-runtime-protocol-version").into());
    }
    if v.main_snapshot
        .as_ref()
        .is_some_and(|old| old.boot_id != main.boot_id)
    {
        v.cached_tasks.clear();
    }
    if let Some(page) = schedules
        && (
            v.schedules_visible,
            v.schedule_selected.clone(),
            v.schedule_offset,
            v.runs_offset,
        ) == sq
    {
        v.schedule_offset = page.offset;
        v.schedules = page;
    }
    v.running_total = executing.total.saturating_sub(finished);
    v.running_tasks = running_tasks;
    if changed("tasks") {
        v.apply_task_page(&query, &selected, task_page);
    }
    if dirty.is_none() || (changed("conversation") && v.main_snapshot == before.main_snapshot) {
        v.main_snapshot = Some(main);
    }
    if dirty.is_none() && v.selected == selected {
        v.replace(snapshot)?;
    }
    Ok(())
}
impl Client {
    pub fn start_mode(mode: &str) -> Self {
        let view = Arc::new(Mutex::new(View {
            client_mode: mode.into(),
            error: crate::i18n::text("client.connecting-to-runtime").into(),
            task_query: TaskQuery {
                limit: 20,
                ..Default::default()
            },
            tasks_loading: true,
            ..Default::default()
        }));
        let running = Arc::new(AtomicBool::new(true));
        let (tx, rx) = mpsc::channel::<(String, SendMessage)>();
        let (sender, refresh_rx) = mpsc::sync_channel(1);
        let refresh = Refresh {
            sender,
            dirty: Arc::new(Mutex::new(None)),
        };
        let v = view.clone();
        let active = running.clone();
        let trigger = refresh.clone();
        let mode = mode.to_owned();
        std::thread::spawn(move || {
            let mut versions = serde_json::Value::Null;
            let notifications = trigger.clone();
            let status = v.clone();
            let _changes =
                crate::backend::subscribe(mode.clone(), "/api/v1/changes".into(), move |frame| {
                    match frame {
                        Ok(frame) => {
                            let next: Value =
                                serde_json::from_str(&frame.data).map_err(|e| e.to_string())?;
                            if next["boot_id"] != versions["boot_id"] {
                                let _ = notifications.try_send(());
                            } else {
                                let dirty = next["versions"]
                                    .as_object()
                                    .into_iter()
                                    .flatten()
                                    .filter(|(key, value)| versions["versions"][*key] != **value)
                                    .map(|(key, _)| key.clone())
                                    .collect();
                                notifications.changed(dirty);
                            }
                            versions = next;
                        }
                        Err(error) => {
                            status.lock().unwrap().error = error;
                            versions = Value::Null;
                            crate::wake_ui();
                        }
                    }
                    Ok(())
                });
            let mut conversation: Option<crate::backend::Subscription> = None;
            while refresh_rx.recv().is_ok() && active.load(Ordering::Relaxed) {
                let dirty = trigger.dirty.lock().unwrap().replace(BTreeSet::new());
                if dirty.is_none() {
                    conversation.take();
                }
                let owner = v.lock().unwrap().assistant_id().to_owned();
                if let Err(error) = receive(&v, dirty.as_ref()) {
                    let mut state = v.lock().unwrap();
                    if state.assistant_id() == owner {
                        state.error = error;
                    }
                    drop(state);
                    crate::wake_ui();
                    // A failed fetch is retried only while disconnected, not as
                    // a healthy-state polling loop.
                    if active.load(Ordering::Relaxed) {
                        std::thread::sleep(Duration::from_secs(1));
                        let _ = trigger.try_send(());
                    }
                    continue;
                }
                if dirty.is_none() {
                    let state = v.lock().unwrap().clone();
                    let owner = state.assistant_id().to_owned();
                    let selected = state.selected.clone();
                    let generation = state.generation;
                    let path = if selected.is_empty() || selected == "new" {
                        format!("/api/v1/assistants/{owner}/events?snapshot=true")
                    } else {
                        format!("/api/v1/assistants/{owner}/tasks/{selected}/events")
                    };
                    let state = v.clone();
                    conversation = Some(crate::backend::subscribe(
                        mode.clone(),
                        path,
                        move |frame| {
                            let mut state = state.lock().unwrap();
                            if state.assistant_id() != owner
                                || state.selected != selected
                                || state.generation != generation
                            {
                                return Ok(());
                            }
                            match frame {
                                Ok(frame) if frame.event == "snapshot" => {
                                    state.replace(
                                        serde_json::from_str(&frame.data)
                                            .map_err(|e| e.to_string())?,
                                    )?;
                                }
                                Ok(frame) => state.apply(
                                    serde_json::from_str(&frame.data).map_err(|e| e.to_string())?,
                                )?,
                                Err(error) => state.error = error,
                            }
                            if selected.is_empty() {
                                state.main_snapshot = state.snapshot.clone();
                            }
                            drop(state);
                            crate::wake_ui();
                            Ok(())
                        },
                    ));
                }
                crate::wake_ui();
            }
        });
        // Writes never block behind the long-lived SSE response.
        let write_refresh = refresh.clone();
        let v = view.clone();
        let active = running.clone();
        std::thread::spawn(move || {
            while let Ok((assistant_id, req)) = rx.recv() {
                while active.load(Ordering::Relaxed) {
                    if let Err(error) = crate::backend::ready() {
                        v.lock().unwrap().command_error = error;
                        crate::wake_ui();
                        std::thread::sleep(Duration::from_millis(500));
                        continue;
                    }
                    let path = match req.conversation_id.as_str() {
                        id if id == assistant_id => PATH_MESSAGES.to_string(),
                        "new" => PATH_TASKS.to_string(),
                        id => format!("{PATH_TASKS}/{id}/messages"),
                    };
                    match requests(&v)
                        .scope(&assistant_id)
                        .post(&path)
                        .config()
                        .http_status_as_error(false)
                        .build()
                        .send_json(json!(req))
                    {
                        Ok(mut response)
                            if response.status().is_client_error()
                                || response.status().is_server_error() =>
                        {
                            let detail = rejection_detail(
                                response.body_mut(),
                                "client.server-rejected-the-request",
                            );
                            let mut state = v.lock().unwrap();
                            state.pending = false;
                            state.sending.reject(&req.request_id);
                            state.command_error = detail;
                            break;
                        }
                        Ok(mut response) => match response.body_mut().read_json::<Value>() {
                            Ok(result) => {
                                let mut state = v.lock().unwrap();
                                state.command_error.clear();
                                if req.conversation_id == "new" {
                                    if let Some(id) = result["id"].as_str() {
                                        state.generation += 1;
                                        state.selected = id.into();
                                        let _ = write_refresh.try_send(());
                                        state.snapshot = None;
                                    }
                                    state.pending = false;
                                    state.sending.accept(&req.request_id);
                                    state.accepted_text = Some(req.text.clone());
                                    state.accepted_conversation = Some(req.conversation_id.clone());
                                } else {
                                    match serde_json::from_value::<SendMessageAccepted>(result) {
                                        Ok(result) => {
                                            state.accepted(&req, result);
                                        }
                                        Err(_) => {
                                            state.pending = false;
                                            state.sending.reject(&req.request_id);
                                            state.command_error = crate::i18n::text(
                                                "client.invalid-send-response-format",
                                            )
                                            .into();
                                        }
                                    }
                                }
                                break;
                            }
                            Err(_) => {
                                v.lock().unwrap().command_error = crate::i18n::text(
                                    "client.send-not-confirmed-retrying-with-the-same",
                                )
                                .into();
                            }
                        },
                        Err(_) => {
                            v.lock().unwrap().command_error = crate::i18n::text(
                                "client.send-not-confirmed-retrying-with-the-same",
                            )
                            .into();
                        }
                    }
                    crate::wake_ui();
                    std::thread::sleep(Duration::from_millis(500));
                }
                crate::wake_ui();
            }
        });
        Self {
            view,
            tx,
            running,
            refresh,
        }
    }
    pub fn cancel(&self) {
        let owner = self.view.lock().unwrap().assistant_id().to_owned();
        let selected = self.view.lock().unwrap().selected.clone();
        let message_id = {
            let mut state = self.view.lock().unwrap();
            if let Some(turn) = state.snapshot.as_mut().and_then(|s| s.turn.as_mut()) {
                if turn.cancelling {
                    return;
                }
                turn.cancelling = true;
                turn.user_message_id.clone()
            } else if !selected.is_empty() && selected != "new" {
                String::new()
            } else {
                return;
            }
        };
        crate::wake_ui();
        let view = self.view.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<(), String> {
                crate::backend::ready()?;
                requests(&view).scope(&owner)
                    .timeout_global(Some(Duration::from_secs(3)))
                    .build()
                    .new_agent()
                    .post(&if selected.is_empty(){PATH_CANCEL.to_string()}else{format!("{PATH_TASKS}/{selected}/interrupt")})
                    .send_json(json!({"request_id":uuid::Uuid::new_v4().to_string(),"user_message_id":message_id}))
                    .map_err(|e| e.to_string())?;
                Ok(())
            })();
            if let Err(e) = result {
                let mut state = view.lock().unwrap();
                if state.assistant_id() != owner {
                    return;
                }
                state.command_error =
                    crate::i18n::tr!("client.stop-request-not-confirmed-e", e = e);
                if let Some(turn) = state.snapshot.as_mut().and_then(|s| s.turn.as_mut()) {
                    turn.cancelling = false;
                }
            }
            crate::wake_ui();
        });
    }
    pub fn permission_decision(&self, body: Value) {
        self.machine_operation("/api/v1/permissions", body);
    }
    pub fn sandbox_lifecycle(&self, mut body: Value) {
        body["request_id"] = json!(uuid::Uuid::new_v4().to_string());
        body.as_object_mut().unwrap().remove("sandbox_id");
        self.machine_operation("/api/v1/sandboxes/shared", body);
    }
    fn machine_operation(&self, path: &'static str, body: Value) {
        let owner = self.view.lock().unwrap().assistant_id().to_owned();
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<(), String> {
                let mut response = requests(&view)
                    .scope(&owner)
                    .timeout_global(Some(Duration::from_secs(60)))
                    .build()
                    .new_agent()
                    .post(path)
                    .config()
                    .http_status_as_error(false)
                    .build()
                    .send_json(body)
                    .map_err(|e| e.to_string())?;
                if response.status().is_client_error() || response.status().is_server_error() {
                    return Err(rejection_detail(
                        response.body_mut(),
                        "client.operation-rejected",
                    ));
                }
                Ok(())
            })();
            {
                let mut state = view.lock().unwrap();
                if state.assistant_id() == owner {
                    state.command_error = result.err().unwrap_or_default();
                }
            }
            let _ = refresh.try_send(());
            crate::wake_ui();
        });
    }
    pub fn usage_action(&self, path: &'static str, body: Value) {
        let owner = self.view.lock().unwrap().assistant_id().to_owned();
        {
            let mut v = self.view.lock().unwrap();
            if v.usage_pending {
                return;
            }
            v.usage_pending = true;
            v.usage_notice.clear();
            v.usage_error.clear();
        }
        crate::wake_ui();
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<(), String> {
                crate::backend::ready()?;
                let mut response = requests(&view)
                    .scope(&owner)
                    .timeout_global(Some(Duration::from_secs(90)))
                    .build()
                    .new_agent()
                    .post(&format!("/api/v1/usage/{path}"))
                    .config()
                    .http_status_as_error(false)
                    .build()
                    .send_json(body)
                    .map_err(|e| e.to_string())?;
                if response.status().is_client_error() || response.status().is_server_error() {
                    return Err(rejection_detail(
                        response.body_mut(),
                        "client.operation-rejected",
                    ));
                }
                Ok(())
            })();
            let mut v = view.lock().unwrap();
            if v.assistant_id() != owner {
                return;
            }
            v.usage_pending = false;
            v.usage_fetched = None;
            if let Err(e) = result {
                v.usage_error = e;
            } else {
                v.usage_notice = crate::i18n::text("client.saved").into();
            }
            drop(v);
            let _ = refresh.try_send(());
            crate::wake_ui();
        });
    }
    pub fn schedule_select(&self, id: String) {
        let mut v = self.view.lock().unwrap();
        v.schedule_selected = id;
        v.runs_offset = 0;
        v.schedules.selected = None;
        v.schedules.runs.clear();
        v.schedule_error.clear();
        drop(v);
        let _ = self.refresh.try_send(());
        crate::wake_ui();
    }
    pub fn schedule_action(&self, req: Value) {
        let owner = self.view.lock().unwrap().assistant_id().to_owned();
        {
            let mut v = self.view.lock().unwrap();
            if v.schedule_pending {
                return;
            }
            v.schedule_pending = true;
            v.schedule_error.clear();
        }
        crate::wake_ui();
        let view = self.view.clone();
        let active = self.running.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let agent = requests(&view)
                .scope(&owner)
                .timeout_global(Some(Duration::from_secs(90)))
                .build()
                .new_agent();
            while active.load(Ordering::Relaxed) {
                let result = (|| -> Result<Value, String> {
                    crate::backend::ready()?;
                    match agent
                        .post("/api/v1/schedules")
                        .config()
                        .http_status_as_error(false)
                        .build()
                        .send_json(req.clone())
                    {
                        Ok(mut r)
                            if r.status().is_client_error() || r.status().is_server_error() =>
                        {
                            let detail = rejection_detail(
                                r.body_mut(),
                                "client.schedule-operation-rejected",
                            );
                            Ok(json!({"rejected":detail}))
                        }
                        Ok(mut r) => r.body_mut().read_json().map_err(|e| e.to_string()),
                        Err(e) => Err(e.to_string()),
                    }
                })();
                match result {
                    Ok(reply) => {
                        let mut v = view.lock().unwrap();
                        if v.assistant_id() != owner {
                            return;
                        }
                        v.schedule_pending = false;
                        if let Some(detail) = reply["rejected"].as_str() {
                            v.schedule_error = detail.into();
                        } else {
                            v.schedule_success += 1;
                            v.schedule_error.clear();
                            if req["action"] == "delete" {
                                v.schedule_selected.clear();
                                v.schedules.selected = None;
                            } else if let Some(id) = reply["id"].as_str() {
                                v.schedule_selected = id.into();
                            }
                        }
                        break;
                    }
                    Err(_) => {
                        let mut state = view.lock().unwrap();
                        if state.assistant_id() != owner {
                            break;
                        }
                        state.schedule_error = crate::i18n::text(
                            "client.connection-interrupted-retrying-with-the-same-request",
                        )
                        .into();
                        // Never hold the presentation lock during retry backoff:
                        // render and input handlers need it even while offline.
                        drop(state);
                        crate::wake_ui();
                        std::thread::sleep(Duration::from_millis(1000));
                    }
                }
            }
            let _ = refresh.try_send(());
            crate::wake_ui();
        });
    }
    pub fn refresh(&self) {
        self.view.lock().unwrap().usage_fetched = None;
        let _ = self.refresh.try_send(());
        crate::wake_ui();
    }
    pub fn filter_execution_target(&self, execution_target: String) {
        let mut v = self.view.lock().unwrap();
        v.task_query.execution_target = execution_target;
        v.task_query.scope = "active".into();
        v.task_query.q.clear();
        v.task_query.offset = 0;
        v.tasks.clear();
        v.tasks_loading = true;
        drop(v);
        let _ = self.refresh.try_send(());
        crate::wake_ui();
    }
    pub fn filter_tasks(&self, scope: String, q: String, offset: usize) {
        let mut v = self.view.lock().unwrap();
        if v.task_query.scope == scope && v.task_query.q == q && v.task_query.offset == offset {
            return;
        }
        v.task_query.scope = scope;
        v.task_query.q = q;
        v.task_query.offset = offset;
        v.tasks.clear();
        v.tasks_loading = true;
        drop(v);
        let _ = self.refresh.try_send(());
        crate::wake_ui();
    }
    pub fn machine_command(&self, value: Value) {
        {
            let mut state = self.view.lock().unwrap();
            if state.machine_pending {
                return;
            }
            state.machine_pending = true;
        }
        crate::wake_ui();
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<Value, String> {
                if value["command"]["action"] == "configure_control" {
                    return crate::backend::configure_control(value["command"].clone());
                }
                let target = value["mode"]
                    .as_str()
                    .map(crate::backend::Requests::for_mode)
                    .unwrap_or_else(|| requests(&view));
                target
                    .post("/api/v1/machine-connections")
                    .send_json(value["command"].clone())?
                    .body_mut()
                    .read_json()
                    .map_err(|e| e.to_string())
            })();
            let mut state = view.lock().unwrap();
            state.machine_pending = false;
            let value = if value["command"]["action"] == "configure_control" {
                json!({"command":{"action":"configure_control"}})
            } else {
                value
            };
            state.machine_reply = match result {
                Ok(result) => json!({"intent":value,"result":result}),
                Err(error) => json!({"intent":value,"error":error}),
            };
            drop(state);
            let _ = refresh.try_send(());
            crate::wake_ui();
        });
    }
    pub fn assistant_save(&self, value: Value) {
        {
            let mut state = self.view.lock().unwrap();
            if state.assistant_pending {
                return;
            }
            state.assistant_pending = true;
        }
        crate::wake_ui();
        let destination = requests(&self.view);
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<Value, String> {
                let requests = destination;
                let owner = value["owner"].as_str().unwrap_or_default();
                if owner == "new" {
                    return requests
                        .post("/api/v1/assistants")
                        .send_json(json!({"request_id":value["request_id"],"name":value["name"],"profile_id":value["profile_id"],"source_assistant":value["source_assistant"]}))?
                        .body_mut()
                        .read_json()
                        .map_err(|e| e.to_string());
                }
                requests
                    .post(&format!("/api/v1/assistants/{owner}/definition"))
                    .send_json(value.clone())?
                    .body_mut()
                    .read_json()
                    .map_err(|e| e.to_string())
            })();
            let mut v = view.lock().unwrap();
            v.assistant_pending = false;
            match result {
                Ok(reply) => {
                    let definition = reply.get("definition").unwrap_or(&reply);
                    if let Some(list) = v.assistants["assistants"].as_array_mut() {
                        if let Some(old) = list.iter_mut().find(|d| d["id"] == definition["id"]) {
                            *old = definition.clone();
                        } else if !definition["id"].is_null() {
                            list.push(definition.clone());
                        }
                    }
                    if value["owner"] == v.assistant_id() && !reply["rules"].is_null() {
                        v.assistant_rules = reply["rules"].clone();
                    }
                    v.command_error.clear();
                    v.assistant_saved_owner = value["owner"].as_str().map(str::to_owned);
                    if value["owner"] == "new" {
                        v.created_assistant = definition["id"].as_str().map(str::to_owned);
                    }
                }
                Err(e) => v.command_error = e,
            }
            drop(v);
            let _ = refresh.try_send(());
            crate::wake_ui();
        });
    }
    pub fn select_assistant(&self, id: String) {
        let mut v = self.view.lock().unwrap();
        if !v.select_assistant(id.clone()) {
            return;
        }
        crate::backend::select_assistant(&id);
        drop(v);
        let _ = self.refresh.try_send(());
        crate::wake_ui();
    }
    pub fn select(&self, id: String) {
        let owner = self
            .view
            .lock()
            .unwrap()
            .tasks
            .iter()
            .find(|task| task.id == id)
            .map(|t| t.assistant_id.clone());
        if let Some(owner) = owner {
            self.select_assistant(owner);
        }
        if self.view.lock().unwrap().select(id) {
            let _ = self.refresh.try_send(());
            crate::wake_ui();
        }
    }
    pub fn send(&self, text: String) -> bool {
        let mut state = self.view.lock().unwrap();
        if !state.can_send() {
            return false;
        }
        let conversation = if state.selected.is_empty() {
            state.assistant_id().to_owned()
        } else {
            state.selected.clone()
        };
        let Ok(request) = state
            .sending
            .begin(uuid::Uuid::new_v4().to_string(), conversation, text)
        else {
            return false;
        };
        state.pending = true;
        state.command_error.clear();
        if self
            .tx
            .send((state.assistant_id().to_owned(), request.clone()))
            .is_err()
        {
            state.pending = false;
            state.sending.reject(&request.request_id);
            state.command_error =
                crate::i18n::text("client.send-service-exited-reopen-the-app").into();
            return false;
        }
        drop(state);
        crate::wake_ui();
        true
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn delayed_send_receipt_keeps_its_original_conversation() {
        let mut state = View {
            selected: "task-b".into(),
            ..Default::default()
        };
        state.accepted(
            &SendMessage {
                request_id: "sent-in-a".into(),
                conversation_id: "task-a".into(),
                text: "same text".into(),
            },
            SendMessageAccepted {
                message_id: "accepted".into(),
                duplicate: false,
            },
        );
        assert_eq!(state.accepted_conversation.as_deref(), Some("task-a"));
        assert_eq!(state.accepted_text.as_deref(), Some("same text"));
        assert_eq!(state.selected, "task-b");
    }

    #[test]
    fn http_rejection_preserves_diagnostics_and_localizes_invalid_bodies() {
        for key in [
            "client.server-rejected-the-request",
            "client.operation-rejected",
            "client.schedule-operation-rejected",
        ] {
            for data in ["", "<html>Bad gateway</html>", "{}"] {
                let mut body = crate::backend::Body::builder().data(data);
                let detail = rejection_detail(&mut body, key);
                assert_eq!(detail, crate::i18n::text(key));
                assert_eq!(crate::i18n::message(&detail), crate::i18n::text(key),);
            }
            let mut body = crate::backend::Body::builder()
                .data(r#"{"code":"provider_rejected","message":"Provider diagnostic 中文 {0}"}"#);
            let detail = rejection_detail(&mut body, key);
            assert_eq!(detail, "Provider diagnostic 中文 {0}");
            assert_eq!(crate::i18n::message(&detail), detail);
        }
    }

    #[test]
    fn late_execution_target_page_cannot_replace_the_current_execution_target() {
        let mut v = View::default();
        let old = v.task_query.clone();
        v.task_query.execution_target =
            serde_json::json!({"runtime_id":"other-machine","target":{"kind":"host"}}).to_string();
        v.tasks_loading = true;
        v.apply_task_page(
            &old,
            "",
            TaskPage {
                total: 12,
                ..Default::default()
            },
        );
        assert!(v.tasks_loading);
        assert_eq!(v.task_page.total, 0);
    }

    #[test]
    fn stale_task_search_pages_do_not_replace_current_results_or_selection() {
        let mut v = View::default();
        let old = v.task_query.clone();
        v.task_query.scope = "archived".into();
        v.selected = "chosen".into();
        v.tasks_loading = true;
        v.apply_task_page(
            &old,
            "old-selection",
            TaskPage {
                total: 100,
                counts: TaskCounts {
                    active: 5,
                    ..Default::default()
                },
                ..Default::default()
            },
        );
        assert!(v.tasks_loading);
        assert_eq!(v.task_page.total, 0);
        assert_eq!(v.task_page.counts.active, 5);
        let query = v.task_query.clone();
        v.apply_task_page(
            &query,
            "chosen",
            TaskPage {
                offset: 0,
                total: 0,
                counts: TaskCounts {
                    active: 5,
                    ..Default::default()
                },
                ..Default::default()
            },
        );
        assert!(!v.tasks_loading);
        assert_eq!(v.task_page.counts.active, 5);
    }

    #[test]
    fn cooking_uses_execution_independently_of_visible_filters() {
        let mut v = view();
        v.main_snapshot = v.snapshot.clone();
        v.task_page.counts.active = 8; // Queued/waiting lanes alone are idle.
        assert!(!v.is_cooking());
        v.running_total = 1;
        v.task_query.scope = "archived".into();
        v.task_query.q = "unrelated".into();
        assert!(v.is_cooking());
        v.running_total = 0;
        let main = v.main_snapshot.as_mut().unwrap();
        main.turn = Some(TurnState {
            user_message_id: "u".into(),
            cancelling: false,
        });
        assert!(v.is_cooking());
        v.main_snapshot.as_mut().unwrap().agent.detail = Some("等待 Pot 额度恢复".into());
        assert!(!v.is_cooking());
        v.running_total = 1;
        assert!(v.is_cooking()); // A paused main does not hide background execution.
        v.running_total = 0;
        v.main_snapshot.as_mut().unwrap().turn = None;
        assert!(!v.is_cooking());
    }

    #[test]
    fn progress_uses_current_message_not_a_completed_tool() {
        let mut snapshot = view().snapshot.unwrap();
        snapshot.messages.push(Message {
            id: "tool".into(),
            conversation_id: "task".into(),
            role: Role::Tool,
            text: "读取文件\nprivate body".into(),
            status: MessageStatus::Streaming,
            created_at_ms: 0,
            request_id: None,
        });
        assert_eq!(
            task_progress(&snapshot),
            crate::i18n::tr!("client.running", "读取文件")
        );
        snapshot.messages[0].status = MessageStatus::Complete;
        assert_eq!(
            task_progress(&snapshot),
            crate::i18n::text("client.working")
        );
        snapshot.messages.push(Message {
            id: "reply".into(),
            conversation_id: "task".into(),
            role: Role::Assistant,
            text: "draft".into(),
            status: MessageStatus::Streaming,
            created_at_ms: 0,
            request_id: None,
        });
        assert_eq!(
            task_progress(&snapshot),
            crate::i18n::text("client.generating-a-reply")
        );
        snapshot.turn = Some(TurnState {
            user_message_id: "u".into(),
            cancelling: true,
        });
        assert_eq!(
            task_progress(&snapshot),
            crate::i18n::text("client.stopping")
        );
    }

    use super::*;
    fn view() -> View {
        View {
            snapshot: Some(Snapshot {
                api_version: API_VERSION,
                boot_id: "boot".into(),
                cursor: 0,
                agent: AgentStatus {
                    model: "test".into(),
                    phase: AgentPhase::Ready,
                    detail: None,
                },
                conversation_id: "main".into(),
                messages: vec![],
                turn: None,
                budget: None,
            }),
            ..Default::default()
        }
    }
    fn message() -> Message {
        Message {
            id: "m1".into(),
            conversation_id: "main".into(),
            role: Role::User,
            text: "hello".into(),
            status: MessageStatus::Complete,
            created_at_ms: 0,
            request_id: Some("r1".into()),
        }
    }
    #[test]
    fn reopening_current_chat_preserves_messages_and_errors() {
        let mut v = view();
        v.snapshot.as_mut().unwrap().messages.push(message());
        v.command_error = "retry needed".into();
        assert!(!v.select(String::new()));
        assert_eq!(v.snapshot.as_ref().unwrap().messages.len(), 1);
        assert_eq!(v.command_error, "retry needed");
    }
    #[test]
    fn switching_back_uses_cached_conversation_without_crossing_messages() {
        let mut v = view();
        v.snapshot.as_mut().unwrap().messages.push(message());
        v.main_snapshot = v.snapshot.clone();
        assert!(v.select("task-a".into()));
        assert!(v.snapshot.is_none());
        let mut task = v.main_snapshot.clone().unwrap();
        task.conversation_id = "task-a".into();
        task.messages.clear();
        v.replace(task).unwrap();
        assert!(v.select(String::new()));
        assert_eq!(v.snapshot.as_ref().unwrap().messages.len(), 1);
        assert!(v.select("task-a".into()));
        assert_eq!(v.snapshot.as_ref().unwrap().conversation_id, "task-a");
        assert!(v.snapshot.as_ref().unwrap().messages.is_empty());
        assert!(v.error.is_empty());
    }
    #[test]
    fn duplicate_gap_and_delayed_ack_do_not_corrupt_state() {
        let mut v = view();
        let event = Event {
            event_id: "e1".into(),
            cursor: Some(1),
            kind: EventKind::Message { message: message() },
        };
        v.apply(event.clone()).unwrap();
        v.apply(event.clone()).unwrap();
        assert_eq!(v.snapshot.as_ref().unwrap().messages.len(), 1);
        let mut gap = event;
        gap.cursor = Some(3);
        assert!(v.apply(gap).is_err());
        assert_eq!(v.snapshot.as_ref().unwrap().cursor, 1);
        v.accepted(
            &SendMessage {
                request_id: "r1".into(),
                conversation_id: "main".into(),
                text: "hello".into(),
            },
            SendMessageAccepted {
                message_id: "m1".into(),
                duplicate: false,
            },
        );
        assert!(v.snapshot.as_ref().unwrap().turn.is_none());
        assert!(v.can_send());
    }
    #[test]
    fn unconfigured_or_auth_required_blocks_send_until_ready() {
        let mut v = view();
        v.snapshot.as_mut().unwrap().agent.phase = AgentPhase::NeedsConfiguration;
        assert!(!v.can_send());
        v.selected = "task-a".into();
        assert!(!v.can_send());
        v.selected.clear();
        v.snapshot.as_mut().unwrap().agent.phase = AgentPhase::Ready;
        assert!(v.can_send());
    }
    #[test]
    fn reconnect_preserves_command_error_and_exhausted_budget_blocks_send() {
        let mut v = view();
        v.command_error = "rejected".into();
        v.error = "offline".into();
        assert!(!v.can_send());
        let mut snapshot = v.snapshot.clone().unwrap();
        snapshot.boot_id = "new-boot".into();
        snapshot.budget = Some(RequestBudget {
            used: 3,
            limit: 3,
            max_output_tokens: 128,
        });
        v.replace(snapshot).unwrap();
        assert!(v.error.is_empty());
        assert_eq!(v.command_error, "rejected");
        assert!(!v.can_send());
    }
    #[test]
    fn queued_guidance_ack_does_not_replace_active_turn() {
        let mut v = view();
        v.selected = "task-a".into();
        let s = v.snapshot.as_mut().unwrap();
        s.conversation_id = "task-a".into();
        s.turn = Some(TurnState {
            user_message_id: "active".into(),
            cancelling: false,
        });
        assert!(v.can_send());
        v.accepted(
            &SendMessage {
                request_id: "guide".into(),
                conversation_id: "task-a".into(),
                text: "cost".into(),
            },
            SendMessageAccepted {
                message_id: "queued".into(),
                duplicate: false,
            },
        );
        assert_eq!(v.snapshot.unwrap().turn.unwrap().user_message_id, "active");
    }
}
