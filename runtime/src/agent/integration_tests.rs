//! Real HTTP and real tools against deterministic provider streams; no model spend.
use super::*;
use axum::{
    Router,
    response::sse::{Event as WireEvent, Sse},
    routing::post,
};
use gateway::codecs::Protocol;
use std::convert::Infallible;
use std::sync::atomic::{AtomicUsize, Ordering};

fn wire(protocol: &str, calls: Vec<Value>, text: &str) -> Vec<Value> {
    match protocol {
        "openai_chat" => vec![
            json!({"choices":[{"delta":{"content":text,"tool_calls":calls.iter().enumerate().map(|(i,c)|json!({"index":i,"id":c["id"],"type":"function","function":{"name":c["name"],"arguments":c["arguments"].to_string()}})).collect::<Vec<_>>()},"finish_reason":null}]}),
            json!({"choices":[{"delta":{},"finish_reason":if calls.is_empty(){"stop"}else{"tool_calls"}}]}),
            json!({"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}),
        ],
        "openai_responses" => {
            let mut output = vec![
                json!({"id":"reason","type":"reasoning","summary":[],"encrypted_content":"opaque-reasoning"}),
            ];
            if !text.is_empty() {
                output.push(json!({"id":"message","type":"message","role":"assistant","content":[{"type":"output_text","text":text}]}));
            }
            output.extend(calls.iter().map(|c|json!({"id":format!("item-{}",c["id"]),"type":"function_call","call_id":c["id"],"name":c["name"],"arguments":c["arguments"].to_string()})));
            vec![
                json!({"type":"response.output_text.delta","delta":text}),
                json!({"type":"response.completed","response":{"status":"completed","output":output,"usage":{"input_tokens":10,"output_tokens":5}}}),
            ]
        }
        "anthropic_messages" => {
            let mut events = vec![
                json!({"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}),
                json!({"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}),
                json!({"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"brief"}}),
                json!({"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}),
                json!({"type":"content_block_stop","index":0}),
            ];
            events.extend([json!({"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}),json!({"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":text}}),json!({"type":"content_block_stop","index":1})]);
            for (i, c) in calls.iter().enumerate() {
                events.extend([json!({"type":"content_block_start","index":i+2,"content_block":{"type":"tool_use","id":c["id"],"name":c["name"],"input":{}}}),json!({"type":"content_block_delta","index":i+2,"delta":{"type":"input_json_delta","partial_json":c["arguments"].to_string()}}),json!({"type":"content_block_stop","index":i+2})]);
            }
            events.extend([json!({"type":"message_delta","delta":{"stop_reason":if calls.is_empty(){"end_turn"}else{"tool_use"}},"usage":{"output_tokens":5}}),json!({"type":"message_stop"})]);
            events
        }
        "gemini" => {
            let mut parts = vec![];
            if !text.is_empty() {
                parts.push(json!({"text":text}));
            }
            parts.extend(calls.iter().map(|c|json!({"functionCall":{"id":c["id"],"name":c["name"],"args":c["arguments"]},"thoughtSignature":"opaque-thought"})));
            vec![
                json!({"candidates":[{"content":{"role":"model","parts":parts},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}),
            ]
        }
        _ => panic!(),
    }
}
async fn fixture(
    protocol: &str,
    first: Vec<Value>,
) -> (
    Service,
    tokio::sync::mpsc::UnboundedReceiver<SendMessage>,
    tokio::task::JoinHandle<()>,
    Arc<Mutex<Vec<Value>>>,
) {
    let mut s = tasks::tests::fixture();
    // Model/receipt tests explicitly inject local tools in an isolated test root.
    // Product execution uses Runtime Sandbox routing, verified separately.
    s.test_tool_executor = Some(Arc::new(pot_tool_executor::LocalTools::default()));
    s.usage = Arc::new(Mutex::new(
        usage::Ledger::open(&s.data.join("usage-v2.sqlite")).unwrap(),
    ));
    initialize(&s).unwrap();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    execution::private_json(
        &s.data.join(RUNTIME_ENDPOINT_FILE),
        &json!(RuntimeEndpoint {
            api_version: API_VERSION,
            port: address.port(),
            token: s.token.clone(),
            boot_id: s.boot.clone()
        }),
    )
    .unwrap();
    let endpoint = if protocol == "gemini" {
        format!("http://{address}/models/{{model}}:streamGenerateContent")
    } else {
        format!("http://{address}/mock")
    };
    execution::private_json(&s.data.join("providers.json"),&json!([{"id":"mock","endpoint":endpoint,"allowed_hosts":["127.0.0.1"],"api_key":"upstream-secret","protocol":protocol}])).unwrap();
    let mut profile = Profile::api(&execution::Config {
        model: "fixture".into(),
        max_tokens: 512,
        ..Default::default()
    });
    profile.provider = "mock".into();
    profile.auth_ref = "providers.json:mock".into();
    let spec = gateway::resolve(&s, &profile).unwrap();
    assign_new(&mut s.inner.lock().unwrap().store, spec).unwrap();
    let count = Arc::new(AtomicUsize::new(0));
    let captured = Arc::new(Mutex::new(vec![]));
    let requests = captured.clone();
    let protocol = protocol.to_owned();
    let ledger = s.usage.clone();
    let handler = move |h: HeaderMap, Json(body): Json<Value>| {
        let i = count.fetch_add(1, Ordering::SeqCst);
        requests.lock().unwrap().push(body.clone());
        let protocol = protocol.clone();
        let first = first.clone();
        let ledger = ledger.clone();
        async move {
            if i == 0 && first.iter().any(|c| c["pause_after_first"] == true) {
                ledger
                    .lock()
                    .unwrap()
                    .set_policy(&usage::Policy {
                        period: "day".into(),
                        limit: 1,
                        ..Default::default()
                    })
                    .unwrap();
            }
            match Protocol::from_name(&protocol).unwrap() {
                Protocol::AnthropicMessages => assert_eq!(h["x-api-key"], "upstream-secret"),
                Protocol::Gemini => assert_eq!(h["x-goog-api-key"], "upstream-secret"),
                _ => assert_eq!(h["authorization"], "Bearer upstream-secret"),
            };
            let events = wire(
                &protocol,
                if i == 0 {
                    first
                } else if (i == 1 || i == 2) && first.iter().any(|c| c["plugin_session"] == true) {
                    let tool = body["messages"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .rev()
                        .find(|m| m["role"] == "tool")
                        .unwrap();
                    let result: Value =
                        serde_json::from_str(tool["content"].as_str().unwrap()).unwrap();
                    let args = if i == 1 {
                        json!({"session_id":result["session_id"],"code":"nodeRepl.write(43)"})
                    } else {
                        json!({"session_id":result["session_id"],"reset":true})
                    };
                    vec![json!({"id":format!("plugin-{i}"),"name":"cua_repl","arguments":args})]
                } else if i == 1 && first.iter().any(|c| c["process_session"] == true) {
                    let content = body["messages"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .rev()
                        .find(|m| m["role"] == "tool")
                        .unwrap()["content"]
                        .as_str()
                        .unwrap();
                    let result: Value = serde_json::from_str(content).unwrap();
                    vec![
                        json!({"id":"stdin","name":"write_stdin","arguments":{"session_id":result["session_id"],"chars":"APRICOT\n","close_stdin":true,"yield_time_ms":1000}}),
                    ]
                } else if i == 1 && first.iter().any(|c| c["repeat_after_first"] == true) {
                    first
                        .into_iter()
                        .map(|mut c| {
                            if c["conflict_after_first"] == true {
                                c["arguments"]["cmd"] = json!("echo bad > duplicate-effect.txt");
                            }
                            c
                        })
                        .collect()
                } else {
                    vec![]
                },
                if i == 0 {
                    "Working."
                } else {
                    "APRICOT completed."
                },
            );
            Sse::new(futures::stream::iter(events.into_iter().map(|e| {
                Ok::<_, Infallible>(WireEvent::default().data(e.to_string()))
            })))
        }
    };
    let router = Router::new()
        .route("/gw/{provider}/{*path}", post(gateway::routed_protocol))
        .route("/api/v1/mcp", post(tool_bridge::mcp))
        .route("/api/agent/compactions", post(execution::compaction_event))
        .with_state(s.clone())
        .route("/mock", post(handler.clone()))
        .route("/models/fixture:streamGenerateContent", post(handler));
    let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
    let (tx, rx) = mpsc::unbounded_channel();
    s.commands = tx;
    (s, rx, server, captured)
}
async fn turn(s: &Service, rx: &mut mpsc::UnboundedReceiver<SendMessage>, id: &str) {
    let _ = crate::send(
        State(s.clone()),
        tasks::tests::headers(),
        Json(SendMessage {
            request_id: id.into(),
            conversation_id: "main".into(),
            text: "Create APRICOT and report it.".into(),
        }),
    )
    .await
    .unwrap();
    tokio::time::timeout(
        std::time::Duration::from_secs(20),
        run(s, rx.recv().await.unwrap()),
    )
    .await
    .unwrap()
    .unwrap();
    s.turn(None);
}
#[tokio::test]
async fn native_agent_four_protocols_tools_usage_and_reopen() {
    for protocol in [
        "openai_chat",
        "openai_responses",
        "anthropic_messages",
        "gemini",
    ] {
        let calls = vec![
            json!({"id":"patch","name":"apply_patch","arguments":{"patch":"*** Begin Patch\n*** Add File: note.txt\n+APRICOT\n*** End Patch"}}),
            json!({"id":"exec","name":"exec_command","arguments":{"cmd":"cat note.txt; echo once >> effects.txt"}}),
        ];
        let (s, mut rx, server, captured) = fixture(protocol, calls).await;
        turn(&s, &mut rx, "first").await;
        assert_eq!(
            std::fs::read_to_string(s.workspace.join("note.txt")).unwrap(),
            "APRICOT\n"
        );
        assert!(
            s.snapshot()
                .unwrap()
                .messages
                .iter()
                .any(|m| m.role == Role::Assistant && m.text == "APRICOT completed."),
            "{:?}",
            s.snapshot().unwrap().messages
        );
        s.inner.lock().unwrap().store = Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        turn(&s, &mut rx, "reopen").await;
        assert_eq!(
            std::fs::read_to_string(s.workspace.join("effects.txt")).unwrap(),
            "once\n"
        );
        let captured = captured.lock().unwrap();
        assert_eq!(captured.len(), 3);
        assert!(captured[1].to_string().contains("APRICOT"));
        assert!(captured[2].to_string().contains("APRICOT completed."));
        let signature = match protocol {
            "openai_responses" => Some("opaque-reasoning"),
            "anthropic_messages" => Some("opaque-signature"),
            "gemini" => Some("opaque-thought"),
            _ => None,
        };
        if let Some(sig) = signature {
            assert!(captured[1].to_string().contains(sig));
        }
        let ledger = s.usage.lock().unwrap();
        let tokens: u64 = ledger
            .0
            .query_row("SELECT SUM(input+output) FROM requests", [], |r| r.get(0))
            .unwrap();
        assert_eq!(tokens, 45);
        drop(ledger);
        drop(captured);
        let state = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
            .unwrap()
            .unwrap();
        assert!(!state.to_string().contains("upstream-secret"));
        server.abort();
        std::fs::remove_dir_all(s.data).unwrap();
    }
}
#[tokio::test]
async fn native_agent_cancel_kills_shell_descendants() {
    let calls = vec![
        json!({"id":"slow","name":"exec_command","arguments":{"cmd":"echo $$ > child.pid; sleep 10; echo bad > after-cancel.txt","timeout_ms":30000}}),
    ];
    let (s, mut rx, server, _) = fixture("openai_chat", calls).await;
    let _ = crate::send(
        State(s.clone()),
        tasks::tests::headers(),
        Json(SendMessage {
            request_id: "cancel-turn".into(),
            conversation_id: "main".into(),
            text: "slow".into(),
        }),
    )
    .await
    .unwrap();
    let req = rx.recv().await.unwrap();
    let runner = s.clone();
    let job = tokio::spawn(async move { run(&runner, req).await });
    let pid: i32 = tokio::time::timeout(std::time::Duration::from_secs(10), async {
        loop {
            if let Some(pid) = std::fs::read_to_string(s.workspace.join("child.pid"))
                .ok()
                .and_then(|text| text.trim().parse::<i32>().ok())
                .filter(|pid| *pid > 0)
            {
                break pid;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    let _ = crate::cancel(
        State(s.clone()),
        tasks::tests::headers(),
        Json(CancelTurn {
            request_id: "cancel".into(),
            user_message_id: s.snapshot().unwrap().turn.unwrap().user_message_id,
        }),
    )
    .await
    .unwrap();
    tokio::time::timeout(std::time::Duration::from_secs(8), job)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(!s.workspace.join("after-cancel.txt").exists());
    assert_eq!(unsafe { libc::kill(pid, 0) }, -1);
    s.turn(None);
    turn(&s, &mut rx, "after-cancel").await;
    assert!(!s.workspace.join("after-cancel.txt").exists());
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_allowance_checkpoint_resumes_without_tool_replay() {
    let calls = vec![
        json!({"id":"once","name":"exec_command","arguments":{"cmd":"echo APRICOT >> once.txt"},"pause_after_first":true}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    turn(&s, &mut rx, "pause").await;
    // Use the actual request size to confirm the fixture stopped at the desired boundary.
    assert_eq!(
        std::fs::read_to_string(s.workspace.join("once.txt")).unwrap(),
        "APRICOT\n"
    );
    assert!(usage::paused(&s).unwrap().is_some());
    assert_eq!(captured.lock().unwrap().len(), 1);
    s.inner.lock().unwrap().store = Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
    s.usage
        .lock()
        .unwrap()
        .set_policy(&usage::Policy::default())
        .unwrap();
    usage::resume_main(&s).unwrap();
    run(&s, rx.recv().await.unwrap()).await.unwrap();
    s.turn(None);
    assert!(usage::paused(&s).unwrap().is_none());
    assert_eq!(captured.lock().unwrap().len(), 2);
    assert_eq!(
        std::fs::read_to_string(s.workspace.join("once.txt")).unwrap(),
        "APRICOT\n"
    );
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}
#[tokio::test]
async fn native_agent_compaction_is_persisted_and_metered() {
    let calls = vec![
        json!({"id":"once","name":"exec_command","arguments":{"cmd":"echo APRICOT >> once.txt"}}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    turn(&s, &mut rx, "seed").await;
    let mut value = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
        .unwrap()
        .unwrap();
    let last = value["messages"]
        .as_array_mut()
        .unwrap()
        .last_mut()
        .unwrap();
    last["text"] = json!("old context ".repeat(6000));
    last["replay"]["content"] = last["text"].clone();
    pot_storage::bindings::save_context(&s.inner.lock().unwrap().store, &value).unwrap();
    turn(&s, &mut rx, "compact").await;
    assert_eq!(captured.lock().unwrap().len(), 4);
    let ledger = s.usage.lock().unwrap();
    let operations:u64=ledger.0.query_row("SELECT COUNT(*) FROM compactions WHERE state='complete' AND tokens_after<tokens_before",[],|r|r.get(0)).unwrap();
    assert_eq!(operations, 1);
    let tagged: u64 = ledger
        .0
        .query_row(
            "SELECT COUNT(*) FROM requests WHERE compaction_id IS NOT NULL",
            [],
            |r| r.get(0),
        )
        .unwrap();
    assert_eq!(tagged, 1);
    drop(ledger);
    assert_eq!(
        std::fs::read_to_string(s.workspace.join("once.txt")).unwrap(),
        "APRICOT\n"
    );
    let state = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
        .unwrap()
        .unwrap();
    assert!(state.to_string().contains("Previous context summary"));
    assert!(!state.to_string().contains("old context old context"));
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}
#[tokio::test]
async fn native_agent_interrupted_tool_receipt_is_not_replayed() {
    let (s, mut rx, server, _) = fixture("openai_chat", vec![]).await;
    turn(&s, &mut rx, "seed").await;
    let mut value = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
        .unwrap()
        .unwrap();
    let call =
        json!({"id":"unknown","name":"exec_command","arguments":{"cmd":"echo bad > replayed.txt"}});
    value["pending"] = json!([call]);
    value["started"] = json!("unknown");
    value["messages"].as_array_mut().unwrap().push(json!({"role":"assistant","text":"","calls":[call],"call_id":"","replay":{"role":"assistant","content":null,"tool_calls":[{"id":"unknown","type":"function","function":{"name":"exec_command","arguments":call["arguments"].to_string()}}]}}));
    pot_storage::bindings::save_context(&s.inner.lock().unwrap().store, &value).unwrap();
    turn(&s, &mut rx, "recover").await;
    assert!(!s.workspace.join("replayed.txt").exists());
    let state = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
        .unwrap()
        .unwrap();
    assert!(
        state
            .to_string()
            .contains("external effects may have occurred")
    );
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_duplicate_tool_ids_reuse_receipts_and_reject_conflicts() {
    for conflict in [false, true] {
        let calls = vec![
            json!({"id":"once","name":"exec_command","arguments":{"cmd":"echo APRICOT >> once.txt"},"repeat_after_first":true,"conflict_after_first":conflict}),
        ];
        let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
        turn(&s, &mut rx, "receipts").await;
        assert_eq!(
            std::fs::read_to_string(s.workspace.join("once.txt")).unwrap(),
            "APRICOT\n"
        );
        assert!(!s.workspace.join("duplicate-effect.txt").exists());
        let requests = captured.lock().unwrap();
        assert_eq!(requests.len(), 3);
        if conflict {
            assert!(
                requests[2]
                    .to_string()
                    .contains("Conflicting repeated tool call ID")
            );
        }
        drop(requests);
        server.abort();
        std::fs::remove_dir_all(s.data).unwrap();
    }
}
#[tokio::test]
async fn native_agent_dispatches_pot_task_tools_through_shared_authorization() {
    let calls = vec![
        json!({"id":"delegate","name":"create_task","arguments":{"text":"Research APRICOT with sources"}}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    turn(&s, &mut rx, "delegate").await;
    let tasks = tasks::list(State(s.clone()), tasks::tests::headers())
        .await
        .unwrap()
        .0;
    assert_eq!(tasks.len(), 1);
    assert!(tasks[0].title.contains("APRICOT"));
    assert!(captured.lock().unwrap()[1].to_string().contains("task"));
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_shell_timeout_cleans_process_and_returns_tool_error() {
    let calls = vec![
        json!({"id":"timeout","name":"exec_command","arguments":{"cmd":"echo $$ > timeout.pid; sleep 2; echo bad > timeout-effect.txt","timeout_ms":30}}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    turn(&s, &mut rx, "timeout").await;
    let pid: i32 = std::fs::read_to_string(s.workspace.join("timeout.pid"))
        .unwrap()
        .trim()
        .parse()
        .unwrap();
    assert_eq!(unsafe { libc::kill(pid, 0) }, -1);
    assert!(!s.workspace.join("timeout-effect.txt").exists());
    assert!(captured.lock().unwrap()[1].to_string().contains("timeout"));
    assert!(
        s.snapshot()
            .unwrap()
            .messages
            .iter()
            .any(|m| m.role == Role::Tool && m.status == MessageStatus::Failed)
    );
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

fn configure_agent(s: &Service, settings: pot_core::AgentSettings, cap: u64) {
    let mut profile = Profile::api(&execution::Config {
        model: "fixture".into(),
        max_tokens: cap,
        ..Default::default()
    });
    profile.provider = "mock".into();
    profile.auth_ref = "providers.json:mock".into();
    profile.params.agent = settings;
    let spec = gateway::resolve(s, &profile).unwrap();
    assign_new(&mut s.inner.lock().unwrap().store, spec).unwrap();
}

#[tokio::test]
async fn native_agent_pipe_session_input_output_and_turn_cleanup() {
    let calls = vec![
        json!({"id":"interactive","name":"exec_command","process_session":true,"arguments":{"cmd":"printf READY; read line; printf '%s' \"$line\" > interactive.txt; printf DONE","yield_time_ms":20}}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    turn(&s, &mut rx, "interactive").await;
    assert_eq!(
        std::fs::read_to_string(s.workspace.join("interactive.txt")).unwrap(),
        "APRICOT"
    );
    let bodies = captured.lock().unwrap();
    assert_eq!(bodies.len(), 3);
    assert!(bodies[2].to_string().contains("DONE"));
    drop(bodies);
    let diag = s
        .inner
        .lock()
        .unwrap()
        .store
        .get("execution.diagnostics")
        .unwrap()
        .unwrap();
    assert!(diag.contains("write_stdin"));
    assert!(!diag.contains("printf"));
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_yielded_process_is_killed_at_turn_end() {
    let calls = vec![
        json!({"id":"background","name":"exec_command","arguments":{"cmd":"echo $$ > yielded.pid; sleep 10; echo bad > leaked.txt","yield_time_ms":30}}),
    ];
    let (s, mut rx, server, _) = fixture("openai_chat", calls).await;
    turn(&s, &mut rx, "background").await;
    let pid: i32 = std::fs::read_to_string(s.workspace.join("yielded.pid"))
        .unwrap()
        .trim()
        .parse()
        .unwrap();
    assert_eq!(unsafe { libc::kill(pid, 0) }, -1);
    assert!(!s.workspace.join("leaked.txt").exists());
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_profile_limits_prompt_and_tool_filter_are_effective() {
    let calls = vec![
        json!({"id":"denied","name":"exec_command","arguments":{"cmd":"echo bad > forbidden.txt"}}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    configure_agent(
        &s,
        pot_core::AgentSettings {
            context_window: 64000,
            auto_compact: false,
            system_prompt: Some("A configurable system prompt.".into()),
            builtin_tools: vec!["apply_patch".into()],
            allowed_tools: Some(vec!["apply_patch".into()]),
            ..Default::default()
        },
        128,
    );
    turn(&s, &mut rx, "configured").await;
    assert!(!s.workspace.join("forbidden.txt").exists());
    let bodies = captured.lock().unwrap();
    assert_eq!(bodies[0]["max_tokens"], 128);
    assert_eq!(
        bodies[0]["messages"][0]["content"],
        "A configurable system prompt."
    );
    assert_eq!(bodies[0]["tools"].as_array().unwrap().len(), 1);
    assert_eq!(bodies[0]["tools"][0]["function"]["name"], "apply_patch");
    assert!(bodies[1].to_string().contains("Tool unavailable"));
    drop(bodies);
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

struct CustomAgent {
    estimates: Arc<AtomicUsize>,
}
impl pot_agent::AgentExtensions for CustomAgent {
    fn tools(&self, _: &pot_core::ExecutionContext) -> Result<Vec<Value>> {
        Ok(vec![
            json!({"name":"custom_tool","description":"Custom code tool","inputSchema":{"type":"object","properties":{}}}),
        ])
    }
    fn execute<'a>(
        &'a self,
        context: &'a pot_core::ExecutionContext,
        name: &'a str,
        _: &'a Value,
    ) -> futures::future::BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            anyhow::ensure!(name == "custom_tool", "Unknown tool");
            std::fs::write(context.workspace.join("custom.txt"), "custom effect")?;
            Ok(json!({"custom":true}))
        })
    }
    fn prepare_context(
        &self,
        _: &pot_core::ExecutionContext,
        messages: &mut Vec<pot_gateway::model::Message>,
    ) -> Result<()> {
        messages.push(pot_gateway::model::Message::text(
            "user",
            "CODE CONTEXT HOOK".into(),
        ));
        Ok(())
    }
    fn estimate_tokens(
        &self,
        _: &pot_gateway::codecs::Protocol,
        _: &str,
        _: &str,
        _: &[pot_gateway::model::Message],
        _: &[Value],
    ) -> Result<u64> {
        self.estimates.fetch_add(1, Ordering::SeqCst);
        Ok(10)
    }
}
#[tokio::test]
async fn native_agent_code_extensions_tools_context_and_token_estimation() {
    let (mut s, mut rx, server, captured) = fixture(
        "openai_chat",
        vec![json!({"id":"custom","name":"custom_tool","arguments":{}})],
    )
    .await;
    let estimates = Arc::new(AtomicUsize::new(0));
    s.agent_host = Arc::new(pot_agent::AgentHost::with_extensions(Arc::new(
        CustomAgent {
            estimates: estimates.clone(),
        },
    )));
    configure_agent(
        &s,
        pot_core::AgentSettings {
            context_window: 1000,
            context_reserve: 10,
            builtin_tools: vec![],
            allowed_tools: Some(vec!["custom_tool".into()]),
            ..Default::default()
        },
        128,
    );
    turn(&s, &mut rx, "custom").await;
    assert_eq!(
        std::fs::read_to_string(s.workspace.join("custom.txt")).unwrap(),
        "custom effect"
    );
    assert!(
        captured.lock().unwrap()[0]
            .to_string()
            .contains("CODE CONTEXT HOOK")
    );
    assert!(estimates.load(Ordering::SeqCst) >= 2);
    let state = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
        .unwrap()
        .unwrap();
    assert!(!state.to_string().contains("CODE CONTEXT HOOK"));
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_pause_steering_resume_and_cancellation_are_scoped() {
    let calls = vec![
        json!({"id":"gate","name":"exec_command","arguments":{"cmd":"touch steering-gate; sleep 0.2; echo APRICOT","timeout_ms":2000}}),
    ];
    let (s, mut rx, server, captured) = fixture("openai_chat", calls).await;
    let _ = crate::send(
        State(s.clone()),
        tasks::tests::headers(),
        Json(SendMessage {
            request_id: "steer-turn".into(),
            conversation_id: "main".into(),
            text: "Start".into(),
        }),
    )
    .await
    .unwrap();
    let target = s
        .inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .unwrap()
        .user_message_id
        .clone();
    let running = s.clone();
    let req = rx.recv().await.unwrap();
    let job = tokio::spawn(async move { run(&running, req).await });
    tokio::time::timeout(std::time::Duration::from_secs(5), async {
        while !s.workspace.join("steering-gate").exists() {
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    use crate::control::{Steer, TurnControl};
    let _ = crate::control::control(
        State(s.clone()),
        tasks::tests::headers(),
        Json(TurnControl {
            user_message_id: target.clone(),
            action: "pause".into(),
        }),
    )
    .await
    .unwrap();
    let steer = || Steer {
        request_id: "guide-once".into(),
        user_message_id: target.clone(),
        text: "Include the guidance APRICOT".into(),
    };
    assert!(
        !crate::control::steer(State(s.clone()), tasks::tests::headers(), Json(steer()))
            .await
            .unwrap()
            .0
            .duplicate
    );
    assert!(
        crate::control::steer(State(s.clone()), tasks::tests::headers(), Json(steer()))
            .await
            .unwrap()
            .0
            .duplicate
    );
    let mut conflict = steer();
    conflict.text = "different".into();
    assert_eq!(
        crate::control::steer(State(s.clone()), tasks::tests::headers(), Json(conflict))
            .await
            .unwrap_err()
            .0,
        StatusCode::CONFLICT
    );
    let mut stale = steer();
    stale.user_message_id = "stale".into();
    assert_eq!(
        crate::control::steer(State(s.clone()), tasks::tests::headers(), Json(stale))
            .await
            .unwrap_err()
            .0,
        StatusCode::CONFLICT
    );
    // Reopen the durable queue while the turn is active, then wait for actual pause.
    s.inner.lock().unwrap().store = Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
    tokio::time::timeout(std::time::Duration::from_secs(5), async {
        loop {
            if s.inner.lock().unwrap().engine.detail.as_deref() == Some("已暂停，等待继续")
            {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(captured.lock().unwrap().len(), 1);
    let _ = crate::control::control(
        State(s.clone()),
        tasks::tests::headers(),
        Json(TurnControl {
            user_message_id: target.clone(),
            action: "resume".into(),
        }),
    )
    .await
    .unwrap();
    tokio::time::timeout(std::time::Duration::from_secs(5), job)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(
        captured.lock().unwrap()[1]
            .to_string()
            .contains("Include the guidance APRICOT")
    );
    let state = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
        .unwrap()
        .unwrap();
    assert_eq!(
        state["messages"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|m| m["role"] == "user" && m["text"] == "Include the guidance APRICOT")
            .count(),
        1
    );
    assert!(
        crate::control::steer(State(s.clone()), tasks::tests::headers(), Json(steer()))
            .await
            .is_err()
    );
    s.turn(None);
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_oversized_context_stops_before_network_when_compaction_disabled() {
    let (s, mut rx, server, captured) = fixture("openai_chat", vec![]).await;
    configure_agent(
        &s,
        pot_core::AgentSettings {
            context_window: 100,
            context_reserve: 1,
            auto_compact: false,
            builtin_tools: vec![],
            allowed_tools: Some(vec![]),
            ..Default::default()
        },
        50,
    );
    let _ = crate::send(
        State(s.clone()),
        tasks::tests::headers(),
        Json(SendMessage {
            request_id: "too-large".into(),
            conversation_id: "main".into(),
            text: "A large context".repeat(100),
        }),
    )
    .await
    .unwrap();
    run(&s, rx.recv().await.unwrap()).await.unwrap();
    assert!(
        s.snapshot()
            .unwrap()
            .messages
            .iter()
            .any(|m| m.status == MessageStatus::Failed && m.text.contains("Context exceeds"))
    );
    assert!(captured.lock().unwrap().is_empty());
    s.turn(None);
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_cancel_while_manually_paused_settles_without_sampling() {
    let (s, mut rx, server, captured) = fixture("openai_chat", vec![]).await;
    let _ = crate::send(
        State(s.clone()),
        tasks::tests::headers(),
        Json(SendMessage {
            request_id: "pause-cancel".into(),
            conversation_id: "main".into(),
            text: "Start".into(),
        }),
    )
    .await
    .unwrap();
    let target = s
        .inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .unwrap()
        .user_message_id
        .clone();
    let _ = crate::control::control(
        State(s.clone()),
        tasks::tests::headers(),
        Json(crate::control::TurnControl {
            user_message_id: target.clone(),
            action: "pause".into(),
        }),
    )
    .await
    .unwrap();
    let req = rx.recv().await.unwrap();
    let running = s.clone();
    let job = tokio::spawn(async move { run(&running, req).await });
    tokio::time::timeout(std::time::Duration::from_secs(5), async {
        loop {
            if s.inner.lock().unwrap().engine.detail.as_deref() == Some("已暂停，等待继续")
            {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    let mut unauthorized = HeaderMap::new();
    unauthorized.insert("authorization", "Bearer invalid".parse().unwrap());
    assert_eq!(
        crate::control::control(
            State(s.clone()),
            unauthorized,
            Json(crate::control::TurnControl {
                user_message_id: target.clone(),
                action: "resume".into()
            })
        )
        .await
        .unwrap_err()
        .0,
        StatusCode::UNAUTHORIZED
    );
    let _ = crate::cancel(
        State(s.clone()),
        tasks::tests::headers(),
        Json(CancelTurn {
            request_id: "stop-paused".into(),
            user_message_id: target,
        }),
    )
    .await
    .unwrap();
    tokio::time::timeout(std::time::Duration::from_secs(5), job)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(captured.lock().unwrap().is_empty());
    s.turn(None);
    server.abort();
    std::fs::remove_dir_all(s.data).unwrap();
}

#[tokio::test]
async fn native_agent_display_duplicates_do_not_trigger_compaction_or_extra_usage() {
    for protocol in [
        "openai_chat",
        "openai_responses",
        "anthropic_messages",
        "gemini",
    ] {
        let calls =
            vec![json!({"id":"once","name":"exec_command","arguments":{"cmd":"echo APRICOT"}})];
        let (s, mut rx, server, captured) = fixture(protocol, calls).await;
        turn(&s, &mut rx, "seed-meter").await;
        let mut state = pot_storage::bindings::load_context(&s.inner.lock().unwrap().store)
            .unwrap()
            .unwrap();
        assert!(state["usage_anchor"].is_object());
        for message in state["messages"].as_array_mut().unwrap() {
            if message["role"] == "assistant" {
                assert_eq!(message["usage"], json!({"input":10,"output":5}));
                message["text"] = json!("DISPLAY_ONLY_DUPLICATE ".repeat(6000));
            }
        }
        pot_storage::bindings::save_context(&s.inner.lock().unwrap().store, &state).unwrap();
        s.inner.lock().unwrap().store = Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        turn(&s, &mut rx, "after-meter-reopen").await;
        let requests = captured.lock().unwrap();
        assert_eq!(
            requests.len(),
            3,
            "{protocol}: unexpected compaction request"
        );
        assert!(!requests[2].to_string().contains("DISPLAY_ONLY_DUPLICATE"));
        drop(requests);
        let ledger = s.usage.lock().unwrap();
        let totals: (u64, u64) = ledger
            .0
            .query_row("SELECT SUM(input), SUM(output) FROM requests", [], |r| {
                Ok((r.get(0)?, r.get(1)?))
            })
            .unwrap();
        assert_eq!(
            totals,
            (30, 15),
            "Context estimates must not become billed usage"
        );
        let compactions: u64 = ledger
            .0
            .query_row("SELECT COUNT(*) FROM compactions", [], |r| r.get(0))
            .unwrap();
        assert_eq!(compactions, 0);
        drop(ledger);
        server.abort();
        std::fs::remove_dir_all(s.data).unwrap();
    }
}

#[tokio::test]
async fn native_pot_host_tools_wait_for_owner_decisions_and_persist_only_explicit_grants() {
    for action in ["deny", "allow_once", "allow_persistent"] {
        let directory = std::env::temp_dir().join(format!("pot-owner-{}", id()));
        std::fs::create_dir(&directory).unwrap();
        let path = directory.canonicalize().unwrap().join("file.txt");
        let (mut s, mut rx, server, captured) = fixture("openai_chat", vec![json!({"id":"host","name":"exec_command","arguments":{"cmd":"printf owner-approved > file.txt","workdir":directory}})]).await;
        let permissions_file = s.sandboxes.agent_home.join("permissions.json");
        s.permissions = Arc::new(crate::permissions::Hub::open(&permissions_file).unwrap());
        let owner = async {
            let record = tokio::time::timeout(std::time::Duration::from_secs(3), async {
                loop {
                    let Json(book) =
                        crate::permissions::inspect(State(s.clone()), tasks::tests::headers())
                            .await
                            .unwrap();
                    if let Some(record) = book["requests"].as_array().unwrap().first() {
                        break record.clone();
                    }
                    tokio::time::sleep(std::time::Duration::from_millis(10)).await;
                }
            })
            .await
            .unwrap();
            assert!(!path.exists());
            assert_eq!(
                captured.lock().unwrap().len(),
                1,
                "Agent sampled while waiting for approval"
            );
            assert_eq!(record["request"]["owner"], "main");
            assert_eq!(
                record["request"]["args"]["cmd"],
                "printf owner-approved > file.txt"
            );
            let decision =
                serde_json::from_value(json!({"id":record["id"],"action":action})).unwrap();
            let Json(result) = crate::permissions::decide(
                State(s.clone()),
                tasks::tests::headers(),
                Json(decision),
            )
            .await
            .unwrap();
            assert_eq!(result["ok"], true);
        };
        tokio::join!(turn(&s, &mut rx, "owner-turn"), owner);
        assert_eq!(path.exists(), action != "deny");
        if path.exists() {
            assert_eq!(std::fs::read_to_string(&path).unwrap(), "owner-approved");
        }
        let reopened = crate::permissions::Hub::open(&permissions_file).unwrap();
        assert_eq!(
            reopened.grants("main").as_array().unwrap().len(),
            usize::from(action == "allow_persistent")
        );
        server.abort();
        std::fs::remove_dir_all(directory).unwrap();
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
}

#[tokio::test]
async fn main_host_computer_use_waits_for_owner_and_keeps_session_scope() {
    let browser_url = std::env::var("POT_TEST_HOST_BROWSER_URL").ok();
    let code = browser_url.as_ref().map_or_else(|| "let value = 41; nodeRepl.write(++value);".to_owned(), |url| format!(r#"let tab = await cua.open({}); let state = await tab.observe(); let input = state.elements.find(e => e.role === 'textbox'); state = await tab.fill({{element:input.element,sequence:state.sequence,text:'Pot'}}); let button = state.elements.find(e => e.role === 'button'); state = await tab.click({{element:button.element,sequence:state.sequence}}); await tab.observe(); nodeRepl.write(42);"#, json!(url)));
    for approved in [false, true] {
        let calls = vec![
            json!({"id":"host-cua","name":"cua_repl","arguments":{"code":code},"plugin_session":approved}),
        ];
        let (mut s, mut rx, server, captured) = fixture("openai_chat", calls).await;
        s.test_tool_executor = None;
        let runner = s.clone();
        let job = tokio::spawn(async move {
            turn(&runner, &mut rx, "host-plugin").await;
        });
        let mut approvals = 0;
        tokio::time::timeout(std::time::Duration::from_secs(20), async {
            while !job.is_finished() {
                let pending =
                    crate::permissions::inspect(State(s.clone()), tasks::tests::headers())
                        .await
                        .unwrap()
                        .0;
                for card in pending["requests"].as_array().unwrap() {
                    assert_eq!(card["request"]["tool"], "cua_repl");
                    let plugin = binding(&s)
                        .unwrap()
                        .spec
                        .params
                        .agent
                        .plugin_for_tool("cua_repl")
                        .unwrap()
                        .unwrap();
                    assert_eq!(
                        card["request"]["plugin"],
                        serde_json::to_value(plugin.identity()).unwrap()
                    );
                    assert_eq!(card["request"]["environment"], "host");
                    assert!(card["request"]["args"].get("environment").is_none());
                    assert!(card["request"].get("proposed_grant").is_none());
                    assert_eq!(
                        card["request"]["display"]["summary"]["zh-CN"],
                        "是否允许使用或管理浏览器自动化会话？"
                    );
                    if approvals == 0 {
                        assert_eq!(captured.lock().unwrap().len(), 1);
                        assert!(
                            s.permissions
                                .host_executor("main", binding(&s).unwrap().epoch)
                                .plugin_sessions()
                                .await
                                .is_empty()
                        );
                    }
                    let decision: crate::permissions::Decision = serde_json::from_value(
                        json!({"id":card["id"],"action":if approved {"allow_once"} else {"deny"}}),
                    )
                    .unwrap();
                    let _ = crate::permissions::decide(
                        State(s.clone()),
                        tasks::tests::headers(),
                        Json(decision),
                    )
                    .await
                    .unwrap();
                    approvals += 1;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        job.await.unwrap();
        assert_eq!(approvals, if approved { 3 } else { 1 });
        {
            let requests = captured.lock().unwrap();
            let continuation = requests[1].to_string();
            assert_eq!(continuation.contains("Computer-use API"), approved);
            if approved {
                assert!(continuation.contains("42"));
                if browser_url.is_some() {
                    assert!(continuation.contains("Saved Pot"));
                }
                assert!(requests[2].to_string().contains("43"));
            }
        }
        s.permissions.finish_host(&s).await.unwrap();
        server.abort();
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
}
