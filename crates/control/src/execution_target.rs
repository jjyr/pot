//! Execution target implementations are independent from authorization storage.
use crate::sandbox::{guest_output, tool};
use crate::*;
use std::collections::BTreeMap;

/// Host processes and plugins belong to the target Runtime, not a Sandbox or
/// a permissions file. Callers must authorize before obtaining an executor.
#[derive(Default)]
pub(crate) struct HostExecution {
    executors: Mutex<BTreeMap<String, (u64, Arc<pot_tool_executor::LocalTools>)>>,
}
impl HostExecution {
    pub fn host_executor(
        &self,
        owner: &str,
        generation: u64,
    ) -> Arc<pot_tool_executor::LocalTools> {
        let mut executors = self.executors.lock().unwrap();
        if executors
            .get(owner)
            .is_some_and(|(old, _)| *old != generation)
        {
            executors.remove(owner);
        }
        executors
            .entry(owner.into())
            .or_insert_with(|| (generation, Arc::new(pot_tool_executor::LocalTools::host())))
            .1
            .clone()
    }
    pub(crate) async fn host_session(&self, owner: &str, generation: u64, session: &str) -> bool {
        let executor = self
            .executors
            .lock()
            .unwrap()
            .get(owner)
            .filter(|(bound, _)| *bound == generation)
            .map(|(_, e)| e.clone());
        match executor {
            Some(e) => e.has_session(session).await,
            None => false,
        }
    }
    pub(crate) async fn sessions(&self) -> Value {
        let executors: Vec<_> = self
            .executors
            .lock()
            .unwrap()
            .iter()
            .map(|(owner, (generation, executor))| (owner.clone(), *generation, executor.clone()))
            .collect();
        let mut sessions = vec![];
        for (owner, generation, executor) in executors {
            sessions.push(json!({"owner":owner,"generation":generation,"sessions":executor.session_inventory().await}));
        }
        json!(sessions)
    }
    pub async fn reap_host_plugins(&self) -> Result<()> {
        let executors: Vec<_> = self
            .executors
            .lock()
            .unwrap()
            .values()
            .map(|(_, e)| e.clone())
            .collect();
        for executor in executors {
            executor.reap_plugins().await?;
        }
        Ok(())
    }
    pub async fn finish_stale_host(&self, owner: &str, generation: u64) -> Result<()> {
        let executor = {
            let mut executors = self.executors.lock().unwrap();
            if executors
                .get(owner)
                .is_some_and(|(bound, _)| *bound != generation)
            {
                executors.remove(owner).map(|(_, e)| e)
            } else {
                None
            }
        };
        if let Some(executor) = executor {
            executor.shutdown().await?;
        }
        Ok(())
    }
    pub async fn shutdown_host(&self) -> Result<()> {
        let executors = std::mem::take(&mut *self.executors.lock().unwrap());
        let mut errors = vec![];
        for (_, (_, executor)) in executors {
            if let Err(error) = executor.shutdown().await {
                errors.push(error.to_string());
            }
        }
        anyhow::ensure!(errors.is_empty(), "{}", errors.join("; "));
        Ok(())
    }
    pub async fn finish_host(&self, owner: &str) -> Result<()> {
        let executor = self.executors.lock().unwrap().remove(owner).map(|(_, e)| e);
        if let Some(executor) = executor {
            executor.shutdown().await?;
        }
        Ok(())
    }
}

fn host_workspace_view(workspace: &std::path::Path) -> (PathBuf, PathBuf, PathBuf, PathBuf) {
    let base = workspace
        .ancestors()
        .find(|p| p.join(".git").exists())
        .unwrap_or(workspace)
        .to_path_buf();
    (base.clone(), base, workspace.into(), workspace.into())
}

