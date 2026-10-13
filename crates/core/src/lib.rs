//! Product domain and execution contracts. No database, HTTP server or CLI implementation.
mod agent;
mod execution_target;
pub mod execution_tools;
pub use execution_target::*;
mod execution;
pub mod plugins;
pub use agent::*;
mod model;
mod model_catalog;
mod placement;
pub use execution::*;
pub use model::*;
pub use model_catalog::*;
pub use placement::*;

/// Shared Pot/MCP tool schema; adapters cannot maintain divergent definitions.
pub const TOOL_SCHEMA: &str = include_str!("pot-tools.json");
