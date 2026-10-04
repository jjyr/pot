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
    Running,
    Complete,
    Failed,
    Cancelled,
    Interrupted,
}
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskInfo {
    pub id: String,
    pub title: String,
    pub phase: TaskPhase,
    pub pending: usize,
    pub created_at_ms: i64,
    #[serde(default)]
    pub updated_at_ms: i64,
    /// Explicit cooperative resource name; empty means a private task directory.
    pub resource: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateTask {
    pub request_id: String,
    pub text: String,
    #[serde(default)]
    pub resource: String,
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
            Self::Queued | Self::Running => "active",
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
