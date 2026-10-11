use pot_protocol::{PATH_EVENTS, PATH_SNAPSHOT, RUNTIME_ENDPOINT_FILE, RuntimeEndpoint, Snapshot};
use pot_runtime::RuntimeConfig;
use pot_server::{Server, ServerConfig};
use serde_json::{Value, json};
use std::{os::unix::fs::PermissionsExt, path::PathBuf, time::Duration};
static NEXT_FIXTURE: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        Self(std::path::PathBuf::from("/tmp").join(format!(
                "pot-server-{}-{}-{}",
                std::process::id(),
                NEXT_FIXTURE.fetch_add(1, std::sync::atomic::Ordering::Relaxed),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            )))
    }
    async fn start(&self) -> Server {
        let server = Server::bind(self.config()).await.unwrap();
        for (id, resource) in [
            ("assistant", json!({"kind":"assistant","name":"Assistant"})),
            ("host", json!({"kind":"host"})),
        ] {
            let reply = server
                .local_client()
                .request(
                    "/api/v1/runtime/resources",
                    Some(json!({"request_id":id,"resource":resource})),
                )
                .await
                .unwrap();
            assert_eq!(reply.status, 200);
        }
        server
    }
    fn config(&self) -> ServerConfig {
        ServerConfig {
            web_control: None,
            runtime: RuntimeConfig {
                data: self.0.join("data"),
                workspace: self.0.join("workspace"),
            },
            bind: "127.0.0.1:0".into(),
        }
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

#[tokio::test]
async fn standalone_server_auth_persistence_sse_and_shutdown_without_app() {
    let fixture = Fixture::new();
    let server = fixture.start().await;
    let endpoint = server.endpoint().unwrap();
    let published: RuntimeEndpoint = serde_json::from_slice(
        &std::fs::read(fixture.0.join("data").join(RUNTIME_ENDPOINT_FILE)).unwrap(),
    )
    .unwrap();
    assert_eq!(published, endpoint);
    assert_eq!(
        std::fs::metadata(fixture.0.join("data").join(RUNTIME_ENDPOINT_FILE))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    let base = format!("http://{}", server.local_addr().unwrap());
    let (stop, shutdown) = tokio::sync::oneshot::channel();
    let serving = tokio::spawn(server.serve(async {
        let _ = shutdown.await;
    }));
    let client = reqwest::Client::new();
    assert_eq!(
        client
            .get(format!("{base}{PATH_SNAPSHOT}"))
            .send()
            .await
            .unwrap()
            .status()
            .as_u16(),
        401
    );
    let snapshot: Snapshot = client
        .get(format!("{base}{PATH_SNAPSHOT}"))
        .bearer_auth(&endpoint.token)
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(
        client
            .post(format!("{base}/api/agent/compactions"))
            .json(&json!({}))
            .send()
            .await
            .unwrap()
            .status()
            .as_u16(),
        401
    );
    assert_eq!(
        client
            .post(format!("{base}/api/pi/compactions"))
            .json(&json!({}))
            .send()
            .await
            .unwrap()
            .status()
            .as_u16(),
        404
    );
    assert!(snapshot.messages.is_empty());
    assert!(snapshot.turn.is_none());
    // Save through the real API; restart must preserve settings and create a new boot/token.
    let policy =
        json!({"period":"day","limit":12345,"timezone":"Asia/Shanghai","auto_resume":false});
    client
        .post(format!("{base}/api/v1/usage/policy"))
        .bearer_auth(&endpoint.token)
        .json(&policy)
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap();
    let mut events = client
        .get(format!("{base}{PATH_EVENTS}?after=0"))
        .bearer_auth(&endpoint.token)
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap();
    let initial = events.chunk().await.unwrap().unwrap();
    assert!(String::from_utf8_lossy(&initial).contains("snapshot"));
    stop.send(()).unwrap();
    // Keep SSE connected: graceful shutdown must close it instead of hanging.
    tokio::time::timeout(Duration::from_secs(5), serving)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(
        tokio::time::timeout(Duration::from_secs(2), events.chunk())
            .await
            .unwrap()
            .unwrap()
            .is_none()
    );
    let next = fixture.start().await;
    let next_endpoint = next.endpoint().unwrap();
    assert_ne!(next_endpoint.boot_id, endpoint.boot_id);
    assert_ne!(next_endpoint.token, endpoint.token);
    let base = format!("http://{}", next.local_addr().unwrap());
    let (stop, shutdown) = tokio::sync::oneshot::channel();
    let serving = tokio::spawn(next.serve(async {
        let _ = shutdown.await;
    }));
    assert_eq!(
        client
            .get(format!("{base}{PATH_SNAPSHOT}"))
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .status()
            .as_u16(),
        401
    );
    let usage: Value = client
        .get(format!("{base}/api/v1/usage/summary"))
        .bearer_auth(&next_endpoint.token)
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
    assert!(usage.to_string().contains("12345"), "{usage}");
    stop.send(()).unwrap();
    tokio::time::timeout(Duration::from_secs(5), serving)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
}

#[tokio::test]
async fn occupied_listener_does_not_initialize_product_storage() {
    let fixture = Fixture::new();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let mut config = fixture.config();
    config.bind = listener.local_addr().unwrap().to_string();
    assert!(Server::bind(config).await.is_err());
    assert!(!fixture.0.exists());
}

#[tokio::test]
async fn shutdown_settles_active_main_and_worker_model_requests_without_host_execution() {
    use axum::{
        Router,
        response::sse::{Event, Sse},
        routing::post,
    };
    use std::convert::Infallible;
    let fixture = Fixture::new();
    let upstream = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = upstream.local_addr().unwrap();
    let active = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let observed = active.clone();
    let model = tokio::spawn(async move {
        let router = Router::new().route(
            "/model",
            post(move || {
                let observed = observed.clone();
                async move {
                    observed.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
                    Sse::new(futures::stream::pending::<Result<Event, Infallible>>())
                }
            }),
        );
        axum::serve(upstream, router).await.unwrap();
    });
    let server = fixture.start().await;
    let endpoint = server.endpoint().unwrap();
    let base = format!("http://{}", server.local_addr().unwrap());
    let (stop, shutdown) = tokio::sync::oneshot::channel();
    let serving = tokio::spawn(server.serve(async {
        let _ = shutdown.await;
    }));
    let client = reqwest::Client::new();
    client.post(format!("{base}/api/v1/providers")).bearer_auth(&endpoint.token).json(&json!({"request_id":uuid::Uuid::new_v4().to_string(),"expected_revision":0,"endpoint":format!("http://{address}/model"),"model":"fixture","protocol":"openai_chat","api_key":"fixture","max_output_tokens":1024})).send().await.unwrap().error_for_status().unwrap();
    client
        .post(format!("{base}/api/v1/messages"))
        .bearer_auth(&endpoint.token)
        .json(&json!({"request_id":"main","conversation_id":"main","text":"wait"}))
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap();
    let task: Value = client
        .post(format!("{base}/api/v1/tasks"))
        .bearer_auth(&endpoint.token)
        .json(&json!({"request_id":"worker","text":"wait"}))
        .send()
        .await
        .unwrap()
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
    assert!(!task["id"].as_str().unwrap().is_empty());
    tokio::time::timeout(Duration::from_secs(10), async {
        while active.load(std::sync::atomic::Ordering::SeqCst) < 2 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    stop.send(()).unwrap();
    tokio::time::timeout(Duration::from_secs(8), serving)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(!fixture.0.join("workspace/child.pid").exists());
    model.abort();
}

#[tokio::test]
async fn embedded_dispatch_needs_no_ui_socket_and_keeps_driver_listener_private() {
    let fixture = Fixture::new();
    let server = Server::embedded(fixture.config()).await.unwrap();
    let client = server.local_client();
    let reply = client.request(PATH_SNAPSHOT, None).await.unwrap();
    assert!(reply.status.is_success());
    let snapshot: Snapshot = serde_json::from_slice(&reply.body).unwrap();
    assert_eq!(snapshot.conversation_id, "main");
    assert!(!fixture.0.join("data/control.sock").exists());
    assert!(Server::embedded(fixture.config()).await.is_err());
    let endpoint = server.endpoint().unwrap();
    let base = format!("http://{}", server.local_addr().unwrap());
    let (stop, rx) = tokio::sync::oneshot::channel();
    let task = tokio::spawn(server.serve(async {
        let _ = rx.await;
    }));
    assert_eq!(
        reqwest::Client::new()
            .get(format!("{base}{PATH_SNAPSHOT}"))
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .status()
            .as_u16(),
        403
    );
    stop.send(()).unwrap();
    task.await.unwrap().unwrap();
    // A retained in-process handle still owns the directory until it is dropped.
    assert!(Server::embedded(fixture.config()).await.is_err());
    drop(client);
    let next = Server::embedded(fixture.config()).await.unwrap();
    drop(next);
}

#[tokio::test]
async fn local_socket_authorizes_without_tokens_and_refuses_browser_origin() {
    let fixture = Fixture::new();
    let server = fixture.start().await;
    let (stop, rx) = tokio::sync::oneshot::channel();
    let task = tokio::spawn(server.serve_local(async {
        let _ = rx.await;
    }));
    let path = fixture.0.join("data/control.sock");
    for _ in 0..100 {
        if path.exists() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(
        std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
        0o600
    );
    let client = reqwest::Client::builder()
        .unix_socket(path.clone())
        .build()
        .unwrap();
    let response = client
        .get("http://localhost/api/v1/snapshot")
        .send()
        .await
        .unwrap();
    assert!(response.status().is_success());
    assert_eq!(
        client
            .get("http://localhost/api/v1/snapshot")
            .header("origin", "http://localhost")
            .send()
            .await
            .unwrap()
            .status()
            .as_u16(),
        403
    );
    stop.send(()).unwrap();
    task.await.unwrap().unwrap();
    assert!(!path.exists());
}

#[tokio::test]
async fn bare_server_exposes_identity_without_assistant_or_environment_storage() {
    let fixture = Fixture::new();
    let config = fixture.config();
    let server = Server::bind(config).await.unwrap();
    let client = server.local_client();
    let response = client.request("/api/v1/runtime", None).await.unwrap();
    assert_eq!(response.status.as_u16(), 200);
    let identity: Value = serde_json::from_slice(&response.body).unwrap();
    assert_eq!(identity["capabilities"], json!(["runtime"]));
    assert_eq!(
        client
            .request(PATH_SNAPSHOT, None)
            .await
            .unwrap()
            .status
            .as_u16(),
        404
    );
    assert!(!fixture.0.join("data/pot-v2.sqlite").exists());
    assert!(!fixture.0.join("workspace").exists());
    assert!(!fixture.0.join("data/sandboxes.json").exists());
    drop(client);
    server.serve(async {}).await.unwrap();
}

#[tokio::test]
async fn assistant_clients_select_owners_and_edit_rules_without_model_calls() {
    let fixture = Fixture::new();
    let server = fixture.start().await;
    let client = server.local_client();
    let reply = client
        .request(
            "/api/v1/assistants",
            Some(json!({"request_id":"assistant-a","name":"A"})),
        )
        .await
        .unwrap();
    assert_eq!(reply.status.as_u16(), 200);
    let a: Value = serde_json::from_slice(&reply.body).unwrap();
    let reply = client
        .request(
            "/api/v1/assistants",
            Some(json!({"request_id":"assistant-b","name":"B"})),
        )
        .await
        .unwrap();
    let b: Value = serde_json::from_slice(&reply.body).unwrap();
    assert_ne!(a["id"], b["id"]);
    for assistant in [&a, &b] {
        let id = assistant["id"].as_str().unwrap();
        let response = client
            .request(&format!("/api/v1/assistants/{id}/snapshot"), None)
            .await
            .unwrap();
        assert_eq!(response.status.as_u16(), 200);
        let snapshot: Value = serde_json::from_slice(&response.body).unwrap();
        assert_eq!(snapshot["conversation_id"], assistant["id"]);
        assert_eq!(snapshot["messages"], json!([]));
        let missing = client
            .request(
                &format!("/api/v1/assistants/{id}/tasks/{}", uuid::Uuid::new_v4()),
                None,
            )
            .await
            .unwrap();
        assert_eq!(
            missing.status.as_u16(),
            404,
            "{}",
            String::from_utf8_lossy(&missing.body)
        );
    }
    let grouped = client
        .request("/api/v1/assistant-tasks", None)
        .await
        .unwrap();
    assert_eq!(grouped.status.as_u16(), 200);
    let response = client
        .request(
            &format!("/api/v1/assistants/{}/agent", a["id"].as_str().unwrap()),
            None,
        )
        .await
        .unwrap();
    assert_eq!(response.status.as_u16(), 200);
    drop(client);
    server.serve(async {}).await.unwrap();
}

#[tokio::test]
async fn caller_created_resources_are_idle_idempotent_isolated_and_restored() {
    let fixture = Fixture::new();
    let config = fixture.config();
    let client = reqwest::Client::new();
    let mut original = Value::Null;
    for restarting in [false, true] {
        let server = Server::bind(config.clone()).await.unwrap();
        let endpoint = server.endpoint().unwrap();
        let base = format!("http://{}", server.local_addr().unwrap());
        let (stop, shutdown) = tokio::sync::oneshot::channel();
        let serving = tokio::spawn(server.serve(async {
            let _ = shutdown.await;
        }));
        let path = format!("{base}/api/v1/runtime/resources");
        assert_eq!(
            client
                .post(&path)
                .json(&json!({"request_id":"intruder","resource":{"kind":"host"}}))
                .send()
                .await
                .unwrap()
                .status()
                .as_u16(),
            401
        );
        let before: Value = client
            .get(&path)
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        if !restarting {
            assert!(before["resources"].as_array().unwrap().is_empty());
            assert!(!fixture.0.join("data/pot-v2.sqlite").exists());
            assert!(!fixture.0.join("data/sandboxes.json").exists());
            assert_eq!(client.post(&path).bearer_auth(&endpoint.token).json(&json!({"request_id":"invalid-assistant","resource":{"kind":"assistant","name":"\n"}})).send().await.unwrap().status().as_u16(), 400);
            assert!(!fixture.0.join("data/pot-v2.sqlite").exists());
            let request = json!({"request_id":"create-assistant","resource":{"kind":"assistant","name":"Research"}});
            original = client
                .post(&path)
                .bearer_auth(&endpoint.token)
                .json(&request)
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
            assert_eq!(original["definition"]["id"], "main");
            let environments: Value = client
                .get(format!("{base}/api/v1/environments"))
                .bearer_auth(&endpoint.token)
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
            assert!(environments["items"].as_array().unwrap().is_empty());
            assert!(!fixture.0.join("data/sandboxes.json").exists());
            let runtime: Value = client
                .get(format!("{base}/api/v1/runtime"))
                .bearer_auth(&endpoint.token)
                .send()
                .await
                .unwrap()
                .json()
                .await
                .unwrap();
            assert!(
                !runtime["capabilities"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|c| c == "host_execution")
            );
            let conflict = client.post(&path).bearer_auth(&endpoint.token).json(&json!({"request_id":"create-assistant","resource":{"kind":"assistant","name":"Different"}})).send().await.unwrap();
            assert_eq!(conflict.status().as_u16(), 409);
            client
                .post(&path)
                .bearer_auth(&endpoint.token)
                .json(&json!({"request_id":"enable-host","resource":{"kind":"host"}}))
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap();
        } else {
            assert_eq!(before["resources"].as_array().unwrap().len(), 2);
        }
        let duplicate: Value = client.post(&path).bearer_auth(&endpoint.token).json(&json!({"request_id":"create-assistant","resource":{"kind":"assistant","name":"Research"}})).send().await.unwrap().error_for_status().unwrap().json().await.unwrap();
        assert_eq!(duplicate, original);
        let assistants: Value = client
            .get(format!("{base}/api/v1/assistants"))
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        assert_eq!(assistants["assistants"].as_array().unwrap().len(), 1);
        let environments: Value = client
            .get(format!("{base}/api/v1/environments"))
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        assert_eq!(environments["items"].as_array().unwrap().len(), 1);
        assert_eq!(environments["items"][0]["kind"], "host");
        assert!(!fixture.0.join("data/sandboxes.json").exists());
        let snapshot: Snapshot = client
            .get(format!("{base}{PATH_SNAPSHOT}"))
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        assert!(snapshot.messages.is_empty() && snapshot.turn.is_none());
        stop.send(()).unwrap();
        serving.await.unwrap().unwrap();
    }
}

#[tokio::test]
async fn resource_manifest_restores_current_assistants_rules_and_sandbox_definitions() {
    let fixture = Fixture::new();
    let client = reqwest::Client::new();
    let mut original_rules = Value::Null;
    for upgraded in [false, true] {
        let server = if upgraded {
            Server::bind(fixture.config()).await.unwrap()
        } else {
            fixture.start().await
        };
        if !upgraded {
            let provider = if cfg!(target_os = "linux") {
                pot_sandbox::LINUX_PODMAN_PROVIDER
            } else {
                pot_sandbox::MACOS_APPLE_CONTAINER_PROVIDER
            };
            server.local_client().request("/api/v1/runtime/resources", Some(json!({"request_id":"sandbox","resource":{"kind":"sandbox","sandbox_id":"default","name":"Linux","provider":provider}}))).await.unwrap();
        }
        let endpoint = server.endpoint().unwrap();
        let base = format!("http://{}", server.local_addr().unwrap());
        let (stop, shutdown) = tokio::sync::oneshot::channel();
        let serving = tokio::spawn(server.serve(async {
            let _ = shutdown.await;
        }));
        let path = format!("{base}/api/v1/assistants/main/instructions");
        let rules: Value = client
            .get(&path)
            .bearer_auth(&endpoint.token)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        if !upgraded {
            original_rules = client
                .post(&path)
                .bearer_auth(&endpoint.token)
                .json(&json!({"text":"CURRENT_DB_RULE","expected_revision":rules["revision"]}))
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
        } else {
            assert_eq!(rules, original_rules);
            let environments: Value = client
                .get(format!("{base}/api/v1/environments"))
                .bearer_auth(&endpoint.token)
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
            assert!(
                environments["items"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|e| e["kind"] == "host")
            );
            assert!(
                environments["items"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|e| e["id"] == "default")
            );
            let assistants: Value = client
                .get(format!("{base}/api/v1/assistants"))
                .bearer_auth(&endpoint.token)
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
            assert_eq!(assistants["assistants"].as_array().unwrap().len(), 1);
        }
        stop.send(()).unwrap();
        serving.await.unwrap().unwrap();
    }
}

#[tokio::test]
async fn sandbox_creation_receipts_do_not_undo_later_inventory_changes() {
    let fixture = Fixture::new();
    let client = reqwest::Client::new();
    let config = fixture.config();
    let provider = if cfg!(target_os = "linux") {
        pot_sandbox::LINUX_PODMAN_PROVIDER
    } else {
        pot_sandbox::MACOS_APPLE_CONTAINER_PROVIDER
    };
    for stage in 0..3 {
        let server = Server::bind(config.clone()).await.unwrap();
        let endpoint = server.endpoint().unwrap();
        let base = format!("http://{}", server.local_addr().unwrap());
        let (stop, shutdown) = tokio::sync::oneshot::channel();
        let serving = tokio::spawn(server.serve(async {
            let _ = shutdown.await;
        }));
        if stage == 0 {
            client.post(format!("{base}/api/v1/runtime/resources")).bearer_auth(&endpoint.token).json(&json!({"request_id":"build-definition","resource":{"kind":"sandbox","sandbox_id":"build","name":"Build","provider":provider}})).send().await.unwrap().error_for_status().unwrap();
        } else {
            let inventory: Value = client
                .get(format!("{base}/api/v1/sandboxes"))
                .bearer_auth(&endpoint.token)
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
            if stage == 1 {
                assert_eq!(inventory["items"][0]["name"], "Renamed");
            } else {
                assert!(inventory["items"].as_array().unwrap().is_empty());
            }
        }
        assert!(!fixture.0.join("data/pot-v2.sqlite").exists());
        stop.send(()).unwrap();
        serving.await.unwrap().unwrap();
        // Represent current persisted management results independently from old creation receipts.
        let path = fixture.0.join("data/sandboxes.json");
        let mut inventory: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        if stage == 0 {
            inventory["items"]["build"]["name"] = json!("Renamed");
        }
        if stage == 1 {
            inventory["items"] = json!({});
            inventory["retired"] = json!(["build"]);
        }
        std::fs::write(path, serde_json::to_vec(&inventory).unwrap()).unwrap();
    }
}
