use super::*;
use pot_core::{ExecutionContext, ExecutionFeedback, ExecutionRole};

struct Feedback {
    service: Service,
    epoch: u64,
    request: Option<String>,
}
impl Feedback {
    fn check(&self) -> Result<()> {
        let s = &self.service;
        let inner = s.inner.lock().unwrap();
        let binding = current(&inner.store)?.ok_or_else(|| anyhow!("Binding missing"))?;
        anyhow::ensure!(
            binding.epoch == self.epoch && binding.pot_owner == s.usage_scope,
            "Execution binding changed"
        );
        if let Some(request) = &self.request {
            let turn = inner
                .turn
                .as_ref()
                .ok_or_else(|| anyhow!("Execution turn ended"))?;
            let message = inner
                .store
                .duplicate(request)?
                .ok_or_else(|| anyhow!("Accepted input missing"))?;
            anyhow::ensure!(turn.user_message_id == message.id, "Execution turn changed");
        }
        Ok(())
    }
}
impl ExecutionFeedback for Feedback {
    fn cancelled(&self) -> bool {
        self.service.stopping() || self.check().is_err() || cancelled(&self.service)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(self.service.cancel_notify.notified())
    }
    fn native_identity(&self, identity: &str) -> Result<()> {
        self.check()?;
        let mut inner = self.service.inner.lock().unwrap();
        match current(&inner.store)?
            .ok_or_else(|| anyhow!("Binding missing"))?
            .spec
            .engine
        {
            Engine::Pi => save_pi_session(&mut inner.store, identity),
            Engine::Codex => save_codex_thread(&mut inner.store, identity),
            Engine::Grok => save_grok_session(&mut inner.store, identity),
        }
    }
    fn binding_broken(&self, reason: &str) -> Result<()> {
        self.check()?;
        mark_broken(&mut self.service.inner.lock().unwrap().store, reason)
    }
    fn needs_rebind(&self, reason: &str) -> Result<()> {
        self.check()?;
        self.service
            .inner
            .lock()
            .unwrap()
            .store
            .set("agent.needs_rebind", reason)
    }
    fn record_turn(&self, request: &str) -> Result<()> {
        self.check()?;
        anyhow::ensure!(
            self.request.as_deref() == Some(request),
            "Execution request changed"
        );
        record_turn(&self.service, request)
    }
    fn input_streaming(&self, request: &str) -> Result<()> {
        self.check()?;
        anyhow::ensure!(
            self.request.as_deref() == Some(request),
            "Execution request changed"
        );
        let mut message = self
            .service
            .inner
            .lock()
            .unwrap()
            .store
            .duplicate(request)?
            .ok_or_else(|| anyhow!("Input missing"))?;
        message.status = MessageStatus::Streaming;
        self.service.save(message)
    }
    fn begin_usage(&self, ledger: &str, spec: &ResolvedSpec, epoch: u64) -> Result<()> {
        self.check()?;
        anyhow::ensure!(
            epoch == self.epoch && spec.spec_hash == binding(&self.service)?.spec.spec_hash,
            "Usage binding changed"
        );
        let turn = self
            .service
            .inner
            .lock()
            .unwrap()
            .turn
            .as_ref()
            .ok_or_else(|| anyhow!("No active turn"))?
            .user_message_id
            .clone();
        self.service.usage.lock().unwrap().begin_native(
            ledger,
            &self.service.usage_scope,
            &turn,
            spec,
            epoch,
        )
    }
    fn initialize_usage(&self, native: &str) -> Result<()> {
        self.check()?;
        self.service
            .usage
            .lock()
            .unwrap()
            .initialize_native_usage(native)
    }
    fn cumulative_usage(&self, ledger: &str, native: &str, usage: &Value) -> Result<()> {
        self.check()?;
        self.service
            .usage
            .lock()
            .unwrap()
            .native_usage(ledger, native, usage)
    }

    fn incomplete_usage(&self, ledger: &str) -> Result<()> {
        self.check()?;
        self.service.usage.lock().unwrap().native_incomplete(ledger)
    }
}
fn make_context(
    s: &Service,
    binding: &Binding,
    request: Option<&SendMessage>,
) -> Result<ExecutionContext> {
    let endpoint: RuntimeEndpoint =
        serde_json::from_slice(&std::fs::read(s.data.join(RUNTIME_ENDPOINT_FILE))?)?;
    let role = if request.is_some_and(|r| r.request_id.starts_with(orchestration::RESULT_PREFIX)) {
        ExecutionRole::Summary
    } else if s.is_worker {
        ExecutionRole::Worker
    } else {
        ExecutionRole::Main
    };
    Ok(ExecutionContext {
        data: s.data.clone(),
        workspace: s.workspace.clone(),
        owner: s.usage_scope.clone(),
        role,
        backend_url: format!("http://127.0.0.1:{}", endpoint.port),
        lane_token: s.token.clone(),
        pi_program: s.pi_program.clone(),
        main_prompt: orchestration::MAIN_PROMPT.into(),
        model_api: if binding.spec.engine == Engine::Pi {
            crate::gateway::codecs::Protocol::from_name(&binding.spec.gateway_protocol)?
                .pi_api()
                .into()
        } else {
            String::new()
        },
        feedback: Arc::new(Feedback {
            service: s.clone(),
            epoch: binding.epoch,
            request: request.map(|r| r.request_id.clone()),
        }),
    })
}
pub(super) fn context(
    s: &Service,
    request: &SendMessage,
    binding: &Binding,
) -> Result<ExecutionContext> {
    if binding.spec.engine == Engine::Pi {
        native_session(s)?;
    }
    make_context(s, binding, Some(request))
}
pub(super) fn prepare_context(s: &Service) -> Result<ExecutionContext> {
    make_context(s, &binding(s)?, None)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn feedback_rejects_foreign_requests_old_turns_and_epochs() {
        let mut s = tasks::tests::fixture();
        let (commands, _receiver) = mpsc::unbounded_channel();
        s.commands = commands;
        let request = SendMessage {
            request_id: "feedback-turn".into(),
            conversation_id: "main".into(),
            text: "fixture".into(),
        };
        let _ = crate::send(
            State(s.clone()),
            tasks::tests::headers(),
            Json(request.clone()),
        )
        .await
        .unwrap();
        let binding = binding(&s).unwrap();
        let feedback = Feedback {
            service: s.clone(),
            epoch: binding.epoch,
            request: Some(request.request_id.clone()),
        };
        assert!(!feedback.cancelled());
        assert!(feedback.record_turn("other").is_err());
        feedback.record_turn(&request.request_id).unwrap();
        let mut all = bindings(&s.inner.lock().unwrap().store).unwrap();
        all[0].epoch += 1;
        s.inner
            .lock()
            .unwrap()
            .store
            .set(BINDINGS, &serde_json::to_string(&all).unwrap())
            .unwrap();
        assert!(feedback.needs_rebind("stale update").is_err());
        assert!(feedback.cancelled());
        assert_ne!(
            s.inner
                .lock()
                .unwrap()
                .store
                .get("agent.needs_rebind")
                .unwrap()
                .as_deref(),
            Some("stale update")
        );
        let current_feedback = Feedback {
            service: s.clone(),
            epoch: all[0].epoch,
            request: Some(request.request_id),
        };
        s.turn(None);
        assert!(current_feedback.check().is_err());
        assert!(current_feedback.cancelled());
        std::fs::remove_dir_all(&s.data).unwrap();
    }
}
