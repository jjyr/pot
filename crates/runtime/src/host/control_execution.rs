//! Execution after peer authority has been checked by the owning server.
use super::*;
use futures::future::BoxFuture;
use pot_core::{ExecutionContext, ExecutionFeedback, ExecutionRole, ToolExecutor};

pub struct ControlOperation {
    pub scope: String,
    pub workspace_scope: String,
    pub run_id: String,
    pub generation: u64,
    pub target: pot_core::EnvironmentRef,
    pub call_id: String,
    pub name: String,
    pub args: Value,
    pub resources_revision: Option<String>,
}
struct Feedback {
    host: Host,
    cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
}
impl ExecutionFeedback for Feedback {
    fn cancelled(&self) -> bool {
        *self.host.stop.borrow() || (self.cancelled)()
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(async move {
            while !self.cancelled() {
                tokio::time::sleep(std::time::Duration::from_millis(50)).await;
            }
        })
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        bail!("Peer execution has no local conversation")
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        bail!("Peer execution has no local model input")
    }
}
impl Host {
    pub async fn manage_controlled_sandbox(
        &self,
        args: Value,
        cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
        hub: Option<Arc<crate::assistants::Hub>>,
    ) -> Result<Value> {
        anyhow::ensure!(
            matches!(
                args["action"].as_str(),
                Some("start" | "stop" | "recover" | "update" | "delete")
            ),
            "Unsupported remote Sandbox management action"
        );
        anyhow::ensure!(
            !cancelled() && !*self.stop.borrow(),
            "Management authority expired"
        );
        let manager = self
            .sandboxes
            .as_ref()
            .ok_or_else(|| anyhow!("Sandbox service unavailable"))?;
        let id = args["sandbox_id"]
            .as_str()
            .ok_or_else(|| anyhow!("Sandbox identity required"))?;
        manager.assignment(id)?;
        if args["action"] == "start"
            && let Some(resources) = &manager.resources
            && let Some(key) = resources.inspect(manager, id, "operator", None).await?
        {
            return Ok(
                json!({"sandbox_id":id,"resource_request_id":key,"phase":"awaiting-resources"}),
            );
        }
        let result = manager
            .management(
                &args,
                false,
                &|| cancelled() || *self.stop.borrow(),
                &|id, delete| {
                    anyhow::ensure!(!cancelled(), "Management authority expired");
                    if let Some(hub) = &hub {
                        hub.guard_sandbox_delete(id, delete)
                    } else {
                        delete()
                    }
                },
            )
            .await?;
        if args["action"] == "delete"
            && let Some(resources) = &manager.resources
        {
            resources.forget(id)?;
        }
        Ok(result)
    }
    /// This is an internal authority port, never a native-token substitute on a network route.
    pub async fn execute_controlled(
        &self,
        request: ControlOperation,
        cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
    ) -> Result<Value> {
        request.target.validate()?;
        anyhow::ensure!(
            request.target.is_local_to(&self.runtime_id),
            "Peer target belongs to another Runtime"
        );
        anyhow::ensure!(
            !request.scope.is_empty()
                && request
                    .scope
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_'),
            "Invalid execution scope"
        );
        anyhow::ensure!(
            !request.workspace_scope.is_empty()
                && request
                    .workspace_scope
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
                && request.generation > 0,
            "Unsupported peer operation"
        );
        let (operation, execution_args, policy) = self.controlled_tool(
            &request.target.target,
            &request.workspace_scope,
            &request.name,
            request.args.clone(),
            request.resources_revision.as_deref(),
        )?;
        let feedback = Arc::new(Feedback {
            host: self.clone(),
            cancelled: cancelled.clone(),
        });
        anyhow::ensure!(!feedback.cancelled(), "Peer execution authority expired");
        match &request.target.target {
            pot_core::EnvironmentTarget::Host {} => {
                anyhow::ensure!(self.host_enabled, "Host resource is disabled");
                let environment = self
                    .host_environment
                    .as_ref()
                    .ok_or_else(|| anyhow!("Host service unavailable"))?;
                let workspace = self
                    .data
                    .join("peer-workspaces")
                    .join(&request.workspace_scope);
                std::fs::create_dir_all(&workspace)?;
                let mut args = execution_args;
                if operation != "plugin_call" {
                    pot_tool_executor::tools::normalize(
                        &operation, &mut args, &workspace, &policy,
                    )?;
                }
                let executor = environment.host_executor(&request.scope, request.generation);
                let context = ExecutionContext {
                    instructions: String::new(),
                    skill_resources: Default::default(),
                    data: workspace.clone(),
                    workspace,
                    owner: request.scope.clone(),
                    role: ExecutionRole::Worker,
                    backend_url: String::new(),
                    lane_token: String::new(),
                    main_prompt: String::new(),
                    feedback: feedback.clone(),
                    tool_executor: None,
                };
                let mut emit = |_| Ok(());
                tokio::select! {
                    result=executor.execute(&context,&policy,&operation,&args,&request.call_id,&mut emit)=>result.map(|value|json!({"result":value})),
                    _=feedback.cancellation()=>{environment.finish_host(&request.scope).await?;bail!("Peer lease expired or was revoked")}
                }
            }
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                let manager = self
                    .sandboxes
                    .as_ref()
                    .ok_or_else(|| anyhow!("Sandbox service unavailable"))?;
                let _lease = manager.acquire(sandbox_id).await?;
                if request.name != "write_stdin" {
                    manager
                        .prepare(sandbox_id, &request.scope, Some(&request.run_id), &|| {
                            feedback.cancelled()
                        })
                        .await?;
                }
                let grant = pot_sandbox::AccessGrant {
                    workspace_owner: Some(request.workspace_scope.clone()),
                    owner: request.scope,
                    turn: request.run_id,
                    thread_generation: request.generation,
                    target: sandbox_id.clone(),
                    worker: true,
                    policy,
                };
                let mut args = execution_args;
                args["sandbox_id"] = json!(sandbox_id);
                manager
                    .execute(
                        &grant,
                        &operation,
                        &request.call_id,
                        &args,
                        &|| feedback.cancelled(),
                        None,
                    )
                    .await
            }
        }
    }
    pub async fn cancel_controlled(
        &self,
        scope: &str,
        target: &pot_core::EnvironmentTarget,
    ) -> Result<()> {
        match target {
            pot_core::EnvironmentTarget::Host {} => {
                if let Some(environment) = &self.host_environment {
                    environment.finish_host(scope).await?;
                }
            }
            pot_core::EnvironmentTarget::Sandbox { .. } => {
                if let Some(manager) = &self.sandboxes {
                    manager.finish(scope).await?;
                    manager.drain(scope).await?;
                }
            }
        }
        Ok(())
    }
    pub async fn environment_inventory(&self) -> Value {
        let mut items = vec![];
        if self.host_enabled && self.host_environment.is_some() {
            items.push(json!({"environment_ref":pot_core::EnvironmentRef::host(&self.runtime_id),"kind":"host","management":false,
                "workspace":self.data.join("peer-workspaces"),"supported_operations":["exec_command","write_stdin","apply_patch"]}));
        }
        if let Some(manager) = &self.sandboxes {
            let inventory = manager.inventory().await;
            for mut item in inventory["items"].as_array().cloned().unwrap_or_default() {
                let target = item["id"].as_str().unwrap_or("");
                item["environment_ref"] =
                    json!(pot_core::EnvironmentRef::sandbox(&self.runtime_id, target));
                items.push(item);
            }
        }
        json!({"runtime_id":self.runtime_id,"items":items})
    }
}

