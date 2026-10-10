use super::*;
/// Backend filesystem paths belong to the machine running Server.
#[derive(Clone, Debug)]
pub struct RuntimeConfig {
    pub data: PathBuf,
    pub workspace: PathBuf,
}
pub struct Backend {
    host: host::Host,
    state: Option<Service>,
    activated: std::sync::atomic::AtomicBool,
    main: Option<tokio::task::JoinHandle<()>>,
    coordinator: Mutex<Option<tokio::task::JoinHandle<()>>>,
}
impl Backend {
    pub fn instance_lock(&self) -> Arc<std::fs::File> {
        self.host.lock.clone()
    }
    pub fn state(&self) -> Result<Service> {
        self.state
            .clone()
            .ok_or_else(|| anyhow!("Assistant service is disabled"))
    }
    pub fn optional_state(&self) -> Option<Service> {
        self.state.clone()
    }
    pub fn host(&self) -> &host::Host {
        &self.host
    }
    pub fn data_dir(&self) -> &std::path::Path {
        &self.host.data
    }
    pub fn endpoint(&self, port: u16) -> RuntimeEndpoint {
        self.host.endpoint(port)
    }
    /// Called after the Server has published its authenticated internal endpoint.
    pub fn activate(&self) -> Result<()> {
        anyhow::ensure!(
            !self
                .activated
                .swap(true, std::sync::atomic::Ordering::SeqCst),
            "Backend already activated"
        );
        if let Some(state) = &self.state {
            orchestration::initialize(state)?;
            tasks::restore(state)?;
            *self.coordinator.lock().unwrap() = Some(tasks::start(state.clone()));
        }
        Ok(())
    }

    pub async fn shutdown(&mut self) {
        self.host.request_stop();
        if let Some(state) = &self.state {
            state.stop.send_replace(true);
            tasks::request_shutdown(state);
            terminal::shutdown(state).await;
        }
        let coordinator = self.coordinator.lock().unwrap().take();
        if let Some(mut coordinator) = coordinator {
            settle(&mut coordinator).await;
        }
        if let Some(mut main) = self.main.take() {
            settle(&mut main).await;
        }
        if let Some(state) = &self.state {
            if let Err(error) = sandbox::shutdown(state).await {
                eprintln!("{error}");
            }
        } else if let Err(error) = self.host.shutdown().await {
            eprintln!("{error}");
        }
    }
}
pub async fn initialize(config: RuntimeConfig) -> Result<Backend> {
    let composition = host::Composition::load(&config.data)?;
    initialize_composition(config, composition, vec![]).await
}
/// Embedders can install code-level tools, context transforms and token estimation.
pub async fn initialize_with_agent_executor(
    config: RuntimeConfig,
    agent_executor: Arc<pot_agent::Agent>,
) -> Result<Backend> {
    initialize_with_providers_and_agent(config, agent_executor, vec![]).await
}
pub async fn initialize_with_providers(
    config: RuntimeConfig,
    providers: Vec<Arc<dyn pot_sandbox::Provider>>,
) -> Result<Backend> {
    initialize_with_providers_and_agent(config, Arc::new(pot_agent::Agent::default()), providers)
        .await
}
async fn initialize_with_providers_and_agent(
    config: RuntimeConfig,
    agent_executor: Arc<pot_agent::Agent>,
    providers: Vec<Arc<dyn pot_sandbox::Provider>>,
) -> Result<Backend> {
    let mut host = host::Host::open(&config.data, host::Composition::Assistant)?;
    initialize_assistant(config, agent_executor, providers, &mut host).await
}

