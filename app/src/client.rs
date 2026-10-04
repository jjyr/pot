use pot_protocol::*;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Write},
    os::unix::net::UnixStream,
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    time::Duration,
};

#[derive(Default, Clone, PartialEq)]
pub struct View {
    pub snapshot: Option<Snapshot>,
    /// Connection error is cleared only after a successful snapshot/stream update.
    pub error: String,
    /// Command errors survive background updates until the next user attempt.
    pub command_error: String,
    pub pending: bool,
    pub accepted_text: Option<String>,
    pub selected: String,
    pub tasks: Vec<TaskInfo>,
    pub task_query: TaskQuery,
    pub task_page: TaskPage,
    pub selected_task: Option<TaskInfo>,
    pub tasks_loading: bool,
    pub main_snapshot: Option<Snapshot>,
    cached_tasks: HashMap<String, Arc<Snapshot>>,
}
impl View {
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
        if !self.selected.is_empty() && self.selected != "new" {
            if let Some(snapshot) = &self.snapshot {
                if self.cached_tasks.len() >= 16 {
                    self.cached_tasks.clear();
                }
                self.cached_tasks
                    .insert(self.selected.clone(), Arc::new(snapshot.clone()));
            }
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
            self.error = "正在加载…".into();
        } else if self.snapshot.is_some() && self.error == "正在加载…" {
            self.error.clear();
        }
        true
    }
    pub fn can_send(&self) -> bool {
        !self.pending
            && self.error.is_empty()
            && self.snapshot.as_ref().is_some_and(|s| {
                (s.turn.is_none() || !self.selected.is_empty())
                    && matches!(s.engine.phase, EnginePhase::Ready | EnginePhase::NeedsAuth)
                    && s.budget.as_ref().is_none_or(|b| b.remaining() > 0)
            })
    }
    fn replace(&mut self, snapshot: Snapshot) -> Result<(), String> {
        if snapshot.api_version != API_VERSION {
            return Err("Runtime 协议版本不兼容".into());
        }
        self.snapshot = Some(snapshot);
        self.error.clear();
        Ok(())
    }
    #[cfg(test)]
    fn apply(&mut self, event: Event) -> Result<(), String> {
        let s = self.snapshot.as_mut().ok_or("缺少聊天快照")?;
        if let Some(cursor) = event.cursor {
            if cursor <= s.cursor {
                return Ok(());
            }
            if cursor != s.cursor + 1 {
                return Err("事件缺失，正在重新同步…".into());
            }
            s.cursor = cursor;
        }
        match event.kind {
            EventKind::Message { message } => {
                if message.conversation_id != s.conversation_id {
                    return Err("会话不匹配".into());
                }
                if let Some(old) = s.messages.iter_mut().find(|m| m.id == message.id) {
                    *old = message;
                } else {
                    s.messages.push(message);
                }
            }
            EventKind::Engine { status } => s.engine = status,
            EventKind::Turn { turn } => s.turn = turn,
            EventKind::Budget { budget } => s.budget = Some(budget),
        }
        self.error.clear();
        Ok(())
    }
    fn accepted(&mut self, req: &SendMessage, result: SendMessageAccepted) {
        self.pending = false;
        self.accepted_text = Some(req.text.clone());
        if let Some(s) = self
            .snapshot
            .as_mut()
            .filter(|s| s.conversation_id == req.conversation_id)
        {
            let settled = s.messages.iter().any(|m| {
                m.request_id.as_deref() == Some(&req.request_id)
                    && !matches!(m.status, MessageStatus::Accepted | MessageStatus::Streaming)
            });
            if !settled && s.turn.is_none() && req.conversation_id == "main" {
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
    tx: mpsc::Sender<SendMessage>,
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
fn endpoint() -> Result<(String, String), String> {
    let root = root();
    let mut socket =
        UnixStream::connect(root.join("helper.sock")).map_err(|_| "正在启动环境…".to_string())?;
    socket
        .set_read_timeout(Some(Duration::from_secs(2)))
        .map_err(|e| e.to_string())?;
    socket
        .write_all(b"{\"id\":\"chat-status\",\"method\":\"status\"}\n")
        .map_err(|e| e.to_string())?;
    let mut line = String::new();
    BufReader::new(socket)
        .read_line(&mut line)
        .map_err(|e| e.to_string())?;
    let state: Value = serde_json::from_str(&line).map_err(|e| e.to_string())?;
    let ip = state["result"]["ip"]
        .as_str()
        .filter(|s| !s.is_empty())
        .ok_or_else(|| {
            format!(
                "环境：{} {}",
                state["result"]["phase"], state["result"]["error"]
            )
        })?;
    let bytes = std::fs::read(root.join("workspace/product/runtime.json"))
        .map_err(|_| "正在准备 Runtime…".to_string())?;
    let endpoint: RuntimeEndpoint = serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
    if endpoint.api_version != API_VERSION {
        return Err("Runtime 协议版本不兼容".into());
    }
    Ok((format!("http://{ip}:{}", endpoint.port), endpoint.token))
}
fn receive(view: &Arc<Mutex<View>>, _running: &AtomicBool) -> Result<(), String> {
    let (url, token) = endpoint()?;
    let (selected, query) = {
        let v = view.lock().unwrap();
        (v.selected.clone(), v.task_query.clone())
    };
    let auth = format!("Bearer {token}");
    let agent = ureq::AgentBuilder::new()
        .timeout(Duration::from_secs(3))
        .build();
    let task_page: TaskPage = agent
        .get(&format!("{url}{PATH_TASKS}/query"))
        .query("scope", &query.scope)
        .query("q", &query.q)
        .query("offset", &query.offset.to_string())
        .query("limit", &query.limit.to_string())
        .query("selected", &selected)
        .set("Authorization", &auth)
        .call()
        .map_err(|e| e.to_string())?
        .into_json()
        .map_err(|e| e.to_string())?;
    let main: Snapshot = agent
        .get(&format!("{url}{PATH_SNAPSHOT}"))
        .set("Authorization", &auth)
        .call()
        .map_err(|e| e.to_string())?
        .into_json()
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
            .get(&format!("{url}{path}"))
            .set("Authorization", &auth)
            .call()
            .map_err(|e| e.to_string())?
            .into_json()
            .map_err(|e| e.to_string())?
    };
    let mut v = view.lock().unwrap();
    if main.api_version != API_VERSION || snapshot.api_version != API_VERSION {
        return Err("Runtime 协议版本不兼容".into());
    }
    if v.main_snapshot
        .as_ref()
        .is_some_and(|old| old.boot_id != main.boot_id)
    {
        v.cached_tasks.clear();
    }
    v.main_snapshot = Some(main);
    v.apply_task_page(&query, &selected, task_page);
    if v.selected == selected {
        v.replace(snapshot)?;
    }
    Ok(())
}
impl Client {
    pub fn start() -> Self {
        let view = Arc::new(Mutex::new(View {
            error: "正在连接环境…".into(),
            task_query: TaskQuery {
                limit: 20,
                ..Default::default()
            },
            tasks_loading: true,
            ..Default::default()
        }));
        let running = Arc::new(AtomicBool::new(true));
        let (tx, rx) = mpsc::channel::<SendMessage>();
        let (refresh, refresh_rx) = mpsc::sync_channel(1);
        let v = view.clone();
        let active = running.clone();
        std::thread::spawn(move || {
            while active.load(Ordering::Relaxed) {
                if let Err(e) = receive(&v, &active) {
                    v.lock().unwrap().error = e;
                }
                crate::wake_ui();
                let _ = refresh_rx.recv_timeout(Duration::from_millis(500));
            }
        });
        // Writes never block behind the long-lived SSE response.
        let v = view.clone();
        let active = running.clone();
        std::thread::spawn(move || {
            let agent = ureq::AgentBuilder::new()
                .timeout(Duration::from_secs(3))
                .build();
            while let Ok(req) = rx.recv() {
                while active.load(Ordering::Relaxed) {
                    let (url, token) = match endpoint() {
                        Ok(v) => v,
                        Err(e) => {
                            v.lock().unwrap().command_error = e;
                            std::thread::sleep(Duration::from_millis(500));
                            continue;
                        }
                    };
                    let path = match req.conversation_id.as_str() {
                        "main" => PATH_MESSAGES.to_string(),
                        "new" => PATH_TASKS.to_string(),
                        id => format!("{PATH_TASKS}/{id}/messages"),
                    };
                    match agent
                        .post(&format!("{url}{path}"))
                        .set("Authorization", &format!("Bearer {token}"))
                        .send_json(json!(req))
                    {
                        Ok(response) => match response.into_json::<Value>() {
                            Ok(result) => {
                                let mut state = v.lock().unwrap();
                                state.command_error.clear();
                                if req.conversation_id == "new" {
                                    if let Some(id) = result["id"].as_str() {
                                        state.selected = id.into();
                                        state.snapshot = None;
                                    }
                                    state.pending = false;
                                    state.accepted_text = Some(req.text.clone());
                                } else {
                                    match serde_json::from_value::<SendMessageAccepted>(result) {
                                        Ok(result) => {
                                            state.accepted(&req, result);
                                        }
                                        Err(_) => {
                                            state.pending = false;
                                            state.command_error = "发送结果格式错误".into();
                                        }
                                    }
                                }
                                break;
                            }
                            Err(_) => {
                                v.lock().unwrap().command_error =
                                    "发送结果未确认，正在使用同一请求编号重试…".into();
                            }
                        },
                        Err(ureq::Error::Status(_, response)) => {
                            let detail = response
                                .into_json::<ApiError>()
                                .map(|e| e.message)
                                .unwrap_or_else(|_| "服务器拒绝请求".into());
                            let mut state = v.lock().unwrap();
                            state.pending = false;
                            state.command_error = detail;
                            break;
                        }
                        Err(_) => {
                            v.lock().unwrap().command_error =
                                "发送结果未确认，正在使用同一请求编号重试…".into();
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
                let (url, token) = endpoint()?;
                ureq::AgentBuilder::new()
                    .timeout(Duration::from_secs(3))
                    .build()
                    .post(&format!("{url}{}",if selected.is_empty(){PATH_CANCEL.to_string()}else{format!("{PATH_TASKS}/{selected}/cancel")}))
                    .set("Authorization", &format!("Bearer {token}"))
                    .send_json(json!({"request_id":uuid::Uuid::new_v4().to_string(),"user_message_id":message_id}))
                    .map_err(|e| e.to_string())?;
                Ok(())
            })();
            if let Err(e) = result {
                let mut state = view.lock().unwrap();
                state.command_error = format!("停止请求未确认：{e}");
                if let Some(turn) = state.snapshot.as_mut().and_then(|s| s.turn.as_mut()) {
                    turn.cancelling = false;
                }
            }
        });
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
    pub fn select(&self, id: String) {
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
        state.pending = true;
        state.command_error.clear();
        if self
            .tx
            .send(SendMessage {
                request_id: uuid::Uuid::new_v4().to_string(),
                conversation_id: if state.selected.is_empty() {
                    "main".into()
                } else {
                    state.selected.clone()
                },
                text,
            })
            .is_err()
        {
            state.pending = false;
            state.command_error = "发送服务已退出，请重开 App".into();
            return false;
        }
        true
    }
}

#[cfg(test)]
mod tests {
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

    use super::*;
    fn view() -> View {
        View {
            snapshot: Some(Snapshot {
                api_version: API_VERSION,
                boot_id: "boot".into(),
                cursor: 0,
                engine: EngineStatus {
                    engine: "test".into(),
                    phase: EnginePhase::Ready,
                    detail: None,
                    version: None,
                    auth_methods: vec![],
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
