//! Code-level extension points. Runtime authorization and allowance checks remain authoritative.
use anyhow::Result;
use futures::future::BoxFuture;
use pot_core::ExecutionContext;
use pot_model_protocol::{codecs::Protocol, model::Message};
use serde_json::Value;

pub trait AgentExtensions: Send + Sync {
    /// Caller-owned instruction injection for Main and Worker. No discovery or I/O.
    /// Summary turns do not invoke this provider; changes append classified history sections.
    fn instructions(&self, context: &ExecutionContext) -> Result<String> {
        Ok(context.instructions.clone())
    }
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
    /// A caller may supply its own tokenizer; the default uses Codex's content-cost
    /// rules (UTF-8 bytes / 4, rounded per item). This is not billed usage.
    fn estimate_tokens(
        &self,
        protocol: &Protocol,
        model: &str,
        system: &str,
        messages: &[Message],
        tools: &[Value],
    ) -> Result<u64> {
        crate::token_estimate::estimate(protocol, model, system, messages, tools)
    }
}
pub(crate) struct Defaults;
impl AgentExtensions for Defaults {}
