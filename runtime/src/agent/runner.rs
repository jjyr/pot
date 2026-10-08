use super::*;
#[cfg(test)]
use pot_core::AgentEvent;
pub use pot_core::{EventGate, Outcome, TurnRequest, Update};
mod feedback;
use feedback::context;
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
    let thread = thread(s)?;
    sandbox::finish_stale(s, thread.generation).await?;
    sandbox::refresh(s).await?;
    crate::gateway::validate_spec(s, &thread.config)?;
    anyhow::ensure!(
        !resuming || thread.config.supports_checkpoint(),
        "Configured protocol does not support checkpoint resume"
    );
    let generation = thread.generation;
    let context = context(s, request, &thread)?;
    let mut gate = EventGate::new(generation, &request.request_id);
    let result = s
        .agent_executor
        .execute(
            &context,
            &thread,
            &TurnRequest {
                request_id: request.request_id.clone(),
                text: request.text.clone(),
            },
            resuming,
            &mut |event| {
                if gate.accept(&event) {
                    transcript.record(s, &event.update)?;
                }
                Ok(())
            },
        )
        .await;
    let result = result.and_then(|()| match gate.finish() {
        Outcome::Success | Outcome::Cancelled => Ok(()),
        Outcome::Failed(reason) => bail!("{reason}"),
        Outcome::Unknown => {
            bail!("Agent event stream ended without a terminal event; stop unconfirmed")
        }
    });
    if result.is_err() || super::cancelled(s) || s.stopping() {
        sandbox::finish(s).await?;
    } else {
        sandbox::finish_turn(s).await?;
    }
    sandbox::drain(s).await?;
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn late_duplicate_and_post_terminal_events_are_isolated() {
        let mut gate = EventGate::new(2, "turn");
        let mut event = AgentEvent {
            generation: 1,
            turn_id: "turn".into(),
            seq: 1,
            update: Update::TextDelta("old".into()),
        };
        assert!(!gate.accept(&event));
        event.generation = 2;
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
