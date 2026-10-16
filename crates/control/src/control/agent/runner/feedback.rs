use super::*;
use futures::future::BoxFuture;
use pot_core::{ExecutionContext, ExecutionFeedback, ExecutionRole};

struct Feedback {
    service: Service,
    generation: u64,
    request: Option<String>,
    instruction_reviews: Mutex<std::collections::BTreeMap<String, String>>,
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
    fn execute_authorized_host(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            self.check()?;
            if ["read_agent_instructions", "update_agent_instructions"].contains(&name.as_str()) {
                let revision = self
                    .instruction_reviews
                    .lock()
                    .unwrap()
                    .remove(&args.to_string());
                return crate::assistant_instructions::execute(
                    &self.service,
                    &name,
                    &args,
                    revision.as_deref(),
                );
            }
            anyhow::bail!("Unsupported owner operation")
        })
    }
    fn prepare_permission_operation(&self, name: &str, args: Value) -> Result<Value> {
        self.check()?;
        if ["read_agent_instructions", "update_agent_instructions"].contains(&name) {
            crate::assistant_instructions::validate_tool(&self.service, name, &args)?;
            if name == "update_agent_instructions" {
                let current = crate::assistant_instructions::read(&self.service)?;
                self.instruction_reviews.lock().unwrap().insert(
                    args.to_string(),
                    current["revision"].as_str().unwrap().to_owned(),
                );
            }
            return Ok(args);
        }
        Ok(args)
    }
    fn execution_home(&self) -> Option<PathBuf> {
        std::env::var_os("HOME").map(PathBuf::from)
    }
    fn resolve_execution_operation(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            self.check()?;
            #[cfg(test)]
            if self.service.test_tool_executor.is_some()
                && !self.service.execution_service.permissions.configured()
            {
                return Ok(args);
            }
            crate::permissions::resolve_operation(&self.service, &name, args).await
        })
    }
    fn prepare_execution_operation(&self, name: &str, args: Value) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            self.check()?;
            #[cfg(test)]
            if self.service.test_tool_executor.is_some()
                && !self.service.execution_service.permissions.configured()
            {
                return Ok(args);
            }
            crate::permissions::prepare(&self.service, &name, args).await
        })
    }
    fn owner_authorization_available(&self) -> Result<bool> {
        self.check()?;
        #[cfg(test)]
        let available = self.service.test_tool_executor.is_none()
            || self.service.execution_service.permissions.configured();
        #[cfg(not(test))]
        let available = true;
        Ok(available)
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
            crate::remote_execution::ready(&self.service).await?;
            crate::turn_control::ready(&self.service).await
        })
    }
    fn steering(&self, finalizing: bool) -> Result<Vec<(String, String)>> {
        self.check()?;
        crate::turn_control::pending(&self.service, self.generation, finalizing)
    }
    fn acknowledge_steering(&self, ids: &[String]) -> Result<()> {
        self.check()?;
        crate::turn_control::acknowledge(&self.service, self.generation, ids)
    }
    fn diagnostic(&self, metadata: &Value) -> Result<()> {
        self.check()?;
        crate::turn_control::record(&self.service, self.generation, metadata)
    }
    fn load_context(&self) -> Result<Option<Value>> {
        self.check()?;
        pot_assistant::threads::load_context(&self.service.conversation.inner.lock().unwrap().store)
    }
    fn save_context(&self, value: &Value) -> Result<()> {
        self.check()?;
        pot_assistant::threads::save_context(
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
    let role = if request.is_some_and(|r| {
        r.request_id
            .starts_with(pot_assistant::orchestration::RESULT_PREFIX)
    }) {
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
        pot_assistant::orchestration::MAIN_PROMPT.into()
    };
    if s.is_worker {
        main_prompt.push_str(&format!("For files you actually create inside your task workspace, include Markdown download links [filename](pot-file://{}/RELATIVE_PATH) in your reply. Percent-encode spaces and non-ASCII path characters, preserving slashes. Do not link outside the task workspace or claim files were downloaded to the user's machine.", s.usage_scope));
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
    let workspace = if crate::remote_execution::is_remote(s) {
        let contexts = s.remote_context.lock().unwrap();
        let context = contexts
            .get(s.execution_target.as_ref().unwrap())
            .ok_or_else(|| anyhow!("Remote context missing"))?;
        PathBuf::from(
            context.context["workspace"]
                .as_str()
                .ok_or_else(|| anyhow!("Destination workspace missing"))?,
        )
    } else {
        #[cfg(test)]
        let path = if s.test_tool_executor.is_some() {
            s.workspace.clone()
        } else {
            if s.is_worker || s.execution_service.sandboxes.assignment("").is_err() {
                s.workspace.clone()
            } else {
                s.data.join("agent-workspace")
            }
        };
        #[cfg(not(test))]
        let path = if s.is_worker || s.execution_service.sandboxes.assignment("").is_err() {
            s.workspace.clone()
        } else {
            s.data.join("agent-workspace")
        };
        std::fs::create_dir_all(&path)?;
        path
    };
    Ok(ExecutionContext {
        instructions,
        context_sections: if role == ExecutionRole::Summary {
            vec![]
        } else {
            let visible_workspace = if crate::remote_execution::is_remote(s) {
                workspace.clone()
            } else {
                crate::execution_target::workspace_view(s)?.3
            };
            let mut sections = vec![pot_core::ContextSection {
                key: "environment".into(),
                text: format!(
                    "Runtime clock (UTC): {}. Execution workspace: {}",
                    chrono::Utc::now().to_rfc3339(),
                    visible_workspace.display()
                ),
            }];
            if role == ExecutionRole::Main {
                sections.push(pot_core::ContextSection {
                    key: "models".into(),
                    text: super::super::catalog::context(s)?,
                });
                sections.push(pot_core::ContextSection {
                    key: "tasks".into(),
                    text: crate::tasks::context(s),
                });
            }
            sections
        },
        skill_resources,
        tool_executor: {
            #[cfg(test)]
            if let Some(executor) = &s.test_tool_executor
                && !s.execution_service.permissions.configured()
            {
                Some(executor.clone())
            } else {
                Some(Arc::new(crate::execution_target::Tools {
                    service: s.clone(),
                    generation: thread.generation,
                }))
            }
            #[cfg(not(test))]
            {
                Some(Arc::new(crate::execution_target::Tools {
                    service: s.clone(),
                    generation: thread.generation,
                }))
            }
        },
        data: s.data.clone(),
        workspace,
        owner: s.usage_scope.clone(),
        role,

        main_prompt,
        feedback: Arc::new(Feedback {
            service: s.clone(),
            generation: thread.generation,
            request: request.map(|r| r.request_id.clone()),
            instruction_reviews: Default::default(),
        }),
    })
}
pub(crate) fn context(
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
            instruction_reviews: Default::default(),
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
            instruction_reviews: Default::default(),
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
