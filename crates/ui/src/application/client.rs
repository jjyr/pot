use pot_protocol::*;
use serde_json::Value;
use std::{
    collections::HashMap,
    rc::Rc,
    sync::{Arc, Mutex},
};
#[derive(Clone, PartialEq)]
pub struct RunningTask {
    pub id: String,
    pub title: String,
    pub progress: String,
    pub executor_icon: String,
}

#[derive(Default, Clone, PartialEq)]
pub struct View {
    pub client_mode: String,
    pub runtime: Value,
    pub machines: Value,
    pub completions: Value,
    pub machine_reply: Value,
    pub machine_pending: bool,
    pub assistants: Value,
    pub selected_assistant: String,
    pub assistant_rules: Value,
    pub assistant_saved_owner: Option<String>,
    pub created_assistant: Option<String>,
    pub assistant_pending: bool,
    pub permissions: Value,
    pub usage_query: Option<(String, u32, usize, String)>,
    pub usage: Value,
    pub usage_error: String,
    pub usage_pending: bool,
    pub usage_notice: String,
    pub usage_fetched: Option<std::time::Instant>,
    pub usage_loaded: Option<(String, u32, usize, String)>,
    pub snapshot: Option<Snapshot>,
    /// Connection error is cleared only after a successful snapshot/stream update.
    pub error: String,
    /// Command errors survive background updates until the next user attempt.
    pub command_error: String,
    pub pending: bool,
    pub sending: crate::state::SendState,
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
    pub generation: u64,
    pub cached_tasks: HashMap<String, Arc<Snapshot>>,
    pub cached_assistants: HashMap<String, Arc<Snapshot>>,
}
impl View {
    pub fn set_runtime(&mut self, runtime: Value) {
        if self.runtime["runtime_id"] != runtime["runtime_id"] {
            self.cached_assistants.clear();
            self.cached_tasks.clear();
            self.snapshot = None;
            self.main_snapshot = None;
        }
        self.runtime = runtime;
    }
    pub fn assistant_id(&self) -> &str {
        if self.selected_assistant.is_empty() {
            "main"
        } else {
            &self.selected_assistant
        }
    }
    pub fn select_assistant(&mut self, id: String) -> bool {
        if self.assistant_id() == id || self.pending {
            return false;
        }
        let runtime = self.runtime["runtime_id"].as_str().unwrap_or("");
        if let Some(snapshot) = &self.main_snapshot {
            if self.cached_assistants.len() >= 64 {
                self.cached_assistants.clear();
            }
            self.cached_assistants.insert(
                format!("{runtime}/{}", self.assistant_id()),
                Arc::new(snapshot.clone()),
            );
        }
        let cached = self
            .cached_assistants
            .get(&format!("{runtime}/{id}"))
            .map(|s| s.as_ref().clone());
        self.generation += 1;
        self.selected_assistant = id;
        self.selected.clear();
        self.snapshot = cached.clone();
        self.main_snapshot = cached;
        self.cached_tasks.clear();
        self.permissions = Value::Null;
        self.usage_pending = false;
        self.usage_loaded = None;
        self.usage_fetched = None;
        self.schedule_pending = false;
        self.schedules = SchedulePage::default();
        self.schedule_selected.clear();
        self.usage = Value::Null;
        self.assistant_rules = Value::Null;
        self.selected_task = None;
        self.running_tasks.clear();
        self.running_total = 0;
        self.error = if self.snapshot.is_some() {
            String::new()
        } else {
            super::i18n::text("client.loading").into()
        };
        true
    }
    pub fn is_cooking(&self) -> bool {
        self.running_total > 0
            || self.main_snapshot.as_ref().is_some_and(|s| {
                s.turn.is_some()
                    && s.agent.phase == AgentPhase::Ready
                    && s.agent.detail.as_deref() != Some("等待 Pot 额度恢复")
            })
    }

