//! Pi is a guest subprocess. Rust owns the API, request ledger and UI transcript;
//! Pi owns its native JSONL session and built-in tools.
use super::*;

#[cfg(test)]
use std::time::Duration;

#[derive(Clone, Deserialize)]
pub struct Config {
    pub api_key: String,
    pub model: String,
    pub request_limit: u64,
    pub max_tokens: u64,
    pub max_requests_per_turn: u64,
    #[serde(default = "default_parallel_tasks")]
    pub max_parallel_tasks: usize,
}
pub fn default_parallel_tasks() -> usize {
    5
}
impl Default for Config {
    fn default() -> Self {
        Self {
            api_key: String::new(),
            model: String::new(),
            request_limit: 10000,
            max_tokens: 8192,
            max_requests_per_turn: 100,
            max_parallel_tasks: 5,
        }
    }
}
impl Config {
    pub fn load(path: &std::path::Path) -> Result<Self> {
        let c: Self = serde_json::from_slice(&std::fs::read(path)?)?;
        anyhow::ensure!(
            c.model == "deepseek-v4-flash" && !c.api_key.is_empty(),
            "Pi requires DeepSeek Flash configuration"
        );
        anyhow::ensure!(
            c.request_limit <= 1_000_000
                && (1..=9_007_199_254_740_991).contains(&c.max_tokens)
                && (1..=1000).contains(&c.max_requests_per_turn)
                && (1..=1000).contains(&c.max_parallel_tasks),
            "Invalid Pi request limits"
        );
        Ok(c)
    }
}
pub use pot_driver::private_json;
pub const MAX_MODEL_BODY: usize = 16 * 1024 * 1024;

pub async fn compaction_event(
    State(root): State<Service>,
    h: HeaderMap,
    Json(event): Json<Value>,
) -> ApiResult<Json<Value>> {
    let s = tasks::pi_service(&root, &h)?;
    let inner = s.inner.lock().unwrap();
    let turn = inner
        .turn
        .as_ref()
        .ok_or_else(|| error(StatusCode::CONFLICT, "no_turn", "No active turn"))?;
    if turn.cancelling && event["action"] == "start" {
        return Err(error(
            StatusCode::CONFLICT,
            "cancelling",
            "Turn is stopping",
        ));
    }
    s.usage
        .lock()
        .unwrap()
        .compaction(&s.usage_scope, &turn.user_message_id, &event, now())
        .map_err(internal)?;
    Ok(Json(json!({"ok":true})))
}

