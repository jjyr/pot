mod agent;
mod deepseek;
mod discovery;
mod gateway;
mod orchestration;
mod pi;
mod schedules;
mod store;
mod tasks;
mod terminal;
mod tool_bridge;
mod usage;
use anyhow::{Result, anyhow, bail};
use axum::{
    Json, Router,
    extract::{Query, State},
    http::{HeaderMap, StatusCode},
    response::{
        IntoResponse,
        sse::{Event as SseEvent, KeepAlive, Sse},
    },
    routing::{get, post},
};
use pot_protocol::*;
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    convert::Infallible,
    path::PathBuf,
    process::Stdio,
    sync::{Arc, Mutex},
};
use store::{Store, id, now};
use tokio::{
    io::{AsyncBufReadExt, BufReader},
    process::Command,
    sync::{broadcast, mpsc},
};

struct Inner {
    store: Store,
    engine: EngineStatus,
    turn: Option<TurnState>,
    model_requests: u64,
}
#[derive(Clone)]
struct Service {
    usage: Arc<Mutex<usage::Ledger>>,
    usage_scope: String,
    inner: Arc<Mutex<Inner>>,
    events: broadcast::Sender<Event>,
    commands: mpsc::UnboundedSender<SendMessage>,
    token: String,
    boot: String,
    workspace: PathBuf,
    program: String,
    api_budget: Option<deepseek::BudgetSettings>,
    pi: Option<pi::Config>,
    pi_program: String,
    pi_upstream: String,
    is_worker: bool,
    data: PathBuf,
    cancel_notify: Arc<tokio::sync::Notify>,
    tasks: Arc<Mutex<tasks::Hub>>,
    terminals: Arc<Mutex<terminal::Hub>>,
}
impl Service {
    fn snapshot(&self) -> Result<Snapshot> {
        let s = self.inner.lock().unwrap();
        self.snapshot_locked(&s)
    }
    fn snapshot_locked(&self, s: &Inner) -> Result<Snapshot> {
        Ok(Snapshot {
            budget: self.budget()?,
            api_version: API_VERSION,
            boot_id: self.boot.clone(),
            cursor: s.store.cursor()?,
            engine: {
                let mut engine = s.engine.clone();
                if s.store.get("pi.paused")?.is_some_and(|v| !v.is_empty()) {
                    engine.detail = Some("等待 Pot 额度恢复".into());
                }
                engine
            },
            conversation_id: "main".into(),
            messages: s.store.messages()?,
            turn: s.turn.clone(),
        })
    }
    fn budget(&self) -> Result<Option<RequestBudget>> {
        self.api_budget.as_ref().map(|b| b.status()).transpose()
    }
    fn publish_budget(&self) -> Result<()> {
        let _inner = self.inner.lock().unwrap();
        if let Some(budget) = self.budget()? {
            let _ = self.events.send(Event {
                event_id: id(),
                cursor: None,
                kind: EventKind::Budget { budget },
            });
        }
        Ok(())
    }
    fn save(&self, message: Message) -> Result<()> {
        let mut inner = self.inner.lock().unwrap();
        let event = inner.store.save(message)?;
        let _ = self.events.send(event);
        Ok(())
    }
    fn phase(&self, phase: EnginePhase, detail: Option<String>) {
        let mut s = self.inner.lock().unwrap();
        s.engine.phase = phase;
        s.engine.detail = detail;
        let _ = self.events.send(Event {
            event_id: id(),
            cursor: None,
            kind: EventKind::Engine {
                status: s.engine.clone(),
            },
        });
    }
    fn turn(&self, turn: Option<TurnState>) {
        let mut inner = self.inner.lock().unwrap();
        inner.turn = turn.clone();
        let _ = self.events.send(Event {
            event_id: id(),
            cursor: None,
            kind: EventKind::Turn { turn },
        });
    }
    fn authorized(&self, h: &HeaderMap) -> ApiResult<()> {
        if h.get("authorization").and_then(|h| h.to_str().ok())
            == Some(&format!("Bearer {}", self.token))
        {
            Ok(())
        } else {
            Err(error(
                StatusCode::UNAUTHORIZED,
                "unauthorized",
                "Runtime token required",
            ))
        }
    }
}
type ApiResult<T> = std::result::Result<T, (StatusCode, Json<ApiError>)>;
fn error(status: StatusCode, code: &str, message: &str) -> (StatusCode, Json<ApiError>) {
    (
        status,
        Json(ApiError {
            code: code.into(),
            message: message.into(),
        }),
    )
}
fn internal(e: anyhow::Error) -> (StatusCode, Json<ApiError>) {
    error(
        StatusCode::INTERNAL_SERVER_ERROR,
        "internal",
        &e.to_string(),
    )
}
async fn snapshot(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Snapshot>> {
    s.authorized(&h)?;
    Ok(Json(s.snapshot().map_err(internal)?))
}
async fn send(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<SendMessage>,
) -> ApiResult<Json<SendMessageAccepted>> {
    s.authorized(&h)?;
    if req.conversation_id != "main"
        || req.request_id.is_empty()
        || req.text.trim().is_empty()
        || req.text.len() > 65536
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_request",
            "Invalid conversation, request ID or message",
        ));
    }
    if s.commands.is_closed() {
        return Err(error(
            StatusCode::SERVICE_UNAVAILABLE,
            "worker_exited",
            "Restart environment",
        ));
    }
    let mut inner = s.inner.lock().unwrap();
    if let Some(old) = inner.store.duplicate(&req.request_id).map_err(internal)? {
        if old.text != req.text || old.conversation_id != req.conversation_id {
            return Err(error(
                StatusCode::CONFLICT,
                "request_conflict",
                "Request ID already used for different content",
            ));
        }
        return Ok(Json(SendMessageAccepted {
            message_id: old.id,
            duplicate: true,
        }));
    }
    if inner
        .store
        .get("agent.setup.pending")
        .map_err(internal)?
        .as_deref()
        == Some("true")
    {
        return Err(error(
            StatusCode::CONFLICT,
            "setup_required",
            "请先在助手设置中选择并配置主 Agent；Terminal 仍可使用",
        ));
    }
    if inner
        .store
        .get("pi.paused")
        .map_err(internal)?
        .is_some_and(|v| !v.is_empty())
    {
        return Err(error(
            StatusCode::CONFLICT,
            "waiting_allowance",
            "主聊天正在等待额度；请继续或取消后再发送新消息",
        ));
    }
    if inner.turn.is_some() {
        return Err(error(
            StatusCode::CONFLICT,
            "busy",
            "A turn is already running",
        ));
    }
    if !matches!(
        inner.engine.phase,
        EnginePhase::Ready | EnginePhase::NeedsAuth
    ) {
        return Err(error(
            StatusCode::CONFLICT,
            "engine_unavailable",
            "Authenticate the guest engine, then restart the environment",
        ));
    }
    if s.budget()
        .map_err(internal)?
        .is_some_and(|b| b.remaining() == 0)
    {
        return Err(error(
            StatusCode::TOO_MANY_REQUESTS,
            "budget_exhausted",
            "测试额度已用完；没有发送模型请求",
        ));
    }
    let message = Message {
        id: id(),
        conversation_id: req.conversation_id.clone(),
        role: Role::User,
        text: req.text.clone(),
        status: MessageStatus::Accepted,
        created_at_ms: now(),
        request_id: Some(req.request_id.clone()),
    };
    let mid = message.id.clone();
    let event = inner.store.save(message).map_err(internal)?;
    inner.model_requests = 0;
    inner.turn = Some(TurnState {
        user_message_id: mid.clone(),
        cancelling: false,
    });
    let _ = s.events.send(event);
    let _ = s.events.send(Event {
        event_id: id(),
        cursor: None,
        kind: EventKind::Turn {
            turn: inner.turn.clone(),
        },
    });
    s.commands.send(req).map_err(|_| {
        error(
            StatusCode::SERVICE_UNAVAILABLE,
            "worker_exited",
            "Restart environment",
        )
    })?;
    Ok(Json(SendMessageAccepted {
        message_id: mid,
        duplicate: false,
    }))
}
#[derive(Deserialize)]
struct Cursor {
    #[serde(default)]
    after: i64,
}
async fn events(
    State(s): State<Service>,
    h: HeaderMap,
    Query(q): Query<Cursor>,
) -> ApiResult<impl IntoResponse> {
    s.authorized(&h)?;
    // Subscribe and capture replay/snapshot under the same lock as publishers.
    // This closes the snapshot-to-subscription gap for ephemeral turn state too.
    let (mut rx, history, initial) = {
        let inner = s.inner.lock().unwrap();
        let rx = s.events.subscribe();
        let initial = s.snapshot_locked(&inner).map_err(internal)?;
        if q.after < 0 || q.after > initial.cursor {
            return Err(error(
                StatusCode::BAD_REQUEST,
                "invalid_cursor",
                "Fetch a fresh snapshot before reconnecting",
            ));
        }
        let history = inner.store.events(q.after).map_err(internal)?;
        (rx, history, initial)
    };
    let stream = async_stream::stream! {
     let mut cursor=q.after;
     for event in history {cursor=event.cursor.unwrap_or(cursor);yield Ok::<_,Infallible>(SseEvent::default().id(cursor.to_string()).json_data(&event).unwrap());}
     yield Ok(SseEvent::default().event("snapshot").json_data(&initial).unwrap());
     loop {match rx.recv().await {
      Ok(event)=>{if event.cursor.is_some_and(|c|c<=cursor){continue;}let mut out=SseEvent::default().json_data(&event).unwrap();if let Some(c)=event.cursor{cursor=c;out=out.id(c.to_string());}yield Ok(out);},
      // Reconnect from a snapshot after lag; never silently lose durable messages.
      Err(_)=>break,
     }}
    };
    Ok(Sse::new(stream)
        .keep_alive(KeepAlive::default().interval(std::time::Duration::from_secs(5))))
}
async fn cancel(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<CancelTurn>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if s.pi.is_none() {
        return Err(error(
            StatusCode::NOT_IMPLEMENTED,
            "cancel_unavailable",
            "Cancellation requires Pi",
        ));
    }
    let mut inner = s.inner.lock().unwrap();
    if let Some(turn) = inner.turn.as_mut() {
        if turn.user_message_id != req.user_message_id {
            return Err(error(
                StatusCode::CONFLICT,
                "turn_changed",
                "This turn has already ended",
            ));
        }
        turn.cancelling = true;
    }
    let _ = s.events.send(Event {
        event_id: id(),
        cursor: None,
        kind: EventKind::Turn {
            turn: inner.turn.clone(),
        },
    });
    s.cancel_notify.notify_one();
    Ok(Json(json!({"stopping":inner.turn.is_some()})))
}
async fn usage(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let inner = s.inner.lock().unwrap();
    let budget = s.budget().map_err(internal)?;
    let usage = inner
        .store
        .get(if s.pi.is_some() {
            "pi.usage"
        } else {
            "deepseek.usage"
        })
        .map_err(internal)?
        .unwrap_or_else(|| "[]".into());
    Ok(Json(
        json!({"requests":budget.as_ref().map(|b|b.used),"request_limit":budget.as_ref().map(|b|b.limit),"usage":serde_json::from_str::<Value>(&usage).unwrap_or(json!([]))}),
    ))
}
async fn authenticate(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Err(error(
        StatusCode::NOT_IMPLEMENTED,
        "interactive_login_required",
        "Use the guest AGY terminal to sign in, then restart the environment",
    ))
}
async fn run_agy(s: &Service, req: SendMessage) -> Result<()> {
    let mut user = s
        .inner
        .lock()
        .unwrap()
        .store
        .duplicate(&req.request_id)?
        .ok_or_else(|| anyhow!("Missing accepted message"))?;
    let mut assistant = Message {
        id: id(),
        conversation_id: req.conversation_id,
        role: Role::Assistant,
        text: String::new(),
        status: MessageStatus::Streaming,
        created_at_ms: now(),
        request_id: None,
    };
    s.save(assistant.clone())?;
    let result:Result<()>=async {
  let session=s.inner.lock().unwrap().store.get("agy.session")?;
  let mut cmd=Command::new(&s.program);
  cmd.current_dir(&s.workspace).args(["--print",&req.text,"--output-format","stream-json","--mode","accept-edits","--disable-slash-commands"]);
  if let Some(session)=session {cmd.args(["--conversation",&session]);}
  // No blanket tool approval: headless permission failures stay visible.
  let mut child=cmd.stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).kill_on_drop(true).spawn()?;
  let mut stderr=BufReader::new(child.stderr.take().unwrap()).lines();
  let (auth_tx,mut auth_rx)=mpsc::unbounded_channel();
  tokio::spawn(async move {
   let mut auth=false;
   while let Ok(Some(line))=stderr.next_line().await {
    if line.contains("Authentication required") {auth=true;let _=auth_tx.send(());}
    if !auth {eprintln!("agy: {line}");}
   }
  });
  let mut lines=BufReader::new(child.stdout.take().unwrap()).lines();let mut completed=false;
  loop {
   let line=tokio::select! {
    line=lines.next_line()=>match line?{Some(line)=>line,None=>break},
    Some(())=auth_rx.recv()=>{
     child.kill().await?;let _=child.wait().await;
     s.phase(EnginePhase::NeedsAuth,Some("Sign in through the guest AGY terminal, then send a new message".into()));
     bail!("Guest AGY login required. Use the tray menu to sign in, then retry.");
    }
   };
   let event:Value=serde_json::from_str(&line)?;
   match event["event"].as_str() {
    Some("init")=>{if let Some(session)=event["conversation_id"].as_str(){s.inner.lock().unwrap().store.set("agy.session",session)?;}},
    Some("step_update")=>{
     let step=&event["step_update"];
     if step["step_type"]=="agent_response" {if let Some(delta)=step["text_delta"].as_str(){assistant.text.push_str(delta);s.save(assistant.clone())?;}}
     if step["step_type"]=="tool" && step["state"]=="DONE" {s.save(Message{id:id(),conversation_id:"main".into(),role:Role::Tool,text:json!({"name":step["tool_name"],"detail":step["tool_info"]}).to_string(),status:MessageStatus::Complete,created_at_ms:now(),request_id:None})?;}
    },
    Some("result")=>{let r=&event["result"];if let Some(session)=r["conversation_id"].as_str(){s.inner.lock().unwrap().store.set("agy.session",session)?;}if r["status"]!="SUCCESS"{bail!("AGY {}: {}",r["status"],r["error"]);}if let Some(text)=r["response"].as_str(){assistant.text=text.into();}completed=true;},
    _=>{}
   }
  }
  let status=child.wait().await?;
  if !status.success() || !completed {bail!("AGY exited without a successful result ({status}); inspect engine log; guest login may be required");}Ok(())
 }.await;
    match result {
        Ok(()) => {
            s.phase(EnginePhase::Ready, None);
            assistant.status = MessageStatus::Complete;
            user.status = MessageStatus::Complete;
        }
        Err(e) => {
            assistant.status = MessageStatus::Failed;
            assistant.text.push_str(&format!("\n{e}"));
            user.status = MessageStatus::Failed;
        }
    }
    s.save(assistant)?;
    s.save(user)?;
    Ok(())
}
#[tokio::main]
async fn main() -> Result<()> {
    let data =
        PathBuf::from(std::env::var("POT_DATA_DIR").unwrap_or_else(|_| "/data/product".into()));
    std::fs::create_dir_all(&data)?;
    let workspace =
        PathBuf::from(std::env::var("POT_WORKSPACE").unwrap_or_else(|_| "/data/workspace".into()));
    std::fs::create_dir_all(&workspace)?;
    let provider_path = data.join("deepseek.json");
    let pi_config = if data.join("pi.json").exists() {
        Some(pi::Config::load(&data.join("pi.json"))?)
    } else {
        None
    };
    let use_pi = pi_config.is_some();
    let use_api = !use_pi && provider_path.exists();
    let api_budget = if let Some(c) = &pi_config {
        Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: data.clone(),
            limit: c.request_limit,
            max_output_tokens: c.max_tokens,
        })
    } else if use_api {
        let config = deepseek::Config::load(&provider_path)?;
        Some(deepseek::BudgetSettings {
            namespace: "deepseek-attempts",
            data: data.clone(),
            limit: config.request_limit,
            max_output_tokens: config.max_tokens,
        })
    } else {
        None
    };
    let program = std::env::var("POT_AGY_BIN").unwrap_or_else(|_| "agy".into());
    let (events, _) = broadcast::channel(256);
    let (commands, mut rx) = mpsc::unbounded_channel();
    let s = Service {
        usage: Arc::new(Mutex::new(usage::Ledger::open(&data.join("usage.sqlite"))?)),
        usage_scope: "main".into(),
        inner: Arc::new(Mutex::new(Inner {
            store: Store::open(&data.join("pot.sqlite"))?,
            engine: EngineStatus {
                engine: if use_pi {"Pi · DeepSeek Flash"} else if use_api {"deepseek-v4-flash"} else {"agy-cli"}.into(),
                phase: if use_pi || use_api {EnginePhase::Ready} else {EnginePhase::NeedsAuth},
                detail: Some(
                    if use_pi {"Pi tools and native session"} else if use_api {"API smoke test: at most 3 requests, 128 output tokens each"} else {"Sign in through the guest AGY terminal; if already signed in, send a message to verify access"}.into(),
                ),
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
        program,
        api_budget,
        pi: pi_config,
        pi_program: std::env::var("POT_PI_BIN").unwrap_or_else(|_|"pi".into()),
        pi_upstream: "https://api.deepseek.com/chat/completions".into(),
        is_worker: false,
        data,
        cancel_notify: Arc::new(tokio::sync::Notify::new()),
            tasks: Arc::new(Mutex::new(tasks::Hub::default())),
            terminals: Arc::new(Mutex::new(terminal::Hub::default())),
    };
    s.inner.lock().unwrap().store.set(
        "agent.setup.pending",
        if use_pi || use_api { "false" } else { "true" },
    )?;
    if !use_pi && !use_api {
        s.phase(
            EnginePhase::NeedsAuth,
            Some("请先配置主 Agent；Sandbox Terminal 可独立使用".into()),
        );
    }
    s.usage
        .lock()
        .unwrap()
        .import_legacy("main", &s.inner.lock().unwrap().store)?;
    if usage::paused(&s)?.is_none() {
        s.usage.lock().unwrap().clear_wait("main")?;
    }
    agent::initialize(&s)?;
    agent::migration::sync_legacy_settings(&s).await?;
    let worker = s.clone();
    tokio::spawn(async move {
        while let Some(mut req) = rx.recv().await {
            if use_pi && usage::paused(&worker).ok().flatten().is_none() {
                req.text.push_str(&tasks::context(&worker));
            }
            let result = if use_pi {
                agent::run(&worker, req).await
            } else if use_api {
                deepseek::run(&worker, req, &provider_path).await
            } else {
                run_agy(&worker, req).await
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
    let router = Router::new()
        .route(
            "/api/v1/agents/tools",
            get(discovery::list).post(discovery::save),
        )
        .route("/api/v1/agents/refresh", post(discovery::refresh))
        .route(
            "/api/v1/terminals",
            get(terminal::list).post(terminal::create),
        )
        .route("/api/v1/terminals/{id}/socket", get(terminal::connect))
        .route("/api/v1/terminals/{id}/close", post(terminal::close))
        .route(
            "/api/pi/v1/chat/completions",
            post(tasks::gateway).layer(axum::extract::DefaultBodyLimit::max(pi::MAX_MODEL_BODY)),
        )
        .route(
            "/gw/{provider}/v1/chat/completions",
            post(gateway::routed).layer(axum::extract::DefaultBodyLimit::max(pi::MAX_MODEL_BODY)),
        )
        .route("/api/pi/compactions", post(pi::compaction_event))
        .route("/api/v1/orchestration", post(orchestration::invoke))
        .route("/api/v1/mcp", post(tool_bridge::mcp))
        .route(PATH_TASKS, get(tasks::list).post(tasks::create))
        .route("/api/v1/tasks/query", get(tasks::query))
        .route(
            "/api/v1/schedules",
            get(schedules::list).post(schedules::command),
        )
        .route("/api/v1/tasks/{id}", get(tasks::snapshot))
        .route("/api/v1/tasks/{id}/messages", post(tasks::input))
        .route("/api/v1/tasks/{id}/cancel", post(tasks::cancel))
        .route(
            "/api/v1/agent",
            get(agent::inspect).post(agent::migration::settings),
        )
        .route("/api/v1/agent/migrate", post(agent::migration::migrate))
        .route(PATH_SNAPSHOT, get(snapshot))
        .route("/api/v1/usage", get(usage))
        .route("/api/v1/usage/summary", get(usage::summary))
        .route("/api/v1/usage/policy", post(usage::policy))
        .route("/api/v1/usage/resume", post(usage::resume))
        .route("/api/v1/usage/cancel", post(usage::cancel_wait))
        .route(PATH_EVENTS, get(events_handler))
        .route(PATH_MESSAGES, post(send))
        .route(PATH_CANCEL, post(cancel))
        .route(PATH_AUTHENTICATE, post(authenticate))
        .with_state(s.clone());
    let address =
        std::env::var("POT_RUNTIME_BIND").unwrap_or_else(|_| format!("0.0.0.0:{RUNTIME_PORT}"));
    let listener = tokio::net::TcpListener::bind(address).await?;
    let endpoint = RuntimeEndpoint {
        api_version: API_VERSION,
        port: listener.local_addr()?.port(),
        token: s.token.clone(),
        boot_id: s.boot.clone(),
    };
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let path = s.data.join(RUNTIME_ENDPOINT_FILE);
    let temp = path.with_extension("tmp");
    let mut f = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    f.write_all(&serde_json::to_vec(&endpoint)?)?;
    std::fs::rename(temp, &path)?;
    orchestration::initialize(&s)?;
    tasks::restore(&s)?;
    tasks::start(s.clone());
    eprintln!("pot runtime listening on {}", listener.local_addr()?);
    axum::serve(listener, router)
        .with_graceful_shutdown(async move {
            let mut term =
                tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                    .expect("signal handler");
            tokio::select! { _ = tokio::signal::ctrl_c() => {}, _ = term.recv() => {} }
            terminal::shutdown(&s).await;
        })
        .await?;
    Ok(())
}
// Keep the endpoint name distinct from the broadcast sender in main.
async fn events_handler(
    s: State<Service>,
    h: HeaderMap,
    q: Query<Cursor>,
) -> ApiResult<impl IntoResponse> {
    events(s, h, q).await
}

#[cfg(test)]
mod tests {
    use super::*;
    pub(super) fn service() -> Service {
        let (events, _) = broadcast::channel(8);
        let (commands, _rx) = mpsc::unbounded_channel();
        Service {
            usage: Arc::new(Mutex::new(
                usage::Ledger::open(std::path::Path::new(":memory:")).unwrap(),
            )),
            usage_scope: "main".into(),
            inner: Arc::new(Mutex::new(Inner {
                store: Store::open(std::path::Path::new(":memory:")).unwrap(),
                engine: EngineStatus {
                    engine: "test".into(),
                    phase: EnginePhase::Ready,
                    detail: None,
                    version: None,
                    auth_methods: vec![],
                },
                turn: None,
                model_requests: 0,
            })),
            events,
            commands,
            token: "test-token".into(),
            boot: id(),
            workspace: PathBuf::new(),
            program: String::new(),
            api_budget: None,
            pi: None,
            pi_program: String::new(),
            pi_upstream: String::new(),
            is_worker: false,
            data: PathBuf::new(),
            cancel_notify: Arc::new(tokio::sync::Notify::new()),
            tasks: Arc::new(Mutex::new(tasks::Hub::default())),
            terminals: Arc::new(Mutex::new(terminal::Hub::default())),
        }
    }
    #[tokio::test]
    async fn request_retry_is_idempotent_and_conflicting_reuse_is_rejected() {
        let mut s = service();
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let req = SendMessage {
            request_id: "request-1".into(),
            conversation_id: "main".into(),
            text: "hello".into(),
        };
        assert_eq!(
            send(State(s.clone()), HeaderMap::new(), Json(req.clone()))
                .await
                .unwrap_err()
                .0,
            StatusCode::UNAUTHORIZED
        );
        let first = send(State(s.clone()), h.clone(), Json(req.clone()))
            .await
            .unwrap()
            .0;
        let retry = send(State(s.clone()), h.clone(), Json(req.clone()))
            .await
            .unwrap()
            .0;
        assert!(!first.duplicate);
        assert!(retry.duplicate);
        assert_eq!(first.message_id, retry.message_id);
        assert!(rx.try_recv().is_ok());
        assert!(rx.try_recv().is_err());
        let mut conflicting = req.clone();
        conflicting.text = "different".into();
        assert_eq!(
            send(State(s.clone()), h.clone(), Json(conflicting))
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        let mut other = req;
        other.request_id = "request-2".into();
        assert_eq!(
            send(State(s.clone()), h, Json(other)).await.unwrap_err().0,
            StatusCode::CONFLICT
        );
        let snapshot = s.snapshot().unwrap();
        assert_eq!(snapshot.messages.len(), 1);
        assert_eq!(snapshot.cursor, 1);
    }
    #[tokio::test]
    async fn exhausted_ledger_rejects_without_writes_but_allows_deduplication() {
        let mut s = service();
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        let data = std::env::temp_dir().join(format!("pot-budget-{}", id()));
        std::fs::create_dir_all(data.join("deepseek-attempts")).unwrap();
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "deepseek-attempts",
            data: data.clone(),
            limit: 1,
            max_output_tokens: 128,
        });
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let req = SendMessage {
            request_id: "r1".into(),
            conversation_id: "main".into(),
            text: "hello".into(),
        };
        let _ = send(State(s.clone()), h.clone(), Json(req.clone()))
            .await
            .unwrap();
        rx.try_recv().unwrap();
        s.turn(None);
        std::fs::write(data.join("deepseek-attempts/1"), "reserved").unwrap();
        let before = s.snapshot().unwrap();
        assert_eq!(before.budget.as_ref().unwrap().used, 1);
        assert!(
            send(State(s.clone()), h.clone(), Json(req.clone()))
                .await
                .unwrap()
                .0
                .duplicate
        );
        let mut next = req;
        next.request_id = "r2".into();
        let rejected = send(State(s.clone()), h, Json(next)).await.unwrap_err();
        assert_eq!(rejected.0, StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(rejected.1.0.code, "budget_exhausted");
        assert_eq!(s.snapshot().unwrap(), before);
        assert!(rx.try_recv().is_err());
        std::fs::remove_dir_all(data).unwrap();
    }
    #[tokio::test]
    async fn sse_replays_then_snapshots_then_delivers_live_updates() {
        use futures::StreamExt;
        let s = service();
        s.save(Message {
            id: "m1".into(),
            conversation_id: "main".into(),
            role: Role::Assistant,
            text: "partial".into(),
            status: MessageStatus::Streaming,
            created_at_ms: 0,
            request_id: None,
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "u1".into(),
            cancelling: false,
        }));
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let response = events(State(s.clone()), h, Query(Cursor { after: 0 }))
            .await
            .unwrap()
            .into_response();
        // Update after subscription, before the response is polled.
        s.turn(None);
        let mut body = response.into_body().into_data_stream();
        let replay = body.next().await.unwrap().unwrap();
        assert!(std::str::from_utf8(&replay).unwrap().contains("partial"));
        let snapshot = body.next().await.unwrap().unwrap();
        let snapshot = std::str::from_utf8(&snapshot).unwrap();
        assert!(snapshot.contains("event: snapshot"));
        assert!(snapshot.contains("u1"));
        let live = body.next().await.unwrap().unwrap();
        assert!(
            std::str::from_utf8(&live)
                .unwrap()
                .contains("\"turn\":null")
        );
    }
}
