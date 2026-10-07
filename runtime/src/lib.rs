//! Reusable backend application services. The executable and route assembly live in pot-server.
pub mod agent;
pub mod authorization;
mod budget;
pub mod control;
pub mod discovery;
pub mod execution;
pub mod gateway;
mod instructions;
pub mod orchestration;
pub mod sandbox;
pub mod schedules;
mod skills;
mod store;
pub mod tasks;
pub mod terminal;
pub mod tool_bridge;
pub mod usage;
use anyhow::{Result, anyhow, bail};
use axum::{
    Json,
    extract::{Query, State},
    http::{HeaderMap, StatusCode},
    response::{
        IntoResponse,
        sse::{Event as SseEvent, KeepAlive, Sse},
    },
};
use pot_protocol::*;
mod bootstrap;
pub use bootstrap::{
    Backend, RuntimeConfig, initialize, initialize_with_agent_host, initialize_with_providers,
};
pub mod api {
    pub use crate::usage as accounting;
    pub use crate::{Service, cancel, events_handler, send, snapshot, usage};
    pub use crate::{
        agent, authorization, control, discovery, execution, gateway, orchestration, sandbox,
        schedules, tasks, terminal, tool_bridge,
    };
}
#[cfg(test)]
use axum::{
    Router,
    routing::{get, post},
};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    convert::Infallible,
    path::PathBuf,
    sync::{Arc, Mutex},
};
use store::{Store, id, now};
use tokio::sync::{broadcast, mpsc};

struct Inner {
    store: Store,
    engine: EngineStatus,
    turn: Option<TurnState>,
    model_requests: u64,
}
#[derive(Clone)]
pub struct Service {
    skill_selection: Option<Vec<String>>,
    #[cfg(test)]
    test_tool_executor: Option<Arc<dyn pot_core::ToolExecutor>>,
    sandboxes: Arc<sandbox::Manager>,
    sandbox_id: Option<String>,
    usage: Arc<Mutex<usage::Ledger>>,
    usage_scope: String,
    inner: Arc<Mutex<Inner>>,
    events: broadcast::Sender<Event>,
    commands: mpsc::UnboundedSender<SendMessage>,
    token: String,
    boot: String,
    workspace: PathBuf,
    api_budget: Option<budget::BudgetSettings>,
    execution: Option<execution::Config>,
    agent_upstream: String,
    is_worker: bool,
    data: PathBuf,
    cancel_notify: Arc<tokio::sync::Notify>,
    stop: tokio::sync::watch::Sender<bool>,
    tasks: Arc<Mutex<tasks::Hub>>,
    terminals: Arc<Mutex<terminal::Hub>>,
    codex: Arc<agent::codex::Hosts>,
    agent_host: Arc<dyn pot_core::DriverHost>,
    authorization: Arc<tokio::sync::Mutex<authorization::Hub>>,
}
impl Service {
    /// Trusted in-process notification adapter. Only the owner main Store is eligible.
    pub fn notification_sequence(&self) -> Result<i64> {
        self.inner.lock().unwrap().store.notification_sequence()
    }
    pub fn notifications(&self, after: i64) -> Result<Vec<(i64, Message)>> {
        if self.is_worker {
            return Ok(Vec::new());
        }
        self.inner.lock().unwrap().store.notifications(after)
    }

    fn stopping(&self) -> bool {
        *self.stop.borrow()
    }
    async fn stop_requested(&self) {
        let mut stop = self.stop.subscribe();
        let _ = stop.wait_for(|stopping| *stopping).await;
    }

