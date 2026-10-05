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
    let epoch = binding.epoch;
    let mut session = match binding.spec.engine {
        Engine::Pi => PiHost.session(s.clone(), binding)?,
        Engine::Codex => super::codex::CodexHost.session(s.clone(), binding)?,
        Engine::Claude => super::claude::ClaudeHost.session(s.clone(), binding)?,
        Engine::Agy => super::agy::AgyHost.session(s.clone(), binding)?,
    };
    run_session(session.as_mut(), epoch, request, resuming, &mut |update| {
        transcript.record(s, update)
    })
    .await
}

async fn run_session(
    session: &mut dyn SessionHandle,
    epoch: u64,
    request: &SendMessage,
    resuming: bool,
    sink: &mut (dyn FnMut(&Update) -> Result<()> + Send),
) -> Result<()> {
    let mut gate = EventGate::new(epoch, &request.request_id);
    let result = session
        .start_turn(request, resuming, &mut |event| {
            if gate.accept(&event) {
                sink(&event.update)?;
            }
            Ok(())
        })
        .await;
    result?;
    match gate.finish() {
        Outcome::Success | Outcome::Cancelled => Ok(()),
        Outcome::Failed(reason) => bail!("{reason}"),
        Outcome::Unknown => {
            bail!("Agent event stream ended without a terminal event; stop unconfirmed")
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    struct FakeSession {
        events: Vec<DriverEvent>,
        fail_after_terminal: bool,
    }
    impl SessionHandle for FakeSession {
        fn start_turn<'a>(
            &'a mut self,
            _: &'a SendMessage,
            _: bool,
            sink: Sink<'a>,
        ) -> BoxFuture<'a, Result<()>> {
            Box::pin(async move {
                for event in self.events.drain(..) {
                    tokio::task::yield_now().await;
                    sink(event)?;
                }
                if self.fail_after_terminal {
                    bail!("cleanup failed; stop unconfirmed");
                }
                Ok(())
            })
        }
    }
    fn fake_request(id: &str) -> SendMessage {
        SendMessage {
            request_id: id.into(),
            conversation_id: "main".into(),
            text: "test".into(),
        }
    }
    fn event(turn: &str, seq: u64, update: Update) -> DriverEvent {
        DriverEvent {
            epoch: 1,
            turn_id: turn.into(),
            seq,
            update,
        }
    }
    #[tokio::test]
    async fn fake_sessions_isolate_concurrent_events_and_confirm_cancel_only_after_cleanup() {
        async fn run(turn: &str, other: &str) -> Vec<String> {
            let mut fake = FakeSession {
                events: vec![
                    event(other, 1, Update::TextDelta("foreign".into())),
                    event(turn, 1, Update::TextDelta(turn.into())),
                    event(turn, 1, Update::TextDelta("duplicate".into())),
                    event(turn, 2, Update::TurnFinished(Outcome::Cancelled)),
                    event(turn, 3, Update::TextDelta("late".into())),
                ],
                fail_after_terminal: false,
            };
            let mut accepted = vec![];
            run_session(&mut fake, 1, &fake_request(turn), false, &mut |u| {
                if let Update::TextDelta(v) = u {
                    accepted.push(v.clone());
                }
                Ok(())
            })
            .await
            .unwrap();
            accepted
        }
        let (a, b) = tokio::join!(run("a", "b"), run("b", "a"));
        assert_eq!(a, ["a"]);
        assert_eq!(b, ["b"]);
        let mut missing = FakeSession {
            events: vec![event(
                "a",
                1,
                Update::MessageFinished {
                    text: "looks complete".into(),
                    outcome: Outcome::Success,
                },
            )],
            fail_after_terminal: false,
        };
        assert!(
            run_session(&mut missing, 1, &fake_request("a"), false, &mut |_| Ok(()))
                .await
                .unwrap_err()
                .to_string()
                .contains("stop unconfirmed")
        );
        let mut failed = FakeSession {
            events: vec![event("a", 1, Update::TurnFinished(Outcome::Cancelled))],
            fail_after_terminal: true,
        };
        assert!(
            run_session(&mut failed, 1, &fake_request("a"), false, &mut |_| Ok(()))
                .await
                .unwrap_err()
                .to_string()
                .contains("stop unconfirmed")
        );
    }

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

/// Validate a candidate before settings commit; preparation never calls a model.
pub async fn prepare(s: &Service, spec: &ResolvedSpec) -> Result<Option<NativeRef>> {
    // Initial selection is metadata-only; open the native session on the first real turn.
    if current(&s.inner.lock().unwrap().store)?.is_none() {
        return Ok(None);
    }
    if spec.engine == Engine::Agy {
        agy::prepare(spec).await?;
        Ok(None)
    } else if spec.engine == Engine::Claude {
        claude::prepare(spec).await?;
        Ok(None)
    } else {
        codex::prepare(s, spec).await
    }
}
