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
