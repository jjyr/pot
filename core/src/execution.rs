use crate::*;
use anyhow::Result;
use futures::future::BoxFuture;
use serde_json::Value;
use std::{path::PathBuf, sync::Arc};

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Outcome {
    Success,
    Failed(String),
    Cancelled,
    Unknown,
}
#[derive(Clone, Debug)]
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
    CompactionObserved {
        recovered: bool,
    },
    TurnFinished(Outcome),
}
#[derive(Clone, Debug)]
pub struct DriverEvent {
    pub epoch: u64,
    pub turn_id: String,
    pub seq: u64,
    pub update: Update,
}
pub type Sink<'a> = &'a mut (dyn FnMut(DriverEvent) -> Result<()> + Send);

/// Host and session lifetimes differ: Pi sessions spawn a process per turn;
/// a future long-lived host can multiplex sessions behind this same boundary.
pub trait DriverHost: Send + Sync {
    fn probe(&self, spec: &ResolvedSpec) -> Result<()>;
    fn session(
        &self,
        context: ExecutionContext,
        binding: Binding,
    ) -> Result<Box<dyn SessionHandle>>;
}
pub trait SessionHandle: Send {
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
    pub pi_program: String,
    pub main_prompt: String,
    pub model_api: String,
    pub feedback: Arc<dyn ExecutionFeedback>,
}
/// Synchronous acknowledgements preserve persistence-before-network ordering.
/// Runtime validates owner/epoch/turn before applying these observations.
pub trait ExecutionFeedback: Send + Sync {
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
    fn final_usage(&self, ledger: &str, engine: Engine, usage: &Value) -> Result<()>;
    fn incomplete_usage(&self, ledger: &str) -> Result<()>;
}
