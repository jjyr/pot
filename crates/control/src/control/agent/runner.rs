use super::*;
pub use pot_core::{Outcome, TurnRequest, Update};
mod feedback;
use feedback::context;
pub async fn execute(
    s: &Service,
    request: &SendMessage,
    transcript: &mut super::transcript::Transcript,
    resuming: bool,
) -> Result<()> {
    if s.is_worker {
        crate::permissions::authorize_task(s).await?;
    }
    let _lease = acquire_local_environment(s).await?;
    let thread = thread(s)?;
    s.conversation
        .inner
        .lock()
        .unwrap()
        .store
        .set("execution.remote_interrupted", "")?;
    environment::finish_stale(s, thread.generation).await?;
    environment::refresh(s).await?;
    crate::gateway::validate_spec(s, &thread.config)?;
    anyhow::ensure!(
        !resuming || thread.config.supports_checkpoint(),
        "Configured protocol does not support checkpoint resume"
    );
    let _renewal = crate::remote_environment::renewal(s);
    if s.is_worker && crate::remote_environment::is_remote(s) {
        let binding = crate::remote_environment::binding(s, s.environment_ref.clone().unwrap())?;
        let worker = s.clone();
        let result = crate::remote_environment::port(s)?
            .run_task(
                pot_runner::Launch {
                    task_id: binding.owner,
                    run_id: binding.run_id,
                    generation: binding.generation,
                    environment: binding.environment,
                    config: thread.config.clone(),
                    resources: crate::control_resources::snapshot(s)?,
                    skills: s.skill_selection.clone(),
                    text: request.text.clone(),
                },
                Arc::new(move || super::cancelled(&worker) || worker.stopping()),
            )
            .await;
        let result = match result {
            Err(error) if error.downcast_ref::<pot_runner::Interrupted>().is_some() => {
                let mut inner = s.conversation.inner.lock().unwrap();
                inner.store.set("execution.remote_interrupted", "true")?;
                if let Some(turn) = inner.turn.as_mut() {
                    turn.cancelling = true;
                }
                drop(inner);
                s.phase(AgentPhase::Ready, Some(error.to_string()));
                return Ok(());
            }
            result => result?,
        };
        transcript.record(s, &Update::MessageStarted)?;
        transcript.record(
            s,
            &Update::MessageFinished {
                text: result.text,
                outcome: result.outcome.clone(),
            },
        )?;
        environment::finish_turn(s).await?;
        environment::drain(s).await?;
        return match result.outcome {
            Outcome::Success | Outcome::Cancelled => Ok(()),
            Outcome::Failed(reason) => bail!("{reason}"),
            Outcome::Unknown => bail!("Remote Task Agent outcome remains unresolved"),
        };
    }
    if crate::remote_environment::is_remote(s) {
        crate::remote_environment::start(s).await?;
        if super::cancelled(s) || s.stopping() {
            environment::finish(s).await?;
            environment::drain(s).await?;
            return Ok(());
        }
    }
    let context = context(s, request, &thread)?;
    let result = s
        .agent_service
        .executions
        .execute(
            pot_runtime_execution::Invocation {
                context: &context,
                thread: &thread,
                request: &TurnRequest {
                    request_id: request.request_id.clone(),
                    text: request.text.clone(),
                },
                resuming,
                services: Arc::new(crate::local_services::LocalServices(s.clone())),
                #[cfg(test)]
                extensions: s.agent_service.extensions.clone(),
                #[cfg(not(test))]
                extensions: None,
            },
            &mut |event| transcript.record(s, &event.update),
        )
        .await
        .and_then(|outcome| match outcome {
            Outcome::Success | Outcome::Cancelled => Ok(()),
            Outcome::Failed(reason) => bail!("{reason}"),
            Outcome::Unknown => {
                bail!("Agent execution ended without a terminal event; stop unconfirmed")
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
}
