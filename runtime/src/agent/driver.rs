//! Driver boundary: product projection is independent of transport events.
use super::*;
use futures::future::BoxFuture;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Outcome {
    Success,
    Failed(String),
    Cancelled,
    Unknown,
}
#[derive(Clone, Debug)]
pub enum Update {
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
    fn session(&self, service: Service, binding: Binding) -> Result<Box<dyn SessionHandle>>;
}
pub trait SessionHandle: Send {
    fn start_turn<'a>(
        &'a mut self,
        request: &'a SendMessage,
        resuming: bool,
        sink: Sink<'a>,
    ) -> BoxFuture<'a, Result<()>>;
}

pub struct PiHost;
struct PiSession {
    service: Service,
    binding: Binding,
}
impl DriverHost for PiHost {
    fn probe(&self, spec: &ResolvedSpec) -> Result<()> {
        spec.validate()?;
        anyhow::ensure!(
            spec.engine == Engine::Pi,
            "Unsupported engine; needs rebind"
        );
        Ok(())
    }
    fn session(&self, service: Service, binding: Binding) -> Result<Box<dyn SessionHandle>> {
        self.probe(&binding.spec)?;
        anyhow::ensure!(
            binding.state == BindingState::Active,
            "Agent binding is broken; needs rebind"
        );
        Ok(Box::new(PiSession { service, binding }))
    }
}
impl SessionHandle for PiSession {
    fn start_turn<'a>(
        &'a mut self,
        request: &'a SendMessage,
        resuming: bool,
        sink: Sink<'a>,
    ) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let mut seq = 0;
            let epoch = self.binding.epoch;
            let mut emit = |update| {
                seq += 1;
                sink(DriverEvent {
                    epoch,
                    turn_id: request.request_id.clone(),
                    seq,
                    update,
                })
            };
            let result = pi::execute(&self.service, request, &mut emit, resuming).await;
            // execute returns only after process disposal. An uncertain shutdown
            // is an error, never a successful cancellation confirmation.
            let outcome = match &result {
                Err(e) => Outcome::Failed(e.to_string()),
                Ok(()) if cancelled(&self.service) => Outcome::Cancelled,
                Ok(()) => Outcome::Success,
            };
            emit(Update::TurnFinished(outcome))?;
            result
        })
    }
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

pub async fn execute(
    s: &Service,
    request: &SendMessage,
    transcript: &mut super::transcript::Transcript,
    resuming: bool,
) -> Result<()> {
    let binding = binding(s)?;
    if let Err(e) = crate::gateway::validate_spec(s, &binding.spec) {
        s.inner
            .lock()
            .unwrap()
            .store
            .set("agent.needs_rebind", &e.to_string())?;
        return Err(e);
    }
    s.inner
        .lock()
        .unwrap()
        .store
        .set("agent.needs_rebind", "")?;
    anyhow::ensure!(
        !resuming || binding.spec.capabilities.safe_checkpoint_resume,
        "Engine does not support checkpoint resume"
    );
    let mut gate = EventGate::new(binding.epoch, &request.request_id);
    let mut session = PiHost.session(s.clone(), binding)?;
    let result = session
        .start_turn(request, resuming, &mut |event| {
            if gate.accept(&event) {
                transcript.record(s, &event.update)?;
            }
            Ok(())
        })
        .await;
    result?;
    match gate.finish() {
        Outcome::Success | Outcome::Cancelled => Ok(()),
        Outcome::Failed(reason) => bail!("{reason}"),
        Outcome::Unknown => {
            bail!("Agent event stream ended without a terminal event; outcome unknown")
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn late_duplicate_and_post_terminal_events_are_isolated() {
        let mut gate = EventGate::new(2, "turn");
        let mut event = DriverEvent {
            epoch: 1,
            turn_id: "turn".into(),
            seq: 1,
            update: Update::TextDelta("old".into()),
        };
        assert!(!gate.accept(&event));
        event.epoch = 2;
        event.turn_id = "other".into();
        assert!(!gate.accept(&event));
        event.turn_id = "turn".into();
        assert!(gate.accept(&event));
        assert!(!gate.accept(&event));
        event.seq = 2;
        event.update = Update::TurnFinished(Outcome::Cancelled);
        assert!(gate.accept(&event));
        event.seq = 3;
        assert!(!gate.accept(&event));
        assert_eq!(gate.finish(), Outcome::Cancelled);
        assert_eq!(EventGate::new(1, "missing").finish(), Outcome::Unknown);
    }
}
