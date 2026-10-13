//! A fresh, destination-local loop with explicitly supplied Control services.
use super::*;
use futures::future::BoxFuture;
use pot_core::{
    ExecutionContext, ExecutionFeedback, ExecutionRole, ExecutionServices, SkillResources, Thread,
    ToolExecutor, TurnRequest, Update,
};
use std::sync::Arc;

/// The server supplies authenticated, run-bound Control ports and receipt-backed
/// destination execution. There is deliberately no local model or approval fallback.
/// Execution target capabilities supplied by Runtime assembly. No assistant or database access.
pub trait Destination: Send + Sync {
    fn executions(&self) -> &pot_runtime_execution::Manager;
    fn runtime_id(&self) -> &str;
    fn boot_id(&self) -> &str;
    fn stopping(&self) -> bool;
    fn install_resources<'a>(
        &'a self,
        target: &'a pot_core::ExecutionTargetKind,
        workspace: &'a str,
        resources: crate::resources::GlobalResources,
    ) -> BoxFuture<'a, Result<Value>>;
}

pub struct RunServices {
    pub model: Arc<dyn ExecutionServices>,
    pub control: Arc<dyn ExecutionFeedback>,
    pub tools: Arc<dyn ToolExecutor>,
}

struct Feedback {
    host: Arc<dyn Destination>,
    journal: Arc<Journal>,
    control_id: String,
    task_id: String,
    run_id: String,
    control: Arc<dyn ExecutionFeedback>,
}
impl Feedback {
    fn run(&self) -> Result<Run> {
        ensure!(!self.host.stopping(), "Execution Runtime is stopping");
        let run = self
            .journal
            .inspect(&self.control_id, &self.task_id, &self.run_id)?
            .ok_or_else(|| anyhow!("Task Agent record missing"))?;
        ensure!(
            run.phase == Phase::Running && run.boot_id == self.journal.boot_id,
            "Task Agent run is fenced"
        );
        Ok(run)
    }
}
impl ExecutionFeedback for Feedback {
    fn settle(&self) -> BoxFuture<'_, Result<()>> {
        self.control.settle()
    }
    fn execution_home(&self) -> Option<PathBuf> {
        self.control.execution_home()
    }
    fn resolve_execution_operation(
        &self,
        name: &str,
        mut args: Value,
    ) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            let run = self.run()?;
            if let Some(value) = args.get("execution_target") {
                let target = if value.is_object() {
                    serde_json::from_value::<ExecutionTarget>(value.clone())?
                } else if value == "host" {
                    ExecutionTarget::host(&run.launch.execution_target.runtime_id)
                } else if let Some(id) = value.as_str().and_then(|v| v.strip_prefix("sandbox:")) {
                    ExecutionTarget::sandbox(&run.launch.execution_target.runtime_id, id)
                } else {
                    anyhow::bail!("Invalid Task Agent target")
                };
                ensure!(
                    target == run.launch.execution_target,
                    "Worker cannot change its fixed target"
                );
            }
            ensure!(
                args.get("sandbox_id").is_none(),
                "Worker cannot change its sandbox"
            );
            args["execution_target"] = serde_json::to_value(&run.launch.execution_target)?;
            self.control.resolve_execution_operation(&name, args).await
        })
    }
    fn prepare_execution_operation(
        &self,
        name: &str,
        mut args: Value,
    ) -> BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            let run = self.run()?;
            ensure!(args.is_object(), "Tool arguments must be an object");
            // This check is independent of any owner approval.
            if let Some(execution_target) = args.get("execution_target") {
                let requested = if execution_target.is_object() {
                    serde_json::from_value::<ExecutionTarget>(execution_target.clone())?
                } else if let Some(raw) = execution_target
                    .as_str()
                    .and_then(|v| v.strip_prefix("remote:"))
                {
                    serde_json::from_str::<ExecutionTarget>(raw)?
                } else {
                    let target = match execution_target.as_str() {
                        Some("host") => pot_core::ExecutionTargetKind::Host {},
                        Some(value) if value.starts_with("sandbox:") => {
                            pot_core::ExecutionTargetKind::Sandbox {
                                sandbox_id: value[8..].into(),
                            }
                        }
                        _ => return Err(anyhow!("Invalid Task Agent execution target selector")),
                    };
                    ExecutionTarget {
                        runtime_id: run.launch.execution_target.runtime_id.clone(),
                        target,
                    }
                };
                ensure!(
                    requested == run.launch.execution_target,
                    "Worker cannot switch its assigned Execution target"
                );
            }
            args["execution_target"] = serde_json::to_value(&run.launch.execution_target)?;
            let prepared = self
                .control
                .prepare_execution_operation(&name, args)
                .await?;
            ensure!(
                prepared["execution_target"] == serde_json::to_value(&run.launch.execution_target)?
                    || prepared["execution_target"]
                        == format!(
                            "remote:{}",
                            serde_json::to_string(&run.launch.execution_target)?
                        ),
                "Control preparation changed the fixed Task Agent execution target"
            );
            Ok(prepared)
        })
    }

    fn owner_authorization_available(&self) -> Result<bool> {
        self.run()?;
        let available = self.control.owner_authorization_available()?;
        ensure!(available, "Control authorization is unavailable");
        Ok(true)
    }
    fn request_permission(&self, request: Value) -> BoxFuture<'_, Result<Value>> {
        self.control.request_permission(request)
    }
    fn observe(&self, event: &pot_core::ExecutionObservation) -> Result<()> {
        self.run()?;
        self.control.observe(event)
    }
    fn steering(&self, finalizing: bool) -> Result<Vec<(String, String)>> {
        self.run()?;
        self.control.steering(finalizing)
    }
    fn acknowledge_steering(&self, ids: &[String]) -> Result<()> {
        self.run()?;
        self.control.acknowledge_steering(ids)
    }
    fn fetch_steering(&self, finalizing: bool) -> BoxFuture<'_, Result<Vec<(String, String)>>> {
        Box::pin(async move {
            self.run()?;
            self.control.fetch_steering(finalizing).await
        })
    }
    fn commit_steering<'a>(&'a self, ids: &'a [String]) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            self.run()?;
            self.control.commit_steering(ids).await
        })
    }
    fn diagnostic(&self, event: &Value) -> Result<()> {
        self.run()?;
        self.control.diagnostic(event)
    }
    fn ready(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async {
            self.run()?;
            self.control.ready().await
        })
    }
    fn load_context(&self) -> Result<Option<Value>> {
        Ok(self.run()?.context)
    }
    fn save_context(&self, context: &Value) -> Result<()> {
        self.journal.save_context(
            &self.control_id,
            &self.task_id,
            &self.run_id,
            context.clone(),
        )
    }
    fn cancelled(&self) -> bool {
        self.run().is_err() || self.control.cancelled()
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(async move {
            loop {
                if self.cancelled() {
                    return;
                }
                tokio::select! {
                    _ = self.control.cancellation() => return,
                    _ = tokio::time::sleep(std::time::Duration::from_millis(100)) => {},
                }
            }
        })
    }
    fn record_turn(&self, request: &str) -> Result<()> {
        self.run()?;
        ensure!(request == self.run_id, "Task Agent turn identity mismatch");
        self.control.record_turn(request)
    }
    fn input_streaming(&self, request: &str) -> Result<()> {
        self.run()?;
        ensure!(request == self.run_id, "Task Agent turn identity mismatch");
        self.control.input_streaming(request)
    }
}

