use anyhow::{Result, anyhow};
use futures::{StreamExt, future::BoxFuture};
use pot_core::{
    ExecutionContext, ExecutionFeedback, ExecutionServices, ModelRequest, ShellSettings,
    ToolExecutor, Update,
};
use pot_core::{ExecutionTarget, Outcome};
use pot_runner::*;
use serde_json::Value;
use serde_json::json;
use std::sync::Arc;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};

fn id() -> String {
    uuid::Uuid::new_v4().to_string()
}
fn launch(runtime: &str) -> Launch {
    let config = pot_core::Profile::api(&pot_control::execution::Config {
        model: "fixture".into(),
        ..Default::default()
    })
    .resolve()
    .unwrap();
    let vision = config
        .params
        .agent
        .model_descriptor(&config.provider, &config.model)
        .unwrap()
        .vision();
    Launch {
        task_id: id(),
        run_id: id(),
        generation: 1,
        execution_target: ExecutionTarget::host(runtime),
        resources: pot_runner::resources::GlobalResources {
            plugins: config.params.agent.plugins.clone(),
            shell: config.params.agent.shell.clone(),
            vision,
            ..Default::default()
        },
        config,
        skills: None,
        text: "Produce a plain text result; no file needed".into(),
    }
}

#[test]
fn duplicate_delivery_claims_once_and_lost_notification_retains_result() {
    let data = std::env::temp_dir().join(format!("pot-task-agent-{}", id()));
    let runtime = id();
    let boot = id();
    let control = id();
    let journal = Arc::new(Journal::open(&data, &runtime, &boot).unwrap());
    let launch = launch(&runtime);
    let threads: Vec<_> = (0..8)
        .map(|_| {
            let journal = journal.clone();
            let launch = launch.clone();
            let control = control.clone();
            std::thread::spawn(move || {
                journal.accept(&control, launch.clone()).unwrap();
                journal
                    .claim(&control, &launch.task_id, &launch.run_id)
                    .unwrap()
            })
        })
        .collect();
    assert_eq!(
        threads
            .into_iter()
            .map(|t| usize::from(t.join().unwrap()))
            .sum::<usize>(),
        1
    );
    let context = serde_json::json!({"messages":["completed effect"]});
    journal
        .save_context(&control, &launch.task_id, &launch.run_id, context.clone())
        .unwrap();
    let result = Terminal {
        outcome: Outcome::Success,
        text: "Done without a file".into(),
    };
    journal
        .finish(&control, &launch.task_id, &launch.run_id, result.clone())
        .unwrap();
    journal
        .finish(&control, &launch.task_id, &launch.run_id, result.clone())
        .unwrap();
    assert!(
        journal
            .finish(
                &control,
                &launch.task_id,
                &launch.run_id,
                Terminal {
                    outcome: Outcome::Cancelled,
                    text: String::new()
                }
            )
            .is_err()
    );
    drop(journal);
    let reopened = Journal::open(&data, &runtime, &id()).unwrap();
    let run = reopened.accept(&control, launch.clone()).unwrap();
    assert_eq!(run.phase, Phase::Complete);
    assert_eq!(run.result, Some(result));
    assert_eq!(run.context, Some(context));
    assert!(
        !reopened
            .claim(&control, &launch.task_id, &launch.run_id)
            .unwrap()
    );
    assert!(
        reopened
            .inspect(&id(), &launch.task_id, &launch.run_id)
            .unwrap()
            .is_none()
    );
    std::fs::remove_dir_all(data).unwrap();
}

#[test]
fn restart_does_not_relaunch_or_accept_replacement_of_unsettled_run() {
    let data = std::env::temp_dir().join(format!("pot-task-agent-{}", id()));
    let runtime = id();
    let boot = id();
    let control = id();
    let launch = launch(&runtime);
    {
        let journal = Journal::open(&data, &runtime, &boot).unwrap();
        journal.accept(&control, launch.clone()).unwrap();
        assert!(
            journal
                .claim(&control, &launch.task_id, &launch.run_id)
                .unwrap()
        );
        assert!(
            journal
                .finish(
                    &control,
                    &launch.task_id,
                    &launch.run_id,
                    Terminal {
                        outcome: Outcome::Unknown,
                        text: String::new()
                    }
                )
                .is_err()
        );
    }
    let journal = Journal::open(&data, &runtime, &id()).unwrap();
    assert_eq!(
        journal.accept(&control, launch.clone()).unwrap().phase,
        Phase::RecoveryRequired
    );
    assert!(
        !journal
            .claim(&control, &launch.task_id, &launch.run_id)
            .unwrap()
    );
    assert!(
        journal
            .save_context(&control, &launch.task_id, &launch.run_id, Value::Null)
            .is_err()
    );
    let mut replacement = launch.clone();
    replacement.run_id = id();
    assert!(journal.accept(&control, replacement).is_err());
    let mut conflicting = launch.clone();
    conflicting.text = "different task".into();
    assert!(journal.accept(&control, conflicting).is_err());
    std::fs::remove_dir_all(data).unwrap();
}

