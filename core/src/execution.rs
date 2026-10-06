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
pub struct DriverEvent {
    pub epoch: u64,
    pub turn_id: String,
    pub seq: u64,
    pub update: Update,
}
pub type Sink<'a> = &'a mut (dyn FnMut(DriverEvent) -> Result<()> + Send);

/// Host and session lifetimes differ: Pot runs in-process; native hosts can
/// multiplex independent sessions behind this execution boundary.
pub trait DriverHost: Send + Sync {
    /// Conservative defaults: unsupported observations are never inferred.
    fn describe(&self, spec: &ResolvedSpec) -> Result<DriverDescription> {
        self.probe(spec)?;
        Ok(DriverDescription::default())
    }
    fn probe(&self, spec: &ResolvedSpec) -> Result<()>;
    fn session(
        &self,
        context: ExecutionContext,
        binding: Binding,
    ) -> Result<Box<dyn SessionHandle>>;
}
pub trait SessionHandle: Send {
    /// Must release any session-owned resources after the active turn has stopped.
    fn close(&mut self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }
    fn start_turn<'a>(
        &'a mut self,
        request: &'a TurnRequest,
        resuming: bool,
        sink: Sink<'a>,
    ) -> BoxFuture<'a, Result<()>>;
}

pub struct EventGate {
    epoch: u64,
    turn_id: String,
    seq: u64,
    terminal: Option<Outcome>,
}
impl EventGate {
    pub fn new(epoch: u64, turn_id: &str) -> Self {
        Self {
            epoch,
            turn_id: turn_id.into(),
            seq: 0,
            terminal: None,
        }
    }
    pub fn accept(&mut self, event: &DriverEvent) -> bool {
        if event.epoch != self.epoch
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

/// Fixed per-execution capabilities; never exposes the Runtime or database.
#[derive(Clone)]
pub struct ExecutionContext {
    pub data: PathBuf,
    pub workspace: PathBuf,
    pub owner: String,
    pub role: ExecutionRole,
    pub backend_url: String,
    pub lane_token: String,
    pub main_prompt: String,
    pub feedback: Arc<dyn ExecutionFeedback>,
    pub tool_executor: Option<Arc<dyn ToolExecutor>>,
}
/// Synchronous acknowledgements preserve persistence-before-network ordering.
/// Runtime validates owner/epoch/turn before applying these observations.
pub trait ExecutionFeedback: Send + Sync {
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
    /// Metadata only: never provider payloads, credentials, shell output, or arguments.
    fn diagnostic(&self, _event: &Value) -> Result<()> {
        Ok(())
    }
    /// Cooperative pause gate; does not interrupt an in-flight request/tool.
    fn ready(&self) -> BoxFuture<'_, Result<()>> {
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
    fn native_identity(&self, identity: &str) -> Result<()>;
    fn binding_broken(&self, reason: &str) -> Result<()>;
    fn needs_rebind(&self, reason: &str) -> Result<()>;
    fn record_turn(&self, request: &str) -> Result<()>;
    fn input_streaming(&self, request: &str) -> Result<()>;
    fn begin_usage(&self, ledger: &str, spec: &ResolvedSpec, epoch: u64) -> Result<()>;
    fn initialize_usage(&self, native: &str) -> Result<()>;
    fn cumulative_usage(&self, ledger: &str, native: &str, usage: &Value) -> Result<()>;
    fn incomplete_usage(&self, ledger: &str) -> Result<()>;
}

/// Execution controls and observation precision are negotiated independently.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct DriverDescription {
    pub configurable_context: bool,
    pub configurable_tools: bool,
    pub injectable_services: bool,
    pub request_usage: bool,
    pub native_cumulative_usage: bool,
    pub compaction_lifecycle: bool,
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
#[derive(Clone, Debug)]
pub struct ModelRequest {
    pub id: String,
    pub protocol: String,
    pub provider: String,
    pub model: String,
    pub body: Value,
    pub compaction_id: Option<String>,
}
/// Optional standalone services. Product hosts continue using authenticated HTTP
/// services when none are injected; context/control remain in ExecutionFeedback.
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
    fn execute<'a>(
        &'a self,
        context: &'a ExecutionContext,
        policy: &'a ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>>;
    /// Await remote session cleanup before emitting a terminal turn event.
    fn finish<'a>(&'a self, context: &'a ExecutionContext) -> BoxFuture<'a, Result<()>>;
}
