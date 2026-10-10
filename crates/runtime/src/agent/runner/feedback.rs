use super::*;
use futures::future::BoxFuture;
use pot_core::{ExecutionContext, ExecutionFeedback, ExecutionRole};

struct Feedback {
    service: Service,
    generation: u64,
    request: Option<String>,
}
impl Feedback {
    fn check(&self) -> Result<()> {
        let s = &self.service;
        let inner = s.conversation.inner.lock().unwrap();
        let thread = current(&inner.store)?.ok_or_else(|| anyhow!("Thread missing"))?;
        anyhow::ensure!(
            thread.generation == self.generation && thread.owner == s.usage_scope,
            "Execution thread changed"
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
    fn requires_owner_review(&self, tool: &str, args: &Value) -> bool {
        crate::permissions::rules_edit_requires_review(&self.service, tool, args)
    }

    fn execute_authorized_host(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            self.check()?;
            anyhow::ensure!(
                !self.cancelled() && !self.service.is_worker && name == "set_sandbox_mappings",
                "Host operation unavailable"
            );
            crate::sandbox::management(&self.service, &json!({"action":"set_mounts","sandbox_id":args["sandbox_id"],"mappings":args["mappings"]})).await
        })
    }
    fn prepare_permission_operation(&self, name: &str, mut args: Value) -> Result<Value> {
        self.check()?;
        if name == "manage_sandbox" && args["action"] != "create" {
            args["sandbox_id"] = json!(
                self.service
                    .environment_service
                    .manager
                    .assignment(args["sandbox_id"].as_str().unwrap_or(""))?
            );
        }
        Ok(args)
    }
    fn execution_home(&self) -> Option<PathBuf> {
        std::env::var_os("HOME").map(PathBuf::from)
    }
    fn prepare_execution_operation(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            self.check()?;
            #[cfg(test)]
            if self.service.test_tool_executor.is_some()
                && !self.service.environment_service.permissions.configured()
            {
                return Ok(args);
            }
            crate::permissions::prepare(&self.service, &name, args).await
        })
    }
    fn environment_grants(&self, environment: &str) -> BoxFuture<'_, Result<Option<Value>>> {
        let environment = environment.to_owned();
        Box::pin(async move {
            self.check()?;
            #[cfg(test)]
            if self.service.test_tool_executor.is_some()
                && !self.service.environment_service.permissions.configured()
            {
                return Ok(None);
            }
            Ok(Some(
                self.service
                    .environment_service
                    .permissions
                    .environment(&environment)?
                    .state(&self.service.usage_scope),
            ))
        })
    }
    fn permission_grants(&self) -> Result<Option<Value>> {
        self.check()?;
        #[cfg(test)]
        if self.service.test_tool_executor.is_some()
            && !self.service.environment_service.permissions.configured()
        {
            return Ok(None);
        }
        Ok(Some(
            self.service
                .environment_service
                .permissions
                .grants(&self.service.usage_scope),
        ))
    }
    fn request_permission(&self, request: Value) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            self.check()?;
            crate::permissions::request(
                &self.service,
                self.generation,
                self.request
                    .as_deref()
                    .ok_or_else(|| anyhow!("Active turn required"))?,
                request,
            )
            .await
        })
    }
    fn ready(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async {
            self.check()?;
            crate::control::ready(&self.service).await
        })
    }
    fn steering(&self, finalizing: bool) -> Result<Vec<(String, String)>> {
        self.check()?;
        crate::control::pending(&self.service, self.generation, finalizing)
    }
    fn acknowledge_steering(&self, ids: &[String]) -> Result<()> {
        self.check()?;
        crate::control::acknowledge(&self.service, self.generation, ids)
    }
    fn diagnostic(&self, metadata: &Value) -> Result<()> {
        self.check()?;
        crate::control::record(&self.service, self.generation, metadata)
    }
    fn load_context(&self) -> Result<Option<Value>> {
        self.check()?;
        pot_storage::threads::load_context(&self.service.conversation.inner.lock().unwrap().store)
    }
    fn save_context(&self, value: &Value) -> Result<()> {
        self.check()?;
        pot_storage::threads::save_context(
            &self.service.conversation.inner.lock().unwrap().store,
            value,
        )
    }
    fn cancelled(&self) -> bool {
        self.service.stopping() || self.check().is_err() || cancelled(&self.service)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(self.service.cancel_notify.notified())
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
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .duplicate(request)?
            .ok_or_else(|| anyhow!("Input missing"))?;
        message.status = MessageStatus::Streaming;
        self.service.save(message)
    }
}
fn make_context(
    s: &Service,
    thread: &Thread,
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
    let skill_resources = if role == ExecutionRole::Summary {
        Default::default()
    } else {
        crate::skills::resources(s)?
    };
    let mut main_prompt = if s.is_worker {
        String::new()
    } else {
        orchestration::MAIN_PROMPT.into()
    };
    {
        main_prompt = main_prompt.replace("Environment tools execute in the current default Sandbox unless you pass an explicit sandbox_id.", "Main execution defaults to Host; use environment=sandbox:ID for explicit Sandbox execution.").replace("Direct Host tool execution is unavailable.", "You normally chat on the Host. exec_command, write_stdin and apply_patch operate in the selected execution environment. Main defaults to host; task agents default to their assigned Sandbox. environment=host explicitly requests Host execution; environment=sandbox:ID selects a Sandbox. Each environment loads its own ~/.pot/agent/permissions.json. Host policy defaults to ask: execution needs a matching grant or owner approval. Sandbox policy defaults to allow: ordinary Sandbox execution does not need approval, unless the owner configures ask. Task and Sandbox management do not require permission approval. write_stdin continues an authorized process session without asking again. Role and assignment boundaries still apply. Use set_sandbox_mappings only for an owner-reviewed mapping change. Never claim permanent permission exists before approval.");
    }
    let instructions = if role == ExecutionRole::Summary {
        String::new()
    } else {
        let catalog = crate::instructions::load(s)?;
        for message in catalog.diagnostics {
            eprintln!("AGENTS.md discovery: {message}");
        }
        catalog.text
    };
    Ok(ExecutionContext {
        instructions,
        skill_resources,
        tool_executor: {
            #[cfg(test)]
            if let Some(executor) = &s.test_tool_executor
                && !s.environment_service.permissions.configured()
            {
                Some(executor.clone())
            } else {
                Some(Arc::new(crate::sandbox::Tools {
                    service: s.clone(),
                    generation: thread.generation,
                }))
            }
            #[cfg(not(test))]
            {
                Some(Arc::new(crate::sandbox::Tools {
                    service: s.clone(),
                    generation: thread.generation,
                }))
            }
        },
        data: s.data.clone(),
        workspace: {
            #[cfg(test)]
            let path = if s.test_tool_executor.is_some() {
                s.workspace.clone()
            } else {
                s.data.join("agent-workspace")
            };
            #[cfg(not(test))]
            let path = s.data.join("agent-workspace");
            std::fs::create_dir_all(&path)?;
            path
        },
        owner: s.usage_scope.clone(),
        role,
        backend_url: format!("http://127.0.0.1:{}", endpoint.port),
        lane_token: s.token.clone(),
        main_prompt,
        feedback: Arc::new(Feedback {
            service: s.clone(),
            generation: thread.generation,
            request: request.map(|r| r.request_id.clone()),
        }),
    })
}
pub(super) fn context(
    s: &Service,
    request: &SendMessage,
    thread: &Thread,
) -> Result<ExecutionContext> {
    make_context(s, thread, Some(request))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn feedback_rejects_foreign_requests_old_turns_and_generations() {
        let mut s = tasks::tests::fixture();
        let (commands, _receiver) = mpsc::unbounded_channel();
        s.conversation.commands = commands;
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
        let thread = thread(&s).unwrap();
        let feedback = Feedback {
            service: s.clone(),
            generation: thread.generation,
            request: Some(request.request_id.clone()),
        };
        assert!(!feedback.cancelled());
        assert!(feedback.record_turn("other").is_err());
        feedback.record_turn(&request.request_id).unwrap();
        let mut next = current(&s.conversation.inner.lock().unwrap().store)
            .unwrap()
            .unwrap();
        next.generation += 1;
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(THREAD, &serde_json::to_string(&next).unwrap())
            .unwrap();
        assert!(feedback.record_turn(&request.request_id).is_err());
        assert!(feedback.cancelled());
        let current_feedback = Feedback {
            service: s.clone(),
            generation: next.generation,
            request: Some(request.request_id),
        };
        s.turn(None);
        assert!(current_feedback.check().is_err());
        assert!(current_feedback.cancelled());
        std::fs::remove_dir_all(&s.data).unwrap();
    }
}

#[cfg(test)]
#[path = "feedback_tool_tests.rs"]
mod tool_tests;
