use pot_protocol::*;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
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

#[derive(Clone, PartialEq)]
pub struct RunningTask {
    pub id: String,
    pub title: String,
    pub progress: String,
    pub executor_icon: String,
}

fn task_progress(snapshot: &Snapshot) -> String {
    if snapshot.turn.as_ref().is_some_and(|t| t.cancelling) {
        return crate::i18n::text("client.stopping").into();
    }
    match snapshot.messages.last() {
        Some(m) if m.role == Role::Tool && m.status == MessageStatus::Streaming => {
            // Tool headers are runtime facts, not generated summaries.
            crate::i18n::tr!(
                "client.running",
                m.text
                    .lines()
                    .next()
                    .unwrap_or(crate::i18n::text("client.tool"))
                    .chars()
                    .take(36)
                    .collect::<String>()
            )
        }
        Some(m) if m.role == Role::Assistant && m.status == MessageStatus::Streaming => {
            crate::i18n::text("client.generating-a-reply").into()
        }
        _ => crate::i18n::text("client.working").into(),
    }
}

#[derive(Default, Clone, PartialEq)]
pub struct View {
    pub client_mode: String,
    pub runtime: Value,
    pub machines: Value,
    pub machine_reply: Value,
    pub machine_pending: bool,
    pub assistants: Value,
    pub selected_assistant: String,
    pub assistant_rules: Value,
    pub assistant_saved_owner: Option<String>,
    pub assistant_pending: bool,
    pub permissions: Value,
    pub usage_query: Option<(String, u32, usize, String)>,
    pub usage: Value,
    pub usage_error: String,
    pub usage_pending: bool,
    pub usage_notice: String,
    usage_fetched: Option<std::time::Instant>,
    usage_loaded: Option<(String, u32, usize, String)>,
    pub snapshot: Option<Snapshot>,
    /// Connection error is cleared only after a successful snapshot/stream update.
    pub error: String,
    /// Command errors survive background updates until the next user attempt.
    pub command_error: String,
    pub pending: bool,
    sending: pot_ui::state::SendState,
    pub accepted_text: Option<String>,
    pub accepted_conversation: Option<String>,
    pub selected: String,
    pub tasks: Vec<TaskInfo>,
    pub running_tasks: Vec<RunningTask>,
    pub running_total: usize,
    pub task_query: TaskQuery,
    pub task_page: TaskPage,
    pub selected_task: Option<TaskInfo>,
    pub tasks_loading: bool,
    pub schedules_visible: bool,
    pub schedules: SchedulePage,
    pub schedule_selected: String,
    pub schedule_offset: usize,
    pub runs_offset: usize,
    pub schedule_pending: bool,
    pub schedule_success: u64,
    pub schedule_error: String,
    pub main_snapshot: Option<Snapshot>,
    cached_tasks: HashMap<String, Arc<Snapshot>>,
}
impl View {
    pub fn assistant_id(&self) -> &str {
        if self.selected_assistant.is_empty() {
            "main"
        } else {
            &self.selected_assistant
        }
    }
    pub fn is_cooking(&self) -> bool {
        self.running_total > 0
            || self.main_snapshot.as_ref().is_some_and(|s| {
                s.turn.is_some()
                    && s.agent.phase == AgentPhase::Ready
                    && s.agent.detail.as_deref() != Some("等待 Pot 额度恢复")
            })
    }

    fn apply_task_page(&mut self, query: &TaskQuery, selected: &str, page: TaskPage) {
        // Counts describe all tasks, never just the visible page. Keep lifecycle
        // guards correct when a running task is hidden by a search or archive tab.
        self.task_page.counts = page.counts.clone();
        if self.selected == selected {
            self.selected_task = page.selected.clone();
        }
        if &self.task_query == query {
            self.task_query.offset = page.offset;
            self.tasks = page.items.clone();
            self.task_page = page;
            self.tasks_loading = false;
        }
    }