#[test]
fn invalid_configuration_is_rejected_before_acceptance() {
    let runtime = id();
    let mut request = launch(&runtime);
    assert!(request.validate(&runtime).is_ok());
    assert!(request.validate(&id()).is_err());
    request.resources.shell.allow_login_shell = !request.resources.shell.allow_login_shell;
    assert!(request.validate(&runtime).is_err());
    request = launch(&runtime);
    request.config.model = "substitute".into();
    assert!(request.validate(&runtime).is_err());
    request = launch(&runtime);
    request.text = "x".repeat(256 * 1024 + 1);
    assert!(request.validate(&runtime).is_err());
}

#[derive(Default)]
struct Control {
    requests: Mutex<Vec<Value>>,
    ready: AtomicBool,
    cancelled: AtomicBool,
    turns: AtomicUsize,
    switch_execution_target: AtomicBool,
}
impl ExecutionFeedback for Control {
    fn owner_authorization_available(&self) -> Result<bool> {
        Ok(true)
    }
    fn request_permission(&self, request: Value) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move { Ok(json!({"request":request,"approved":true})) })
    }
    fn ready(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async {
            anyhow::ensure!(!self.cancelled(), "Task Agent cancelled");
            while !self.ready.load(Ordering::SeqCst) {
                tokio::time::sleep(std::time::Duration::from_millis(5)).await;
            }
            Ok(())
        })
    }
    fn cancelled(&self) -> bool {
        self.cancelled.load(Ordering::SeqCst)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(std::future::pending())
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        self.turns.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        Ok(())
    }
}
impl ExecutionServices for Control {
    fn model(
        &self,
        request: ModelRequest,
    ) -> BoxFuture<'_, Result<futures::stream::BoxStream<'_, Result<Vec<u8>>>>> {
        Box::pin(async move {
            assert_eq!(request.model, "fixture");
            self.requests.lock().unwrap().push(request.body);
            let event = if self.switch_execution_target.swap(false, Ordering::SeqCst) {
                json!({"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"switch","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"touch forbidden\",\"execution_target\":\"sandbox:other\"}"}}]},"finish_reason":"tool_calls"}]})
            } else {
                json!({"choices":[{"index":0,"delta":{"content":"Task result without a download"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}})
            };
            Ok(futures::stream::iter([Ok(
                format!("data: {event}\n\ndata: [DONE]\n\n").into_bytes()
            )])
            .boxed())
        })
    }
    fn rpc<'a>(
        &'a self,
        _: Option<&'a str>,
        _: &'a str,
        _: &'a str,
        _: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        Box::pin(async { Err(anyhow!("Worker must not request an assistant MCP session")) })
    }
    fn compaction(&self, _: Value) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }
}
struct NoTools;
impl ToolExecutor for NoTools {
    fn execute<'a>(
        &'a self,
        _: &'a ExecutionContext,
        _: &'a ShellSettings,
        _: &'a str,
        _: &'a Value,
        _: &'a str,
        _: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async { Err(anyhow!("No tool call expected")) })
    }
    fn finish<'a>(&'a self, _: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { Ok(()) })
    }
}
fn services(control: &Arc<Control>) -> RunServices {
    RunServices {
        model: control.clone(),
        control: control.clone(),
        tools: Arc::new(NoTools),
    }
}

