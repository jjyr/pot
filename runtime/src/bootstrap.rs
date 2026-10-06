use super::*;
/// Backend filesystem paths belong to the machine running Server.
#[derive(Clone, Debug)]
pub struct RuntimeConfig {
    pub data: PathBuf,
    pub workspace: PathBuf,
}
pub struct Backend {
    state: Service,
    main: Option<tokio::task::JoinHandle<()>>,
    coordinator: Mutex<Option<tokio::task::JoinHandle<()>>>,
}
impl Backend {
    pub fn state(&self) -> Service {
        self.state.clone()
    }
    pub fn data_dir(&self) -> &std::path::Path {
        &self.state.data
    }
    pub fn endpoint(&self, port: u16) -> RuntimeEndpoint {
        RuntimeEndpoint {
            api_version: API_VERSION,
            port,
            token: self.state.token.clone(),
            boot_id: self.state.boot.clone(),
        }
    }
    /// Called after the Server has published its authenticated internal endpoint.
    pub fn activate(&self) -> Result<()> {
        let mut coordinator = self.coordinator.lock().unwrap();
        anyhow::ensure!(
            coordinator.is_none() && !self.state.stopping(),
            "Backend already activated or stopped"
        );
        orchestration::initialize(&self.state)?;
        tasks::restore(&self.state)?;
        *coordinator = Some(tasks::start(self.state.clone()));
        Ok(())
    }
    pub async fn shutdown(&mut self) {
        self.state.stop.send_replace(true);
        tasks::request_shutdown(&self.state);
        terminal::shutdown(&self.state).await;
        let coordinator = self.coordinator.lock().unwrap().take();
        if let Some(mut coordinator) = coordinator {
            settle(&mut coordinator).await;
        }
        if let Some(mut main) = self.main.take() {
            settle(&mut main).await;
        }
        self.state.codex.shutdown().await;
    }
}
pub async fn initialize(config: RuntimeConfig) -> Result<Backend> {
    initialize_with_agent_host(config, Arc::new(pot_agent::AgentHost)).await
}
/// Embedders can install code-level tools, context transforms and token estimation.
pub async fn initialize_with_agent_host(
    config: RuntimeConfig,
    agent_host: Arc<dyn pot_core::DriverHost>,
) -> Result<Backend> {
    let data = config.data;
    std::fs::create_dir_all(&data)?;
    let workspace = config.workspace;
    std::fs::create_dir_all(&workspace)?;
    let execution_config = execution::execution_config(execution::Config::default(), &data)?;
    let api_budget = Some(deepseek::BudgetSettings {
        namespace: "agent-attempts",
        data: data.clone(),
        limit: execution_config.request_limit,
        max_output_tokens: execution_config.max_tokens,
    });
    let (events, _) = broadcast::channel(256);
    let (commands, mut rx) = mpsc::unbounded_channel();
    let s = Service {
        usage: Arc::new(Mutex::new(usage::Ledger::open(
            &data.join("usage-v2.sqlite"),
        )?)),
        usage_scope: "main".into(),
        inner: Arc::new(Mutex::new(Inner {
            store: Store::open(&data.join("pot-v2.sqlite"))?,
            engine: EngineStatus {
                engine: "unconfigured".into(),
                phase: EnginePhase::NeedsAuth,
                detail: Some("Open AI Configuration to select an Agent and model".into()),
                version: None,
                auth_methods: vec![],
            },
            turn: None,
            model_requests: 0,
        })),
        events,
        commands,
        token: id(),
        boot: id(),
        workspace,
        api_budget,
        execution: None,
        agent_upstream: "https://api.deepseek.com/chat/completions".into(),
        is_worker: false,
        data,
        cancel_notify: Arc::new(tokio::sync::Notify::new()),
        stop: tokio::sync::watch::channel(false).0,
        tasks: Arc::new(Mutex::new(tasks::Hub::default())),
        terminals: Arc::new(Mutex::new(terminal::Hub::default())),
        codex: Arc::new(agent::codex::Hosts::default()),
        agent_host,
        authorization: Arc::new(tokio::sync::Mutex::new(authorization::Hub::default())),
    };
    let configured = agent::configured(&s);
    s.inner.lock().unwrap().store.set(
        "agent.setup.pending",
        if configured { "false" } else { "true" },
    )?;
    if configured {
        s.phase(EnginePhase::Ready, None);
    }
    if !configured {
        s.phase(
            EnginePhase::NeedsAuth,
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
                        worker.phase(EnginePhase::Failed, Some(e.to_string()));
                        worker.turn(None);
                        continue;
                    }
                }
            }
            let result = if use_agent {
                agent::run(&worker, req).await
            } else {
                Err(anyhow!("Configure a main Agent before sending messages"))
            };
            if let Err(e) = result {
                worker.phase(EnginePhase::Failed, Some(e.to_string()));
            }
            if let Err(e) = usage::settle_cancelled_pause(&worker) {
                worker.phase(EnginePhase::Failed, Some(e.to_string()));
            }
            worker.turn(None);
        }
    });
    Ok(Backend {
        state: s,
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
        self.state.stop.send_replace(true);
        if let Some(main) = self.main.take() {
            main.abort();
        }
        if let Some(coordinator) = self.coordinator.get_mut().unwrap().take() {
            coordinator.abort();
        }
    }
}
