//! Runtime-owned Agent invocation. No placement, model policy or conversation storage.
mod feedback;
mod log;
use anyhow::{Result, ensure};
use pot_core::*;
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, VecDeque},
    path::Path,
    sync::{Arc, Mutex},
};
use tokio::sync::{OwnedSemaphorePermit, Semaphore, watch};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Limits {
    pub max_active_agents: usize,
    pub max_queued_agents: usize,
}
impl Default for Limits {
    fn default() -> Self {
        Self {
            max_active_agents: 8,
            max_queued_agents: 256,
        }
    }
}
impl Limits {
    pub fn load(data: &Path) -> Result<Self> {
        let path = data.join("runtime-execution.json");
        match std::fs::read(path) {
            Ok(bytes) => Ok(serde_json::from_slice(&bytes)?),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(Self::default()),
            Err(error) => Err(error.into()),
        }
    }
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Phase {
    Queued,
    Running,
    Succeeded,
    Failed,
    Cancelled,
    Interrupted,
}
#[derive(Clone, Debug, Serialize)]
pub struct Execution {
    pub execution_id: String,
    pub runtime_id: String,
    pub boot_id: String,
    pub owner: String,
    pub request_id: String,
    pub generation: u64,
    pub role: String,
    pub phase: Phase,
}
struct Active {
    execution: Execution,
    cancel: watch::Sender<bool>,
    // Unconfirmed exits retain capacity until Runtime restart.
    permit: Option<OwnedSemaphorePermit>,
}
#[derive(Default)]
struct Registry {
    active: BTreeMap<String, Active>,
    recent: VecDeque<Execution>,
}
struct Inner {
    runtime_id: String,
    boot_id: String,
    limits: Limits,
    slots: Arc<Semaphore>,
    registry: Mutex<Registry>,
    stopped: watch::Sender<bool>,
    changed: watch::Sender<u64>,
    log: Mutex<log::Log>,
}
#[derive(Clone)]
pub struct Manager(Arc<Inner>);

/// Business adapters supply a fixed context and services; Runtime alone starts the engine.
pub struct Invocation<'a> {
    pub context: &'a ExecutionContext,
    pub thread: &'a Thread,
    pub request: &'a TurnRequest,
    pub resuming: bool,
    pub services: Arc<dyn ExecutionServices>,
    pub extensions: Option<Arc<dyn pot_agent::AgentExtensions>>,
}
impl Manager {
    pub fn open(data: &Path, runtime_id: &str, boot_id: &str, limits: Limits) -> Result<Self> {
        ensure!(
            (1..=Semaphore::MAX_PERMITS).contains(&limits.max_active_agents),
            "Invalid Runtime Agent concurrency"
        );
        ensure!(
            limits.max_queued_agents > 0,
            "Invalid Runtime Agent queue size"
        );
        Ok(Self(Arc::new(Inner {
            runtime_id: runtime_id.into(),
            boot_id: boot_id.into(),
            slots: Arc::new(Semaphore::new(limits.max_active_agents)),
            limits,
            registry: Mutex::new(Registry::default()),
            changed: watch::channel(0).0,
            stopped: watch::channel(false).0,
            log: Mutex::new(log::Log::open(data)?),
        })))
    }
    pub fn snapshot(&self) -> Vec<Execution> {
        let registry = self.0.registry.lock().unwrap();
        registry
            .active
            .values()
            .map(|a| a.execution.clone())
            .chain(registry.recent.iter().cloned())
            .collect()
    }
    /// Requests cooperative cancellation. Capacity is held through engine cleanup.
    pub fn cancel(&self, execution_id: &str) -> bool {
        let registry = self.0.registry.lock().unwrap();
        if let Some(active) = registry.active.get(execution_id) {
            active.cancel.send_replace(true);
            true
        } else {
            false
        }
    }
    pub fn stop(&self) {
        self.0.stopped.send_replace(true);
        self.0.slots.close();
    }
    /// Wait for cooperative cleanup. Interrupted runs require reconciliation/restart.
    pub async fn drain(&self) -> Result<()> {
        let mut changed = self.0.changed.subscribe();
        loop {
            {
                let registry = self.0.registry.lock().unwrap();
                ensure!(
                    !registry
                        .active
                        .values()
                        .any(|a| a.execution.phase == Phase::Interrupted),
                    "Runtime has an unconfirmed Agent execution"
                );
                if registry.active.is_empty() {
                    return Ok(());
                }
            }
            changed.changed().await?;
        }
    }
    fn log(&self, execution: &Execution, event: &str, sequence: Option<u64>) {
        if let Err(error) = self
            .0
            .log
            .lock()
            .unwrap()
            .record(execution, event, sequence, None)
        {
            eprintln!("Runtime execution log unavailable: {error}");
        }
    }
    fn observe(&self, execution_id: &str, observation: &ExecutionObservation) {
        let execution = self
            .0
            .registry
            .lock()
            .unwrap()
            .active
            .get(execution_id)
            .map(|a| a.execution.clone());
        let Some(execution) = execution else {
            return;
        };
        let (event, operation_id) = match observation {
            ExecutionObservation::RequestStarted { id, .. } => ("model_started", Some(id.as_str())),
            ExecutionObservation::RequestFinished { id, .. } => {
                ("model_finished", Some(id.as_str()))
            }
            _ => return,
        };
        if let Err(error) = self
            .0
            .log
            .lock()
            .unwrap()
            .record(&execution, event, None, operation_id)
        {
            eprintln!("Runtime execution log unavailable: {error}");
        }
    }
    fn register(&self, invocation: &Invocation<'_>) -> Result<(Guard, Arc<feedback::Feedback>)> {
        let mut registry = self.0.registry.lock().unwrap();
        ensure!(!*self.0.stopped.borrow(), "Runtime is stopping");
        ensure!(
            invocation.context.owner == invocation.thread.owner,
            "Execution owner differs from Thread"
        );
        ensure!(
            !registry
                .active
                .values()
                .any(|a| a.execution.owner == invocation.thread.owner
                    && a.execution.generation == invocation.thread.generation
                    && a.execution.request_id == invocation.request.request_id),
            "Agent execution already active"
        );
        ensure!(
            registry
                .active
                .values()
                .filter(|a| a.execution.phase == Phase::Queued)
                .count()
                < self.0.limits.max_queued_agents,
            "Runtime Agent queue is full"
        );
        let execution = Execution {
            execution_id: uuid::Uuid::new_v4().to_string(),
            runtime_id: self.0.runtime_id.clone(),
            boot_id: self.0.boot_id.clone(),
            owner: invocation.thread.owner.clone(),
            request_id: invocation.request.request_id.clone(),
            generation: invocation.thread.generation,
            role: match invocation.context.role {
                ExecutionRole::Main => "assistant",
                ExecutionRole::Worker => "task",
                ExecutionRole::Summary => "summary",
            }
            .into(),
            phase: Phase::Queued,
        };
        let (cancel, receiver) = watch::channel(false);
        let feedback = Arc::new(feedback::Feedback {
            inner: invocation.context.feedback.clone(),
            manager: self.clone(),
            cancel: receiver,
            stopped: self.0.stopped.subscribe(),
            execution_id: execution.execution_id.clone(),
        });
        registry.active.insert(
            execution.execution_id.clone(),
            Active {
                execution: execution.clone(),
                cancel,
                permit: None,
            },
        );
        drop(registry);
        self.log(&execution, "queued", None);
        Ok((
            Guard {
                manager: self.clone(),
                execution,
                finished: false,
            },
            feedback,
        ))
    }
    pub async fn execute(&self, invocation: Invocation<'_>, sink: Sink<'_>) -> Result<Outcome> {
        let (mut guard, feedback) = self.register(&invocation)?;
        let permit = tokio::select! {
            biased;
            _ = feedback.cancellation() => None,
            result = self.0.slots.clone().acquire_owned() => result.ok(),
        };
        let Some(permit) = permit.filter(|_| !feedback.cancelled()) else {
            guard.finish(Phase::Cancelled);
            sink(AgentEvent {
                generation: invocation.thread.generation,
                turn_id: invocation.request.request_id.clone(),
                seq: 1,
                update: Update::TurnFinished(Outcome::Cancelled),
            })?;
            return Ok(Outcome::Cancelled);
        };
        guard.start(permit);
        let mut context = invocation.context.clone();
        context.feedback = feedback;
        let cleanup_failed = Arc::new(std::sync::atomic::AtomicBool::new(false));
        context.tool_executor = context.tool_executor.map(|inner| {
            Arc::new(feedback::Tools {
                inner,
                cleanup_failed: cleanup_failed.clone(),
            }) as Arc<dyn ToolExecutor>
        });
        let agent = match invocation.extensions {
            Some(extensions) => pot_agent::Agent::with_extensions(extensions),
            None => pot_agent::Agent::default(),
        }
        .services(invocation.services);
        let mut gate = EventGate::new(invocation.thread.generation, &invocation.request.request_id);
        let result = agent
            .execute(
                &context,
                invocation.thread,
                invocation.request,
                invocation.resuming,
                &mut |event| {
                    if gate.accept(&event) {
                        let kind = match &event.update {
                            Update::ToolStarted { .. } => Some("tool_started"),
                            Update::ToolFinished { .. } => Some("tool_finished"),
                            Update::MessageStarted => Some("message_started"),
                            Update::MessageFinished { .. } => Some("message_finished"),
                            Update::TurnFinished(_) => Some("turn_finished"),
                            _ => None,
                        };
                        if let Some(kind) = kind {
                            self.log(&guard.execution, kind, Some(event.seq));
                        }
                        sink(event)?;
                    }
                    Ok(())
                },
            )
            .await;
        let settlement = context.feedback.settle().await;
        if settlement.is_err() || cleanup_failed.load(std::sync::atomic::Ordering::SeqCst) {
            self.log(&guard.execution, "settlement_unconfirmed", None);
            guard.finish(Phase::Interrupted);
            return Ok(Outcome::Unknown);
        }
        let outcome = match result {
            Ok(()) => gate.finish(),
            Err(error) => Outcome::Failed(error.to_string()),
        };
        guard.finish(match &outcome {
            Outcome::Success => Phase::Succeeded,
            Outcome::Failed(_) => Phase::Failed,
            Outcome::Cancelled => Phase::Cancelled,
            Outcome::Unknown => Phase::Interrupted,
        });
        Ok(outcome)
    }
}
struct Guard {
    manager: Manager,
    execution: Execution,
    finished: bool,
}
impl Guard {
    fn start(&mut self, permit: OwnedSemaphorePermit) {
        self.execution.phase = Phase::Running;
        let mut registry = self.manager.0.registry.lock().unwrap();
        let active = registry
            .active
            .get_mut(&self.execution.execution_id)
            .unwrap();
        active.execution.phase = Phase::Running;
        active.permit = Some(permit);
        drop(registry);
        self.manager.log(&self.execution, "running", None);
    }
    fn finish(&mut self, phase: Phase) {
        self.execution.phase = phase;
        let mut registry = self.manager.0.registry.lock().unwrap();
        if self.execution.phase == Phase::Interrupted {
            if let Some(active) = registry.active.get_mut(&self.execution.execution_id) {
                active.execution.phase = Phase::Interrupted;
            }
        } else {
            registry.active.remove(&self.execution.execution_id);
            registry.recent.push_back(self.execution.clone());
            if registry.recent.len() > 256 {
                registry.recent.pop_front();
            }
        }
        drop(registry);
        self.manager
            .0
            .changed
            .send_modify(|v| *v = v.wrapping_add(1));
        self.manager.log(&self.execution, "finished", None);
        self.finished = true;
    }
}
impl Drop for Guard {
    fn drop(&mut self) {
        if !self.finished {
            self.finish(if self.execution.phase == Phase::Queued {
                Phase::Cancelled
            } else {
                Phase::Interrupted
            });
        }
    }
}
#[cfg(test)]
mod tests;
