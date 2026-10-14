//! Sandbox inventory, management and contained execution adapter.
use super::*;

// Guest output cannot manufacture Agent lifecycle/control events. IDs are
// remapped from the execution receipt to the caller's model tool invocation.
pub(crate) fn guest_output(
    value: Value,
    call: &str,
    remaining: &mut usize,
) -> Result<pot_core::Update> {
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
/// Product context is kept outside the Sandbox Manager.
pub struct Manager {
    pub(crate) resources: Option<Arc<crate::sandbox_resources::Hub>>,
    sandboxes: Arc<pot_sandbox::Manager>,
}
impl std::ops::Deref for Manager {
    type Target = Arc<pot_sandbox::Manager>;
    fn deref(&self) -> &Self::Target {
        &self.sandboxes
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
            hub.wait(&self.sandboxes, id, owner, turn, cancelled)
                .await?;
        }
        Ok(())
    }
    /// Register installed Providers without creating or starting a Sandbox.
    pub fn empty(
        data: &std::path::Path,
        providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    ) -> Result<Self> {
        Ok(Self {
            sandboxes: Arc::new(pot_sandbox::Manager::open_empty(data, providers)?),
            resources: Some(crate::sandbox_resources::Hub::open(data)?),
        })
    }
    pub fn with_providers(
        data: &std::path::Path,
        providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    ) -> Result<Self> {
        Ok(Self {
            sandboxes: Arc::new(pot_sandbox::Manager::open(data, providers)?),
            resources: Some(crate::sandbox_resources::Hub::open(data)?),
        })
    }
}
pub async fn list(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(inventory(&s).await))
}
pub(crate) async fn inventory(s: &Service) -> Value {
    let mut value = s.execution_service.sandboxes.inventory().await;
    let tasks = tasks::sandbox_tasks(s);
    if let Some(rows) = value["items"].as_array_mut() {
        for row in rows {
            let id = row["id"].as_str().unwrap_or("").to_owned();
            let assigned: Vec<_> = tasks
                .iter()
                .filter(|task| {
                    task.execution_target.as_ref()
                        == Some(&pot_core::ExecutionTarget::sandbox(&s.runtime_id, &id))
                })
                .collect();
            row["running_tasks"] = json!(
                assigned
                    .iter()
                    .filter(|task| task.phase == TaskPhase::Running)
                    .count()
            );
            row["tasks"] = json!(assigned);
            row["referenced"] = json!(tasks::uses_sandbox(s, &id));
            row["resource_request"] = s
                .execution_service
                .sandboxes
                .resources
                .as_ref()
                .map(|hub| hub.pending(&id))
                .unwrap_or(Value::Null);
        }
    }
    value["resource_requests"] = s
        .execution_service
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
pub(crate) fn remote_management_target(
    s: &Service,
    args: &Value,
) -> Result<Option<pot_core::ExecutionTarget>> {
    let Some(runtime) = args.get("runtime_id") else {
        return Ok(None);
    };
    let runtime = runtime
        .as_str()
        .ok_or_else(|| anyhow!("Invalid Runtime identity"))?;
    if runtime == s.runtime_id {
        return Ok(None);
    }
    anyhow::ensure!(!s.is_worker, "Workers cannot manage Sandboxes");
    anyhow::ensure!(
        matches!(
            args["action"].as_str(),
            Some("start" | "stop" | "recover" | "update" | "delete")
        ),
        "Remote management supports existing Sandbox lifecycle only; create/select are local and desktop needs separate authority"
    );
    let id = args["sandbox_id"]
        .as_str()
        .ok_or_else(|| anyhow!("Explicit remote Sandbox identity required"))?;
    let target = pot_core::ExecutionTarget::sandbox(runtime, id);
    target.validate()?;
    remote_execution::port(s)?.validate_management(&target)?;
    Ok(Some(target))
}
pub async fn management(s: &Service, args: &Value) -> Result<Value> {
    anyhow::ensure!(!s.is_worker, "Workers cannot manage Sandboxes");
    anyhow::ensure!(
        remote_management_target(s, args)?.is_none(),
        "Remote management requires a tool invocation identity"
    );
    if args["action"] == "delete" {
        let id = s
            .execution_service
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
            .execution_service
            .sandboxes
            .assignment(args["sandbox_id"].as_str().unwrap_or(""))?;
        if let Some(hub) = &s.execution_service.sandboxes.resources
            && let Some(key) = hub
                .inspect(&s.execution_service.sandboxes, &id, &s.usage_scope, None)
                .await?
        {
            return Ok(
                json!({"sandbox_id":id,"resource_request_id":key,"phase":"awaiting-resources"}),
            );
        }
    }
    let mut result = s
        .execution_service
        .sandboxes
        .sandboxes
        .management(
            args,
            s.is_worker,
            &|| agent::cancelled(s) || s.stopping(),
            &|id, delete| tasks::guard_sandbox_delete(s, id, delete),
        )
        .await?;
    if action == "create" {
        let id = result["sandbox_id"].as_str().unwrap_or("").to_owned();
        if let Some(hub) = &s.execution_service.sandboxes.resources
            && let Some(key) = hub
                .inspect(&s.execution_service.sandboxes, &id, &s.usage_scope, None)
                .await?
        {
            result["resource_request_id"] = json!(key);
            result["phase"] = json!("awaiting-resources");
        }
    }
    if action == "delete"
        && let Some(hub) = &s.execution_service.sandboxes.resources
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
        let inner = s.conversation.inner.lock().unwrap();
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
        !turn.starts_with(pot_assistant::orchestration::RESULT_PREFIX),
        "Summary is read-only"
    );
    if name == "list_execution_targets" {
        anyhow::ensure!(!s.is_worker, "Workers cannot list Sandbox inventory");
        return Ok(crate::execution_target::inventory(s).await);
    }
    if s.is_worker {
        let reference = s
            .execution_target
            .as_ref()
            .ok_or_else(|| anyhow!("Worker Execution target binding missing"))?;
        anyhow::ensure!(
            reference.is_local_to(&s.runtime_id)
                && matches!(&reference.target, pot_core::ExecutionTargetKind::Sandbox { sandbox_id } if Some(sandbox_id.as_str()) == s.sandbox_id.as_deref()),
            "Workers cannot switch Execution target targets"
        );
    }
    let thread = agent::thread(s)?;
    let settings = &thread.config.params.agent;
    let plugin = settings.plugin_for_tool(name)?;
    anyhow::ensure!(
        plugin.is_some() || ["exec_command", "write_stdin", "apply_patch"].contains(&name),
        "Unknown execution operation"
    );
    anyhow::ensure!(
        (plugin.is_some() || settings.builtin_tools.iter().any(|n| n == name))
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
        pot_core::execution_tools::validate(name, &validated)?;
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
        let id = s.execution_service.sandboxes.assignment(if s.is_worker {
            s.sandbox_id.as_deref().unwrap_or("")
        } else {
            requested
        })?;
        s.execution_service
            .sandboxes
            .prepare(&id, &s.usage_scope, Some(&turn), &|| {
                !turn_alive(s, &turn) || s.stopping()
            })
            .await?;
    }
    let (name, args) = if let Some(plugin) = plugin {
        let descriptor =
            settings.model_descriptor(&thread.config.provider, &thread.config.model)?;
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
    let grant = pot_sandbox::AccessGrant {
        workspace_owner: None,
        owner: s.usage_scope.clone(),
        turn: turn.clone(),
        thread_generation: thread.generation,
        target: s.sandbox_id.clone().unwrap_or_default(),
        worker: s.is_worker,
        policy: thread.config.params.agent.shell.clone(),
    };
    s.execution_service
        .sandboxes
        .sandboxes
        .execute(&grant, name, call_id, args, &|| {
            !turn_alive(s, &turn) || s.stopping()
        })
        .await
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
    let inner = s.conversation.inner.lock().unwrap();
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
        worker.execution_target = Some(pot_core::ExecutionTarget::sandbox(
            &worker.runtime_id,
            "default",
        ));
        active(&worker);
        assert!(
            management(&worker, &json!({"action":"create","sandbox_id":"escape"}))
                .await
                .is_err()
        );
        assert!(
            tool(&worker, "list_execution_targets", "list", &json!({}))
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
    async fn settings_validate_before_io_and_refuse_busy_execution_target() {
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
        let lease = s
            .execution_service
            .sandboxes
            .acquire("default")
            .await
            .unwrap();
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
