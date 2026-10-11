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
pub struct RunServices {
    pub model: Arc<dyn ExecutionServices>,
    pub control: Arc<dyn ExecutionFeedback>,
    pub tools: Arc<dyn ToolExecutor>,
}

struct Feedback {
    host: crate::host::Host,
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
    fn execution_home(&self) -> Option<PathBuf> {
        self.control.execution_home()
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
            if let Some(environment) = args.get("environment") {
                let requested = if environment.is_object() {
                    serde_json::from_value::<EnvironmentRef>(environment.clone())?
                } else {
                    let target = match environment.as_str() {
                        Some("host") => pot_core::EnvironmentTarget::Host {},
                        Some(value) if value.starts_with("sandbox:") => {
                            pot_core::EnvironmentTarget::Sandbox {
                                sandbox_id: value[8..].into(),
                            }
                        }
                        _ => return Err(anyhow!("Invalid Task Agent environment selector")),
                    };
                    EnvironmentRef {
                        runtime_id: run.launch.environment.runtime_id.clone(),
                        target,
                    }
                };
                ensure!(
                    requested == run.launch.environment,
                    "Worker cannot switch its assigned Environment"
                );
            }
            args["environment"] = serde_json::to_value(&run.launch.environment)?;
            let prepared = self
                .control
                .prepare_execution_operation(&name, args)
                .await?;
            ensure!(
                prepared["environment"] == serde_json::to_value(&run.launch.environment)?
                    || prepared["environment"]
                        == format!("remote:{}", serde_json::to_string(&run.launch.environment)?),
                "Control preparation changed the fixed Task Agent environment"
            );
            Ok(prepared)
        })
    }
    fn environment_grants(&self, environment: &str) -> BoxFuture<'_, Result<Option<Value>>> {
        self.control.environment_grants(environment)
    }
    fn permission_grants(&self) -> Result<Option<Value>> {
        self.run()?;
        let grants = self.control.permission_grants()?;
        ensure!(grants.is_some(), "Control approval policy is unavailable");
        Ok(grants)
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

impl crate::host::Host {
    /// Accepting a launch and claiming execution are separate durable steps.
    /// Duplicate delivery reads the journal rather than running the Agent again.
    pub async fn execute_task_agent(
        &self,
        journal: Arc<Journal>,
        control_id: &str,
        task_id: &str,
        run_id: &str,
        services: RunServices,
    ) -> Result<Option<Terminal>> {
        let accepted = journal
            .inspect(control_id, task_id, run_id)?
            .ok_or_else(|| anyhow!("Task Agent launch was not accepted"))?;
        accepted.launch.validate(self.runtime_id())?;
        let workspace_scope = accepted.launch.workspace_scope(control_id)?;
        ensure!(
            journal.runtime_id == self.runtime_id() && journal.boot_id == self.boot,
            "Task Agent journal belongs to another Runtime or boot"
        );
        if !journal.claim(control_id, task_id, run_id)? {
            return Ok(journal
                .inspect(control_id, task_id, run_id)?
                .and_then(|run| run.result));
        }
        let feedback = Arc::new(Feedback {
            host: self.clone(),
            journal: journal.clone(),
            control_id: control_id.into(),
            task_id: task_id.into(),
            run_id: run_id.into(),
            control: services.control,
        });
        let mut text = String::new();
        let mut gate = pot_core::EventGate::new(accepted.launch.generation, run_id);
        let execution = async {
            feedback.ready().await?;
            let context = self.install_controlled_resources(&accepted.launch.environment.target, &workspace_scope, accepted.launch.resources.clone()).await?;
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
                backend_url: String::new(),
                lane_token: String::new(),
                main_prompt: format!("For actual file deliverables, include Markdown links [filename](pot-file://{task_id}/RELATIVE_PATH), percent-encoding path characters and preserving slashes. Ordinary task results need no file link."),
                feedback: feedback.clone(),
                tool_executor: Some(services.tools),
            };
            pot_agent::Agent::with_extensions(Arc::new(Resources { skills })).services(services.model).execute(
                &context,
                &Thread { owner: task_id.into(), generation: accepted.launch.generation, config: accepted.launch.config },
                &TurnRequest { request_id: run_id.into(), text: accepted.launch.text },
                false,
                &mut |event| {
                    if gate.accept(&event) && let Update::MessageFinished { text: message, .. } = event.update {
                        if !text.is_empty() { text.push('\n'); }
                        text.push_str(&message);
                    }
                    Ok(())
                },
            ).await
        }.await;
        let outcome = match execution {
            Ok(()) => gate.finish(),
            Err(error) => Outcome::Failed(error.to_string()),
        };
        feedback.control.settle().await?;
        let terminal = Terminal { outcome, text };
        // Unknown outcomes deliberately remain unsettled for reconciliation.
        journal.finish(control_id, task_id, run_id, terminal.clone())?;
        Ok(Some(terminal))
    }
}