async fn initialize_assistant(
    config: RuntimeConfig,
    agent_executor: Arc<pot_agent::Agent>,
    providers: Vec<Arc<dyn pot_sandbox::Provider>>,
    host: &mut host::Host,
) -> Result<Backend> {
    let data = config.data;
    std::fs::create_dir_all(&data)?;
    let workspace = config.workspace;
    std::fs::create_dir_all(&workspace)?;
    let execution_config = execution::execution_config(execution::Config::default(), &data)?;
    let api_budget = Some(budget::BudgetSettings {
        namespace: "agent-attempts",
        data: data.clone(),
        limit: execution_config.request_limit,
        max_output_tokens: execution_config.max_tokens,
    });
    let (events, _) = broadcast::channel(256);
    let (commands, mut rx) = mpsc::unbounded_channel();
    let resources = AgentResources::paths(&data);
    let sandboxes = Arc::new(sandbox::Manager::with_providers(&data, providers)?);
    host.attach_environment(sandboxes.clone());
    let permissions = Arc::new(permissions::Hub::open_with_legacy(
        &resources.home.join("permissions.json"),
        &data.join("permissions-v1.sqlite"),
    )?);
    let s = Service {
        scope: OwnerScope {
            runtime_id: host.runtime_id().into(),
            skill_selection: None,
            #[cfg(test)]
            test_tool_executor: None,
            sandbox_id: None,
            usage: Arc::new(Mutex::new(usage::Ledger::open(
                &data.join("usage-v2.sqlite"),
            )?)),
            usage_scope: "main".into(),
            token: host.token.clone(),
            boot: host.boot.clone(),
            workspace,
            api_budget,
            execution: Some(execution_config),
            agent_upstream: "https://api.deepseek.com/chat/completions".into(),
            is_worker: false,
            data: data.clone(),
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
            executor: agent_executor,
            resources: Arc::new(AgentResources::paths(&data)),
        },
        environment_service: EnvironmentService {
            permissions,
            manager: sandboxes,
            terminals: Arc::new(Mutex::new(terminal::Hub::default())),
        },
        tasks: TaskService {
            hub: Arc::new(Mutex::new(tasks::Hub::default())),
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
            Some("请先配置主 Agent；Sandbox Terminal 可独立使用".into()),
        );
    }
    if usage::paused(&s)?.is_none() {
        s.usage.lock().unwrap().clear_wait("main")?;
    }
    agent::initialize(&s)?;
    let worker = s.clone();
    let main = tokio::spawn(async move {
        while let Some(mut req) =
            tokio::select! { _ = worker.stop_requested() => None, req = rx.recv() => req }
        {
            let use_agent = agent::configured(&worker);
            if use_agent && usage::paused(&worker).ok().flatten().is_none() {
                req.text.push_str(&tasks::context(&worker));
            }
            if use_agent && !req.request_id.starts_with(orchestration::RESULT_PREFIX) {
                match agent::preferences::context(&worker) {
                    Ok(context) => req.text.push_str(&context),
                    Err(e) => {
                        worker.phase(AgentPhase::Failed, Some(e.to_string()));
                        worker.turn(None);
                        continue;
                    }
                }
            }
            let result = if use_agent {
                worker.runner().run(req).await
            } else {
                Err(anyhow!("Configure a main Agent before sending messages"))
            };
            if let Err(e) = result {
                worker.phase(AgentPhase::Failed, Some(e.to_string()));
            }
            if let Err(e) = usage::settle_cancelled_pause(&worker) {
                worker.phase(AgentPhase::Failed, Some(e.to_string()));
            }
            worker.turn(None);
        }
    });
    Ok(Backend {
        host: host.clone(),
        state: Some(s),
        activated: Default::default(),
        main: Some(main),
        coordinator: Mutex::new(None),
    })
}

async fn settle(handle: &mut tokio::task::JoinHandle<()>) {
    if tokio::time::timeout(std::time::Duration::from_secs(20), &mut *handle)
        .await
        .is_err()
    {
        handle.abort();
        let _ = handle.await;
    }
}
impl Drop for Backend {
    fn drop(&mut self) {
        self.host.request_stop();
        if let Some(state) = &self.state {
            state.stop.send_replace(true);
        }
        if let Some(main) = self.main.take() {
            main.abort();
        }
        if let Some(coordinator) = self.coordinator.get_mut().unwrap().take() {
            coordinator.abort();
        }
    }
}

/// Explicit capability assembly; disabled compositions never open assistant stores.
pub async fn initialize_composition(
    config: RuntimeConfig,
    composition: host::Composition,
    providers: Vec<Arc<dyn pot_sandbox::Provider>>,
) -> Result<Backend> {
    anyhow::ensure!(
        composition.has_environments() || providers.is_empty(),
        "Bare Runtime cannot register Sandbox Providers"
    );
    let mut host = host::Host::open(&config.data, composition)?;
    if composition == host::Composition::Assistant {
        return initialize_assistant(
            config,
            Arc::new(pot_agent::Agent::default()),
            providers,
            &mut host,
        )
        .await;
    }
    if composition == host::Composition::Environment {
        host.attach_environment(Arc::new(sandbox::Manager::with_providers(
            &config.data,
            providers,
        )?));
    }
    Ok(Backend {
        host,
        state: None,
        activated: Default::default(),
        main: None,
        coordinator: Mutex::new(None),
    })
}
