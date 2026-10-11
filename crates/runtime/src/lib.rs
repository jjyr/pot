//! Reusable backend application services. The executable and route assembly live in pot-server.
pub mod agent;
mod assistant_instructions;
mod budget;
pub mod control;
pub mod environment;
pub mod execution;
pub mod gateway;
pub mod host;
mod instructions;
pub mod orchestration;
pub mod permissions;
pub mod remote_environment;
pub mod sandbox;
pub mod sandbox_resources;
pub mod schedules;
mod services;
mod skills;
pub mod task_agent;
pub use services::OwnerScope;
use services::{
    AgentResources, AgentService, ConversationService, EnvironmentService, TaskService,
};
pub mod control_resources;
pub mod files;
mod store;
pub mod tasks;
pub mod terminal;
pub mod tool_bridge;
pub mod upgrade;
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
pub mod assistants;
mod bootstrap;
pub use bootstrap::{
    Backend, RuntimeConfig, initialize, initialize_with_agent_executor, initialize_with_providers,
};
pub mod desktop;
pub mod api {
    pub use crate::usage as accounting;
    pub use crate::{Service, cancel, events_handler, send, snapshot, usage};
    pub use crate::{
        agent, control, execution, gateway, orchestration, sandbox, schedules, tasks, terminal,
        tool_bridge,
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
    agent: AgentStatus,
    turn: Option<TurnState>,
    model_requests: u64,
}
#[derive(Clone)]
pub struct Service {
    scope: OwnerScope,
    conversation: ConversationService,
    agent_service: AgentService,
    environment_service: EnvironmentService,
    tasks: TaskService,
}
impl Service {
    pub fn owner_id(&self) -> &str {
        &self.usage_scope
    }
    pub(crate) fn conversation_id(&self) -> String {
        if self.is_worker {
            "main".into()
        } else {
            self.usage_scope.clone()
        }
    }
    /// Trusted in-process notification adapter. Only the owner main Store is eligible.
    pub fn notification_sequence(&self) -> Result<i64> {
        self.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .notification_sequence()
    }
    pub fn notifications(&self, after: i64) -> Result<Vec<(i64, Message)>> {
        if self.is_worker {
            return Ok(Vec::new());
        }
        self.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .notifications(after)
    }

    fn stopping(&self) -> bool {
        *self.stop.borrow()
    }
    async fn stop_requested(&self) {
        let mut stop = self.stop.subscribe();
        let _ = stop.wait_for(|stopping| *stopping).await;
    }