    fn snapshot(&self) -> Result<Snapshot> {
        let s = self.inner.lock().unwrap();
        self.snapshot_locked(&s)
    }
    fn snapshot_locked(&self, s: &Inner) -> Result<Snapshot> {
        Ok(Snapshot {
            budget: self.budget_for(&s.store)?,
            api_version: API_VERSION,
            boot_id: self.boot.clone(),
            cursor: s.store.cursor()?,
            engine: {
                let mut engine = s.engine.clone();
                if let Some(binding) = agent::current(&s.store)? {
                    engine.engine = format!("{:?} · {}", binding.spec.engine, binding.spec.model);
                }
                if s.store
                    .get("execution.paused")?
                    .is_some_and(|v| !v.is_empty())
                {
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
        self.budget_for(&self.inner.lock().unwrap().store)
    }
    fn budget_for(&self, store: &Store) -> Result<Option<RequestBudget>> {
        if let Some(binding) = agent::current(store)? {
            if binding.spec.engine != agent::Engine::Pot {
                return Ok(None);
            }
            return gateway::agent_budget(self, binding.spec.params.max_output_tokens)?
                .status()
                .map(Some);
        }
        self.api_budget.as_ref().map(|b| b.status()).transpose()
    }
    fn publish_budget(&self) -> Result<()> {
        let _inner = self.inner.lock().unwrap();
        if let Some(budget) = self.budget_for(&_inner.store)? {
            let _ = self.events.send(Event {
                event_id: id(),
                cursor: None,
                kind: EventKind::Budget { budget },
            });
        }
        Ok(())
    }
    fn save_non_notifying(&self, message: Message) -> Result<()> {
        let event = self
            .inner
            .lock()
            .unwrap()
            .store
            .save_non_notifying(message)?;
        let _ = self.events.send(event);
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
pub async fn snapshot(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Snapshot>> {
    s.authorized(&h)?;
    Ok(Json(s.snapshot().map_err(internal)?))
}
pub async fn send(
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
    if s.stopping() || s.commands.is_closed() {
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
        .get("execution.paused")
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
    if s.budget_for(&inner.store)
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
pub struct Cursor {
    #[serde(default)]
    after: i64,
}
pub async fn events(
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
     loop {tokio::select! { event = rx.recv() => match event {
      Ok(event)=>{if event.cursor.is_some_and(|c|c<=cursor){continue;}let mut out=SseEvent::default().json_data(&event).unwrap();if let Some(c)=event.cursor{cursor=c;out=out.id(c.to_string());}yield Ok(out);},
      // Reconnect from a snapshot after lag; never silently lose durable messages.
      Err(_)=>break,
     }, _ = s.stop_requested() => break }}
    };
    Ok(Sse::new(stream)
        .keep_alive(KeepAlive::default().interval(std::time::Duration::from_secs(5))))
}
pub async fn cancel(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<CancelTurn>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if !agent::configured(&s) {
        return Err(error(
            StatusCode::NOT_IMPLEMENTED,
            "cancel_unavailable",
            "Cancellation requires an Agent binding",
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
pub async fn usage(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let inner = s.inner.lock().unwrap();
    let budget = s.budget_for(&inner.store).map_err(internal)?;
    let usage = inner
        .store
        .get("execution.usage")
        .map_err(internal)?
        .unwrap_or_else(|| "[]".into());
    Ok(Json(
        json!({"requests":budget.as_ref().map(|b|b.used),"request_limit":budget.as_ref().map(|b|b.limit),"usage":serde_json::from_str::<Value>(&usage).unwrap_or(json!([]))}),
    ))
}
// Keep the endpoint name distinct from the broadcast sender in main.
pub async fn events_handler(
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
            skill_selection: None,
            test_tool_executor: None,
            sandboxes: Arc::new(
                sandbox::Manager::open(
                    &std::env::temp_dir()
                        .join(format!("pot-inventory-{}", id()))
                        .join("product"),
                )
                .unwrap(),
            ),
            sandbox_id: None,
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
            api_budget: None,
            execution: None,
            agent_upstream: String::new(),
            is_worker: false,
            data: PathBuf::new(),
            cancel_notify: Arc::new(tokio::sync::Notify::new()),
            stop: tokio::sync::watch::channel(false).0,
            tasks: Arc::new(Mutex::new(tasks::Hub::default())),
            terminals: Arc::new(Mutex::new(terminal::Hub::default())),
            codex: Arc::new(agent::codex::Hosts::default()),
            agent_host: Arc::new(pot_agent::AgentHost),
            authorization: Arc::new(tokio::sync::Mutex::new(authorization::Hub::default())),
        }
    }
    #[test]
    fn snapshot_reports_persisted_binding_before_first_turn() {
        let s = service();
        agent::initialize_store(
            &mut s.inner.lock().unwrap().store,
            "main",
            std::path::Path::new("/tmp"),
            &execution::Config {
                model: "deepseek-v4-flash".into(),
                ..execution::Config::default()
            },
        )
        .unwrap();
        let binding = agent::current(&s.inner.lock().unwrap().store)
            .unwrap()
            .unwrap();
        assert_eq!(
            s.snapshot().unwrap().engine.engine,
            format!("{:?} · {}", binding.spec.engine, binding.spec.model)
        );
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
        s.api_budget = Some(budget::BudgetSettings {
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