#[tokio::test]
async fn neutral_runtime_runs_fresh_worker_with_control_services_and_project_rules() {
    use base64::Engine;
    let root = std::env::temp_dir().join(format!("pot-task-agent-{}", id()));
    let data = root.join("product");
    let mut backend = crate::initialize(crate::RuntimeConfig {
        data: data.clone(),
        workspace: data.join("workspace"),
    })
    .await
    .unwrap();
    backend.enable_host_resource();
    assert!(
        backend.optional_state().is_none(),
        "Execution node needs no assistant"
    );
    let host = backend.host().clone();
    let journal = Arc::new(Journal::open(&data, host.runtime_id(), host.boot_id()).unwrap());
    let control_id = id();
    let mut launch = launch(host.runtime_id());
    let scope = launch.workspace_scope(&control_id).unwrap();
    let workspace = data.join("execution-workspaces").join(&scope);
    std::fs::create_dir_all(workspace.join(".agents/skills/project-skill")).unwrap();
    std::fs::write(workspace.join("AGENTS.md"), "ACTUAL_PROJECT_RULE").unwrap();
    std::fs::write(
        workspace.join(".agents/skills/project-skill/SKILL.md"),
        "---\nname: project-skill\ndescription: Actual project skill\n---\nProject instructions",
    )
    .unwrap();
    struct Paths {
        home: std::path::PathBuf,
        skill_user: std::path::PathBuf,
    }
    let node_resources = Paths {
        home: data.parent().unwrap().join("agent-home"),
        skill_user: data.parent().unwrap().join("skills-user"),
    };
    std::fs::create_dir_all(&node_resources.home).unwrap();
    std::fs::write(
        node_resources.home.join("AGENTS.md"),
        "NODE_GLOBAL_RULE_MUST_NOT_APPEAR",
    )
    .unwrap();
    std::fs::create_dir_all(node_resources.skill_user.join("node-skill")).unwrap();
    std::fs::write(node_resources.skill_user.join("node-skill/SKILL.md"), "---\nname: node-skill\ndescription: Node global skill must not appear\n---\nNode instructions").unwrap();
    launch.resources.files.insert("skills/user/control-skill/SKILL.md".into(), pot_runner::resources::ResourceFile {
        data: base64::engine::general_purpose::STANDARD.encode("---\nname: control-skill\ndescription: Control supplied skill\n---\nControl instructions"), executable: false,
    });
    journal.accept(&control_id, launch.clone()).unwrap();
    let control = Arc::new(Control::default());
    let result = pot_runner::execute(
        Arc::new(host.clone()),
        journal.clone(),
        &control_id,
        &launch.task_id,
        &launch.run_id,
        services(&control),
    );
    tokio::pin!(result);
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(30), &mut result)
            .await
            .is_err()
    );
    assert!(
        control.requests.lock().unwrap().is_empty(),
        "Disconnected Control must gate new actions"
    );
    control.ready.store(true, Ordering::SeqCst);
    let terminal = result.await.unwrap().unwrap();
    assert_eq!(terminal.outcome, Outcome::Success);
    assert_eq!(terminal.text, "Task result without a download");
    let executions = backend.executions().snapshot();
    let execution = executions
        .iter()
        .find(|e| e.owner == launch.task_id)
        .unwrap();
    assert_eq!(execution.request_id, launch.run_id);
    assert_eq!(execution.runtime_id, host.runtime_id());
    assert_eq!(execution.phase, pot_runtime_execution::Phase::Succeeded);

    let request = control.requests.lock().unwrap()[0].clone();
    let system = request["messages"][0]["content"].as_str().unwrap();
    assert!(system.contains("ACTUAL_PROJECT_RULE"));
    assert!(system.contains("project-skill"));
    assert!(system.contains("control-skill"));
    assert!(!system.contains("NODE_GLOBAL_RULE_MUST_NOT_APPEAR"));
    assert!(!system.contains("node-skill"));
    assert!(!request.to_string().contains("update_agent_instructions"));
    assert!(!data.join("control.sqlite").exists());
    assert!(!data.join("pot-v2.sqlite").exists());
    assert!(
        journal
            .inspect(&control_id, &launch.task_id, &launch.run_id)
            .unwrap()
            .unwrap()
            .context
            .is_some()
    );
    assert_eq!(
        pot_runner::execute(
            Arc::new(host.clone()),
            journal.clone(),
            &control_id,
            &launch.task_id,
            &launch.run_id,
            services(&control)
        )
        .await
        .unwrap(),
        Some(terminal)
    );
    assert_eq!(control.requests.lock().unwrap().len(), 1);
    let mut next = launch.clone();
    next.run_id = id();
    journal.accept(&control_id, next.clone()).unwrap();
    control
        .switch_execution_target
        .store(true, Ordering::SeqCst);
    let blocked = pot_runner::execute(
        Arc::new(host.clone()),
        journal.clone(),
        &control_id,
        &next.task_id,
        &next.run_id,
        services(&control),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(blocked.outcome, Outcome::Success);
    {
        let requests = control.requests.lock().unwrap();
        assert_eq!(requests.len(), 3);
        let tool_result = requests[2]["messages"]
            .as_array()
            .unwrap()
            .iter()
            .find(|m| m["role"] == "tool")
            .unwrap();
        assert!(
            tool_result["content"]
                .as_str()
                .unwrap()
                .contains("Worker cannot change its fixed target")
        );
    }
    assert!(!workspace.join("forbidden").exists());
    let mut missing = next.clone();
    missing.run_id = id();
    missing.skills = Some(vec!["node-skill".into()]);
    journal.accept(&control_id, missing.clone()).unwrap();
    let before = control.requests.lock().unwrap().len();
    let rejected = pot_runner::execute(
        Arc::new(host.clone()),
        journal.clone(),
        &control_id,
        &missing.task_id,
        &missing.run_id,
        services(&control),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(
        matches!(rejected.outcome,Outcome::Failed(ref reason) if reason.contains("skill is unavailable"))
    );
    assert_eq!(
        control.requests.lock().unwrap().len(),
        before,
        "missing destination skill sampled a model or used a node-global fallback"
    );
    backend.shutdown().await;
    std::fs::remove_dir_all(root).unwrap();
}

#[test]
fn confirmed_reboot_interruption_retains_context_for_explicit_new_run() {
    let data = std::env::temp_dir().join(format!("pot-task-reboot-{}", id()));
    let runtime = id();
    let control = id();
    let launch = launch(&runtime);
    let journal = Journal::open(&data, &runtime, &id()).unwrap();
    journal.accept(&control, launch.clone()).unwrap();
    journal
        .claim(&control, &launch.task_id, &launch.run_id)
        .unwrap();
    let context = json!({"messages":["known completed effect"],"pending":[]});
    journal
        .save_context(&control, &launch.task_id, &launch.run_id, context.clone())
        .unwrap();
    assert!(
        journal
            .interrupt_recovered(&control, &launch.task_id, &launch.run_id)
            .is_err()
    );
    drop(journal);
    let journal = Journal::open(&data, &runtime, &id()).unwrap();
    assert_eq!(
        journal
            .inspect(&control, &launch.task_id, &launch.run_id)
            .unwrap()
            .unwrap()
            .phase,
        Phase::RecoveryRequired
    );
    journal
        .interrupt_recovered(&control, &launch.task_id, &launch.run_id)
        .unwrap();
    assert!(
        !journal
            .claim(&control, &launch.task_id, &launch.run_id)
            .unwrap()
    );
    let mut next = launch.clone();
    next.run_id = id();
    next.text = "Explicit guidance".into();
    let accepted = journal.accept(&control, next.clone()).unwrap();
    assert_eq!(accepted.context, Some(context));
    assert_eq!(accepted.phase, Phase::Accepted);
    drop(journal);
    let journal = Journal::open(&data, &runtime, &id()).unwrap();
    assert_eq!(
        journal
            .inspect(&control, &launch.task_id, &launch.run_id)
            .unwrap()
            .unwrap()
            .phase,
        Phase::Interrupted
    );
    std::fs::remove_dir_all(data).unwrap();
}

#[tokio::test]
async fn cancelled_control_error_persists_cancelled_terminal() {
    let root = std::env::temp_dir().join(format!("pot-task-cancel-{}", id()));
    let data = root.join("product");
    let mut backend = crate::initialize(crate::RuntimeConfig {
        data: data.clone(),
        workspace: data.join("workspace"),
    })
    .await
    .unwrap();
    backend.enable_host_resource();
    let host = backend.host().clone();
    let journal = Arc::new(Journal::open(&data, host.runtime_id(), host.boot_id()).unwrap());
    let control_id = id();
    let launch = launch(host.runtime_id());
    journal.accept(&control_id, launch.clone()).unwrap();
    let control = Arc::new(Control::default());
    control.cancelled.store(true, Ordering::SeqCst);
    let terminal = pot_runner::execute(
        Arc::new(host.clone()),
        journal.clone(),
        &control_id,
        &launch.task_id,
        &launch.run_id,
        services(&control),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(terminal.outcome, Outcome::Cancelled);
    assert_eq!(
        journal
            .inspect(&control_id, &launch.task_id, &launch.run_id)
            .unwrap()
            .unwrap()
            .phase,
        Phase::Cancelled
    );
    assert!(control.requests.lock().unwrap().is_empty());
    std::fs::remove_dir_all(root).unwrap();
}