    fn select(&mut self, id: String) -> bool {
        if self.pending || self.selected == id {
            return false;
        }
        if !self.selected.is_empty()
            && self.selected != "new"
            && let Some(snapshot) = &self.snapshot
        {
            if self.cached_tasks.len() >= 16 {
                self.cached_tasks.clear();
            }
            self.cached_tasks
                .insert(self.selected.clone(), Arc::new(snapshot.clone()));
        }
        self.snapshot = if id.is_empty() || id == "new" {
            self.main_snapshot.clone()
        } else {
            self.cached_tasks.get(&id).map(|s| s.as_ref().clone())
        };
        self.selected_task = self.tasks.iter().find(|t| t.id == id).cloned();
        self.selected = id;
        self.command_error.clear();
        if self.snapshot.is_none() && self.error.is_empty() {
            self.error = crate::i18n::text("client.loading").into();
        } else if self.snapshot.is_some()
            && crate::i18n::matches_message("client.loading", &self.error)
        {
            self.error.clear();
        }
        true
    }
    pub fn can_send(&self) -> bool {
        pot_ui::state::can_send(
            self.snapshot.as_ref(),
            !self.selected.is_empty(),
            self.pending,
            self.error.is_empty(),
        )
    }
    fn replace(&mut self, snapshot: Snapshot) -> Result<(), String> {
        if snapshot.api_version != API_VERSION {
            return Err(crate::i18n::text("client.incompatible-runtime-protocol-version").into());
        }
        self.snapshot = Some(snapshot);
        self.error.clear();
        Ok(())
    }
    #[cfg(test)]
    fn apply(&mut self, event: Event) -> Result<(), String> {
        let s = self
            .snapshot
            .as_mut()
            .ok_or(crate::i18n::text("client.chat-snapshot-missing"))?;
        if let Some(cursor) = event.cursor {
            if cursor <= s.cursor {
                return Ok(());
            }
            if cursor != s.cursor + 1 {
                return Err(crate::i18n::text("client.missing-events-resynchronizing").into());
            }
            s.cursor = cursor;
        }
        match event.kind {
            EventKind::Message { message } => {
                if message.conversation_id != s.conversation_id {
                    return Err(crate::i18n::text("client.session-mismatch").into());
                }
                if let Some(old) = s.messages.iter_mut().find(|m| m.id == message.id) {
                    *old = message;
                } else {
                    s.messages.push(message);
                }
            }
            EventKind::Agent { status } => s.agent = status,
            EventKind::Turn { turn } => s.turn = turn,
            EventKind::Budget { budget } => s.budget = Some(budget),
        }
        self.error.clear();
        Ok(())
    }
    fn accepted(&mut self, req: &SendMessage, result: SendMessageAccepted) {
        self.pending = false;
        self.sending.accept(&req.request_id);
        self.accepted_text = Some(pot_ui::state::Receipt::from_request(req).text);
        self.accepted_conversation = Some(req.conversation_id.clone());
        let assistant_id = self.assistant_id().to_owned();
        if let Some(s) = self
            .snapshot
            .as_mut()
            .filter(|s| s.conversation_id == req.conversation_id)
        {
            let settled = s.messages.iter().any(|m| {
                m.request_id.as_deref() == Some(&req.request_id)
                    && !matches!(m.status, MessageStatus::Accepted | MessageStatus::Streaming)
            });
            if !settled && s.turn.is_none() && req.conversation_id == assistant_id {
                s.turn = Some(TurnState {
                    user_message_id: result.message_id,
                    cancelling: false,
                });
            }
        }
    }
}
pub struct Client {
    pub view: Arc<Mutex<View>>,
    tx: mpsc::Sender<(String, SendMessage)>,
    running: Arc<AtomicBool>,
    refresh: mpsc::SyncSender<()>,
}
impl Drop for Client {
    fn drop(&mut self) {
        self.running.store(false, Ordering::Relaxed);
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
fn receive(view: &Arc<Mutex<View>>, _running: &AtomicBool) -> Result<(), String> {
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
    let directory: Value = agent
        .get("/api/v1/runtime")
        .call()?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    let mut machines: Value = agent
        .get("/api/v1/machines")
        .call()?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    if view.lock().unwrap().client_mode == "remote"
        && let Some(items) = machines["machines"].as_array_mut()
    {
        for machine in items {
            if machine["local"] == true {
                machine["name"] = serde_json::json!(crate::i18n::text("machines.control"));
            }
        }
    }
    view.lock().unwrap().machines = machines;
    if !directory["capabilities"]
        .as_array()
        .is_some_and(|caps| caps.iter().any(|c| c == "conversation"))
    {
        let mut v = view.lock().unwrap();
        if v.assistant_id() == assistant_id {
            v.runtime = directory;
            v.permissions = Value::Null;
            v.snapshot = None;
        }
        return Err("Runtime conversation capability unavailable".into());
    }
    let assistants: Value = agent
        .get("/api/v1/assistants")
        .call()?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    let assistant_rules: Value = agent
        .get(&format!("/api/v1/assistants/{assistant_id}/instructions"))
        .call()?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    let task_page: TaskPage = agent
        .get("/api/v1/assistant-tasks")
        .query("scope", &query.scope)
        .query("q", &query.q)
        .query("offset", query.offset.to_string())
        .query("limit", query.limit.to_string())
        .query("selected", &selected)
        .call()
        .map_err(|e| e.to_string())?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    let agent = agent.scope(&assistant_id);
    // Independent of the visible task filter and page. Only executing lanes
    // count as activity; queued and budget/user waits do not.
    let executing: TaskPage = agent
        .get(&format!("{PATH_TASKS}/query"))
        .query("phase", "running")
        .query("limit", "3")
        .call()
        .map_err(|e| e.to_string())?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    let mut running_tasks = Vec::new();
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
    let schedules: Option<SchedulePage> = if sq.0 {
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
            v.usage_loaded.as_ref() != Some(q)
                || v.usage_fetched
                    .is_none_or(|t| t.elapsed() > Duration::from_secs(3))
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
        if v.assistant_id() == assistant_id && v.usage_query.as_ref() == Some(&q) {
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
    let permissions: Value = agent
        .get("/api/v1/permissions")
        .call()
        .map_err(|e| e.to_string())?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;

    let main: Snapshot = agent
        .get(PATH_SNAPSHOT)
        .call()
        .map_err(|e| e.to_string())?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())?;
    let path = if selected.is_empty() || selected == "new" {
        PATH_SNAPSHOT.to_string()
    } else {
        format!("{PATH_TASKS}/{selected}")
    };
    let snapshot: Snapshot = if selected.is_empty() || selected == "new" {
        main.clone()
    } else {
        agent
            .get(&path)
            .call()
            .map_err(|e| e.to_string())?
            .body_mut()
            .read_json()
            .map_err(|e| e.to_string())?
    };
    let mut v = view.lock().unwrap();
    if v.assistant_id() != assistant_id {
        return Ok(());
    }
    if v.runtime["runtime_id"] != directory["runtime_id"] {
        v.cached_tasks.clear();
    }
    v.runtime = directory;
    v.permissions = permissions;
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
    v.main_snapshot = Some(main);
    v.apply_task_page(&query, &selected, task_page);
    if v.selected == selected {
        v.replace(snapshot)?;
    }
    Ok(())
}
impl Client {
    pub fn start() -> Self {
        Self::start_mode(crate::backend::mode())
    }
    pub fn start_mode(mode: &str) -> Self {
        let view = Arc::new(Mutex::new(View {
            client_mode: mode.into(),
            error: crate::i18n::text("client.connecting-to-environment").into(),
            task_query: TaskQuery {
                limit: 20,
                ..Default::default()
            },
            tasks_loading: true,
            ..Default::default()
        }));
        let running = Arc::new(AtomicBool::new(true));
        let (tx, rx) = mpsc::channel::<(String, SendMessage)>();
        let (refresh, refresh_rx) = mpsc::sync_channel(1);
        let v = view.clone();
        let active = running.clone();
        std::thread::spawn(move || {
            while active.load(Ordering::Relaxed) {
                let owner = v.lock().unwrap().assistant_id().to_owned();
                if let Err(e) = receive(&v, &active) {
                    let mut state = v.lock().unwrap();
                    if state.assistant_id() == owner {
                        state.error = e;
                        if state.client_mode == "remote"
                            && let Some(items) = state.machines["machines"].as_array_mut()
                        {
                            for machine in items {
                                machine["connection"] = serde_json::json!("offline");
                            }
                        }
                    }
                }
                crate::wake_ui();
                let _ = refresh_rx.recv_timeout(Duration::from_millis(500));
            }
        });
        // Writes never block behind the long-lived SSE response.
        let v = view.clone();
        let active = running.clone();
        std::thread::spawn(move || {
            while let Ok((assistant_id, req)) = rx.recv() {
                while active.load(Ordering::Relaxed) {
                    if let Err(error) = crate::backend::ready() {
                        v.lock().unwrap().command_error = error;
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
                                        state.selected = id.into();
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
                    std::thread::sleep(Duration::from_millis(500));
                }
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
        });
    }
    pub fn permission_decision(&self, body: Value) {
        let owner = self.view.lock().unwrap().assistant_id().to_owned();
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<(), String> {
                let mut response = requests(&view)
                    .scope(&owner)
                    .timeout_global(Some(Duration::from_secs(10)))
                    .build()
                    .new_agent()
                    .post("/api/v1/permissions")
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
        });
    }
    pub fn schedule_select(&self, id: String) {
        let mut v = self.view.lock().unwrap();
        v.schedule_selected = id;
        v.runs_offset = 0;
        v.schedules.selected = None;
        v.schedules.runs.clear();
        v.schedule_error.clear();
        let _ = self.refresh.try_send(());
    }
    pub fn schedule_action(&self, mut req: Value) {
        let owner = self.view.lock().unwrap().assistant_id().to_owned();
        {
            let mut v = self.view.lock().unwrap();
            if v.schedule_pending {
                return;
            }
            v.schedule_pending = true;
            v.schedule_error.clear();
        }
        req["request_id"] = json!(uuid::Uuid::new_v4().to_string());
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
        let _ = self.refresh.try_send(());
    }
    pub fn machine_command(&self, value: Value) {
        {
            let mut state = self.view.lock().unwrap();
            if state.machine_pending {
                return;
            }
            state.machine_pending = true;
        }
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<Value, String> {
                if value["command"]["action"] == "configure_control" {
                    return crate::backend::configure_control(value["command"].clone());
                }
                requests(&view)
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
        let view = self.view.clone();
        let refresh = self.refresh.clone();
        std::thread::spawn(move || {
            let result = (|| -> Result<Value, String> {
                let requests = requests(&view);
                let owner = value["owner"].as_str().unwrap_or_default();
                if owner == "new" {
                    return requests
                        .post("/api/v1/assistants")
                        .send_json(json!({"request_id":value["request_id"],"name":value["name"]}))?
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
        if v.assistant_id() == id || v.pending {
            return;
        }
        v.selected_assistant = id.clone();
        v.selected.clear();
        v.snapshot = None;
        v.main_snapshot = None;
        v.cached_tasks.clear();
        v.permissions = Value::Null;
        v.usage_pending = false;
        v.usage_loaded = None;
        v.usage_fetched = None;
        v.schedule_pending = false;
        v.schedules = SchedulePage::default();
        v.schedule_selected.clear();
        v.usage = Value::Null;
        v.assistant_rules = Value::Null;
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
