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
    let _lease = acquire_local_execution_target(s).await?;
    if s.is_worker && !crate::remote_execution::is_remote(s) {
        crate::control_resources::refresh_plugins(s)?;
        let (_, _, cwd, _) = crate::execution_target::workspace_view(s)?;
        std::fs::create_dir_all(&cwd)?;
        crate::control_resources::install(&cwd, &crate::control_resources::current(s)?)?;
    }
    let thread = thread(s)?;
    s.conversation
        .inner
        .lock()
        .unwrap()
        .store
        .set("execution.remote_interrupted", "")?;
    execution_target::finish_stale(s, thread.generation).await?;
    execution_target::refresh(s).await?;
    crate::gateway::validate_spec(s, &thread.config)?;
    anyhow::ensure!(
        !resuming || thread.config.supports_checkpoint(),
        "Configured protocol does not support checkpoint resume"
    );
    let _renewal = crate::remote_execution::renewal(s);
    if s.is_worker && crate::remote_execution::is_remote(s) {
        let binding = crate::remote_execution::binding(s, s.execution_target.clone().unwrap())?;
        let worker = s.clone();
        let plugins = thread
            .config
            .params
            .agent
            .plugins
            .iter()
            .filter(|(_, plugin)| plugin.enabled)
            .map(|(name, _)| name.clone())
            .collect();
        let mut config = thread.config.clone();
        config.set_plugins(Default::default())?;
        let result = crate::remote_execution::port(s)?
            .run_task(
                pot_runner::Launch {
                    task_id: binding.owner,
                    run_id: binding.run_id,
                    generation: binding.generation,
                    execution_target: binding.execution_target,
                    config,
                    plugins,
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
        execution_target::finish_turn(s).await?;
        execution_target::drain(s).await?;
        return match result.outcome {
            Outcome::Success | Outcome::Cancelled => Ok(()),
            Outcome::Failed(reason) => bail!("{reason}"),
            Outcome::Unknown => bail!("Remote Task Agent outcome remains unresolved"),
        };
    }
    if crate::remote_execution::is_remote(s) {
        crate::remote_execution::start(s).await?;
        if super::cancelled(s) || s.stopping() {
            execution_target::finish(s).await?;
            execution_target::drain(s).await?;
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
        execution_target::finish(s).await?;
    } else {
        execution_target::finish_turn(s).await?;
    }
    execution_target::drain(s).await?;
    result
}

async fn acquire_local_execution_target(s: &Service) -> Result<Option<pot_sandbox::Lease>> {
    if !s.is_worker {
        return Ok(None);
    }
    let reference = s
        .execution_target
        .as_ref()
        .ok_or_else(|| anyhow!("Worker Execution target binding missing"))?;
    if !reference.is_local_to(&s.runtime_id) {
        crate::remote_execution::port(s)?.validate(reference)?;
        return Ok(None);
    }
    match &reference.target {
        pot_core::ExecutionTargetKind::Host {} => {
            anyhow::ensure!(
                s.sandbox_id.is_none(),
                "Host worker cannot hold a Sandbox binding"
            );
            anyhow::ensure!(
                s.execution_service
                    .host_enabled
                    .load(std::sync::atomic::Ordering::SeqCst),
                "Host execution resource is disabled"
            );
            Ok(None)
        }
        pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => {
            anyhow::ensure!(
                s.sandbox_id.as_deref() == Some(sandbox_id.as_str()),
                "Worker Sandbox projection mismatch"
            );
            Ok(Some(
                s.execution_service.sandboxes.acquire(sandbox_id).await?,
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
        worker.execution_target = Some(pot_core::ExecutionTarget::host(&worker.runtime_id));
        assert!(
            acquire_local_execution_target(&worker)
                .await
                .unwrap()
                .is_none()
        );
        worker.sandbox_id = Some("default".into());
        assert!(acquire_local_execution_target(&worker).await.is_err());
        worker.sandbox_id = None;
        worker.execution_target = Some(pot_core::ExecutionTarget::host("remote"));
        assert!(acquire_local_execution_target(&worker).await.is_err());
        worker.execution_target = None;
        assert!(acquire_local_execution_target(&worker).await.is_err());
        std::fs::remove_dir_all(worker.data.parent().unwrap()).unwrap();
    }
}
