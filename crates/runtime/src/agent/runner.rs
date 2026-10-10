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
    let _lease = acquire_local_environment(s).await?;
    let thread = thread(s)?;
    environment::finish_stale(s, thread.generation).await?;
    environment::refresh(s).await?;
    crate::gateway::validate_spec(s, &thread.config)?;
    anyhow::ensure!(
        !resuming || thread.config.supports_checkpoint(),
        "Configured protocol does not support checkpoint resume"
    );
    if crate::remote_environment::is_remote(s) {
        crate::remote_environment::start(s).await?;
        if super::cancelled(s) || s.stopping() {
            environment::finish(s).await?;
            environment::drain(s).await?;
            return Ok(());
        }
    }
    let _renewal = crate::remote_environment::renewal(s);
    let generation = thread.generation;
    let context = context(s, request, &thread)?;
    let mut gate = EventGate::new(generation, &request.request_id);
    let result = s
        .agent_service
        .executor
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
        environment::finish(s).await?;
    } else {
        environment::finish_turn(s).await?;
    }
    environment::drain(s).await?;
    result
}

async fn acquire_local_environment(s: &Service) -> Result<Option<pot_sandbox::Lease>> {
    if !s.is_worker {
        return Ok(None);
    }
    let reference = s
        .environment_ref
        .as_ref()
        .ok_or_else(|| anyhow!("Worker Environment binding missing"))?;
    if !reference.is_local_to(&s.runtime_id) {
        crate::remote_environment::port(s)?.validate(reference)?;
        return Ok(None);
    }
    match &reference.target {
        pot_core::EnvironmentTarget::Host {} => {
            anyhow::ensure!(
                s.sandbox_id.is_none(),
                "Host worker cannot hold a Sandbox binding"
            );
            anyhow::ensure!(
                s.environment_service
                    .host_enabled
                    .load(std::sync::atomic::Ordering::SeqCst),
                "Host execution resource is disabled"
            );
            Ok(None)
        }
        pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
            anyhow::ensure!(
                s.sandbox_id.as_deref() == Some(sandbox_id.as_str()),
                "Worker Sandbox projection mismatch"
            );
            Ok(Some(
                s.environment_service.sandboxes.acquire(sandbox_id).await?,
            ))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn host_worker_has_no_sandbox_lease_or_provider_dependency() {
        let mut worker = crate::tasks::tests::fixture();
        worker.is_worker = true;
        worker.sandbox_id = None;
        worker.environment_ref = Some(pot_core::EnvironmentRef::host(&worker.runtime_id));
        assert!(acquire_local_environment(&worker).await.unwrap().is_none());
        worker.sandbox_id = Some("default".into());
        assert!(acquire_local_environment(&worker).await.is_err());
        worker.sandbox_id = None;
        worker.environment_ref = Some(pot_core::EnvironmentRef::host("remote"));
        assert!(acquire_local_environment(&worker).await.is_err());
        worker.environment_ref = None;
        assert!(acquire_local_environment(&worker).await.is_err());
        std::fs::remove_dir_all(worker.data.parent().unwrap()).unwrap();
    }
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
