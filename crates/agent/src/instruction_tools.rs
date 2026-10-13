//! Owner-reviewed assistant instruction tools.
use serde_json::{Value, json};
pub fn instructions_tool(name: &str) -> bool {
    ["read_agent_instructions", "update_agent_instructions"].contains(&name)
}
pub fn instruction_schemas() -> Vec<Value> {
    vec![
        json!({"name":"read_agent_instructions","description":"Read this chat assistant's own AGENTS.md content and revision. Independent of the execution target; workers cannot use this tool.","inputSchema":{"type":"object","properties":{},"additionalProperties":false}}),
        json!({"name":"update_agent_instructions","description":"Replace this chat assistant's own AGENTS.md after owner approval. Read it first to inspect the current content. Every update requires approval; permissions are not changed. Workers cannot use this tool.","inputSchema":{"type":"object","properties":{"text":{"type":"string","maxLength":262144}},"required":["text"],"additionalProperties":false}}),
    ]
}
