//! Assistant message wire contracts shared by Control and relay clients.
use serde::{Deserialize, Serialize};

pub const API: &str = "/api/v1/assistant-mail";
pub const MAX_TEXT: usize = 65536;
pub const PAGE_SIZE: usize = 32;
pub const MAX_HOPS: u32 = 16;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(deny_unknown_fields)]
pub struct AssistantRef {
    pub control_id: String,
    pub assistant_id: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Assistant {
    pub address: AssistantRef,
    pub name: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Directory {
    pub control_id: String,
    pub assistants: Vec<Assistant>,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Envelope {
    pub id: String,
    pub from: AssistantRef,
    pub to: AssistantRef,
    pub conversation_id: String,
    pub reply_to: Option<String>,
    pub text: String,
    pub hops: u32,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Send {
    pub to: AssistantRef,
    pub text: String,
    #[serde(default)]
    pub reply_to: Option<String>,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Receipt {
    // Full immutable envelope binds acknowledgement to contents and both endpoints.
    pub message: Envelope,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
pub enum RelayRequest {
    Directory,
    History { assistant_id: String, after: i64 },
    Register { directory: Directory },
    Pending { target_control_id: String },
    Deliver { message: Envelope },
    Acknowledge { receipt: Receipt },
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Record {
    pub sequence: i64,
    pub message: Envelope,
    pub state: String,
    pub detail: Option<String>,
}

pub fn identifier(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_'))
}
impl AssistantRef {
    pub fn valid(&self) -> bool {
        identifier(&self.control_id) && identifier(&self.assistant_id)
    }
}
impl Envelope {
    pub fn valid(&self) -> bool {
        identifier(&self.id)
            && self.from.valid()
            && self.to.valid()
            && uuid::Uuid::parse_str(&self.conversation_id).is_ok()
            && self.reply_to.as_deref().is_none_or(identifier)
            && !self.text.trim().is_empty()
            && self.text.len() <= MAX_TEXT
            && self.hops <= MAX_HOPS
    }
}
