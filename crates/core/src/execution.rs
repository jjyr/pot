use crate::*;
use anyhow::Result;
use futures::{future::BoxFuture, stream::BoxStream};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{path::PathBuf, sync::Arc};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub enum Outcome {
    Success,
    Failed(String),
    Cancelled,
    Unknown,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub enum Update {
    PolicyDenied,
    MessageStarted,
    TextDelta(String),
    MessageFinished {
        text: String,
        outcome: Outcome,
    },
    ToolStarted {
        id: String,
        name: String,
        args: Value,
    },
    ToolFinished {
        id: String,
        name: String,
        result: Value,
        failed: bool,
    },
    ToolOutput {
        id: String,
        stream: String,
        text: String,
    },
    CompactionObserved {
        recovered: bool,
    },
    TurnFinished(Outcome),
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct AgentEvent {
    pub generation: u64,
    pub turn_id: String,
    pub seq: u64,
    pub update: Update,
}
pub type Sink<'a> = &'a mut (dyn FnMut(AgentEvent) -> Result<()> + Send);

pub struct EventGate {
    generation: u64,
    turn_id: String,
    seq: u64,
    terminal: Option<Outcome>,
}
impl EventGate {
    pub fn new(generation: u64, turn_id: &str) -> Self {
        Self {
            generation,
            turn_id: turn_id.into(),
            seq: 0,
            terminal: None,
        }
    }
    pub fn accept(&mut self, event: &AgentEvent) -> bool {
        if event.generation != self.generation
            || event.turn_id != self.turn_id
            || event.seq <= self.seq
            || self.terminal.is_some()
        {
            return false;
        }
        self.seq = event.seq;
        if let Update::TurnFinished(outcome) = &event.update {
            self.terminal = Some(outcome.clone());
        }
        true
    }
    pub fn finish(self) -> Outcome {
        self.terminal.unwrap_or(Outcome::Unknown)
    }
}

/// Transport-independent accepted input, distinct from the HTTP DTO.
#[derive(Clone)]
pub struct TurnRequest {
    pub request_id: String,
    pub text: String,
}
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum ExecutionRole {
    Main,
    Worker,
    Summary,
}

/// Ordered resource directories, highest priority first. The Agent does not
/// interpret roles or mount policies; callers own the visible filesystem view.
#[derive(Clone, Debug)]
pub struct SkillRoot {
    pub path: PathBuf,
    pub location: PathBuf,
    pub builtin: bool,
}
#[derive(Clone, Debug, Default)]
pub struct SkillResources {
    pub roots: Vec<SkillRoot>,
    pub reserved_names: Vec<String>,
    pub selection: Option<Vec<String>>,
}

/// Fixed per-execution capabilities; never exposes the Runtime or database.
#[derive(Clone)]
pub struct ExecutionContext {
    /// Caller-provided instructions, refreshed for this execution. No filesystem discovery.
    pub instructions: String,
    pub skill_resources: SkillResources,
    pub data: PathBuf,
    pub workspace: PathBuf,
    pub owner: String,
    pub role: ExecutionRole,
    pub main_prompt: String,
    pub feedback: Arc<dyn ExecutionFeedback>,
    pub tool_executor: Option<Arc<dyn ToolExecutor>>,
}
/// Synchronous acknowledgements preserve persistence-before-network ordering.
/// Runtime validates owner/generation/turn before applying these observations.
pub trait ExecutionFeedback: Send + Sync {
    /// Protected user instruction edits can require a fresh review even with a grant.
    fn requires_owner_review(&self, _tool: &str, _args: &Value) -> bool {
        false
    }

    /// Prepare execution-visible arguments and fix the environment before approval.
    fn prepare_execution_operation(
        &self,
        _name: &str,
        args: Value,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move { Ok(args) })
    }
    /// Current {policy, grants} for the resolved execution environment. Legacy
    /// grant arrays retain ask-by-default behavior. None disables this service.
    fn environment_grants(&self, _environment: &str) -> BoxFuture<'_, Result<Option<Value>>> {
        Box::pin(async move { self.permission_grants() })
    }
    /// HOME is independent from an operation's working directory.
    fn execution_home(&self) -> Option<PathBuf> {
        None
    }
    /// Resolve mutable product defaults before asking the owner to approve.
    fn prepare_permission_operation(&self, _name: &str, args: Value) -> Result<Value> {
        Ok(args)
    }
    /// Optional owner-controlled permission storage/UI. Policy lives in pot-agent.
    /// None disables Host tools; embeddings must opt in explicitly.
    fn permission_grants(&self) -> Result<Option<Value>> {
        Ok(None)
    }
    /// Execute an already authorized product-specific Host operation.
    fn execute_authorized_host(&self, _name: &str, _args: Value) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async { anyhow::bail!("Host operation backend unavailable") })
    }
    fn request_permission(&self, _request: Value) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async { anyhow::bail!("Owner approval service unavailable") })
    }
    /// Observation never substitutes for authoritative usage settlement.
    fn observe(&self, _event: &ExecutionObservation) -> Result<()> {
        Ok(())
    }
    /// Durable, owner/turn-bound input. Acknowledged only after Agent context is saved.
    fn steering(&self, _finalizing: bool) -> Result<Vec<(String, String)>> {
        Ok(vec![])
    }
    fn acknowledge_steering(&self, _ids: &[String]) -> Result<()> {
        Ok(())
    }
    /// Remote hosts can preserve the same finalization boundary without blocking
    /// a synchronous feedback callback on network I/O.
    fn fetch_steering(&self, finalizing: bool) -> BoxFuture<'_, Result<Vec<(String, String)>>> {
        Box::pin(async move { self.steering(finalizing) })
    }
    fn commit_steering<'a>(&'a self, ids: &'a [String]) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move { self.acknowledge_steering(ids) })
    }
    /// Metadata only: never provider payloads, credentials, shell output, or arguments.
    fn diagnostic(&self, _event: &Value) -> Result<()> {
        Ok(())
    }
    /// Cooperative pause gate; does not interrupt an in-flight request/tool.
    fn ready(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }
    /// A terminal run is acknowledged only after all issued effects are known.
    /// Distributed hosts may wait here for original receipts, without new actions.
    fn settle(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }
    fn load_context(&self) -> Result<Option<Value>> {
        anyhow::bail!("Context storage unavailable")
    }
    fn save_context(&self, _: &Value) -> Result<()> {
        anyhow::bail!("Context storage unavailable")
    }
    fn cancelled(&self) -> bool;
    fn cancellation(&self) -> BoxFuture<'_, ()>;
    fn record_turn(&self, request: &str) -> Result<()>;
    fn input_streaming(&self, request: &str) -> Result<()>;
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum ExecutionObservation {
    RequestStarted {
        id: String,
        stage: String,
        compaction_id: Option<String>,
    },
    UsageObserved {
        id: String,
        input_tokens: Option<u64>,
        output_tokens: Option<u64>,
        cache_read_tokens: Option<u64>,
        cache_write_tokens: Option<u64>,
        reasoning_tokens: Option<u64>,
    },
    RequestFinished {
        id: String,
        input_tokens: Option<u64>,
        output_tokens: Option<u64>,
        error: Option<String>,
    },
    ContextEstimated {
        tokens: u64,
        threshold: u64,
    },
    CompactionLifecycle {
        id: String,
        action: String,
    },
}
/// Credential-free request description. Services own transport and authentication.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModelRequest {
    pub id: String,
    pub protocol: String,
    pub provider: String,
    pub model: String,
    pub body: Value,
    pub compaction_id: Option<String>,
}
/// Explicit Control services, implemented by in-memory or authenticated network adapters.
/// Agents never discover an implicit HTTP backend; context/control use ExecutionFeedback.
pub trait ExecutionServices: Send + Sync {
    fn model<'a>(
        &'a self,
        request: ModelRequest,
    ) -> BoxFuture<'a, Result<BoxStream<'a, Result<Vec<u8>>>>>;
    fn rpc<'a>(
        &'a self,
        session: Option<&'a str>,
        id: &'a str,
        method: &'a str,
        params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>>;
    /// Product implementations must durably acknowledge lifecycle before returning.
    fn compaction(&self, event: Value) -> BoxFuture<'_, Result<()>>;
}

/// Environment effects are dispatched by the embedder, never implicitly executed by the Agent.
pub trait ToolExecutor: Send + Sync {
    /// A routed executor consumes Environment selectors; a direct executor does not.
    fn routes_environments(&self) -> bool {
        false
    }
    fn execute<'a>(
        &'a self,
        context: &'a ExecutionContext,
        policy: &'a ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>>;
    /// Settle turn operations before emitting a terminal event. Normal turns may
    /// retain managed processes scoped to the owning Agent session.
    fn finish<'a>(&'a self, context: &'a ExecutionContext) -> BoxFuture<'a, Result<()>>;
    /// Failed turns must drain their processes rather than retaining them.
    fn abort<'a>(&'a self, context: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        self.finish(context)
    }
}