    pub fn apply_task_page(&mut self, query: &TaskQuery, selected: &str, page: TaskPage) {
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

    pub fn select(&mut self, id: String) -> bool {
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
        self.generation += 1;
        self.snapshot = if id.is_empty() || id == "new" {
            self.main_snapshot.clone()
        } else {
            self.cached_tasks.get(&id).map(|s| s.as_ref().clone())
        };
        self.selected_task = self.tasks.iter().find(|t| t.id == id).cloned();
        self.selected = id;
        self.command_error.clear();
        if self.snapshot.is_none() && self.error.is_empty() {
            self.error = super::i18n::text("client.loading").into();
        } else if self.snapshot.is_some()
            && super::i18n::matches_message("client.loading", &self.error)
        {
            self.error.clear();
        }
        true
    }
    pub fn can_send(&self) -> bool {
        crate::state::can_send(
            self.snapshot.as_ref(),
            !self.selected.is_empty(),
            self.pending,
            self.error.is_empty(),
        )
    }
    pub fn replace(&mut self, snapshot: Snapshot) -> Result<(), String> {
        if snapshot.api_version != API_VERSION {
            return Err(super::i18n::text("client.incompatible-runtime-protocol-version").into());
        }
        self.snapshot = Some(snapshot);
        self.error.clear();
        Ok(())
    }
    pub fn apply(&mut self, event: Event) -> Result<(), String> {
        let s = self
            .snapshot
            .as_mut()
            .ok_or(super::i18n::text("client.chat-snapshot-missing"))?;
        crate::state::apply_event(s, event)?;
        self.error.clear();
        Ok(())
    }
    pub fn accepted(&mut self, req: &SendMessage, result: SendMessageAccepted) {
        self.pending = false;
        self.sending.accept(&req.request_id);
        self.accepted_text = Some(crate::state::Receipt::from_request(req).text);
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

/// A host owns IO and publishes authoritative projections to this shared view.
pub trait Driver {
    fn dispatch(&self, kind: &str, value: Value);
    fn send(&self, text: String) -> bool;
}
#[derive(Clone)]
pub struct Client {
    pub view: Arc<Mutex<View>>,
    driver: Rc<dyn Driver>,
}
impl Client {
    pub fn new(view: Arc<Mutex<View>>, driver: Rc<dyn Driver>) -> Self {
        Self { view, driver }
    }
    pub fn start() -> Self {
        super::platform::with(|p| p.client(&p.mode()))
    }
    pub fn start_mode(mode: &str) -> Self {
        super::platform::with(|p| p.client(mode))
    }
    pub fn send(&self, text: String) -> bool {
        self.driver.send(text)
    }
    pub fn refresh(&self) {
        self.driver.dispatch("refresh", Value::Null)
    }
    pub fn retry(&self) {
        self.driver.dispatch("retry", Value::Null)
    }
    pub fn cancel(&self) {
        self.driver.dispatch("cancel", Value::Null)
    }
    pub fn select(&self, id: String) {
        self.driver.dispatch("select", id.into())
    }
    pub fn select_assistant(&self, id: String) {
        self.driver.dispatch("select-assistant", id.into())
    }
    pub fn permission_decision(&self, v: Value) {
        self.driver.dispatch("permission", v)
    }
    pub fn sandbox_lifecycle(&self, v: Value) {
        self.driver.dispatch("sandbox-lifecycle", v)
    }
    pub fn usage_action(&self, path: &str, v: Value) {
        self.driver
            .dispatch("usage", serde_json::json!({"path":path,"body":v}))
    }
    pub fn schedule_select(&self, id: String) {
        self.driver.dispatch("schedule-select", id.into())
    }
    pub fn schedule_action(&self, mut v: Value) {
        v["request_id"] = serde_json::json!(uuid::Uuid::new_v4().to_string());
        self.driver.dispatch("schedule", v)
    }
    pub fn machine_command(&self, v: Value) {
        self.driver.dispatch("machine", v)
    }
    pub fn assistant_save(&self, v: Value) {
        self.driver.dispatch("assistant-save", v)
    }
    pub fn filter_execution_target(&self, v: String) {
        self.driver.dispatch("execution-target", v.into())
    }
    pub fn filter_tasks(&self, scope: String, q: String, offset: usize) {
        self.driver.dispatch(
            "tasks",
            serde_json::json!({"scope":scope,"q":q,"offset":offset}),
        )
    }
}

pub fn task_progress(snapshot: &Snapshot) -> String {
    if snapshot.turn.as_ref().is_some_and(|t| t.cancelling) {
        return super::i18n::text("client.stopping").into();
    }
    match snapshot.messages.last() {
        Some(m) if m.role == Role::Tool && m.status == MessageStatus::Streaming => {
            // Tool headers are runtime facts, not generated summaries.
            super::i18n::tr!(
                "client.running",
                m.text
                    .lines()
                    .next()
                    .unwrap_or(super::i18n::text("client.tool"))
                    .chars()
                    .take(36)
                    .collect::<String>()
            )
        }
        Some(m) if m.role == Role::Assistant && m.status == MessageStatus::Streaming => {
            super::i18n::text("client.generating-a-reply").into()
        }
        _ => super::i18n::text("client.working").into(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    fn snapshot(owner: &str) -> Snapshot {
        Snapshot {
            api_version: API_VERSION,
            budget: None,
            boot_id: "boot".into(),
            cursor: 1,
            agent: AgentStatus {
                model: "fixture".into(),
                phase: AgentPhase::Ready,
                detail: None,
            },
            conversation_id: owner.into(),
            messages: vec![],
            turn: None,
        }
    }
    #[test]
    fn assistant_switch_restores_cached_history_and_control_change_discards_it() {
        let mut view = View::default();
        view.set_runtime(json!({"runtime_id":"control-a"}));
        view.main_snapshot = Some(snapshot("main"));
        view.snapshot = view.main_snapshot.clone();
        assert!(view.select_assistant("second".into()));
        assert!(view.snapshot.is_none());
        view.main_snapshot = Some(snapshot("second"));
        assert!(view.select_assistant("main".into()));
        assert_eq!(view.snapshot.as_ref().unwrap().conversation_id, "main");
        assert!(view.error.is_empty());
        view.set_runtime(json!({"runtime_id":"control-b"}));
        assert!(view.main_snapshot.is_none());
        assert!(view.snapshot.is_none());
        assert!(view.cached_assistants.is_empty());
    }
}
