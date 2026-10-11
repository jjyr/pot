use serde::{Deserialize, Serialize};
use serde_json::Value;
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Operation {
    Ready,
    Steering { finalizing: bool },
    Acknowledge { ids: Vec<String> },
    Permission { request: Value },
    Compaction { event: Value },
    Model { request: pot_core::ModelRequest },
}
