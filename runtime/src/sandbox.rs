//! Host-owned Sandbox inventory and the only environment execution boundary.
use super::*;

/// In-process Agent calls use the same Runtime authorization as MCP calls.
pub struct Tools {
    pub service: Service,
    pub epoch: u64,
}
impl pot_core::ToolExecutor for Tools {
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
                agent::binding(&self.service)?.epoch == self.epoch,
                "Execution binding changed"
            );
            let environment = args["environment"].as_str().unwrap_or("");
            let mut execution_args = args.clone();
            if let Some(object) = execution_args.as_object_mut() {
                object.remove("environment");
            }
            if environment == "host" {
                let binding = agent::binding(&self.service)?;
                if let Some(plugin) = binding.spec.params.agent.plugin_for_tool(name)? {
                    anyhow::ensure!(
                        plugin.manifest.execution == "environment",
                        "This plugin supports Sandbox execution only"
                    );
                    let descriptor = binding
                        .spec
                        .params
                        .agent
                        .model_descriptor(&binding.spec.provider, &binding.spec.model)?;
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
                    .permissions
                    .host_executor(&self.service.usage_scope, self.epoch);
                return pot_core::ToolExecutor::execute(
                    &*executor,
                    context,
                    policy,
                    if binding.spec.params.agent.plugin_for_tool(name)?.is_some() {
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
            if let Some(target) = environment.strip_prefix("sandbox:") {
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
// Guest output cannot manufacture Agent lifecycle/control events. IDs are
// remapped from the execution receipt to the caller's model tool invocation.
fn guest_output(value: Value, call: &str, remaining: &mut usize) -> Result<pot_core::Update> {
    let pot_core::Update::ToolOutput { stream, text, .. } = serde_json::from_value(value)? else {
        bail!("Guest sent a non-output execution event");
    };
    anyhow::ensure!(
        matches!(stream.as_str(), "stdout" | "stderr") && text.len() <= *remaining,
        "Invalid guest output"
    );
    *remaining -= text.len();
    Ok(pot_core::Update::ToolOutput {
        id: call.into(),
        stream,
        text,
    })
}
/// Product context is kept outside the environment Manager.
pub struct Manager {
    pub(crate) agent_home: PathBuf,
    pub(crate) global_instructions: Mutex<crate::instructions::GlobalInstructions>,
    pub(crate) skill_user: PathBuf,
    pub(crate) skill_builtin: PathBuf,
    pub(crate) resources: Option<Arc<crate::sandbox_resources::Hub>>,
    environment: Arc<pot_sandbox::Manager>,
}
impl std::ops::Deref for Manager {
    type Target = Arc<pot_sandbox::Manager>;
    fn deref(&self) -> &Self::Target {
        &self.environment
    }
}
impl Manager {
    #[cfg(test)]
    pub fn open(data: &std::path::Path) -> Result<Self> {
        let mut manager = Self::with_providers(
            data,
            vec![Arc::new(
                pot_sandbox_macos_apple_container::MacOSAppleContainerProvider::new()?,
            )],
        )?;
        manager.resources = None; // Legacy execution fixtures inject no resource preparation service.
        Ok(manager)
    }
    pub async fn prepare(
        &self,
        id: &str,
        owner: &str,
        turn: Option<&str>,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
    ) -> Result<()> {
        if let Some(hub) = &self.resources {
            hub.wait(&self.environment, id, owner, turn, cancelled)
                .await?;
        }
        Ok(())
    }
    pub fn with_providers(
        data: &std::path::Path,
        providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    ) -> Result<Self> {
        let root = data.parent().unwrap_or(data);
        #[cfg(test)]
        let agent_home = root.join("agent-home");
        #[cfg(not(test))]
        let agent_home = std::env::var_os("POT_AGENT_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                PathBuf::from(std::env::var_os("HOME").unwrap_or_default()).join(".pot/agent")
            });
        #[cfg(test)]
        let skill_user = root.join("skills-user");
        #[cfg(not(test))]
        let skill_user = std::env::var_os("POT_AGENT_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| agent_home.join("skills"));
        #[cfg(not(test))]
        let _ = root;
        let skill_builtin = std::env::var_os("POT_BUILTIN_SKILLS_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../skills"));
        Ok(Self {
            agent_home,
            global_instructions: Mutex::new(Default::default()),
            skill_user,
            skill_builtin,
            environment: Arc::new(pot_sandbox::Manager::open(data, providers)?),
            resources: Some(crate::sandbox_resources::Hub::open(data)?),
        })
    }
}
pub async fn list(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(inventory(&s).await))
}
async fn inventory(s: &Service) -> Value {
    let mut value = s.sandboxes.inventory().await;
    let tasks = tasks::sandbox_tasks(s);
    if let Some(rows) = value["items"].as_array_mut() {
        for row in rows {
            let id = row["id"].as_str().unwrap_or("").to_owned();
            let assigned: Vec<_> = tasks.iter().filter(|task| task.sandbox_id == id).collect();
            row["running_tasks"] = json!(
                assigned
                    .iter()
                    .filter(|task| task.phase == TaskPhase::Running)
                    .count()
            );
            row["tasks"] = json!(assigned);
            row["referenced"] = json!(tasks::uses_sandbox(s, &id));
            row["resource_request"] = s
                .sandboxes
                .resources
                .as_ref()
                .map(|hub| hub.pending(&id))
                .unwrap_or(Value::Null);
        }
    }
    value["resource_requests"] = s
        .sandboxes
        .resources
        .as_ref()
        .map(|hub| hub.requests())
        .unwrap_or(json!([]));
    value
}
pub async fn manage(
    State(s): State<Service>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(management(&s, &args).await.map_err(|e| {
        error(StatusCode::CONFLICT, "sandbox_management", &e.to_string())
    })?))
}
pub async fn management(s: &Service, args: &Value) -> Result<Value> {
    anyhow::ensure!(!s.is_worker, "Workers cannot manage Sandboxes");
    if args["action"] == "delete" {
        let id = s
            .sandboxes
            .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
        anyhow::ensure!(
            !tasks::uses_sandbox(s, &id),
            "Sandbox is assigned to a persisted task or schedule"
        );
    }
    let action = args["action"].as_str().unwrap_or("");
    if matches!(action, "start" | "desktop") {
        let id = s
            .sandboxes
            .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
        if let Some(hub) = &s.sandboxes.resources
            && let Some(key) = hub.inspect(&s.sandboxes, &id, &s.usage_scope, None).await?
        {
            return Ok(
                json!({"sandbox_id":id,"resource_request_id":key,"phase":"awaiting-resources"}),
            );
        }
    }
    let mut result = s
        .sandboxes
        .environment
        .management(
            args,
            s.is_worker,
            &|| agent::cancelled(s) || s.stopping(),
            &|id, delete| tasks::guard_sandbox_delete(s, id, delete),
        )
        .await?;
    if action == "create" {
        let id = result["sandbox_id"].as_str().unwrap_or("").to_owned();
        if let Some(hub) = &s.sandboxes.resources
            && let Some(key) = hub.inspect(&s.sandboxes, &id, &s.usage_scope, None).await?
        {
            result["resource_request_id"] = json!(key);
            result["phase"] = json!("awaiting-resources");
        }
    }
    if action == "delete"
        && let Some(hub) = &s.sandboxes.resources
    {
        hub.forget(args["sandbox_id"].as_str().unwrap_or(""))?;
    }
    Ok(result)
}
pub async fn tool(s: &Service, name: &str, call_id: &str, args: &Value) -> Result<Value> {
    anyhow::ensure!(
        !call_id.is_empty() && call_id.len() <= 256,
        "Invalid execution call ID"
    );
    let turn = {
        let inner = s.inner.lock().unwrap();
        let turn = inner
            .turn
            .as_ref()
            .filter(|t| !t.cancelling)
            .ok_or_else(|| anyhow!("No active execution turn"))?;
        inner
            .store
            .message(&turn.user_message_id)?
            .and_then(|m| m.request_id)
            .ok_or_else(|| anyhow!("Turn identity missing"))?
    };
    anyhow::ensure!(
        !turn.starts_with(orchestration::RESULT_PREFIX),
        "Summary is read-only"
    );
    if name == "list_sandboxes" {
        anyhow::ensure!(!s.is_worker, "Workers cannot list Sandbox inventory");
        return Ok(inventory(s).await);
    }
    if name == "manage_sandbox" {
        anyhow::ensure!(
            args["action"] != "set_mounts",
            "Host mappings require the pot-agent permission flow"
        );
        let result = management(s, args).await?;
        if matches!(
            args["action"].as_str(),
            Some("create" | "start" | "desktop")
        ) {
            let id = s
                .sandboxes
                .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
            s.sandboxes
                .prepare(&id, &s.usage_scope, Some(&turn), &|| {
                    !turn_alive(s, &turn) || s.stopping()
                })
                .await?;
            if args["action"] != "create" {
                return management(s, args).await;
            }
        }
        let mut result = result;
        result["phase"] = json!("stopped");
        result
            .as_object_mut()
            .map(|v| v.remove("resource_request_id"));
        return Ok(result);
    }
    let binding = agent::binding(s)?;
    let settings = &binding.spec.params.agent;
    let plugin = settings.plugin_for_tool(name)?;
    anyhow::ensure!(
        plugin.is_some() || ["exec_command", "write_stdin", "apply_patch"].contains(&name),
        "Unknown execution operation"
    );
    anyhow::ensure!(
        (plugin.is_some()
            || binding.spec.engine != agent::Engine::Pot
            || settings.builtin_tools.iter().any(|n| n == name))
            && settings
                .allowed_tools
                .as_ref()
                .is_none_or(|names| names.iter().any(|n| n == name)),
        "Tool disabled by execution policy"
    );
    let mut validated = args.clone();
    if let Some(object) = validated.as_object_mut() {
        object.remove("sandbox_id");
    }
    if let Some(plugin) = &plugin {
        plugin.validate_input(name, &validated)?;
    } else {
        pot_core::environment_tools::validate(name, &validated)?;
    }

    // Pipe sessions already belong to a prepared scope; their Manager validation
    // must run without consulting the current selection or asking for downloads.
    if name != "write_stdin" && !(plugin.is_some() && args.get("session_id").is_some()) {
        let requested = args["sandbox_id"].as_str().unwrap_or("");
        if s.is_worker {
            anyhow::ensure!(
                requested.is_empty() || Some(requested) == s.sandbox_id.as_deref(),
                "Workers cannot switch Sandboxes"
            );
        }
        let id = s.sandboxes.assignment(if s.is_worker {
            s.sandbox_id.as_deref().unwrap_or("")
        } else {
            requested
        })?;
        s.sandboxes
            .prepare(&id, &s.usage_scope, Some(&turn), &|| {
                !turn_alive(s, &turn) || s.stopping()
            })
            .await?;
    }
    let (name, args) = if let Some(plugin) = plugin {
        let descriptor = settings.model_descriptor(&binding.spec.provider, &binding.spec.model)?;
        let target = args.get("sandbox_id").cloned();
        let mut envelope =
            pot_core::plugins::call(plugin, name, args.clone(), descriptor.vision())?;
        if let Some(target) = target {
            envelope["sandbox_id"] = target;
        }
        ("plugin_call", envelope)
    } else {
        (name, args.clone())
    };
    let args = &args;
    let legacy_key = format!("sandbox.operation:{turn}:{call_id}");
    let legacy = s
        .inner
        .lock()
        .unwrap()
        .store
        .get(&legacy_key)?
        .map(|text| serde_json::from_str(&text))
        .transpose()?;
    let grant = pot_sandbox::AccessGrant {
        owner: s.usage_scope.clone(),
        turn: turn.clone(),
        epoch: binding.epoch,
        target: s.sandbox_id.clone().unwrap_or_default(),
        worker: s.is_worker,
        policy: binding.spec.params.agent.shell.clone(),
    };
    s.sandboxes
        .environment
        .execute(
            &grant,
            name,
            call_id,
            args,
            &|| !turn_alive(s, &turn) || s.stopping(),
            legacy,
        )
        .await
}
pub async fn finish_turn(s: &Service) -> Result<()> {
    if agent::cancelled(s) || s.stopping() {
        finish(s).await
    } else {
        s.sandboxes.environment.finish_turn(&s.usage_scope).await
    }
}
pub async fn refresh(s: &Service) -> Result<()> {
    finish_turn(s).await
}
pub async fn reap_idle(s: &Service) -> Result<()> {
    s.permissions.reap_host_plugins().await?;
    s.sandboxes.environment.reap_idle().await
}
pub async fn finish_stale(s: &Service, epoch: u64) -> Result<()> {
    s.permissions
        .finish_stale_host(&s.usage_scope, epoch)
        .await?;
    s.sandboxes
        .environment
        .finish_stale(&s.usage_scope, epoch)
        .await
}
pub async fn shutdown(s: &Service) -> Result<()> {
    s.permissions.shutdown_host().await?;
    s.sandboxes.environment.shutdown().await
}
pub async fn finish_idle(s: &Service) -> Result<()> {
    s.sandboxes.environment.finish_idle(&s.usage_scope).await
}
pub async fn finish(s: &Service) -> Result<()> {
    s.permissions.finish_host(s).await?;
    s.sandboxes.environment.finish(&s.usage_scope).await
}
pub async fn drain(s: &Service) -> Result<()> {
    s.permissions.finish_host(s).await?;
    s.sandboxes.environment.drain(&s.usage_scope).await
}
pub async fn execute(
    State(root): State<Service>,
    h: HeaderMap,
    Json(call): Json<Value>,
) -> ApiResult<Json<Value>> {
    let s = tasks::execution_service(&root, &h)?;
    let operation = tokio::spawn(async move {
        tool(
            &s,
            call["name"].as_str().unwrap_or(""),
            call["call_id"].as_str().unwrap_or(""),
            &call["args"],
        )
        .await
    });
    Ok(Json(
        operation
            .await
            .map_err(|e| internal(e.into()))?
            .map_err(|e| error(StatusCode::CONFLICT, "sandbox_execution", &e.to_string()))?,
    ))
}

fn turn_alive(s: &Service, request: &str) -> bool {
    let inner = s.inner.lock().unwrap();
    inner
        .turn
        .as_ref()
        .filter(|t| !t.cancelling)
        .and_then(|t| inner.store.message(&t.user_message_id).ok().flatten())
        .is_some_and(|m| m.request_id.as_deref() == Some(request))
}
#[cfg(test)]
mod tests {
    use super::*;
    fn active(s: &Service) {
        s.save(Message {
            id: "input".into(),
            conversation_id: s.usage_scope.clone(),
            role: Role::User,
            text: "fixture".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some("turn".into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "input".into(),
            cancelling: false,
        }));
    }
    #[tokio::test]
    async fn main_inventory_selection_persists_workers_cannot_manage_or_retarget() {
        let s = tasks::tests::fixture();
        management(
            &s,
            &json!({"action":"create","sandbox_id":"linux-b","name":"Second Linux"}),
        )
        .await
        .unwrap();
        management(&s, &json!({"action":"select","sandbox_id":"linux-b"}))
            .await
            .unwrap();
        assert_eq!(
            Manager::open(&s.data).unwrap().assignment("").unwrap(),
            "linux-b"
        );
        assert!(
            management(&s, &json!({"action":"create","sandbox_id":"Linux-B"}))
                .await
                .is_err()
        );
        assert!(!s.data.parent().unwrap().join("helper.sock").exists());
        let mut worker = s.clone();
        worker.is_worker = true;
        worker.sandbox_id = Some("default".into());
        active(&worker);
        assert!(
            management(&worker, &json!({"action":"create","sandbox_id":"escape"}))
                .await
                .is_err()
        );
        assert!(
            tool(&worker, "list_sandboxes", "list", &json!({}))
                .await
                .is_err()
        );
        assert!(
            tool(
                &worker,
                "exec_command",
                "switch",
                &json!({"cmd":"true","sandbox_id":"linux-b"})
            )
            .await
            .unwrap_err()
            .to_string()
            .contains("cannot switch")
        );
        let tools = tool_bridge::schemas(false, true);
        assert_eq!(tools.len(), 4);
        assert!(
            tools
                .iter()
                .all(|t| t["name"].as_str().unwrap() == "exec_command"
                    || t["name"] == "write_stdin"
                    || t["name"] == "apply_patch"
                    || t["name"] == "cua_repl")
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn settings_validate_before_io_and_refuse_busy_environment() {
        let s = tasks::tests::fixture();
        let args = json!({"action":"update","sandbox_id":"default","name":"Linux","config":{"cpus":2,"memory_gib":2,"disk_gib":8}});
        let mut invalid = args.clone();
        invalid["config"]["cpus"] = json!(0);
        assert!(
            management(&s, &invalid)
                .await
                .unwrap_err()
                .to_string()
                .contains("resources")
        );
        invalid = args.clone();
        invalid["name"] = json!("   ");
        assert!(
            management(&s, &invalid)
                .await
                .unwrap_err()
                .to_string()
                .contains("Name")
        );
        let lease = s.sandboxes.acquire("default").await.unwrap();
        assert!(
            management(&s, &args)
                .await
                .unwrap_err()
                .to_string()
                .contains("busy")
        );
        assert!(
            !s.data
                .parent()
                .unwrap()
                .join("sandboxes/default/sandbox.json")
                .exists()
        );
        let mut worker = s.clone();
        worker.is_worker = true;
        assert!(
            management(&worker, &args)
                .await
                .unwrap_err()
                .to_string()
                .contains("Workers")
        );
        drop(lease);
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
    #[test]
    fn guest_events_cannot_forge_agent_lifecycle_and_use_model_call_identity() {
        let mut remaining = 8;
        assert!(guest_output(json!({"TurnFinished":"Success"}), "model", &mut remaining).is_err());
        let output = guest_output(
            serde_json::to_value(pot_core::Update::ToolOutput {
                id: "guest-receipt".into(),
                stream: "stdout".into(),
                text: "hello".into(),
            })
            .unwrap(),
            "model",
            &mut remaining,
        )
        .unwrap();
        assert!(matches!(output,pot_core::Update::ToolOutput{id,..} if id=="model"));
        assert_eq!(remaining, 3);
        assert!(
            guest_output(
                serde_json::to_value(pot_core::Update::ToolOutput {
                    id: "guest".into(),
                    stream: "stdout".into(),
                    text: "oversized".into()
                })
                .unwrap(),
                "model",
                &mut remaining
            )
            .is_err()
        );
    }
}
