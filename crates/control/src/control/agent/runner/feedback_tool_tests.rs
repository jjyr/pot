//! Exercise the production context injection and Sandbox dispatcher with local
//! Helper/executor doubles. No VM, subprocess or model provider is involved.
use super::*;
use axum::{Router, http::Uri, routing::any};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

struct SandboxFixture {
    service: Service,
    http: Arc<Mutex<Vec<Value>>>,
    helper_calls: Arc<Mutex<Vec<Value>>>,
    inspect: Arc<Mutex<std::collections::BTreeMap<String, Value>>>,
    generation: Arc<std::sync::atomic::AtomicU64>,
    servers: Vec<tokio::task::JoinHandle<()>>,
}
impl SandboxFixture {
    async fn new() -> Self {
        let mut service = tasks::tests::fixture();
        // macOS TMPDIR can exceed the Unix socket path limit. Keep this fixture
        // under /tmp and reopen the Store/Manager against the relocated data.
        let root = PathBuf::from("/tmp").join(format!("pot-tools-{}", uuid::Uuid::new_v4()));
        std::fs::rename(service.data.parent().unwrap(), &root).unwrap();
        service.data = root.join("product");
        service.control_config = Arc::new(crate::model_config::ControlConfig::new(
            service.data.clone(),
        ));
        service.workspace = service.data.join("workspace");
        service.conversation.inner.lock().unwrap().store =
            Store::open(&service.data.join("pot-v2.sqlite")).unwrap();
        service.execution_service.sandboxes =
            Arc::new(crate::sandbox::Manager::open(&service.data).unwrap());
        assert!(service.test_tool_executor.is_none());
        let http = Arc::new(Mutex::new(vec![]));
        let mut servers = vec![];
        let mut ports = std::collections::BTreeMap::new();
        let inspect = Arc::new(Mutex::new(
            std::collections::BTreeMap::<String, Value>::new(),
        ));
        let generation = Arc::new(std::sync::atomic::AtomicU64::new(1));
        for target in ["default", "linux-b"] {
            let captured = http.clone();
            let inventory = inspect.clone();
            let app = Router::new().fallback(any(
            move |uri: Uri, headers: HeaderMap, body: axum::body::Bytes| {
                let captured = captured.clone();
                let inventory=inventory.clone();
                async move {
                    let auth = headers.get("authorization").unwrap().to_str().unwrap();
                    assert_eq!(auth,format!("Bearer fixture-{target}"));
                    let body: Value = if body.is_empty() { json!(null) } else { serde_json::from_slice(&body).unwrap() };
                    captured.lock().unwrap().push(json!({"path":uri.path(),"target":target,"auth":auth,"body":body}));
                    Json(match uri.path() {
                        "/prepare" => {
                            let mut args = body["args"].clone();
                            if body["tool"] == "exec_command" {
                                args["workdir"] = json!(format!("/guest-only/{target}"));
                                args["shell"] = body["policy"]["executable"].clone();
                                args["login"] = body["policy"]["allow_login_shell"].clone();
                                args["tty"] = json!(false);
                                if args.get("yield_time_ms").is_none() { args["timeout_ms"] = body["policy"]["default_timeout_ms"].clone(); }
                            }
                            json!({"args":args})
                        },
                        "/health" => json!({"ok":true}),
                        "/execute" => {
                            let mut result = json!({"exit_code":0,"stdout":"guest output"});
                            if body["operation"] == "exec_command" {
                                result["session_id"] = json!(format!("live-{}",auth));
                            }
                            json!({"result":result,"events":[serde_json::to_value(Update::ToolOutput {id:"guest-id".into(),stream:"stdout".into(),text:"guest output".into()}).unwrap()]})
                        },
                        "/inspect" => inventory.lock().unwrap().get(target).cloned().unwrap_or_else(||json!({"sessions":[format!("live-Bearer fixture-{target}")],"active":true,"ended":false})),
                        "/finish" => json!({"settled":true}),
                        _ => panic!("Unexpected executor route: {uri}"),
                    })
                }
            },
        ));
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url = format!("http://{}", listener.local_addr().unwrap());
            let http_server = tokio::spawn(async move {
                axum::serve(listener, app).await.unwrap();
            });
            servers.push(http_server);
            ports.insert(
                target.to_owned(),
                url.rsplit(':').next().unwrap().parse::<u16>().unwrap(),
            );
            let directory = service
                .data
                .parent()
                .unwrap()
                .join("sandboxes")
                .join(target);
            std::fs::create_dir_all(&directory).unwrap();
            std::fs::write(
                directory.join("executor-token"),
                format!("fixture-{target}\n"),
            )
            .unwrap();
        }
        let listener =
            tokio::net::UnixListener::bind(service.data.parent().unwrap().join("helper.sock"))
                .unwrap();
        let helper_calls = Arc::new(Mutex::new(vec![]));
        let captured = helper_calls.clone();
        let helper_generation = generation.clone();
        let helper_server = tokio::spawn(async move {
            loop {
                let (socket, _) = listener.accept().await.unwrap();
                let mut socket = BufReader::new(socket);
                let mut line = String::new();
                socket.read_line(&mut line).await.unwrap();
                let call: Value = serde_json::from_str(&line).unwrap();
                assert!(matches!(
                    call["method"].as_str(),
                    Some("start" | "status" | "stop")
                ));
                let target = call["sandbox_id"].as_str().unwrap();
                assert!(
                    matches!(target, "default" | "linux-b")
                        || pot_sandbox::Manager::is_shared(target)
                );
                let port_target = if ports.contains_key(target) {
                    target
                } else {
                    "default"
                };
                if pot_sandbox::Manager::is_shared(target) {
                    let directory = root.join("sandboxes").join(target);
                    std::fs::create_dir_all(&directory).unwrap();
                    std::fs::write(directory.join("executor-token"), "fixture-default\n").unwrap();
                }
                captured.lock().unwrap().push(call.clone());
                let phase = if call["method"] == "stop" {
                    "stopped"
                } else {
                    "running"
                };
                let reply = json!({"result":{"phase":phase,"ip":"127.0.0.1","executor_port":ports[port_target],"generation":helper_generation.load(std::sync::atomic::Ordering::SeqCst)}});
                socket
                    .get_mut()
                    .write_all(format!("{reply}\n").as_bytes())
                    .await
                    .unwrap();
            }
        });
        crate::sandbox::management(&service, &json!({"action":"create","sandbox_id":"linux-b"}))
            .await
            .unwrap();
        servers.push(helper_server);
        Self {
            service,
            http,
            helper_calls,
            inspect,
            generation,
            servers,
        }
    }
    fn requests(&self, path: &str) -> Vec<Value> {
        self.http
            .lock()
            .unwrap()
            .iter()
            .filter(|r| r["path"] == path)
            .cloned()
            .collect()
    }
    fn activate(s: &Service) -> SendMessage {
        let request = SendMessage {
            request_id: "sandbox-turn".into(),
            conversation_id: "main".into(),
            text: "fixture".into(),
        };
        s.save(Message {
            id: "sandbox-input".into(),
            conversation_id: "main".into(),
            role: Role::User,
            text: request.text.clone(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some(request.request_id.clone()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "sandbox-input".into(),
            cancelling: false,
        }));
        request
    }
}
impl Drop for SandboxFixture {
    fn drop(&mut self) {
        for server in &self.servers {
            server.abort();
        }
        let _ = std::fs::remove_dir_all(self.service.data.parent().unwrap());
    }
}
async fn execute(
    context: &ExecutionContext,
    policy: &pot_core::ShellSettings,
    name: &str,
    args: Value,
    call: &str,
) -> (Value, Vec<Update>) {
    let mut events = vec![];
    let result = tokio::time::timeout(
        std::time::Duration::from_secs(5),
        context.tool_executor.as_ref().unwrap().execute(
            context,
            policy,
            name,
            &args,
            call,
            &mut |event| {
                events.push(event);
                Ok(())
            },
        ),
    )
    .await
    .unwrap()
    .unwrap();
    (result, events)
}
#[tokio::test]
async fn main_context_routes_commands_patch_and_sessions_through_sandbox() {
    let fixture = SandboxFixture::new().await;
    let s = &fixture.service;
    let request = SandboxFixture::activate(s);
    let thread = thread(s).unwrap();
    let context = make_context(s, &thread, Some(&request)).unwrap();
    assert!(context.role == ExecutionRole::Main);
    assert_eq!(context.workspace, s.data.join("agent-workspace"));
    assert!(
        fixture.http.lock().unwrap().is_empty(),
        "context injection started Sandbox"
    );
    let policy = &thread.config.params.agent.shell;
    let args = json!({"cmd":"printf host-write > effects.txt","workdir":"src","yield_time_ms":1});
    let (result, events) =
        execute(&context, policy, "exec_command", args.clone(), "model-exec").await;
    assert_eq!(result["sandbox_id"], "default");
    assert!(
        result.get("events").is_none(),
        "executor wrapper leaked to model"
    );
    assert!(
        matches!(&events[..],[Update::ToolOutput{id,stream,text}] if id=="model-exec" && stream=="stdout" && text=="guest output")
    );
    // A repeated receipt must not execute twice, even after the default changes.
    crate::sandbox::management(s, &json!({"action":"select","sandbox_id":"linux-b"}))
        .await
        .unwrap();
    let (replayed, _) = execute(&context, policy, "exec_command", args.clone(), "model-exec").await;
    assert_eq!(replayed, result);
    let (_, _) = execute(
        &context,
        policy,
        "write_stdin",
        json!({"session_id":result["session_id"],"chars":"input"}),
        "model-stdin",
    )
    .await;
    let patch = "*** Begin Patch\n*** Add File: effects.txt\n+guest-write\n*** End Patch";
    let (_, _) = execute(
        &context,
        policy,
        "apply_patch",
        json!({"patch":patch}),
        "model-patch",
    )
    .await;
    let (_, _) = execute(
        &context,
        policy,
        "exec_command",
        json!({"cmd":"true","sandbox_id":"default"}),
        "model-explicit",
    )
    .await;
    let dispatched = fixture.requests("/execute");
    assert_eq!(dispatched.len(), 4);
    for (record, target, operation) in [
        (&dispatched[0], "default", "exec_command"),
        (&dispatched[1], "default", "write_stdin"),
        (&dispatched[2], "linux-b", "apply_patch"),
        (&dispatched[3], "default", "exec_command"),
    ] {
        assert_eq!(record["target"], target);
        assert_eq!(record["auth"], format!("Bearer fixture-{target}"));
        assert_eq!(record["body"]["operation"], operation);
        assert_eq!(record["body"]["cwd"], "/data/workspace");
        assert_eq!(record["body"]["role"], "main");
        assert_eq!(
            record["body"]["policy"],
            serde_json::to_value(policy).unwrap()
        );
        assert!(record["body"]["args"].get("sandbox_id").is_none());
        assert!(
            record["body"]["id"]
                .as_str()
                .is_some_and(|id| id != "model-exec")
        );
    }
    assert_eq!(dispatched[0]["body"]["args"], args);
    assert_eq!(dispatched[2]["body"]["args"]["patch"], patch);
    assert_eq!(
        dispatched[0]["body"]["scope"],
        dispatched[1]["body"]["scope"]
    );
    assert_eq!(
        dispatched[0]["body"]["scope"],
        dispatched[3]["body"]["scope"]
    );
    assert_ne!(
        dispatched[0]["body"]["scope"],
        dispatched[2]["body"]["scope"]
    );
    assert!(!context.workspace.join("effects.txt").exists());
    assert!(!s.workspace.join("effects.txt").exists());
    crate::execution_target::finish(s).await.unwrap();
    let finishes = fixture.requests("/finish");
    assert_eq!(finishes.len(), 2);
    for index in [0, 2] {
        assert!(
            finishes
                .iter()
                .any(|r| r["body"]["id"] == dispatched[index]["body"]["scope"]
                    && r["auth"] == dispatched[index]["auth"])
        );
    }
    let mut events = vec![];
    let error = context
        .tool_executor
        .as_ref()
        .unwrap()
        .execute(
            &context,
            policy,
            "write_stdin",
            &json!({"session_id":result["session_id"]}),
            "expired",
            &mut |u| {
                events.push(u);
                Ok(())
            },
        )
        .await
        .unwrap_err();
    assert!(error.to_string().contains("expired"));
    assert_eq!(fixture.requests("/execute").len(), 4);
    assert!(
        fixture
            .helper_calls
            .lock()
            .unwrap()
            .iter()
            .any(|r| r["method"] == "start")
    );
}
#[tokio::test]
async fn task_context_keeps_assigned_sandbox_and_rejects_control_calls() {
    let fixture = SandboxFixture::new().await;
    let root = &fixture.service;
    let task = tasks::create(
        State(root.clone()),
        tasks::tests::headers(),
        Json(CreateTask {
            request_id: "task-fixture".into(),
            text: "fixture".into(),
            ..Default::default()
        }),
    )
    .await
    .unwrap()
    .0;
    let (_, worker, request) = tasks::claim(root).unwrap().unwrap();
    assert_eq!(worker.usage_scope, task.id);
    let thread = thread(&worker).unwrap();
    let context = make_context(&worker, &thread, Some(&request)).unwrap();
    assert!(context.role == ExecutionRole::Worker);
    crate::sandbox::management(root, &json!({"action":"select","sandbox_id":"linux-b"}))
        .await
        .unwrap();
    let policy = &thread.config.params.agent.shell;
    let (result, _) = execute(
        &context,
        policy,
        "exec_command",
        json!({"cmd":"true"}),
        "worker-exec",
    )
    .await;
    assert_eq!(result["sandbox_id"], task.sandbox_id);
    let dispatched = fixture.requests("/execute");
    assert_eq!(dispatched.len(), 1);
    assert_eq!(
        dispatched[0]["body"]["cwd"],
        format!("/data/workspace/tasks/{}", task.id)
    );
    assert_eq!(dispatched[0]["auth"], "Bearer fixture-default");
    assert_eq!(dispatched[0]["body"]["role"], "task");
    for (name, args) in [
        ("exec_command", json!({"cmd":"true","sandbox_id":"linux-b"})),
        (
            "manage_sandbox",
            json!({"action":"start","sandbox_id":"linux-b"}),
        ),
        ("list_execution_targets", json!({})),
    ] {
        let before = fixture.helper_calls.lock().unwrap().len();
        let error = context
            .tool_executor
            .as_ref()
            .unwrap()
            .execute(&context, policy, name, &args, name, &mut |_| Ok(()))
            .await
            .unwrap_err();
        let expected = if name == "manage_sandbox" {
            "Unknown execution operation"
        } else {
            "Worker"
        };
        assert!(error.to_string().contains(expected), "{error}");
        assert_eq!(fixture.helper_calls.lock().unwrap().len(), before);
        assert_eq!(fixture.requests("/execute").len(), 1);
    }
    crate::execution_target::finish(&worker).await.unwrap();
    assert_eq!(fixture.requests("/finish").len(), 1);
}

#[tokio::test]
async fn runtime_injects_fresh_instructions_for_main_and_workers_but_not_summary() {
    let fixture = SandboxFixture::new().await;
    let main = &fixture.service;
    let base = main.execution_service.sandboxes.workspace("default");
    std::fs::create_dir_all(&base).unwrap();
    std::fs::write(base.join(".git"), "gitdir").unwrap();
    std::fs::write(base.join("AGENTS.md"), "PROJECT_FIRST").unwrap();
    let host = main.data.join("agent-workspace");
    std::fs::create_dir_all(&host).unwrap();
    std::fs::write(host.join("AGENTS.md"), "HOST_PROJECT").unwrap();
    let task = tasks::create(
        State(main.clone()),
        tasks::tests::headers(),
        Json(CreateTask {
            request_id: "instruction-task".into(),
            text: "fixture".into(),
            ..Default::default()
        }),
    )
    .await
    .unwrap()
    .0;
    let worker = tasks::owner(main, &task.id).unwrap();
    let base = main.execution_service.sandboxes.workspace(&task.sandbox_id);
    std::fs::write(base.join(".git"), "gitdir").unwrap();
    std::fs::write(worker.workspace.join("AGENTS.md"), "WORKER_LOCAL").unwrap();
    {
        for service in [main, &worker] {
            let request = SandboxFixture::activate(service);
            let thread = thread(service).unwrap();
            for text in ["PROJECT_FIRST", "PROJECT_UPDATED"] {
                let project = if service.is_worker { &base } else { &host };
                std::fs::write(project.join("AGENTS.md"), text).unwrap();
                let context = make_context(service, &thread, Some(&request)).unwrap();
                assert!(context.instructions.contains(text));
                if !service.is_worker {
                    assert!(!context.instructions.contains("/data/workspace"));
                }
                assert_eq!(
                    context.instructions.contains("WORKER_LOCAL"),
                    service.is_worker
                );
                assert!(!context.main_prompt.contains(text));
                if text == "PROJECT_UPDATED" {
                    assert!(!context.instructions.contains("PROJECT_FIRST"));
                }
            }
            let mut summary = request.clone();
            summary.request_id = format!("{}fixture", pot_assistant::orchestration::RESULT_PREFIX);
            let context = make_context(service, &thread, Some(&summary)).unwrap();
            assert!(context.role == ExecutionRole::Summary);
            assert!(context.instructions.is_empty());
            assert!(!context.main_prompt.contains("PROJECT_"));
        }
    }
    assert!(fixture.helper_calls.lock().unwrap().is_empty());
}

#[tokio::test]
async fn completed_work_closes_sessions_and_cannot_reuse_handles_in_another_work() {
    let fixture = SandboxFixture::new().await;
    let s = &fixture.service;
    let request = SandboxFixture::activate(s);
    let thread = thread(s).unwrap();
    let context = make_context(s, &thread, Some(&request)).unwrap();
    let (open, _) = execute(
        &context,
        &thread.config.params.agent.shell,
        "exec_command",
        json!({"cmd":"bash","tty":true,"login":false,"yield_time_ms":1}),
        "open",
    )
    .await;
    context
        .tool_executor
        .as_ref()
        .unwrap()
        .finish(&context)
        .await
        .unwrap();
    assert_eq!(fixture.requests("/finish").len(), 1);
    crate::execution_target::drain(s).await.unwrap();
    let result = crate::permissions::resolve_operation(
        s,
        "write_stdin",
        json!({"session_id":open["session_id"],"chars":"pwd\n"}),
    )
    .await;
    assert!(result.is_err());
    assert_eq!(fixture.requests("/execute").len(), 1);
}

#[tokio::test]
async fn idle_scopes_park_without_leases_and_reactivate_but_generation_and_epoch_invalidate() {
    let fixture = SandboxFixture::new().await;
    let s = &fixture.service;
    SandboxFixture::activate(s);
    let thread = thread(s).unwrap();
    let empty = json!({"sessions":[],"active":false,"ended":false});
    fixture
        .inspect
        .lock()
        .unwrap()
        .insert("default".into(), empty);
    crate::sandbox::tool(s, "exec_command", "first", &json!({"cmd":"true"}))
        .await
        .unwrap();
    let initial = fixture.requests("/execute")[0]["body"]["scope"].clone();
    s.execution_service
        .sandboxes
        .finish_turn(&s.usage_scope)
        .await
        .unwrap();
    let inventory = crate::sandbox::tool(s, "list_execution_targets", "inventory", &json!({}))
        .await
        .unwrap();
    let rows = inventory["items"].as_array().unwrap();
    assert_eq!(
        rows.iter().find(|r| r["id"] == "default").unwrap()["active_leases"],
        0
    );
    assert!(
        !std::fs::read_to_string(s.data.join("sandbox-operations.json"))
            .is_ok_and(|v| v.contains("default"))
    );
    crate::sandbox::tool(s, "exec_command", "second", &json!({"cmd":"true"}))
        .await
        .unwrap();
    assert_eq!(fixture.requests("/execute")[1]["body"]["scope"], initial);
    s.execution_service
        .sandboxes
        .finish_turn(&s.usage_scope)
        .await
        .unwrap();
    fixture
        .generation
        .store(2, std::sync::atomic::Ordering::SeqCst);
    s.execution_service
        .sandboxes
        .finish_turn(&s.usage_scope)
        .await
        .unwrap();
    let error = crate::sandbox::tool(
        s,
        "write_stdin",
        "old-generation",
        &json!({"session_id":"live-Bearer fixture-default"}),
    )
    .await
    .unwrap_err();
    assert!(error.to_string().contains("expired"));
    crate::sandbox::tool(s, "exec_command", "third", &json!({"cmd":"true"}))
        .await
        .unwrap();
    assert_ne!(fixture.requests("/execute")[2]["body"]["scope"], initial);
    crate::execution_target::finish_stale(s, thread.generation + 1)
        .await
        .unwrap();
    assert_eq!(fixture.requests("/finish").len(), 1);
    crate::execution_target::drain(s).await.unwrap();
}

#[tokio::test]
async fn expired_guest_scope_and_restart_do_not_resume_process_sessions() {
    let fixture = SandboxFixture::new().await;
    let s = &fixture.service;
    SandboxFixture::activate(s);
    let open = crate::sandbox::tool(
        s,
        "exec_command",
        "open",
        &json!({"cmd":"sleep 10","yield_time_ms":1}),
    )
    .await
    .unwrap();
    fixture.inspect.lock().unwrap().insert(
        "default".into(),
        json!({"sessions":[],"active":false,"ended":true}),
    );
    crate::execution_target::reap_idle(s).await.unwrap();
    let error = crate::sandbox::tool(
        s,
        "write_stdin",
        "expired",
        &json!({"session_id":open["result"]["session_id"]}),
    )
    .await
    .unwrap_err();
    assert!(error.to_string().contains("expired"));
    fixture.inspect.lock().unwrap().clear();
    crate::sandbox::tool(
        s,
        "exec_command",
        "new",
        &json!({"cmd":"sleep 10","yield_time_ms":1}),
    )
    .await
    .unwrap();
    let mut restarted = s.clone();
    restarted.execution_service.sandboxes =
        Arc::new(crate::sandbox::Manager::open(&s.data).unwrap());
    assert!(
        crate::sandbox::tool(
            &restarted,
            "write_stdin",
            "restart",
            &json!({"session_id":open["result"]["session_id"]})
        )
        .await
        .unwrap_err()
        .to_string()
        .contains("expired")
    );
    // Durable unknown scopes prevent fresh execution after an unclean restart.
    assert!(
        crate::sandbox::tool(&restarted, "exec_command", "fresh", &json!({"cmd":"true"}))
            .await
            .unwrap_err()
            .to_string()
            .contains("unsettled")
    );
    crate::execution_target::finish(s).await.unwrap();
}

#[tokio::test]
async fn malformed_session_inspection_quarantines_active_effects() {
    let fixture = SandboxFixture::new().await;
    let s = &fixture.service;
    SandboxFixture::activate(s);
    crate::sandbox::tool(
        s,
        "exec_command",
        "open",
        &json!({"cmd":"sleep 10","yield_time_ms":1}),
    )
    .await
    .unwrap();
    fixture.inspect.lock().unwrap().insert(
        "default".into(),
        json!({"sessions":[],"active":"false","ended":false}),
    );
    assert!(
        s.execution_service
            .sandboxes
            .finish_turn(&s.usage_scope)
            .await
            .unwrap_err()
            .to_string()
            .contains("stop unconfirmed")
    );
    assert!(crate::execution_target::drain(s).await.is_err());
    assert!(
        s.execution_service
            .sandboxes
            .acquire("default")
            .await
            .is_err()
    );
}

#[tokio::test]
async fn completed_task_cancellation_closes_retained_resources_without_rewriting_result() {
    let fixture = SandboxFixture::new().await;
    let root = &fixture.service;
    let task = tasks::create(
        State(root.clone()),
        tasks::tests::headers(),
        Json(CreateTask {
            request_id: "retained-task".into(),
            text: "fixture".into(),
            ..Default::default()
        }),
    )
    .await
    .unwrap()
    .0;
    let (_, worker, request) = tasks::claim(root).unwrap().unwrap();
    crate::sandbox::tool(
        &worker,
        "exec_command",
        "open",
        &json!({"cmd":"sleep 10","yield_time_ms":1}),
    )
    .await
    .unwrap();
    crate::execution_target::finish_turn(&worker).await.unwrap();
    tasks::tests::succeed(root, &task.id, &worker, &request);
    let reply = tasks::cancel(
        State(root.clone()),
        tasks::tests::headers(),
        axum::extract::Path(task.id.clone()),
    )
    .await
    .unwrap()
    .0;
    assert_eq!(reply["sessions_stopped"], true);
    assert_eq!(fixture.requests("/finish").len(), 1);
    assert_eq!(
        tasks::task_phase(&root.tasks.hub.lock().unwrap(), &task.id),
        Some(TaskPhase::Complete)
    );
}

#[tokio::test]
async fn sandbox_tools_need_no_assistant_grant_and_keep_guest_paths() {
    let fixture = SandboxFixture::new().await;
    let s = &fixture.service;
    let turn = SandboxFixture::activate(s);
    let thread = thread(s).unwrap();
    let host = crate::permissions::prepare(s, "exec_command", json!({"cmd":"cat notes.txt"}))
        .await
        .unwrap();
    assert_eq!(host["execution_target"], "host");
    assert!(fixture.requests("/prepare").is_empty());
    let args = crate::permissions::prepare(
        s,
        "exec_command",
        json!({"cmd":"cat notes.txt","execution_target":"sandbox:linux-b"}),
    )
    .await
    .unwrap();
    assert_eq!(args["workdir"], "/guest-only/linux-b");
    let permission = pot_agent::permissions::Request {
        owner: "main".into(),
        execution_target: "sandbox:linux-b".into(),
        generation: thread.generation,
        turn: turn.request_id.clone(),
        call_id: "guest-read".into(),
        tool: "exec_command".into(),
        args: args.clone(),
    };
    let wait_service = s.clone();
    let waiter = tokio::spawn(async move {
        crate::permissions::request(
            &wait_service,
            thread.generation,
            &turn.request_id,
            json!(permission),
        )
        .await
    });
    assert_eq!(waiter.await.unwrap().unwrap()["approved"], true);
    let state = crate::permissions::inspect(State(s.clone()), tasks::tests::headers())
        .await
        .unwrap()
        .0;
    assert!(state["requests"].as_array().unwrap().is_empty());
    assert!(
        fixture
            .requests("/prepare")
            .iter()
            .all(|r| r["body"]["action"] != "write")
    );
    let mut worker = s.clone();
    worker.is_worker = true;
    worker.sandbox_id = Some("default".into());
    worker.execution_target = Some(pot_core::ExecutionTarget::sandbox(
        &worker.runtime_id,
        "default",
    ));
    assert!(
        crate::permissions::prepare(
            &worker,
            "exec_command",
            json!({"cmd":"true","execution_target":"sandbox:linux-b"})
        )
        .await
        .is_err()
    );
    assert!(
        crate::permissions::prepare(
            &worker,
            "exec_command",
            json!({"cmd":"cat notes.txt","execution_target":"host"}),
        )
        .await
        .is_ok()
    );
}

#[tokio::test]
async fn session_continuation_preparation_does_not_boot_guest_or_read_permissions() {
    let fixture = SandboxFixture::new().await;
    let result = crate::permissions::prepare(
        &fixture.service,
        "write_stdin",
        json!({"session_id":"missing-session","execution_target":"sandbox:linux-b","chars":"yes\n"}),
    )
    .await;
    assert!(result.is_err());
    assert!(fixture.http.lock().unwrap().is_empty());
    assert!(fixture.helper_calls.lock().unwrap().is_empty());
    // Skipping approval/preparation never makes an invalid handle executable.
    SandboxFixture::activate(&fixture.service);
    let thread = thread(&fixture.service).unwrap();
    let context = make_context(&fixture.service, &thread, None).unwrap();
    let args = json!({"session_id":"missing-session","execution_target":"sandbox:linux-b","chars":"yes\n"});
    let mut events = vec![];
    assert!(
        context
            .tool_executor
            .as_ref()
            .unwrap()
            .execute(
                &context,
                &thread.config.params.agent.shell,
                "write_stdin",
                &args,
                "invalid-session",
                &mut |event| {
                    events.push(event);
                    Ok(())
                }
            )
            .await
            .is_err()
    );
    assert!(fixture.http.lock().unwrap().is_empty());
    let (opened, _) = execute(
        &context,
        &thread.config.params.agent.shell,
        "exec_command",
        json!({"cmd":"true","execution_target":"sandbox:linux-b"}),
        "open-session",
    )
    .await;
    let continued = crate::permissions::prepare(
        &fixture.service,
        "write_stdin",
        json!({"session_id":opened["session_id"],"execution_target":"sandbox:linux-b","chars":"yes\n"}),
    )
    .await
    .unwrap();
    let (_, _) = execute(
        &context,
        &thread.config.params.agent.shell,
        "write_stdin",
        continued.clone(),
        "continue-session",
    )
    .await;
    assert!(fixture.requests("/prepare").is_empty());
    let mut wrong = continued;
    wrong["execution_target"] = json!("sandbox:default");
    assert!(
        context
            .tool_executor
            .as_ref()
            .unwrap()
            .execute(
                &context,
                &thread.config.params.agent.shell,
                "write_stdin",
                &wrong,
                "wrong-target",
                &mut |_| Ok(())
            )
            .await
            .is_err()
    );
}
