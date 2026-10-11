//! Control-owned model, policy, approval and guidance ports for one assigned run.
use crate::remote_environment::RemoteBinding;
use crate::*;

use pot_runner::control::Operation;
impl Service {
    pub fn validate_task_agent_binding(&self, binding: &RemoteBinding) -> Result<()> {
        anyhow::ensure!(
            self.is_worker && self.environment_ref.as_ref() == Some(&binding.environment),
            "Task Agent assignment mismatch"
        );
        anyhow::ensure!(
            crate::remote_environment::binding(self, binding.environment.clone())? == *binding,
            "Task Agent run is no longer active"
        );
        Ok(())
    }
    pub async fn task_agent_operation(
        &self,
        binding: &RemoteBinding,
        operation: Operation,
    ) -> Result<Value> {
        self.validate_task_agent_binding(binding)?;
        match operation {
            Operation::Ready => {
                let paused = {
                    let inner = self.conversation.inner.lock().unwrap();
                    let value = inner
                        .store
                        .get("execution.control")?
                        .map(|v| serde_json::from_str::<crate::turn_control::TurnControl>(&v))
                        .transpose()?;
                    value.is_some_and(|v| {
                        inner
                            .turn
                            .as_ref()
                            .is_some_and(|t| v.user_message_id == t.user_message_id)
                            && v.action == "pause"
                    })
                };
                let ledger = self.usage.lock().unwrap();
                let waiting = ledger.waiting(&self.usage_scope)?.is_some();
                let allowance = !waiting || ledger.ready(&self.usage_scope, now())?;
                if waiting && allowance {
                    ledger.clear_wait(&self.usage_scope)?;
                }
                drop(ledger);
                let key = crate::remote_environment::key(&binding.environment)?;
                let policy = self
                    .environment_service
                    .permissions
                    .remote_hub(&key)?
                    .state(&self.usage_scope);
                Ok(
                    json!({"ready": !paused && allowance, "cancelled": agent::cancelled(self) || self.stopping(), "policy":policy}),
                )
            }
            Operation::Steering { finalizing } => Ok(json!(crate::turn_control::pending(
                self,
                binding.generation,
                finalizing
            )?)),
            Operation::Acknowledge { ids } => {
                crate::turn_control::acknowledge(self, binding.generation, &ids)?;
                Ok(json!({"acknowledged":true}))
            }
            Operation::Permission { mut request } => {
                anyhow::ensure!(
                    request["owner"] == binding.owner
                        && request["turn"] == binding.run_id
                        && request["generation"] == binding.generation
                        && request["environment"]
                            == crate::remote_environment::key(&binding.environment)?,
                    "Remote approval identity mismatch"
                );
                let original = request.clone();
                let turn = self
                    .conversation
                    .inner
                    .lock()
                    .unwrap()
                    .turn
                    .as_ref()
                    .ok_or_else(|| anyhow!("No active turn"))?
                    .user_message_id
                    .clone();
                let original_request = self
                    .conversation
                    .inner
                    .lock()
                    .unwrap()
                    .store
                    .message(&turn)?
                    .and_then(|m| m.request_id)
                    .ok_or_else(|| anyhow!("Task request missing"))?;
                request["turn"] = json!(original_request);
                let mut result = crate::permissions::request(
                    self,
                    binding.generation,
                    &original_request,
                    request,
                )
                .await?;
                result["request"] = original;
                Ok(result)
            }
            Operation::Compaction { event } => {
                let turn = self
                    .conversation
                    .inner
                    .lock()
                    .unwrap()
                    .turn
                    .as_ref()
                    .ok_or_else(|| anyhow!("No active turn"))?
                    .user_message_id
                    .clone();
                self.usage
                    .lock()
                    .unwrap()
                    .compaction(&self.usage_scope, &turn, &event, now())?;
                Ok(json!({"ok":true}))
            }
            Operation::Model { .. } => bail!("Model requests use the model response port"),
        }
    }
    pub async fn task_agent_model(
        &self,
        binding: &RemoteBinding,
        request: pot_core::ModelRequest,
    ) -> Result<axum::response::Response> {
        self.validate_task_agent_binding(binding)?;
        let spec = agent::thread(self)?.config;
        anyhow::ensure!(
            request.provider == spec.provider
                && request.model == spec.model
                && request.protocol == spec.gateway_protocol,
            "Remote model differs from fixed task configuration"
        );
        use axum::response::IntoResponse;
        Ok(
            match crate::gateway::call(self, request.body, request.compaction_id.as_deref(), None)
                .await
            {
                Ok(response) => response,
                Err(error) => error.into_response(),
            },
        )
    }
}
