//! Product domain and execution contracts. No database, HTTP server or CLI implementation.
mod agent;
pub mod environment_tools;
mod execution;
pub mod plugins;
pub use agent::*;
mod model;
mod model_catalog;
pub use execution::*;
pub use model::*;
pub use model_catalog::*;

/// Shared Pot/MCP tool schema; adapters cannot maintain divergent definitions.
pub const TOOL_SCHEMA: &str = include_str!("pot-tools.json");