/// Source and execution-visible roots for local instruction/skill discovery.
pub(crate) fn workspace_view(s: &Service) -> Result<(PathBuf, PathBuf, PathBuf, PathBuf)> {
    if s.execution_target
        .as_ref()
        .is_some_and(|r| matches!(r.target, pot_core::ExecutionTargetKind::Host {}))
    {
        let reference = s.execution_target.as_ref().unwrap();
        anyhow::ensure!(
            reference.is_local_to(&s.runtime_id),
            "Remote workspace discovery requires destination transport"
        );
        return Ok(host_workspace_view(&s.workspace));
    }
    if !s.is_worker && s.sandbox_id.is_none() {
        let workspace = if s.execution_service.sandboxes.assignment("").is_err() {
            s.workspace.clone()
        } else {
            s.data.join("agent-workspace")
        };
        return Ok(host_workspace_view(&workspace));
    }
    let target = s
        .sandbox_id
        .clone()
        .ok_or_else(|| anyhow!("Worker Sandbox binding missing"))?;
    let base = s.execution_service.sandboxes.workspace(&target);
    let visible_base = PathBuf::from(s.execution_service.sandboxes.guest_workspace(&target)?);
    if !s.is_worker {
        return Ok((base.clone(), visible_base.clone(), base, visible_base));
    }
    let cwd = base.join("tasks").join(&s.usage_scope);
    let visible = visible_base.join("tasks").join(&s.usage_scope);
    Ok((base, visible_base, cwd, visible))
}

pub async fn list(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(inventory(&s).await))
}

pub(crate) async fn inventory(s: &Service) -> Value {
    let mut value = crate::sandbox::inventory(s).await;
    if let Some(items) = value["items"].as_array_mut() {
        for item in items.iter_mut() {
            let id = item["id"].as_str().unwrap_or("");
            item["execution_target"] = json!(pot_core::ExecutionTarget::sandbox(&s.runtime_id, id));
        }
        if s.execution_service
            .host_enabled
            .load(std::sync::atomic::Ordering::SeqCst)
        {
            items.insert(0, json!({"execution_target":pot_core::ExecutionTarget::host(&s.runtime_id),"kind":"host","workspace":s.workspace,"management":false,"execution_policy":"owner_approval","sessions":s.execution_service.host.sessions().await}));
        }
    }
    if let Ok(port) = crate::remote_execution::port(s) {
        let machines = port.machines();
        if let Some(items) = value["items"].as_array_mut() {
            for item in items {
                if let Some(machine) = machines
                    .iter()
                    .find(|m| item["execution_target"]["runtime_id"] == m.runtime_id)
                {
                    item["machine_tags"] = json!(machine.tags);
                }
            }
        }
        match port.inventory().await {
            Ok(remote) => {
                if let Some(items) = value["items"].as_array_mut() {
                    items.extend(remote["items"].as_array().cloned().unwrap_or_default());
                }
            }
            Err(error) => value["remote_error"] = json!(error.to_string()),
        }
    }
    value["runtime_id"] = json!(s.runtime_id);
    value
}

/// Resolve public local strings or a complete typed destination before effects.
pub(crate) fn resolve(s: &Service, value: &Value) -> Result<pot_core::ExecutionTarget> {
    let reference = if let Some(value) = value.as_str() {
        if value.starts_with("remote:") {
            crate::remote_execution::from_key(value)?
        } else if value == "host" {
            pot_core::ExecutionTarget::host(&s.runtime_id)
        } else {
            let sandbox_id = value
                .strip_prefix("sandbox:")
                .filter(|id| !id.is_empty())
                .ok_or_else(|| {
                    anyhow!("Use host, sandbox:ID or a typed Execution target reference")
                })?;
            pot_core::ExecutionTarget::sandbox(&s.runtime_id, sandbox_id)
        }
    } else {
        serde_json::from_value(value.clone())?
    };
    reference.validate()?;
    Ok(reference)
}
pub(crate) fn local_key(s: &Service, reference: &pot_core::ExecutionTarget) -> Result<String> {
    anyhow::ensure!(
        reference.is_local_to(&s.runtime_id),
        "Remote Execution target transport is not available"
    );
    Ok(match &reference.target {
        pot_core::ExecutionTargetKind::Host {} => {
            anyhow::ensure!(
                s.execution_service
                    .host_enabled
                    .load(std::sync::atomic::Ordering::SeqCst),
                "Host execution resource is disabled"
            );
            "host".into()
        }
        pot_core::ExecutionTargetKind::Sandbox { sandbox_id } => format!("sandbox:{sandbox_id}"),
    })
}
pub(crate) fn check_binding(s: &Service, reference: &pot_core::ExecutionTarget) -> Result<()> {
    anyhow::ensure!(
        !s.is_worker
            || s.execution_target.as_ref() == Some(reference)
            || (matches!(reference.target, pot_core::ExecutionTargetKind::Host {})
                && s.execution_target
                    .as_ref()
                    .is_some_and(|bound| bound.runtime_id == reference.runtime_id)),
        "Workers cannot switch Execution target targets"
    );
    Ok(())
}

