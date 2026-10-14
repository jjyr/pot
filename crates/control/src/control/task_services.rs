//! Control-owned model, policy, approval and guidance ports for one assigned run.
use crate::remote_execution::RemoteBinding;
use crate::*;

use pot_runner::control::Operation;
impl Service {
    pub fn validate_task_agent_binding(&self, binding: &RemoteBinding) -> Result<()> {
        anyhow::ensure!(
            self.is_worker && self.execution_target.as_ref() == Some(&binding.execution_target),
            "Task Agent assignment mismatch"
        );
        anyhow::ensure!(
            crate::remote_execution::binding(self, binding.execution_target.clone())? == *binding,
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
            Operation::PrepareHost { name, mut args } => {
                anyhow::ensure!(
                    pot_agent::permissions::is_execution_tool(&name)
                        || agent::thread(self)?
                            .config
                            .params
                            .agent
                            .plugin_for_tool(&name)?
                            .is_some(),
                    "Unsupported Host operation"
                );
                args["execution_target"] = json!(crate::remote_execution::key(
                    &pot_core::ExecutionTarget::host(&binding.execution_target.runtime_id)
                )?);
                crate::permissions::prepare(self, &name, args).await
            }
            Operation::ExecuteHost {
                call_id,
                name,
                mut args,
            } => {
                anyhow::ensure!(
                    pot_agent::permissions::is_execution_tool(&name)
                        || agent::thread(self)?
                            .config
                            .params
                            .agent
                            .plugin_for_tool(&name)?
                            .is_some(),
                    "Unsupported Host operation"
                );
                let host = crate::remote_execution::binding(
                    self,
                    pot_core::ExecutionTarget::host(&binding.execution_target.runtime_id),
                )?;
                let turn = self
                    .conversation
                    .inner
                    .lock()
                    .unwrap()
                    .turn
                    .clone()
                    .ok_or_else(|| anyhow!("No active turn"))?;
                let request_id = self
                    .conversation
                    .inner
                    .lock()
                    .unwrap()
                    .store
                    .message(&turn.user_message_id)?
                    .and_then(|m| m.request_id)
                    .ok_or_else(|| anyhow!("Missing turn identity"))?;
                let decision = crate::permissions::request(
                    self,
                    binding.generation,
                    &request_id,
                    json!(pot_agent::permissions::Request {
                        owner: binding.owner.clone(),
                        execution_target: crate::remote_execution::key(&host.execution_target)?,
                        generation: binding.generation,
                        turn: request_id.clone(),
                        call_id: call_id.clone(),
                        tool: name.clone(),
                        args: args.clone(),
                    }),
                )
                .await?;
                anyhow::ensure!(decision["approved"] == true, "Host operation denied");
                args.as_object_mut()
                    .ok_or_else(|| anyhow!("Invalid arguments"))?
                    .remove("execution_target");
                crate::remote_execution::port(self)?
                    .execute(host, call_id, name, args)
                    .await
            }
            Operation::Resources { skills, plugins } => {
                anyhow::ensure!(
                    skills == self.skill_selection.clone().unwrap_or_default(),
                    "Skill selection differs from task"
                );
                let thread = agent::thread(self)?;
                let expected: Vec<_> = thread
                    .config
                    .params
                    .agent
                    .plugins
                    .iter()
                    .filter(|(_, plugin)| plugin.enabled)
                    .map(|(name, _)| name.clone())
                    .collect();
                anyhow::ensure!(plugins == expected, "Plugin selection differs from task");
                crate::control_resources::refresh_plugins(self)?;
                Ok(json!(crate::control_resources::current(self)?))
            }
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
                Ok(
                    json!({"ready": !paused && allowance, "cancelled": agent::cancelled(self) || self.stopping()}),
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
                        && (request["execution_target"]
                            == crate::remote_execution::key(&binding.execution_target)?
                            || request["execution_target"]
                                == crate::remote_execution::key(
                                    &pot_core::ExecutionTarget::host(
                                        &binding.execution_target.runtime_id
                                    )
                                )?),
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
