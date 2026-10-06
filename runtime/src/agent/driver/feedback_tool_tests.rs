//! Exercise the production context injection and Sandbox dispatcher with local
//! Helper/executor doubles. No VM, subprocess or model provider is involved.
use super::*;
use axum::{Router, http::Uri, routing::any};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

struct SandboxFixture {
    service: Service,
    http: Arc<Mutex<Vec<Value>>>,
    helper_calls: Arc<Mutex<Vec<Value>>>,
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
        service.workspace = service.data.join("workspace");
        service.inner.lock().unwrap().store =
            Store::open(&service.data.join("pot-v2.sqlite")).unwrap();
        service.sandboxes = Arc::new(crate::sandbox::Manager::open(&service.data).unwrap());
        assert!(service.test_tool_executor.is_none());
        let http = Arc::new(Mutex::new(vec![]));
        let mut servers = vec![];
        for target in ["default", "linux-b"] {
            let captured = http.clone();
            let app = Router::new().fallback(any(
            move |uri: Uri, headers: HeaderMap, body: axum::body::Bytes| {
                let captured = captured.clone();
                async move {
                    let auth = headers.get("authorization").unwrap().to_str().unwrap();
                    assert_eq!(auth,format!("Bearer fixture-{target}"));
                    let body: Value = if body.is_empty() { json!(null) } else { serde_json::from_slice(&body).unwrap() };
                    captured.lock().unwrap().push(json!({"path":uri.path(),"target":target,"auth":auth,"body":body}));
                    Json(match uri.path() {
                        "/health" => json!({"ok":true}),
                        "/execute" => {
                            let mut result = json!({"exit_code":0,"stdout":"guest output"});
                            if body["operation"] == "exec_command" {
                                result["session_id"] = json!(format!("live-{}",auth));
                            }
                            json!({"result":result,"events":[serde_json::to_value(Update::ToolOutput {id:"guest-id".into(),stream:"stdout".into(),text:"guest output".into()}).unwrap()]})
                        },
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
            service
                .sandboxes
                .test_executor_urls
                .lock()
                .unwrap()
                .insert(target.into(), url.clone());
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
        let helper_server = tokio::spawn(async move {
            loop {
                let (socket, _) = listener.accept().await.unwrap();
                let mut socket = BufReader::new(socket);
                let mut line = String::new();
                socket.read_line(&mut line).await.unwrap();
                let call: Value = serde_json::from_str(&line).unwrap();
                assert!(matches!(call["method"].as_str(), Some("start" | "status")));
                assert!(matches!(
                    call["sandbox_id"].as_str(),
                    Some("default" | "linux-b")
                ));
                captured.lock().unwrap().push(call.clone());
                let reply =
                    json!({"result":{"phase":"running","ip":call["sandbox_id"],"generation":1}});
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
    let binding = binding(s).unwrap();
    let context = make_context(s, &binding, Some(&request)).unwrap();
    assert!(context.role == ExecutionRole::Main);
    assert_eq!(context.workspace, s.data.join("agent-workspace"));
    assert!(
        fixture.http.lock().unwrap().is_empty(),
        "context injection started Sandbox"
    );
    let policy = &binding.spec.params.agent.shell;
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
    context
        .tool_executor
        .as_ref()
        .unwrap()
        .finish(&context)
        .await
        .unwrap();
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
            sandbox_id: "default".into(),
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
    let binding = binding(&worker).unwrap();
    let context = make_context(&worker, &binding, Some(&request)).unwrap();
    assert!(context.role == ExecutionRole::Worker);
    crate::sandbox::management(root, &json!({"action":"select","sandbox_id":"linux-b"}))
        .await
        .unwrap();
    let policy = &binding.spec.params.agent.shell;
    let (result, _) = execute(
        &context,
        policy,
        "exec_command",
        json!({"cmd":"true"}),
        "worker-exec",
    )
    .await;
    assert_eq!(result["sandbox_id"], "default");
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
            "pot_manage_sandbox",
            json!({"action":"start","sandbox_id":"linux-b"}),
        ),
        ("pot_list_sandboxes", json!({})),
    ] {
        let before = fixture.helper_calls.lock().unwrap().len();
        let error = context
            .tool_executor
            .as_ref()
            .unwrap()
            .execute(&context, policy, name, &args, name, &mut |_| Ok(()))
            .await
            .unwrap_err();
        assert!(error.to_string().contains("Worker"));
        assert_eq!(fixture.helper_calls.lock().unwrap().len(), before);
        assert_eq!(fixture.requests("/execute").len(), 1);
    }
    context
        .tool_executor
        .as_ref()
        .unwrap()
        .finish(&context)
        .await
        .unwrap();
    assert_eq!(fixture.requests("/finish").len(), 1);
}

#[tokio::test]
async fn runtime_injects_fresh_instructions_for_both_engines_and_roles_but_not_summary() {
    let fixture = SandboxFixture::new().await;
    let main = &fixture.service;
    let base = main.sandboxes.workspace("default");
    std::fs::create_dir_all(&base).unwrap();
    std::fs::write(base.join(".git"), "gitdir").unwrap();
    std::fs::write(base.join("AGENTS.md"), "PROJECT_FIRST").unwrap();
    let host = main.data.join("agent-workspace");
    std::fs::create_dir_all(&host).unwrap();
    std::fs::write(host.join("AGENTS.md"), "WRONG_HOST").unwrap();
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
    std::fs::write(worker.workspace.join("AGENTS.md"), "WORKER_LOCAL").unwrap();
    for engine in [Engine::Pot, Engine::Codex] {
        for service in [main, &worker] {
            let request = SandboxFixture::activate(service);
            let mut binding = binding(service).unwrap();
            binding.spec.engine = engine.clone();
            for text in ["PROJECT_FIRST", "PROJECT_UPDATED"] {
                std::fs::write(base.join("AGENTS.md"), text).unwrap();
                let context = make_context(service, &binding, Some(&request)).unwrap();
                assert!(context.instructions.contains(text));
                assert!(!context.instructions.contains("WRONG_HOST"));
                assert_eq!(
                    context.instructions.contains("WORKER_LOCAL"),
                    service.is_worker
                );
                if engine == Engine::Codex {
                    assert_eq!(context.main_prompt.matches(text).count(), 1);
                } else {
                    assert!(!context.main_prompt.contains(text));
                }
                if text == "PROJECT_UPDATED" {
                    assert!(!context.instructions.contains("PROJECT_FIRST"));
                }
            }
            let mut summary = request.clone();
            summary.request_id = format!("{}fixture", orchestration::RESULT_PREFIX);
            let context = make_context(service, &binding, Some(&summary)).unwrap();
            assert!(context.role == ExecutionRole::Summary);
            assert!(context.instructions.is_empty());
            assert!(!context.main_prompt.contains("PROJECT_"));
        }
    }
    assert!(fixture.helper_calls.lock().unwrap().is_empty());
}
