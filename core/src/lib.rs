//! Product domain and execution contracts. No database, HTTP server or CLI implementation.
mod agent;
mod execution;
pub use agent::*;
mod model;
pub use execution::*;
pub use model::*;

/// Shared Pot/MCP tool schema; adapters cannot maintain divergent definitions.
pub const TOOL_SCHEMA: &str = include_str!("pot-tools.json");
