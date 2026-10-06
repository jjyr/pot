//! Product Control API shared by the macOS client and the in-sandbox runtime.
//!
//! Transport: HTTP + Server-Sent Events on [`RUNTIME_PORT`], bearer token auth.
//! Reconnect contract: fetch [`Snapshot`], then subscribe to events after
//! `snapshot.cursor`. Durable events carry a cursor; ephemeral streaming
//! updates carry `cursor: None` and are superseded by the next snapshot.

use serde::{Deserialize, Serialize};

pub const API_VERSION: u32 = 1;
pub const RUNTIME_PORT: u16 = 17700;
/// Written by the runtime into the shared data directory; read by the helper.
pub const RUNTIME_ENDPOINT_FILE: &str = "runtime.json";

pub const PATH_SNAPSHOT: &str = "/api/v1/snapshot";
pub const PATH_EVENTS: &str = "/api/v1/events";
pub const PATH_MESSAGES: &str = "/api/v1/messages";
pub const PATH_AUTHENTICATE: &str = "/api/v1/engine/authenticate";
pub const PATH_CANCEL: &str = "/api/v1/turn/cancel";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct RuntimeEndpoint {
    pub api_version: u32,
    pub port: u16,
    pub token: String,
    pub boot_id: String,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum EnginePhase {
    Installing,
    Starting,
    NeedsAuth,
    Authenticating,
    Ready,
    Failed,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct AuthMethod {
    pub id: String,
    pub name: String,
    pub description: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct EngineStatus {
    pub engine: String,
    pub phase: EnginePhase,
    pub detail: Option<String>,
    pub version: Option<String>,
    pub auth_methods: Vec<AuthMethod>,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Role {
    User,
    Assistant,
    Tool,
    System,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum MessageStatus {
    /// Persisted, not yet consumed by the engine.
    Accepted,
    Streaming,
    Complete,
    Failed,
    Cancelled,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Message {
    pub id: String,
    pub conversation_id: String,
    pub role: Role,
    pub text: String,
    pub status: MessageStatus,
    pub created_at_ms: i64,
    pub request_id: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TurnState {
    pub user_message_id: String,
    pub cancelling: bool,
}

/// Counts reservations, including failed requests. Never inferred from usage tokens.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct RequestBudget {
    pub used: u64,
    pub limit: u64,
    pub max_output_tokens: u64,
}
impl RequestBudget {
    pub fn remaining(&self) -> u64 {
        self.limit.saturating_sub(self.used)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Snapshot {
    #[serde(default)]
    pub budget: Option<RequestBudget>,
    pub api_version: u32,
    pub boot_id: String,
    pub cursor: i64,
    pub engine: EngineStatus,
    pub conversation_id: String,
    pub messages: Vec<Message>,
    pub turn: Option<TurnState>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum EventKind {
    Budget { budget: RequestBudget },
    Engine { status: EngineStatus },
    Message { message: Message },
    Turn { turn: Option<TurnState> },
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Event {
    pub event_id: String,
    pub cursor: Option<i64>,
    #[serde(flatten)]
    pub kind: EventKind,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct SendMessage {
    pub request_id: String,
    pub conversation_id: String,
    pub text: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct SendMessageAccepted {
    pub message_id: String,
    /// True when the request ID was already seen; no new turn was started.
    pub duplicate: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Authenticate {
    pub request_id: String,
    pub method_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct CancelTurn {
    pub request_id: String,
    pub user_message_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ApiError {
    pub code: String,
    pub message: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn event_round_trip_is_tagged_and_flat() {
        let event = Event {
            event_id: "e1".into(),
            cursor: Some(3),
            kind: EventKind::Turn { turn: None },
        };
        let text = serde_json::to_string(&event).unwrap();
        assert_eq!(
            text,
            r#"{"event_id":"e1","cursor":3,"type":"turn","turn":null}"#
        );
        assert_eq!(serde_json::from_str::<Event>(&text).unwrap(), event);
    }
}

pub const PATH_TASKS: &str = "/api/v1/tasks";
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum TaskPhase {
    Queued,
    Waiting,
    Running,
    Complete,
    Failed,
    Cancelled,
    Interrupted,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskInfo {
    pub sandbox_id: String,
    pub id: String,
    pub title: String,
    pub phase: TaskPhase,
    pub pending: usize,
    pub created_at_ms: i64,
    #[serde(default)]
    pub updated_at_ms: i64,
    /// Explicit cooperative resource name; empty means a private task directory.
    pub resource: String,
    #[serde(default)]
    pub routing: Option<TaskRouting>,
    /// Actual executor's brand. Model/configuration details remain in task details.
    #[serde(default)]
    pub executor_icon: String,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskRouting {
    pub profile: String,
    pub revision: u64,
    pub provider: String,
    pub reason: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub preference_revision: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub selection_reason: Option<String>,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct TaskRequirements {
    #[serde(default)]
    pub tools: bool,
    #[serde(default)]
    pub desktop: bool,
    #[serde(default)]
    pub exact_budget: bool,
    #[serde(default)]
    pub live_steering: bool,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct CreateTask {
    #[serde(default)]
    pub sandbox_id: String,
    pub request_id: String,
    pub text: String,
    #[serde(default)]
    pub resource: String,
    #[serde(default)]
    pub profile: Option<String>,
    #[serde(default)]
    pub requirements: TaskRequirements,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub preference_revision: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub selection_reason: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskInput {
    pub request_id: String,
    pub text: String,
}

/// Execution phase is preserved when a task moves into the archive view.
impl TaskPhase {
    pub fn scope(&self) -> &'static str {
        match self {
            Self::Queued | Self::Running | Self::Waiting => "active",
            Self::Failed | Self::Interrupted => "attention",
            Self::Complete | Self::Cancelled => "archived",
        }
    }
}
fn active_scope() -> String {
    "active".into()
}
fn task_page_limit() -> usize {
    10
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskQuery {
    #[serde(default = "active_scope")]
    pub scope: String,
    #[serde(default)]
    pub phase: Option<TaskPhase>,
    #[serde(default)]
    pub q: String,
    #[serde(default)]
    pub offset: usize,
    #[serde(default = "task_page_limit")]
    pub limit: usize,
    /// UI may retain selected task metadata even when it is outside this page.
    #[serde(default)]
    pub selected: String,
}
impl Default for TaskQuery {
    fn default() -> Self {
        Self {
            scope: active_scope(),
            phase: None,
            q: String::new(),
            offset: 0,
            limit: task_page_limit(),
            selected: String::new(),
        }
    }
}
#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskCounts {
    pub active: usize,
    pub attention: usize,
    pub archived: usize,
}
#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskPage {
    pub items: Vec<TaskInfo>,
    pub total: usize,
    pub offset: usize,
    pub next_offset: Option<usize>,
    pub counts: TaskCounts,
    pub selected: Option<TaskInfo>,
}

fn default_zone() -> String {
    "Asia/Shanghai".into()
}
fn default_misfire() -> String {
    "coalesce".into()
}
fn default_notify() -> String {
    "changes".into()
}
fn enabled() -> bool {
    true
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ScheduleSpec {
    #[serde(default)]
    pub sandbox_id: String,
    pub title: String,
    pub goal: String,
    #[serde(default = "default_zone")]
    pub timezone: String,
    /// once / interval / daily / weekly
    pub kind: String,
    #[serde(default)]
    pub at: String,
    #[serde(default)]
    pub every_minutes: u64,
    #[serde(default)]
    pub time: String,
    #[serde(default)]
    pub weekdays: Vec<u32>,
    #[serde(default = "default_misfire")]
    pub misfire: String,
    #[serde(default = "default_notify")]
    pub notify: String,
    #[serde(default = "enabled")]
    pub enabled: bool,
    #[serde(default)]
    pub profile: Option<String>,
    #[serde(default)]
    pub requirements: TaskRequirements,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ScheduleInfo {
    pub id: String,
    pub spec: ScheduleSpec,
    pub next_at_ms: Option<i64>,
    pub next_label: String,
    pub deleted: bool,
    pub last_error: String,
    pub baseline: Option<String>,
    pub created_at_ms: i64,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ScheduleRun {
    pub id: String,
    pub scheduled_for_ms: i64,
    #[serde(default)]
    pub scheduled_label: String,
    #[serde(default)]
    pub generation: u64,
    pub task_id: Option<String>,
    pub state: String,
    pub summary: String,
    pub changed: Option<bool>,
    pub finished_at_ms: Option<i64>,
    pub notify: String,
}
#[derive(Debug, Default, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct SchedulePage {
    pub items: Vec<ScheduleInfo>,
    pub total: usize,
    pub offset: usize,
    pub next_offset: Option<usize>,
    pub selected: Option<ScheduleInfo>,
    pub runs: Vec<ScheduleRun>,
    pub runs_next_offset: Option<usize>,
}
