//! Control business-service lifecycle. Runtime composes this optional component with a Host.
use crate::*;
pub struct Component {
    assistants: Arc<assistants::Hub>,
    state: Service,
    main: Option<tokio::task::JoinHandle<()>>,
    mail: Mutex<Option<tokio::task::JoinHandle<()>>>,
    coordinator: Mutex<Option<tokio::task::JoinHandle<()>>>,
    activated: std::sync::atomic::AtomicBool,
}
impl Component {
    pub fn state(&self) -> Service {
        self.state.clone()
    }
    pub fn assistants(&self) -> Arc<assistants::Hub> {
        self.assistants.clone()
    }
    pub fn enable_host(&self) {
        self.state
            .execution_service
            .host_enabled
            .store(true, std::sync::atomic::Ordering::SeqCst);
    }
    pub fn activate(&self) -> Result<()> {
        anyhow::ensure!(
            !self
                .activated
                .swap(true, std::sync::atomic::Ordering::SeqCst),
            "Control already activated"
        );
        orchestration::initialize(&self.state)?;
        tasks::restore(&self.state)?;
        *self.coordinator.lock().unwrap() = Some(tasks::start(self.state.clone()));
        self.assistants.activate()?;
        *self.mail.lock().unwrap() = Some(crate::control::assistant_message::start(
            self.assistants.clone(),
        ));
        Ok(())
    }
    pub async fn shutdown(&mut self) {
        self.state.stop.send_replace(true);
        tasks::request_shutdown(&self.state);
        terminal::shutdown(&self.state).await;
        let mail = self.mail.lock().unwrap().take();
        if let Some(mut task) = mail {
            settle(&mut task).await;
        }
        self.assistants.shutdown().await;
        let coordinator = self.coordinator.lock().unwrap().take();
        if let Some(mut task) = coordinator {
            settle(&mut task).await;
        }
        if let Some(mut task) = self.main.take() {
            settle(&mut task).await;
        }
        if let Err(error) = execution_target::shutdown(&self.state).await {
            eprintln!("{error}");
        }
    }
}
impl Drop for Component {
    fn drop(&mut self) {
        self.state.stop.send_replace(true);
        if let Some(task) = self.mail.get_mut().unwrap().take() {
            task.abort();
        }
        if let Some(task) = self.main.take() {
            task.abort();
        }
        if let Some(task) = self.coordinator.get_mut().unwrap().take() {
            task.abort();
        }
    }
}
async fn settle(task: &mut tokio::task::JoinHandle<()>) {
    if tokio::time::timeout(std::time::Duration::from_secs(20), &mut *task)
        .await
        .is_err()
    {
        task.abort();
        let _ = task.await;
    }
}
pub async fn open(
    data: PathBuf,
    workspace: PathBuf,
    providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    host: &mut host::Host,
) -> Result<Component> {
    std::fs::create_dir_all(&data)?;
    std::fs::create_dir_all(&workspace)?;
    let execution_config = execution::execution_config(execution::Config::default(), &data)?;
    let api_budget = Some(budget::BudgetSettings {
        namespace: "agent-attempts",
        data: data.clone(),
        limit: execution_config.request_limit,
        max_output_tokens: execution_config.max_tokens,
    });
    let (events, _) = broadcast::channel(256);
    let (commands, rx) = mpsc::unbounded_channel();
    let sandboxes = match host.sandbox_manager() {
        Some(manager) => manager,
        None => Arc::new(sandbox::Manager::empty(&data, providers)?),
    };
    if host.host_execution.is_none() {
        host.attach_host_execution();
    }
    host.attach_sandboxes(sandboxes.clone());
    let permissions = Arc::new(permissions::Hub::open(&data.join("permissions.json"))?);
    let s = Service {
        scope: OwnerScope {
            runtime_id: host.runtime_id().into(),
            assistant_id: "main".into(),
            skill_selection: None,
            remote_context: Default::default(),
            #[cfg(test)]
            test_tool_executor: None,
            sandbox_id: None,
            execution_target: None,
            usage: Arc::new(Mutex::new(usage::Ledger::open(
                &data.join("usage-v2.sqlite"),
            )?)),
            usage_scope: "main".into(),
            token: host.token.clone(),
            boot: host.boot.clone(),
            workspace,
            api_budget,
            execution: Some(execution_config),
            #[cfg(test)]
            agent_upstream: "https://api.deepseek.com/chat/completions".into(),
            is_worker: false,
            data: data.clone(),
            control_config: Arc::new(model_config::ControlConfig::new(data.clone())),
            cancel_notify: Arc::new(tokio::sync::Notify::new()),
            stop: tokio::sync::watch::channel(false).0,
        },
        conversation: ConversationService {
            inner: Arc::new(Mutex::new(Inner {
                store: Store::open(&data.join("pot-v2.sqlite"))?,
                agent: AgentStatus {
                    model: String::new(),
                    phase: AgentPhase::NeedsConfiguration,
                    detail: Some("Open AI Configuration to select an Agent and model".into()),
                },
                turn: None,
                model_requests: 0,
            })),
            events,
            commands,
        },
        agent_service: AgentService {
            executions: host.executions().clone(),
            #[cfg(test)]
            extensions: None,
            resources: Arc::new(AgentResources::paths(&data)),
        },
        execution_service: ExecutionService {
            remote: Default::default(),
            host_enabled: Arc::new(std::sync::atomic::AtomicBool::new(host.host_enabled)),
            host: host
                .host_execution
                .clone()
                .ok_or_else(|| anyhow!("Host Execution target not assembled"))?,
            permissions,
            sandboxes,
            terminals: Arc::new(Mutex::new(terminal::Hub::default())),
        },
        tasks: TaskService {
            hub: Arc::new(Mutex::new(tasks::Hub::default())),
            capacity: Arc::new(Mutex::new(tasks::Capacity::default())),
        },
    };
    let configured = agent::configured(&s);
    s.conversation.inner.lock().unwrap().store.set(
        "agent.setup.pending",
        if configured { "false" } else { "true" },
    )?;
    if configured {
        s.phase(AgentPhase::Ready, None);
    }
    if !configured {
        s.phase(
            AgentPhase::NeedsConfiguration,
            Some("请先配置助手模型".into()),
        );
    }
    if usage::paused(&s)?.is_none() {
        s.usage.lock().unwrap().clear_wait("main")?;
    }
    agent::initialize(&s)?;
    let assistants = Arc::new(assistants::Hub::open(s.clone())?);
    *s.agent_service.resources.assistants.lock().unwrap() = Arc::downgrade(&assistants);
    let main = start_main(s.clone(), rx);
    Ok(Component {
        assistants,
        state: s,
        main: Some(main),
        mail: Default::default(),
        coordinator: Mutex::new(None),
        activated: Default::default(),
    })
}
struct ChatPort(Service);
impl pot_assistant::driver::ChatServices for ChatPort {
    fn stopped(&self) -> futures::future::BoxFuture<'_, ()> {
        Box::pin(self.0.stop_requested())
    }
    fn configured(&self) -> bool {
        agent::configured(&self.0)
    }
    fn paused(&self) -> bool {
        usage::paused(&self.0).ok().flatten().is_some()
    }
    fn task_context(&self) -> String {
        tasks::context(&self.0)
    }
    fn resource_context(&self) -> Result<String> {
        agent::catalog::context(&self.0)
    }
    fn execute(&self, input: SendMessage) -> futures::future::BoxFuture<'_, Result<()>> {
        Box::pin(agent::run(&self.0, input))
    }
    fn failed(&self, message: String) {
        self.0.phase(AgentPhase::Failed, Some(message));
    }
    fn finish_turn(&self) -> Result<()> {
        let result = usage::settle_cancelled_pause(&self.0);
        self.0.turn(None);
        result
    }
}
pub(crate) fn start_main(
    s: Service,
    rx: mpsc::UnboundedReceiver<SendMessage>,
) -> tokio::task::JoinHandle<()> {
    tokio::spawn(pot_assistant::driver::serve(Arc::new(ChatPort(s)), rx))
}