/// In-process Agent calls use the same Runtime authorization as MCP calls.
pub struct Tools {
    pub service: Service,
    pub generation: u64,
}
impl pot_core::ToolExecutor for Tools {
    fn routes_execution_targets(&self) -> bool {
        true
    }
    fn execute<'a>(
        &'a self,
        context: &'a pot_core::ExecutionContext,
        policy: &'a pot_core::ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        emit: &'a mut (dyn FnMut(pot_core::Update) -> Result<()> + Send),
    ) -> futures::future::BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            anyhow::ensure!(
                agent::thread(&self.service)?.generation == self.generation,
                "Execution thread changed"
            );
            let execution_target = args["execution_target"].as_str().unwrap_or("");
            if execution_target.starts_with("remote:") {
                let reference = crate::remote_execution::from_key(execution_target)?;
                check_binding(&self.service, &reference)?;
                let binding = crate::remote_execution::binding(&self.service, reference)?;
                let mut args = args.clone();
                args.as_object_mut()
                    .ok_or_else(|| anyhow!("Arguments must be an object"))?
                    .remove("execution_target");
                let mut value = crate::remote_execution::port(&self.service)?
                    .execute(binding, call.into(), name.into(), args)
                    .await?;
                if let Some(events) = value.get_mut("events").and_then(Value::as_array_mut) {
                    let mut remaining = policy.max_output_bytes.saturating_mul(2);
                    for event in events.drain(..) {
                        emit(guest_output(event, call, &mut remaining)?)?;
                    }
                }
                return Ok(value.get("result").cloned().unwrap_or(value));
            }
            let mut execution_args = args.clone();
            if let Some(object) = execution_args.as_object_mut() {
                object.remove("execution_target");
            }
            if !execution_target.is_empty() {
                check_binding(
                    &self.service,
                    &resolve(&self.service, &json!(execution_target))?,
                )?;
            }
            if execution_target == "host" {
                anyhow::ensure!(
                    self.service
                        .execution_service
                        .host_enabled
                        .load(std::sync::atomic::Ordering::SeqCst),
                    "Host execution resource is disabled"
                );
                let thread = agent::thread(&self.service)?;
                if let Some(plugin) = thread.config.params.agent.plugin_for_tool(name)? {
                    anyhow::ensure!(
                        plugin.manifest.supports_host(),
                        "This plugin supports Sandbox execution only"
                    );
                    let descriptor = thread
                        .config
                        .params
                        .agent
                        .model_descriptor(&thread.config.provider, &thread.config.model)?;
                    execution_args =
                        pot_core::plugins::call(plugin, name, execution_args, descriptor.vision())?;
                } else {
                    anyhow::ensure!(
                        name != "plugin_call",
                        "Internal plugin envelopes cannot be called directly"
                    );
                }
                let executor = self
                    .service
                    .execution_service
                    .host
                    .host_executor(&self.service.usage_scope, self.generation);
                return pot_core::ToolExecutor::execute(
                    &*executor,
                    context,
                    policy,
                    if thread.config.params.agent.plugin_for_tool(name)?.is_some() {
                        "plugin_call"
                    } else {
                        name
                    },
                    &execution_args,
                    call,
                    emit,
                )
                .await;
            }
            if let Some(target) = execution_target.strip_prefix("sandbox:") {
                execution_args["sandbox_id"] = json!(target);
            }
            let mut value = tool(&self.service, name, call, &execution_args).await?;
            if let Some(events) = value.get_mut("events").and_then(Value::as_array_mut) {
                let mut remaining = policy.max_output_bytes.saturating_mul(2);
                for event in events.drain(..) {
                    emit(guest_output(event, call, &mut remaining)?)?;
                }
            }
            Ok(value.get("result").cloned().unwrap_or(value))
        })
    }
    fn abort<'a>(
        &'a self,
        _: &'a pot_core::ExecutionContext,
    ) -> futures::future::BoxFuture<'a, Result<()>> {
        Box::pin(finish(&self.service))
    }
    fn finish<'a>(
        &'a self,
        _: &'a pot_core::ExecutionContext,
    ) -> futures::future::BoxFuture<'a, Result<()>> {
        Box::pin(finish_turn(&self.service))
    }
}
pub async fn finish_turn(s: &Service) -> Result<()> {
    finish(s).await
}
pub async fn refresh(s: &Service) -> Result<()> {
    finish_turn(s).await
}
pub async fn reap_idle(s: &Service) -> Result<()> {
    s.execution_service.host.reap_host_plugins().await?;
    s.execution_service.sandboxes.reap_idle().await?;
    s.execution_service.sandboxes.reap_managed().await
}
pub async fn finish_stale(s: &Service, generation: u64) -> Result<()> {
    s.execution_service
        .host
        .finish_stale_host(&s.usage_scope, generation)
        .await?;
    s.execution_service
        .sandboxes
        .finish_stale(&s.usage_scope, generation)
        .await
}
pub async fn shutdown(s: &Service) -> Result<()> {
    s.execution_service.host.shutdown_host().await?;
    s.execution_service.sandboxes.shutdown().await
}
pub async fn finish_idle(s: &Service) -> Result<()> {
    s.execution_service
        .sandboxes
        .finish_idle(&s.usage_scope)
        .await
}
pub async fn finish(s: &Service) -> Result<()> {
    let remote = crate::remote_execution::cancel(s).await;
    let host = s.execution_service.host.finish_host(&s.usage_scope).await;
    let sandbox = s.execution_service.sandboxes.finish(&s.usage_scope).await;
    remote.and(host).and(sandbox)
}
pub async fn drain(s: &Service) -> Result<()> {
    let remote = crate::remote_execution::cancel(s).await;
    let host = s.execution_service.host.finish_host(&s.usage_scope).await;
    let sandbox = s.execution_service.sandboxes.drain(&s.usage_scope).await;
    remote.and(host).and(sandbox)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn worker_binding_checks_full_runtime_and_target() {
        let mut worker = crate::tests::service();
        worker.is_worker = true;
        let binding = pot_core::ExecutionTarget::sandbox(&worker.runtime_id, "default");
        worker.execution_target = Some(binding.clone());
        assert!(check_binding(&worker, &binding).is_ok());
        assert!(check_binding(&worker, &resolve(&worker, &json!("host")).unwrap()).is_ok());
        for value in [
            json!({"runtime_id":"other","target":{"kind":"host"}}),
            json!({"runtime_id":"other","target":{"kind":"sandbox","sandbox_id":"default"}}),
        ] {
            let target = resolve(&worker, &value).unwrap();
            assert!(check_binding(&worker, &target).is_err());
        }
        worker.execution_target = Some(pot_core::ExecutionTarget::host(&worker.runtime_id));
        assert!(check_binding(&worker, &resolve(&worker, &json!("host")).unwrap()).is_ok());
        assert!(check_binding(&worker, &binding).is_err());
        assert!(local_key(&worker, &pot_core::ExecutionTarget::host("other")).is_err());
    }
}
