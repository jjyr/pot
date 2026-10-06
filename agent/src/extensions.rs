//! Code-level extension points. Runtime authorization and allowance checks remain authoritative.
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::ExecutionContext;
use pot_gateway::{
    codecs::Protocol,
    model::{self, Message},
};
use serde_json::Value;

pub trait AgentExtensions: Send + Sync {
    /// Same catalog injection for Main and Worker; providers select by context
    /// and expose only assigned skills. Summary turns do not request skills.
    /// Bundles must be readable at their Sandbox locations when tools run.
    /// Discovery, installation and resource synchronization belong to callers.
    fn skills(&self, _context: &ExecutionContext) -> Result<Vec<crate::Skill>> {
        let catalog = crate::discover_skills(&_context.skill_resources)?;
        for diagnostic in &catalog.diagnostics {
            _context
                .feedback
                .diagnostic(&serde_json::json!({"kind":"skill_diagnostic","message":diagnostic}))?;
        }
        Ok(catalog.skills)
    }

    fn tools(&self, _context: &ExecutionContext) -> Result<Vec<Value>> {
        Ok(vec![])
    }
    fn execute<'a>(
        &'a self,
        _context: &'a ExecutionContext,
        name: &'a str,
        _args: &'a Value,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move { anyhow::bail!("Unknown extension tool: {name}") })
    }
    /// Called on a copy before each request; persistent history is not mutated.
    fn prepare_context(
        &self,
        _context: &ExecutionContext,
        _messages: &mut Vec<Message>,
    ) -> Result<()> {
        Ok(())
    }
    /// A caller may supply its own tokenizer; the default conservatively counts bytes.
    fn estimate_tokens(
        &self,
        protocol: &Protocol,
        model: &str,
        system: &str,
        messages: &[Message],
        tools: &[Value],
    ) -> Result<u64> {
        Ok(serde_json::to_vec(&model::context_payload(
            protocol, model, system, messages, tools,
        )?)?
        .len() as u64)
    }
}
pub(crate) struct Defaults;
impl AgentExtensions for Defaults {}