struct Resources {
    skills: Vec<pot_agent::Skill>,
}
impl pot_agent::AgentExtensions for Resources {
    fn skills(&self, _: &ExecutionContext) -> Result<Vec<pot_agent::Skill>> {
        Ok(self.skills.clone())
    }
}

/// Accepting a launch and claiming execution are separate durable steps.
/// Duplicate delivery reads the journal rather than running the Agent again.
pub async fn execute(
    host: Arc<dyn Destination>,
    journal: Arc<Journal>,
    control_id: &str,
    task_id: &str,
    run_id: &str,
    services: RunServices,
) -> Result<Option<Terminal>> {
    let accepted = journal
        .inspect(control_id, task_id, run_id)?
        .ok_or_else(|| anyhow!("Task Agent launch was not accepted"))?;
    accepted.launch.validate(host.runtime_id())?;
    let workspace_scope = accepted.launch.workspace_scope(control_id)?;
    ensure!(
        journal.runtime_id == host.runtime_id() && journal.boot_id == host.boot_id(),
        "Task Agent journal belongs to another Runtime or boot"
    );
    if !journal.claim(control_id, task_id, run_id)? {
        return Ok(journal
            .inspect(control_id, task_id, run_id)?
            .and_then(|run| run.result));
    }
    let feedback = Arc::new(Feedback {
        host: host.clone(),
        journal: journal.clone(),
        control_id: control_id.into(),
        task_id: task_id.into(),
        run_id: run_id.into(),
        control: services.control,
    });
    let mut text = String::new();
    let execution = async {
            feedback.ready().await?;
            let context = host.install_resources(&accepted.launch.execution_target.target, &workspace_scope, accepted.launch.resources.clone()).await?;
            let mut skills: Vec<pot_agent::Skill> = serde_json::from_value(context["skills"].clone())?;
            if let Some(selection) = &accepted.launch.skills {
                ensure!(selection.iter().all(|name|skills.iter().any(|skill|&skill.name==name)),"Selected task skill is unavailable at the fixed destination");
                skills.retain(|s| selection.contains(&s.name));
            }
            let data = journal.data.join(Journal::key(control_id, task_id, run_id)?);
            std::fs::create_dir_all(&data)?;
            let context = ExecutionContext {
                instructions: context["instructions"].as_str().ok_or_else(|| anyhow!("Project instructions missing"))?.into(),
                skill_resources: SkillResources::default(),
                data,
                workspace: PathBuf::from(context["workspace"].as_str().ok_or_else(|| anyhow!("Task Agent workspace missing"))?),
                owner: task_id.into(),
                role: ExecutionRole::Worker,


                main_prompt: format!("For actual file deliverables, include Markdown links [filename](pot-file://{task_id}/RELATIVE_PATH), percent-encoding path characters and preserving slashes. Ordinary task results need no file link."),
                feedback: feedback.clone(),
                tool_executor: Some(services.tools),
            };
            host.executions().execute(
                pot_runtime_execution::Invocation {
                    context: &context,
                    thread: &Thread { owner: task_id.into(), generation: accepted.launch.generation, config: accepted.launch.config },
                    request: &TurnRequest { request_id: run_id.into(), text: accepted.launch.text },
                    resuming: false,
                    services: services.model,
                    extensions: Some(Arc::new(Resources { skills })),
                },
                &mut |event| {
                    if let Update::MessageFinished { text: message, .. } = event.update {
                        if !text.is_empty() { text.push('\n'); }
                        text.push_str(&message);
                    }
                    Ok(())
                },
            ).await
        }.await;
    let outcome = match execution {
        Ok(outcome) => outcome,
        Err(_) if feedback.control.cancelled() => Outcome::Cancelled,
        Err(error) => Outcome::Failed(error.to_string()),
    };
    let terminal = Terminal { outcome, text };
    // Unknown outcomes deliberately remain unsettled for reconciliation.
    journal.finish(control_id, task_id, run_id, terminal.clone())?;
    Ok(Some(terminal))
}
