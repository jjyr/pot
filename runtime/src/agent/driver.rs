use super::*;
use futures::future::BoxFuture;
#[cfg(test)]
use pot_core::{DriverEvent, Sink};
pub use pot_core::{DriverHost, EventGate, Outcome, SessionHandle, TurnRequest, Update};
mod feedback;
use feedback::{context, prepare_context};
pub async fn execute(
    s: &Service,
    request: &SendMessage,
    transcript: &mut super::transcript::Transcript,
    resuming: bool,
) -> Result<()> {
    let _lease = if s.is_worker {
        Some(
            s.sandboxes
                .acquire(
                    s.sandbox_id
                        .as_deref()
                        .ok_or_else(|| anyhow!("Worker environment missing"))?,
                )
                .await?,
        )
    } else {
        None
    };
    let binding = binding(s)?;
    sandbox::finish_stale(s, binding.epoch).await?;
    sandbox::refresh(s).await?;
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
        Engine::Pot => s
            .agent_host
            .session(context(s, request, &binding)?, binding)?,
        Engine::Codex => pot_driver::codex::CodexHost {
            hosts: s.codex.clone(),
        }
        .session(context(s, request, &binding)?, binding)?,
    };
    let result = run_session(session.as_mut(), epoch, request, resuming, &mut |update| {
        transcript.record(s, update)
    })
    .await;
    if result.is_err() || super::cancelled(s) || s.stopping() {
        sandbox::finish(s).await?;
    } else {
        sandbox::finish_turn(s).await?;
    }
    sandbox::drain(s).await?;
    result
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
        .start_turn(
            &TurnRequest {
                request_id: request.request_id.clone(),
                text: request.text.clone(),
            },
            resuming,
            &mut |event| {
                if gate.accept(&event) {
                    sink(&event.update)?;
                }
                Ok(())
            },
        )
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

/// Validate a candidate before settings commit; preparation never calls a model.
pub async fn prepare(s: &Service, spec: &ResolvedSpec) -> Result<Option<NativeRef>> {
    // Initial selection is metadata-only; open the native session on the first real turn.
    if current(&s.inner.lock().unwrap().store)?.is_none() {
        return Ok(None);
    }
    if spec.engine == Engine::Codex {
        codex::prepare(&prepare_context(s)?, &s.codex, spec).await
    } else {
        Ok(None)
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
            _: &'a TurnRequest,
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
