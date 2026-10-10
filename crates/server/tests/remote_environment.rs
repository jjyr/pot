//! Full local Agent -> HTTPS remote Host task, deterministic model and actual processes.
use axum::{
    Json, Router,
    response::sse::{Event, Sse},
    routing::post,
};
use pot_runtime::{RuntimeConfig, host::Composition};
use pot_server::{Server, ServerConfig};
use serde_json::{Value, json};
use std::{
    convert::Infallible,
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

struct Node {
    root: PathBuf,
    base: String,
    token: String,
    stop: Option<tokio::sync::oneshot::Sender<()>>,
    task: tokio::task::JoinHandle<anyhow::Result<()>>,
}
impl Node {
    async fn start() -> Self {
        let root = std::env::temp_dir().join(format!("pot-remote-task-{}", uuid::Uuid::new_v4()));
        let server = Server::bind(ServerConfig {
            composition: Some(Composition::Bare),
            runtime: RuntimeConfig {
                data: root.join("data"),
                workspace: root.join("workspace"),
            },
            bind: "127.0.0.1:0".into(),
        })
        .await
        .unwrap();
        let base = format!("http://{}", server.local_addr().unwrap());
        let token = server.endpoint().unwrap().token;
        let (stop, shutdown) = tokio::sync::oneshot::channel();
        let task = tokio::spawn(server.serve(async {
            let _ = shutdown.await;
        }));
        Self {
            root,
            base,
            token,
            stop: Some(stop),
            task,
        }
    }
    async fn api(&self, path: &str, body: Option<Value>) -> Value {
        let client = reqwest::Client::new();
        let request = if let Some(body) = body {
            client.post(format!("{}{path}", self.base)).json(&body)
        } else {
            client.get(format!("{}{path}", self.base))
        };
        let response = request.bearer_auth(&self.token).send().await.unwrap();
        let status = response.status();
        let value: Value = response.json().await.unwrap();
        assert!(status.is_success(), "{path}: {status} {value}");
        value
    }
    async fn stop(mut self) {
        self.stop.take().unwrap().send(()).unwrap();
        tokio::time::timeout(Duration::from_secs(10), &mut self.task)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        if let Err(error) = std::fs::remove_dir_all(&self.root) {
            // Rootless guests can create files owned by subordinate namespace IDs.
            // Server shutdown has already removed this fixture's containers.
            #[cfg(target_os = "linux")]
            if error.kind() == std::io::ErrorKind::PermissionDenied {
                let status = std::process::Command::new("podman")
                    .args(["unshare", "rm", "-rf", "--"])
                    .arg(&self.root)
                    .status()
                    .unwrap();
                assert!(status.success(), "fixture namespace cleanup failed");
                assert!(!self.root.exists(), "fixture root remains after cleanup");
                return;
            }
            panic!("fixture cleanup failed: {error}");
        }
    }
}
#[tokio::test]
async fn local_task_agent_runs_on_remote_host_with_destination_project_rules_and_owner_approval() {
    qualify(false, false, false).await;
}
#[tokio::test]
#[ignore = "requires Linux rootless Podman, prepared executor image and isolated container storage"]
async fn local_task_agent_runs_on_remote_podman_with_destination_project_rules_and_owner_approval()
{
    qualify(true, false, false).await;
}
#[tokio::test]
async fn remote_task_waits_without_sampling_then_recovers_original_receipt() {
    qualify(false, true, false).await;
}
#[tokio::test]
async fn offline_destination_waits_before_first_model_request_then_starts() {
    qualify(false, false, true).await;
}
async fn qualify(sandbox: bool, outage: bool, before_start: bool) {
    let caller = Node::start().await;
    let target = Node::start().await;
    caller
        .api(
            "/api/v1/runtime/resources",
            Some(json!({"request_id":"assistant","resource":{"kind":"assistant","name":"Local"}})),
        )
        .await;
    target
        .api(
            "/api/v1/runtime/resources",
            Some(if sandbox {json!({"request_id":"sandbox","resource":{"kind":"sandbox","sandbox_id":"peer-test","name":"Peer test","provider":"linux.podman"}})} else {json!({"request_id":"host","resource":{"kind":"host"}})}),
        )
        .await;
    let instructions = caller
        .api("/api/v1/assistants/main/instructions", None)
        .await;
    caller.api("/api/v1/assistants/main/instructions",Some(json!({"text":"ASSISTANT_PRIVATE_RULE","expected_revision":instructions["revision"]}))).await;
    std::fs::create_dir_all(caller.root.join("workspace")).unwrap();
    std::fs::write(
        caller.root.join("workspace/AGENTS.md"),
        "LOCAL_PRIVATE_PROJECT_RULE",
    )
    .unwrap();
    let project = if sandbox {
        target.root.join("sandboxes/peer-test/workspace/workspace")
    } else {
        target.root.join("data/peer-workspaces")
    };
    let workspace = if sandbox {
        project.join("tasks")
    } else {
        project.clone()
    };
    std::fs::create_dir_all(project.join(".git")).unwrap();
    std::fs::create_dir_all(&workspace).unwrap();
    std::fs::write(project.join("AGENTS.md"), "REMOTE_PROJECT_RULE").unwrap();
    let remote_skill = project.join(".agents/skills/remote-fixture");
    std::fs::create_dir_all(&remote_skill).unwrap();
    std::fs::write(remote_skill.join("SKILL.md"),
        "---\nname: remote-fixture\ndescription: REMOTE_INSTALLED_SKILL\n---\nDestination-only fixture\n").unwrap();
    let local_skill = caller.root.join("workspace/.agents/skills/local-fixture");
    std::fs::create_dir_all(&local_skill).unwrap();
    std::fs::write(
        local_skill.join("SKILL.md"),
        "---\nname: local-fixture\ndescription: CALLER_PRIVATE_SKILL\n---\nCaller-only fixture\n",
    )
    .unwrap();
    let execution_target = if sandbox {
        json!({"kind":"sandbox","sandbox_id":"peer-test"})
    } else {
        json!({"kind":"host"})
    };
    let rcgen::CertifiedKey { cert, signing_key } =
        rcgen::generate_simple_self_signed(vec!["localhost".into()]).unwrap();
    let certificate = caller.root.join("cert.pem");
    let private_key = caller.root.join("key.pem");
    std::fs::write(&certificate, cert.pem()).unwrap();
    std::fs::write(&private_key, signing_key.serialize_pem()).unwrap();
    let reservation = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = reservation.local_addr().unwrap();
    drop(reservation);
    caller.api("/api/v1/runtime/control",Some(json!({"action":"enable_control","config":{"address":address,"certificate":certificate,"private_key":private_key}}))).await;
    let enrollment = caller
        .api(
            "/api/v1/runtime/control",
            Some(json!({"action":"create_invitation"})),
        )
        .await;
    let target_id = target.api("/api/v1/runtime", None).await["runtime_id"]
        .as_str()
        .unwrap()
        .to_owned();
    target.api("/api/v1/runtime/control",Some(json!({"action":"join","request":{"request_id":uuid::Uuid::new_v4(),"url":format!("https://localhost:{}",address.port()),"invitation":enrollment["invitation"],"expected_runtime_id":enrollment["identity"]["runtime_id"],"expected_fingerprint":enrollment["identity"]["fingerprint"],"ca_pem":cert.pem()}}))).await;
    let machines = caller.api("/api/v1/machines", None).await;
    assert_eq!(machines["machines"].as_array().unwrap().len(), 2);
    let model_listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let model_address = model_listener.local_addr().unwrap();
    let calls = Arc::new(AtomicUsize::new(0));
    let captured = Arc::new(Mutex::new(vec![]));
    let counter = calls.clone();
    let requests = captured.clone();
    let model = tokio::spawn(async move {
        let router=Router::new().route("/model",post(move|Json(body):Json<Value>|{
            let remote=body.to_string().contains("REMOTE_PROJECT_RULE");
            let number=if remote{counter.fetch_add(1,Ordering::SeqCst)}else{usize::MAX};
            requests.lock().unwrap().push(body);
            async move {
                let command = if number == 0 {
                    let skills = if sandbox { "cat ../../.agents/skills/remote-fixture/SKILL.md" } else { "cat ../.agents/skills/remote-fixture/SKILL.md" };
                    format!("{}printf REMOTE > where.txt; cat where.txt; {skills}", if outage { "printf X >> once.txt; sleep 3; " } else { "" })
                } else { "cat where.txt; printf FOLLOWUP >> where.txt".into() };
                let delta=if number==0 || number==2 {json!({"tool_calls":[{"index":0,"id":format!("remote-command-{number}"),"type":"function","function":{"name":"exec_command","arguments":json!({"cmd":command}).to_string()}}]})}else{json!({"content":"Remote task complete."})};
                let events=vec![json!({"choices":[{"delta":delta,"finish_reason":null}]}),json!({"choices":[{"delta":{},"finish_reason":if number==0 || number==2{"tool_calls"}else{"stop"}}]}),json!({"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}})];
                Sse::new(futures::stream::iter(events.into_iter().map(|event|Ok::<_,Infallible>(Event::default().data(event.to_string())))))
            }
        }));
        axum::serve(model_listener, router).await.unwrap();
    });
    caller.api("/api/v1/providers",Some(json!({"request_id":uuid::Uuid::new_v4(),"expected_revision":0,"endpoint":format!("http://{model_address}/model"),"model":"fixture","protocol":"openai_chat","api_key":"fixture","max_output_tokens":1024}))).await;
    if before_start {
        caller
            .api(
                "/api/v1/runtime/control",
                Some(json!({"action":"disable_control"})),
            )
            .await;
    }
    let task=caller.api("/api/v1/tasks",Some(json!({"request_id":"remote-task","text":"Write where.txt on the assigned machine","environment_ref":{"runtime_id":target_id,"target":execution_target}}))).await;
    let task_id = task["id"].as_str().unwrap();
    let mut reviews = 0;
    let mut disconnected = before_start;
    let mut recovered = false;
    let finished = tokio::time::timeout(Duration::from_secs(40), async {
        loop {
            let permissions = caller.api("/api/v1/permissions", None).await;
            for card in permissions["requests"].as_array().unwrap() {
                assert_eq!(card["request"]["owner"], task_id);
                assert_eq!(card["request"]["tool"], "exec_command");
                assert!(
                    card["request"]["environment"]
                        .as_str()
                        .unwrap()
                        .contains(&target_id)
                );
                caller
                    .api(
                        "/api/v1/permissions",
                        Some(json!({"id":card["id"],"action":"allow_once"})),
                    )
                    .await;
                reviews += 1;
            }
            let tasks = caller.api("/api/v1/tasks", None).await;
            let current = tasks
                .as_array()
                .unwrap()
                .iter()
                .find(|item| item["id"] == task_id)
                .unwrap()
                .clone();
            if outage && !disconnected && std::fs::read_dir(&workspace).unwrap()
                .filter_map(Result::ok).any(|entry| entry.path().join("once.txt").exists()) {
                caller.api("/api/v1/runtime/control", Some(json!({"action":"disable_control"}))).await;
                disconnected = true;
            }
            if (outage || before_start) && current["remote_waiting"] == true && !recovered {
                assert_eq!(current["phase"], "waiting");
                assert_eq!(calls.load(Ordering::SeqCst), usize::from(!before_start), "sampled before receipt reconciliation");
                let snapshot = caller.api(&format!("/api/v1/tasks/{task_id}"), None).await;
                assert!(snapshot["agent"]["detail"].as_str().unwrap().contains("等待远程"));
                caller.api("/api/v1/runtime/control",Some(json!({"action":"enable_control","config":{"address":address,"certificate":certificate,"private_key":private_key}}))).await;
                recovered = true;
            }
            if ["complete", "failed", "interrupted"]
                .contains(&current["phase"].as_str().unwrap_or(""))
            {
                break current;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let detail = caller.api(&format!("/api/v1/tasks/{task_id}"), None).await;
    assert_eq!(finished["phase"], "complete", "{finished} {detail}");
    assert_eq!(reviews, 1);
    if before_start {
        assert!(disconnected && recovered);
    }
    if outage {
        assert!(disconnected && recovered);
        let effects = std::fs::read_dir(&workspace)
            .unwrap()
            .filter_map(Result::ok)
            .filter_map(|entry| std::fs::read_to_string(entry.path().join("once.txt")).ok())
            .collect::<Vec<_>>();
        assert_eq!(effects, vec!["X"], "original command was replayed");
    }
    assert!(detail.to_string().contains("Destination-only fixture"));
    let body = captured.lock().unwrap()[0].to_string();
    assert!(body.contains("REMOTE_PROJECT_RULE"), "{body}");
    assert!(!body.contains("ASSISTANT_PRIVATE_RULE"));
    assert!(!body.contains("LOCAL_PRIVATE_PROJECT_RULE"));
    assert!(body.contains("REMOTE_INSTALLED_SKILL"));
    assert!(!body.contains("CALLER_PRIVATE_SKILL"));
    let workspaces = std::fs::read_dir(&workspace).unwrap();
    let files = workspaces
        .filter_map(Result::ok)
        .filter_map(|entry| std::fs::read_to_string(entry.path().join("where.txt")).ok())
        .collect::<Vec<_>>();
    assert_eq!(files, vec!["REMOTE"]);
    assert!(!caller.root.join("workspace/where.txt").exists());
    assert!(!target.root.join("data/pot-v2.sqlite").exists());

    caller.api(&format!("/api/v1/tasks/{task_id}/messages"),Some(json!({"request_id":"remote-followup","conversation_id":task_id,"text":"Continue in the same destination workspace"}))).await;
    tokio::time::timeout(Duration::from_secs(40), async {
        loop {
            for card in caller.api("/api/v1/permissions", None).await["requests"]
                .as_array()
                .unwrap()
            {
                caller
                    .api(
                        "/api/v1/permissions",
                        Some(json!({"id":card["id"],"action":"allow_once"})),
                    )
                    .await;
            }
            let tasks = caller.api("/api/v1/tasks", None).await;
            let task = tasks
                .as_array()
                .unwrap()
                .iter()
                .find(|t| t["id"] == task_id)
                .unwrap();
            if task["phase"] == "complete" && calls.load(Ordering::SeqCst) == 4 {
                break;
            }
            assert_ne!(task["phase"], "failed", "{task}");
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let files = std::fs::read_dir(&workspace)
        .unwrap()
        .filter_map(Result::ok)
        .filter_map(|entry| std::fs::read_to_string(entry.path().join("where.txt")).ok())
        .collect::<Vec<_>>();
    assert_eq!(files, vec!["REMOTEFOLLOWUP"]);
    if sandbox {
        let inventory = target.api("/api/v1/sandboxes", None).await;
        let config = inventory["items"]
            .as_array()
            .unwrap()
            .iter()
            .find(|item| item["id"] == "peer-test")
            .unwrap()["config"]
            .clone();
        for action in ["start", "stop", "update", "recover", "delete"] {
            let operation = format!("qualification-{action}");
            let accept_before_ms = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis() as i64
                + 30_000;
            let mut args = json!({"action":action,"sandbox_id":"peer-test"});
            if action == "update" {
                args["name"] = json!("Managed remotely");
                args["config"] = config.clone();
            }
            let mut receipt = caller.api("/api/v1/runtime/control", Some(json!({"action":"call","runtime_id":target_id,"body":{"action":"manage_sandbox","operation_id":operation,"accept_before_ms":accept_before_ms,"target":execution_target,"args":args}}))).await;
            tokio::time::timeout(Duration::from_secs(40), async {
                while receipt["state"] == "accepted" {
                    tokio::time::sleep(Duration::from_millis(100)).await;
                    receipt = caller.api("/api/v1/runtime/control", Some(json!({"action":"call","runtime_id":target_id,"body":{"action":"inspect_management","operation_id":operation}}))).await;
                }
            }).await.unwrap();
            assert_eq!(receipt["state"], "completed", "{action}: {receipt}");
            assert_eq!(receipt["result"]["ok"], true, "{action}: {receipt}");
        }
    }
    caller.stop().await;
    target.stop().await;
    model.abort();
}