#[cfg(test)]
pub async fn gateway(
    state: State<Service>,
    headers: HeaderMap,
    body: Json<Value>,
) -> ApiResult<axum::response::Response> {
    crate::gateway::forward(state, headers, body).await
}
#[cfg(test)]
use pot_driver::pi::normalize;
#[cfg(test)]
pub async fn run(s: &Service, req: SendMessage) -> Result<()> {
    agent::run(s, req).await
}
impl pot_core::LegacyProfileConfig for Config {
    fn model(&self) -> &str {
        &self.model
    }
    fn max_tokens(&self) -> u64 {
        self.max_tokens
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::response::sse::Event as WireEvent;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[test]
    fn custom_token_limits_survive_configuration_and_protocol() {
        let path =
            std::env::temp_dir().join(format!("pot-token-config-{}.json", uuid::Uuid::new_v4()));
        for count in [1_000_000u64, 4_294_967_296, 9_007_199_254_740_991] {
            private_json(&path, &json!({"api_key":"fixture","model":"deepseek-v4-flash","request_limit":10000,"max_tokens":count,"max_requests_per_turn":100})).unwrap();
            let config = Config::load(&path).unwrap();
            assert_eq!(config.max_tokens, count);
            assert_eq!(config.max_parallel_tasks, 5);
            let budget = pot_protocol::RequestBudget {
                used: 0,
                limit: 10000,
                max_output_tokens: count,
            };
            let decoded: pot_protocol::RequestBudget =
                serde_json::from_value(serde_json::to_value(budget).unwrap()).unwrap();
            assert_eq!(decoded.max_output_tokens, count);
        }
        for count in [0u64, 9_007_199_254_740_992] {
            private_json(&path, &json!({"api_key":"fixture","model":"deepseek-v4-flash","request_limit":10000,"max_tokens":count,"max_requests_per_turn":100})).unwrap();
            assert!(Config::load(&path).is_err());
        }
        std::fs::remove_file(path).unwrap();
    }

    #[test]
    fn authoritative_message_and_tool_failures_replace_partial_output() {
        let s = crate::tests::service();
        let mut t = agent::transcript::Transcript::default();
        t.record(
            &s,
            &normalize(&json!({"type":"message_start","message":{"role":"assistant"}})).unwrap(),
        )
        .unwrap();
        t.record(&s,&normalize(&json!({"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial"}})).unwrap()).unwrap();
        t.record(&s,&normalize(&json!({"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"final"}],"stopReason":"error","errorMessage":"provider offline"}})).unwrap()).unwrap();
        assert_eq!(t.assistant.as_ref().unwrap().status, MessageStatus::Failed);
        assert!(!t.assistant.as_ref().unwrap().text.contains("partial"));
        assert!(t.failure.is_some());
        t.record(&s,&normalize(&json!({"type":"tool_execution_start","toolCallId":"t1","toolName":"bash","args":{"command":"false"}})).unwrap()).unwrap();
        t.record(&s,&normalize(&json!({"type":"tool_execution_end","toolCallId":"t1","toolName":"bash","isError":true,"result":{"content":[{"type":"text","text":"exit 1"}]}})).unwrap()).unwrap();
        assert_eq!(t.tools["t1"].status, MessageStatus::Failed);
    }
    #[tokio::test]
    async fn stale_cancel_cannot_stop_a_new_turn() {
        let mut s = crate::tests::service();
        s.pi = Some(Config {
            api_key: "fixture".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 1,
            max_tokens: 128,
            max_requests_per_turn: 1,
            max_parallel_tasks: 2,
        });
        s.turn(Some(TurnState {
            user_message_id: "new".into(),
            cancelling: false,
        }));
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let e = cancel(
            State(s.clone()),
            h,
            Json(CancelTurn {
                request_id: "c1".into(),
                user_message_id: "old".into(),
            }),
        )
        .await
        .unwrap_err();
        assert_eq!(e.0, StatusCode::CONFLICT);
        assert!(!s.snapshot().unwrap().turn.unwrap().cancelling);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    #[ignore = "Runs installed Pi against a local mock provider; set POT_TEST_PI_BIN"]
    async fn real_pi_tools_resume_cancel_and_budget() {
        let program = std::env::var("POT_TEST_PI_BIN").expect("POT_TEST_PI_BIN required");
        let data = std::env::temp_dir().join(format!("pot-pi-test-{}", id()));
        let workspace = data.join("workspace");
        std::fs::create_dir_all(&workspace).unwrap();
        let calls = Arc::new(AtomicUsize::new(0));
        let captured = Arc::new(Mutex::new(Vec::<Value>::new()));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let mut s = crate::tests::service();
        s.data = data.clone();
        s.workspace = workspace.clone();
        s.pi_program = program;
        s.pi = Some(Config {
            api_key: "local-fixture-only".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 5,
            max_tokens: 512,
            max_requests_per_turn: 3,
            max_parallel_tasks: 2,
        });
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: data.clone(),
            limit: 5,
            max_output_tokens: 512,
        });
        s.pi_upstream = format!("http://127.0.0.1:{port}/mock");
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        private_json(
            &data.join(RUNTIME_ENDPOINT_FILE),
            &json!(RuntimeEndpoint {
                api_version: API_VERSION,
                port,
                token: s.token.clone(),
                boot_id: s.boot.clone()
            }),
        )
        .unwrap();
        let count = calls.clone();
        let requests = captured.clone();
        let router=Router::new().route("/api/pi/v1/chat/completions",post(gateway)).with_state(s.clone())
            .route("/mock",post(move |Json(body):Json<Value>| {
                let count=count.clone();let requests=requests.clone();async move {
                    let index=count.fetch_add(1,Ordering::SeqCst);requests.lock().unwrap().push(body);
                    let (delta,reason)=match index {
                        0=>(json!({"role":"assistant","tool_calls":[{"index":0,"id":"write1","type":"function","function":{"name":"write","arguments":json!({"path":"note.txt","content":"APRICOT"}).to_string()}},{"index":1,"id":"bash1","type":"function","function":{"name":"bash","arguments":json!({"command":"cat note.txt"}).to_string()}}]}),"tool_calls"),
                        1=>(json!({"role":"assistant","content":"Created APRICOT."}),"stop"),
                        2=>(json!({"role":"assistant","content":"Remembered APRICOT."}),"stop"),
                        3=>(json!({"role":"assistant","tool_calls":[{"index":0,"id":"slow","type":"function","function":{"name":"bash","arguments":json!({"command":"echo $$ > child.pid; sleep 30; echo BAD > after-cancel.txt","timeout":60}).to_string()}}]}),"tool_calls"),
                        _=>(json!({"role":"assistant","content":"Recovered."}),"stop"),
                    };
                    let chunk=|d:Value,finish:Value|json!({"id":format!("mock-{index}"),"object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":d,"finish_reason":finish}]}).to_string();
                    let frames=vec![WireEvent::default().data(chunk(delta,Value::Null)),WireEvent::default().data(chunk(json!({}),json!(reason))),WireEvent::default().data(json!({"id":"usage","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}).to_string()),WireEvent::default().data("[DONE]")];
                    Sse::new(futures::stream::iter(frames.into_iter().map(Ok::<_,Infallible>)))
                }
            }));
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let submit = |n: &str| SendMessage {
            request_id: n.into(),
            conversation_id: "main".into(),
            text: format!("Task {n}; remember APRICOT"),
        };
        for n in ["first", "resume"] {
            let _ = send(State(s.clone()), h.clone(), Json(submit(n)))
                .await
                .unwrap();
            let req = rx.recv().await.unwrap();
            tokio::time::timeout(Duration::from_secs(30), run(&s, req))
                .await
                .unwrap()
                .unwrap();
            s.turn(None);
            assert!(
                !s.snapshot()
                    .unwrap()
                    .messages
                    .iter()
                    .any(|m| m.role == Role::System),
                "{:?}",
                s.snapshot().unwrap().messages
            );
        }
        assert_eq!(
            std::fs::read_to_string(workspace.join("note.txt")).unwrap(),
            "APRICOT"
        );
        assert!(data.join("pi-session.jsonl").exists());
        let requests = captured.lock().unwrap();
        assert_eq!(requests.len(), 3);
        assert!(
            requests[2]["messages"]
                .to_string()
                .contains("Created APRICOT")
        );
        assert!(
            requests
                .iter()
                .all(|r| r["max_tokens"] == 512 && r["thinking"]["type"] == "disabled")
        );
        drop(requests);
        let _ = send(State(s.clone()), h.clone(), Json(submit("cancel")))
            .await
            .unwrap();
        let req = rx.recv().await.unwrap();
        let worker = s.clone();
        let job = tokio::spawn(async move { run(&worker, req).await });
        tokio::time::timeout(Duration::from_secs(30), async {
            while !workspace.join("child.pid").exists() {
                tokio::time::sleep(Duration::from_millis(50)).await;
            }
        })
        .await
        .unwrap();
        let _ = cancel(
            State(s.clone()),
            h.clone(),
            Json(CancelTurn {
                request_id: "cancel-command".into(),
                user_message_id: s.snapshot().unwrap().turn.unwrap().user_message_id,
            }),
        )
        .await
        .unwrap();
        tokio::time::timeout(Duration::from_secs(15), job)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        s.turn(None);
        assert!(!workspace.join("after-cancel.txt").exists());
        let pid: i32 = std::fs::read_to_string(workspace.join("child.pid"))
            .unwrap()
            .trim()
            .parse()
            .unwrap();
        assert_ne!(
            unsafe { libc::kill(pid, 0) },
            0,
            "Bash child survived cancellation"
        );
        assert_eq!(
            s.inner
                .lock()
                .unwrap()
                .store
                .duplicate("cancel")
                .unwrap()
                .unwrap()
                .status,
            MessageStatus::Cancelled
        );
        assert_eq!(
            calls.load(Ordering::SeqCst),
            4,
            "No retry after cancellation"
        );
        let _ = send(State(s.clone()), h.clone(), Json(submit("recover")))
            .await
            .unwrap();
        run(&s, rx.recv().await.unwrap()).await.unwrap();
        s.turn(None);
        assert_eq!(calls.load(Ordering::SeqCst), 5);
        let before = s.snapshot().unwrap();
        assert_eq!(
            send(State(s.clone()), h, Json(submit("over-budget")))
                .await
                .unwrap_err()
                .0,
            StatusCode::TOO_MANY_REQUESTS
        );
        assert_eq!(s.snapshot().unwrap(), before);
        assert_eq!(s.budget().unwrap().unwrap().used, 5);
        // A missing native session must not silently create a replacement or
        // spend another request, even though there is budget for a new prompt.
        s.api_budget.as_mut().unwrap().limit = 6;
        std::fs::rename(
            data.join("pi-session.jsonl"),
            data.join("saved-session.jsonl"),
        )
        .unwrap();
        let _ = send(
            State(s.clone()),
            {
                let mut h = HeaderMap::new();
                h.insert("authorization", "Bearer test-token".parse().unwrap());
                h
            },
            Json(submit("missing-session")),
        )
        .await
        .unwrap();
        run(&s, rx.recv().await.unwrap()).await.unwrap();
        s.turn(None);
        assert_eq!(calls.load(Ordering::SeqCst), 5);
        assert!(!data.join("pi-session.jsonl").exists());
        assert_eq!(
            s.inner
                .lock()
                .unwrap()
                .store
                .duplicate("missing-session")
                .unwrap()
                .unwrap()
                .status,
            MessageStatus::Failed
        );
        server.abort();
        std::fs::remove_dir_all(data).unwrap();
    }
    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    #[ignore = "Runs installed Pi against a local mock provider; set POT_TEST_PI_BIN"]
    async fn real_pi_usage_pause_reopen_and_resume_without_replaying_tools() {
        let mut s = tasks::tests::fixture();
        s.pi_program = std::env::var("POT_TEST_PI_BIN").unwrap();
        s.usage = Arc::new(Mutex::new(
            usage::Ledger::open(&s.data.join("usage.sqlite")).unwrap(),
        ));
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: s.data.clone(),
            limit: 10,
            max_output_tokens: 128,
        });
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        s.pi_upstream = format!("http://127.0.0.1:{port}/mock");
        private_json(
            &s.data.join(RUNTIME_ENDPOINT_FILE),
            &json!(RuntimeEndpoint {
                api_version: API_VERSION,
                port,
                token: s.token.clone(),
                boot_id: s.boot.clone()
            }),
        )
        .unwrap();
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        let calls = Arc::new(AtomicUsize::new(0));
        let count = calls.clone();
        let ledger = s.usage.clone();
        let router=Router::new().route("/api/pi/v1/chat/completions",post(gateway)).with_state(s.clone()).route("/mock",post(move|Json(body):Json<Value>|{
            let count=count.clone();let ledger=ledger.clone();async move {
                let i=count.fetch_add(1,Ordering::SeqCst);
                let (delta,reason)=if i==0 {
                    ledger.lock().unwrap().set_policy(&usage::Policy{period:"day".into(),limit:1,..usage::Policy::default()}).unwrap();
                    (json!({"role":"assistant","tool_calls":[{"index":0,"id":"once","type":"function","function":{"name":"bash","arguments":json!({"command":"echo APRICOT >> once.txt; cat once.txt"}).to_string()}}]}),"tool_calls")
                }else{
                    assert!(body["messages"].to_string().contains("APRICOT"),"tool result must survive session reopen");
                    (json!({"role":"assistant","content":"Completed from saved tool result."}),"stop")
                };
                let chunk=|d:Value,r:Value|json!({"id":format!("usage-{i}"),"object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":d,"finish_reason":r}]}).to_string();
                let frames=vec![WireEvent::default().data(chunk(delta,Value::Null)),WireEvent::default().data(chunk(json!({}),json!(reason))),WireEvent::default().data(json!({"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_cache_hit_tokens":4}}).to_string()),WireEvent::default().data("[DONE]")];
                Sse::new(futures::stream::iter(frames.into_iter().map(Ok::<_,Infallible>)))
            }
        }));
        let server = tokio::spawn(async move {
            axum::serve(listener, router).await.unwrap();
        });
        let req = SendMessage {
            request_id: "paused-main".into(),
            conversation_id: "main".into(),
            text: "Write once, then report the result.".into(),
        };
        let _ = send(State(s.clone()), tasks::tests::headers(), Json(req))
            .await
            .unwrap();
        tokio::time::timeout(Duration::from_secs(30), run(&s, rx.recv().await.unwrap()))
            .await
            .unwrap()
            .unwrap();
        s.turn(None);
        assert_eq!(calls.load(Ordering::SeqCst), 1);
        assert!(usage::paused(&s).unwrap().is_some());
        assert_eq!(
            std::fs::read_to_string(s.workspace.join("once.txt")).unwrap(),
            "APRICOT\n"
        );
        // Restart product storage; the saved checkpoint is explicit and Pi has exited.
        s.inner.lock().unwrap().store = Store::open(&s.data.join("pot.sqlite")).unwrap();
        usage::resume_main(&s).unwrap();
        assert!(rx.try_recv().is_err());
        s.usage
            .lock()
            .unwrap()
            .set_policy(&usage::Policy::default())
            .unwrap();
        usage::resume_main(&s).unwrap();
        let req = rx.recv().await.unwrap();
        tokio::time::timeout(Duration::from_secs(30), run(&s, req))
            .await
            .unwrap()
            .unwrap();
        s.turn(None);
        assert!(usage::paused(&s).unwrap().is_none());
        assert_eq!(calls.load(Ordering::SeqCst), 2);
        assert_eq!(
            std::fs::read_to_string(s.workspace.join("once.txt")).unwrap(),
            "APRICOT\n"
        );
        let ledger = s.usage.lock().unwrap();
        assert_eq!(
            ledger
                .0
                .query_row("SELECT SUM(input+output) FROM requests", [], |r| r
                    .get::<_, u64>(0))
                .unwrap(),
            30
        );
        assert_eq!(
            ledger
                .0
                .query_row("SELECT COUNT(*) FROM tools", [], |r| r.get::<_, u64>(0))
                .unwrap(),
            1
        );
        drop(ledger);
        server.abort();
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    #[ignore = "Runs installed Pi against a local mock provider; set POT_TEST_PI_BIN"]
    async fn real_pi_compaction_usage_and_allowance_resume() {
        for scenario in ["normal", "pause", "cancel", "overflow"] {
            let pause = scenario == "pause";
            let cancel_summary = scenario == "cancel";
            let overflow = scenario == "overflow";
            let mut s = tasks::tests::fixture();
            s.pi_program = std::env::var("POT_TEST_PI_BIN").unwrap();
            s.usage = Arc::new(Mutex::new(
                usage::Ledger::open(&s.data.join("usage.sqlite")).unwrap(),
            ));
            s.api_budget = Some(deepseek::BudgetSettings {
                namespace: "pi-attempts",
                data: s.data.clone(),
                limit: 30,
                max_output_tokens: 256,
            });
            let (tx, mut rx) = mpsc::unbounded_channel();
            s.commands = tx;
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let port = listener.local_addr().unwrap().port();
            s.pi_upstream = format!("http://127.0.0.1:{port}/mock");
            private_json(
                &s.data.join(RUNTIME_ENDPOINT_FILE),
                &json!(RuntimeEndpoint {
                    api_version: API_VERSION,
                    port,
                    token: s.token.clone(),
                    boot_id: s.boot.clone()
                }),
            )
            .unwrap();
            let started = Arc::new(tokio::sync::Notify::new());
            let summary_started = started.clone();
            let calls = Arc::new(AtomicUsize::new(0));
            let count = calls.clone();
            let summaries = Arc::new(AtomicUsize::new(0));
            let summary_count = summaries.clone();
            let ledger = s.usage.clone();
            let router=Router::new().route("/api/pi/v1/chat/completions",post(gateway)).route("/api/pi/compactions",post(compaction_event)).with_state(s.clone())
                .route("/mock",post(move |Json(_body):Json<Value>|{
                    let summary_started=summary_started.clone();
                    let index=count.fetch_add(1,Ordering::SeqCst);
                    let is_summary:bool=ledger.lock().unwrap().0.query_row("SELECT compaction_id IS NOT NULL FROM requests ORDER BY rowid DESC LIMIT 1",[],|r|r.get(0)).unwrap();
                    if is_summary {summary_count.fetch_add(1,Ordering::SeqCst);}
                    if pause && index==0 { ledger.lock().unwrap().set_policy(&usage::Policy{period:"day".into(),limit:28006,..Default::default()}).unwrap(); }
                    async move {
                        let content=if is_summary {"Summary: preserve APRICOT and continue the latest request.".to_string()}else if index==0 {format!("Remembered APRICOT. {}", "history ".repeat(9000))}else{"Remembered APRICOT.".to_string()};
                        let chunk=|delta:Value,finish:Value|json!({"id":format!("compact-{index}"),"object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":delta,"finish_reason":finish}]}).to_string();
                        let usage=if index==0 {json!({"prompt_tokens":28000,"completion_tokens":5,"prompt_cache_hit_tokens":100})}else if is_summary {json!({"prompt_tokens":40,"completion_tokens":8,"prompt_cache_hit_tokens":4})}else{json!({"prompt_tokens":10,"completion_tokens":5})};
                        let frames=vec![WireEvent::default().data(chunk(json!({"role":"assistant","content":content}),Value::Null)),WireEvent::default().data(chunk(json!({}),json!(if overflow && index==0 { "length" } else { "stop" }))),WireEvent::default().data(json!({"choices":[],"usage":usage}).to_string()),WireEvent::default().data("[DONE]")];
                        Sse::new(async_stream::stream! {
                            if is_summary && cancel_summary && index==1 {
                                summary_started.notify_one();
                                std::future::pending::<()>().await;
                            }
                            for frame in frames { yield Ok::<_,Infallible>(frame); }
                        })
                    }
                }));
            let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
            let submit = |id: &str, text: String| SendMessage {
                request_id: id.into(),
                conversation_id: "main".into(),
                text,
            };
            let _ = send(
                State(s.clone()),
                tasks::tests::headers(),
                Json(submit(
                    "seed",
                    format!("Remember APRICOT. {}", "context ".repeat(5000)),
                )),
            )
            .await
            .unwrap();
            let req = rx.recv().await.unwrap();
            if cancel_summary {
                let runner = s.clone();
                let running = tokio::spawn(async move { run(&runner, req).await });
                tokio::time::timeout(Duration::from_secs(20), started.notified())
                    .await
                    .unwrap();
                let turn = s.snapshot().unwrap().turn.unwrap();
                let _ = cancel(
                    State(s.clone()),
                    tasks::tests::headers(),
                    Json(CancelTurn {
                        request_id: "cancel-compaction".into(),
                        user_message_id: turn.user_message_id,
                    }),
                )
                .await
                .unwrap();
                tokio::time::timeout(Duration::from_secs(20), running)
                    .await
                    .unwrap()
                    .unwrap()
                    .unwrap();
                let cancelled_count: u64 = s
                    .usage
                    .lock()
                    .unwrap()
                    .0
                    .query_row(
                        "SELECT COUNT(*) FROM compactions WHERE state='cancelled'",
                        [],
                        |r| r.get(0),
                    )
                    .unwrap();
                assert_eq!(cancelled_count, 1);
                assert!(usage::paused(&s).unwrap().is_none());
            } else {
                tokio::time::timeout(Duration::from_secs(40), run(&s, req))
                    .await
                    .unwrap()
                    .unwrap();
            }
            s.turn(None);
            if pause {
                assert!(usage::paused(&s).unwrap().is_some());
                assert_eq!(calls.load(Ordering::SeqCst), 1);
                s.inner.lock().unwrap().store = Store::open(&s.data.join("pot.sqlite")).unwrap();
                s.usage = Arc::new(Mutex::new(
                    usage::Ledger::open(&s.data.join("usage.sqlite")).unwrap(),
                ));
                // Keep the live test server's shared ledger; reopening above verifies durable storage.
                s.usage
                    .lock()
                    .unwrap()
                    .set_policy(&usage::Policy::default())
                    .unwrap();
                usage::resume_main(&s).unwrap();
                tokio::time::timeout(Duration::from_secs(40), run(&s, rx.recv().await.unwrap()))
                    .await
                    .unwrap()
                    .unwrap();
                s.turn(None);
                assert!(usage::paused(&s).unwrap().is_none());
            }
            let _ = send(
                State(s.clone()),
                tasks::tests::headers(),
                Json(submit("continue", "Continue and recall APRICOT.".into())),
            )
            .await
            .unwrap();
            tokio::time::timeout(Duration::from_secs(40), run(&s, rx.recv().await.unwrap()))
                .await
                .unwrap()
                .unwrap();
            s.turn(None);
            let summary_calls = summaries.load(Ordering::SeqCst) as u64;
            assert!(summary_calls > 0, "auto-compaction did not run");
            let l = s.usage.lock().unwrap();
            let (count, total): (u64, u64) =
                l.0.query_row(
                    "SELECT COUNT(*),SUM(COALESCE(input,0)+COALESCE(output,0)) FROM requests",
                    [],
                    |r| Ok((r.get(0)?, r.get(1)?)),
                )
                .unwrap();
            assert_eq!(count, calls.load(Ordering::SeqCst) as u64);
            assert_eq!(
                total,
                28005
                    + 48 * (summary_calls - u64::from(cancel_summary))
                    + 15 * (count - 1 - summary_calls)
            );
            let complete:u64=l.0.query_row("SELECT COUNT(*) FROM compactions WHERE state='complete' AND tokens_after IS NOT NULL",[],|r|r.get(0)).unwrap();
            assert!(complete > 0, "missing completed compaction");
            assert!(
                std::fs::read_to_string(s.data.join("pi-session.jsonl"))
                    .unwrap()
                    .contains("\"type\":\"compaction\"")
            );
            if overflow {
                let n:u64=l.0.query_row("SELECT COUNT(*) FROM compactions WHERE reason='overflow' AND state='complete'",[],|r|r.get(0)).unwrap();
                assert!(n > 0);
                assert!(
                    s.snapshot()
                        .unwrap()
                        .messages
                        .iter()
                        .filter(|m| m.role == Role::User)
                        .all(|m| m.status == MessageStatus::Complete)
                );
            }
            server.abort();
        }
    }
}