impl Host {
    fn controlled_tool(
        &self,
        target: &pot_core::EnvironmentTarget,
        workspace_scope: &str,
        name: &str,
        args: Value,
        expected_revision: Option<&str>,
    ) -> Result<(String, Value, pot_core::ShellSettings)> {
        anyhow::ensure!(
            !workspace_scope.is_empty()
                && workspace_scope
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_'),
            "Invalid workspace scope"
        );
        anyhow::ensure!(
            name != "plugin_call",
            "Internal plugin envelopes cannot be called directly"
        );
        let workspace = match target {
            pot_core::EnvironmentTarget::Host {} => {
                self.data.join("peer-workspaces").join(workspace_scope)
            }
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                let manager = self
                    .sandboxes
                    .as_ref()
                    .ok_or_else(|| anyhow!("Sandbox service unavailable"))?;
                manager.assignment(sandbox_id)?;
                manager
                    .workspace(sandbox_id)
                    .join("tasks")
                    .join(workspace_scope)
            }
        };
        let supplied = if let Some(expected) = expected_revision {
            let supplied = crate::control_resources::installed_snapshot(&workspace)?
                .ok_or_else(|| anyhow!("Control resource snapshot missing"))?;
            anyhow::ensure!(
                supplied.0 == expected,
                "Control resource snapshot binding changed"
            );
            Some(supplied)
        } else {
            // Core-only protocol calls use the protocol defaults, never node settings/cache.
            None
        };
        let policy = supplied
            .as_ref()
            .map(|(_, s)| s.shell.clone())
            .unwrap_or_default();
        if let Some((_, resources)) = supplied {
            let settings = pot_core::AgentSettings {
                plugins: resources.plugins,
                ..Default::default()
            };
            if let Some(plugin) = settings.plugin_for_tool(name)? {
                anyhow::ensure!(
                    !matches!(target, pot_core::EnvironmentTarget::Host {})
                        || plugin.manifest.execution == "environment",
                    "This plugin supports Sandbox execution only"
                );
                if matches!(target, pot_core::EnvironmentTarget::Host {}) {
                    anyhow::ensure!(
                        plugin
                            .manifest
                            .requirements
                            .iter()
                            .all(|r| matches!(r.as_str(), "node" | "chromium")),
                        "Unsupported Host plugin dependency"
                    );
                }
                return Ok((
                    "plugin_call".into(),
                    pot_core::plugins::call(plugin, name, args, resources.vision)?,
                    policy,
                ));
            }
        }
        pot_core::environment_tools::validate(name, &args)?;
        Ok((name.into(), args, policy))
    }
    pub async fn install_controlled_resources(
        &self,
        target: &pot_core::EnvironmentTarget,
        workspace_scope: &str,
        resources: crate::control_resources::GlobalResources,
    ) -> Result<Value> {
        let context = self.controlled_context(target, workspace_scope).await?;
        let workspace = match target {
            pot_core::EnvironmentTarget::Host {} => {
                self.data.join("peer-workspaces").join(workspace_scope)
            }
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => self
                .sandboxes
                .as_ref()
                .ok_or_else(|| anyhow!("Sandbox service unavailable"))?
                .workspace(sandbox_id)
                .join("tasks")
                .join(workspace_scope),
        };
        let revision = tokio::task::spawn_blocking(move || {
            crate::control_resources::install(&workspace, &resources)
        })
        .await??;
        let mut updated = self.controlled_context(target, workspace_scope).await?;
        anyhow::ensure!(
            updated["workspace"] == context["workspace"],
            "Execution workspace changed"
        );
        updated["global_resources_revision"] = json!(revision);
        Ok(updated)
    }
    pub async fn controlled_context(
        &self,
        target: &pot_core::EnvironmentTarget,
        workspace_scope: &str,
    ) -> Result<Value> {
        anyhow::ensure!(
            !workspace_scope.is_empty()
                && workspace_scope
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_'),
            "Invalid workspace scope"
        );
        let (source, mount, cwd) = match target {
            pot_core::EnvironmentTarget::Host {} => {
                anyhow::ensure!(self.host_enabled, "Host resource is disabled");
                let workspace = self.data.join("peer-workspaces").join(workspace_scope);
                std::fs::create_dir_all(&workspace)?;
                let root = workspace
                    .ancestors()
                    .find(|p| p.join(".git").exists())
                    .unwrap_or(&workspace)
                    .to_path_buf();
                (root.clone(), root, workspace)
            }
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                let manager = self
                    .sandboxes
                    .as_ref()
                    .ok_or_else(|| anyhow!("Sandbox service unavailable"))?;
                manager.assignment(sandbox_id)?;
                let source = manager.workspace(sandbox_id);
                std::fs::create_dir_all(source.join("tasks").join(workspace_scope))?;
                let mount = PathBuf::from(manager.guest_workspace(sandbox_id)?);
                let cwd = mount.join("tasks").join(workspace_scope);
                (source, mount, cwd)
            }
        };
        let skills = crate::skills::destination_catalog(
            &self.data,
            &source,
            &mount,
            &cwd,
            matches!(target, pot_core::EnvironmentTarget::Host {}),
        )?;
        let catalog = crate::instructions::project_workspace(source, mount, cwd.clone())?;
        Ok(
            json!({"workspace":cwd,"instructions":catalog.text,"diagnostics":catalog.diagnostics,
            "skills":skills.skills,"skill_diagnostics":skills.diagnostics,
            "supported_operations":["exec_command","write_stdin","apply_patch"]}),
        )
    }
    pub async fn prepare_controlled(
        &self,
        request: ControlOperation,
        cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
    ) -> Result<Value> {
        let ControlOperation {
            target,
            scope,
            workspace_scope,
            run_id: run,
            name,
            args,
            resources_revision,
            ..
        } = request;
        anyhow::ensure!(
            target.is_local_to(&self.runtime_id),
            "Peer target belongs to another Runtime"
        );
        anyhow::ensure!(!cancelled(), "Peer lease expired or revoked");
        let (operation, envelope, policy) = self.controlled_tool(
            &target.target,
            &workspace_scope,
            &name,
            args.clone(),
            resources_revision.as_deref(),
        )?;
        let context = self
            .controlled_context(&target.target, &workspace_scope)
            .await?;
        let mut args = args;
        match &target.target {
            pot_core::EnvironmentTarget::Host {} if operation != "plugin_call" => {
                pot_tool_executor::tools::normalize(
                    &name,
                    &mut args,
                    &PathBuf::from(context["workspace"].as_str().unwrap()),
                    &policy,
                )?
            }
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                let manager = self
                    .sandboxes
                    .as_ref()
                    .ok_or_else(|| anyhow!("Sandbox service unavailable"))?;
                if name != "write_stdin" {
                    manager
                        .prepare(sandbox_id, &scope, Some(&run), &|| cancelled())
                        .await?;
                    manager
                        .ready_for_execution(sandbox_id, &|| cancelled())
                        .await?;
                    let prepared=manager.permissions(sandbox_id,&json!({"action":"prepare","cwd":context["workspace"],"role":"task","tool":operation,"args":if operation == "plugin_call" { &envelope } else { &args },"policy":policy})).await?;
                    if operation != "plugin_call" {
                        args = prepared["args"].clone();
                    }
                }
            }
            pot_core::EnvironmentTarget::Host {} => {}
        }
        anyhow::ensure!(!cancelled(), "Peer lease expired or revoked");
        Ok(args)
    }
}

impl Host {
    pub async fn controlled_file(
        &self,
        target: &pot_core::EnvironmentTarget,
        workspace_scope: &str,
        query: pot_protocol::FileQuery,
    ) -> Result<pot_protocol::FileChunk> {
        anyhow::ensure!(
            !workspace_scope.is_empty()
                && workspace_scope
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_'),
            "Invalid workspace scope"
        );
        let workspace = match target {
            pot_core::EnvironmentTarget::Host {} => {
                anyhow::ensure!(self.host_enabled, "Host resource is disabled");
                self.data.join("peer-workspaces").join(workspace_scope)
            }
            pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                let manager = self
                    .sandboxes
                    .as_ref()
                    .ok_or_else(|| anyhow!("Sandbox service unavailable"))?;
                manager.assignment(sandbox_id)?;
                manager
                    .workspace(sandbox_id)
                    .join("tasks")
                    .join(workspace_scope)
            }
        };
        tokio::task::spawn_blocking(move || crate::files::read_chunk(&workspace, &query)).await?
    }
}