    fn snapshot(&self) -> Result<Snapshot> {
        let s = self.conversation.inner.lock().unwrap();
        self.snapshot_locked(&s)
    }
    fn snapshot_locked(&self, s: &Inner) -> Result<Snapshot> {
        Ok(Snapshot {
            budget: self.budget_for(&s.store)?,
            api_version: API_VERSION,
            boot_id: self.boot.clone(),
            cursor: s.store.cursor()?,
            agent: {
                let mut agent = s.agent.clone();
                if let Some(thread) = agent::current(&s.store)? {
                    agent.model = thread.config.model.clone();
                }
                if s.store
                    .get("execution.paused")?
                    .is_some_and(|v| !v.is_empty())
                {
                    agent.detail = Some("等待 Pot 额度恢复".into());
                }
                if s.turn.is_some()
                    && let Some(reason) = crate::remote_environment::waiting_reason(self)
                {
                    agent.detail = Some(reason);
                }
                agent
            },
            conversation_id: self.conversation_id(),
            messages: s.store.messages()?,
            turn: s.turn.clone(),
        })
    }
    fn budget(&self) -> Result<Option<RequestBudget>> {
        self.budget_for(&self.conversation.inner.lock().unwrap().store)
    }
    fn budget_for(&self, store: &Store) -> Result<Option<RequestBudget>> {
        if let Some(thread) = agent::current(store)? {
            return gateway::agent_budget(self, thread.config.params.max_output_tokens)?
                .status()
                .map(Some);
        }
        self.api_budget.as_ref().map(|b| b.status()).transpose()
    }
    fn publish_budget(&self) -> Result<()> {
        let _inner = self.conversation.inner.lock().unwrap();
        if let Some(budget) = self.budget_for(&_inner.store)? {
            let _ = self.conversation.events.send(Event {
                event_id: id(),
                cursor: None,
                kind: EventKind::Budget { budget },
            });
        }
        Ok(())
    }
    fn save_non_notifying(&self, message: Message) -> Result<()> {
        let event = self
            .conversation
            .inner
            .lock()
            .unwrap()
            .store
            .save_non_notifying(message)?;
        let _ = self.conversation.events.send(event);
        Ok(())
    }
    fn save(&self, message: Message) -> Result<()> {
        let mut inner = self.conversation.inner.lock().unwrap();
        let event = inner.store.save(message)?;
        let _ = self.conversation.events.send(event);
        Ok(())
    }
    fn phase(&self, phase: AgentPhase, detail: Option<String>) {
        let mut s = self.conversation.inner.lock().unwrap();
        s.agent.phase = phase;
        s.agent.detail = detail;
        let _ = self.conversation.events.send(Event {
            event_id: id(),
            cursor: None,
            kind: EventKind::Agent {
                status: s.agent.clone(),
            },
        });
    }
    fn turn(&self, turn: Option<TurnState>) {
        let mut inner = self.conversation.inner.lock().unwrap();
        inner.turn = turn.clone();
        let _ = self.conversation.events.send(Event {
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
    if req.conversation_id != s.conversation_id()
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
    if s.stopping() || s.conversation.commands.is_closed() {
        return Err(error(
            StatusCode::SERVICE_UNAVAILABLE,
            "worker_exited",
            "Restart environment",
        ));
    }
    let mut inner = s.conversation.inner.lock().unwrap();
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
        inner.agent.phase,
        AgentPhase::Ready | AgentPhase::NeedsConfiguration
    ) {
        return Err(error(
            StatusCode::CONFLICT,
            "agent_unavailable",
            "Configure the Agent or settle its current turn before continuing",
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
    let _ = s.conversation.events.send(event);
    let _ = s.conversation.events.send(Event {
        event_id: id(),
        cursor: None,
        kind: EventKind::Turn {
            turn: inner.turn.clone(),
        },
    });
    s.conversation.commands.send(req).map_err(|_| {
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
        let inner = s.conversation.inner.lock().unwrap();
        let rx = s.conversation.events.subscribe();
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
            "Cancellation requires an Agent thread",
        ));
    }
    let mut inner = s.conversation.inner.lock().unwrap();
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
    let _ = s.conversation.events.send(Event {
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
    let inner = s.conversation.inner.lock().unwrap();
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
        let data = std::env::temp_dir()
            .join(format!("pot-inventory-{}", id()))
            .join("product");
        let (events, _) = broadcast::channel(8);
        let (commands, _rx) = mpsc::unbounded_channel();
        Service {
            scope: OwnerScope {
                runtime_id: id(),
                assistant_id: "main".into(),
                skill_selection: None,
                remote_context: Default::default(),
                test_tool_executor: None,
                sandbox_id: None,
                environment_ref: None,
                usage: Arc::new(Mutex::new(
                    usage::Ledger::open(std::path::Path::new(":memory:")).unwrap(),
                )),
                usage_scope: "main".into(),
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
            },
            conversation: ConversationService {
                inner: Arc::new(Mutex::new(Inner {
                    store: Store::open(std::path::Path::new(":memory:")).unwrap(),
                    agent: AgentStatus {
                        model: "test".into(),
                        phase: AgentPhase::Ready,
                        detail: None,
                    },
                    turn: None,
                    model_requests: 0,
                })),
                events,
                commands,
            },
            agent_service: AgentService {
                executor: Arc::new(pot_agent::Agent::default()),
                resources: Arc::new(AgentResources::paths(&data)),
            },
            environment_service: EnvironmentService {
                remote: Default::default(),
                host_enabled: Arc::new(std::sync::atomic::AtomicBool::new(true)),
                host: Arc::new(environment::HostEnvironment::default()),
                permissions: Arc::new(permissions::Hub::memory()),
                sandboxes: Arc::new(sandbox::Manager::open(&data).unwrap()),
                terminals: Arc::new(Mutex::new(terminal::Hub::default())),
            },
            tasks: TaskService {
                hub: Arc::new(Mutex::new(tasks::Hub::default())),
                capacity: Arc::new(Mutex::new(tasks::Capacity::default())),
            },
        }
    }
    #[test]
    fn snapshot_reports_thread_configuration_before_first_turn() {
        let s = service();
        agent::initialize_store(
            &mut s.conversation.inner.lock().unwrap().store,
            "main",
            &execution::Config {
                model: "deepseek-v4-flash".into(),
                ..execution::Config::default()
            },
        )
        .unwrap();
        let thread = agent::current(&s.conversation.inner.lock().unwrap().store)
            .unwrap()
            .unwrap();
        assert_eq!(
            s.snapshot().unwrap().agent.model,
            thread.config.model.clone()
        );
    }
    #[tokio::test]
    async fn request_retry_is_idempotent_and_conflicting_reuse_is_rejected() {
        let mut s = service();
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.conversation.commands = tx;
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
        s.conversation.commands = tx;
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
