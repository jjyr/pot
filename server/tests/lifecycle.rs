use pot_protocol::{PATH_EVENTS, PATH_SNAPSHOT, RUNTIME_ENDPOINT_FILE, RuntimeEndpoint, Snapshot};
use pot_runtime::RuntimeConfig;
use pot_server::{Server, ServerConfig};
use serde_json::{Value, json};
use std::{os::unix::fs::PermissionsExt, path::PathBuf, time::Duration};
static NEXT_FIXTURE: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        Self(std::env::temp_dir().join(format!(
                "pot-server-{}-{}-{}",
                std::process::id(),
                NEXT_FIXTURE.fetch_add(1, std::sync::atomic::Ordering::Relaxed),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            )))
    }
    fn config(&self) -> ServerConfig {
        ServerConfig {
            runtime: RuntimeConfig {
                data: self.0.join("data"),
                workspace: self.0.join("workspace"),
                pi_program: "pi".into(),
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
    let server = Server::bind(fixture.config()).await.unwrap();
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
    let next = Server::bind(fixture.config()).await.unwrap();
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
async fn shutdown_settles_active_main_and_worker_processes() {
    let fixture = Fixture::new();
    let server = Server::bind(fixture.config()).await.unwrap();
    let endpoint = server.endpoint().unwrap();
    let base = format!("http://{}", server.local_addr().unwrap());
    let cli = fixture.0.join("agy-fixture");
    std::fs::write(&cli, r#"#!/usr/bin/env python3
import json, signal, sys, time
from pathlib import Path
if sys.argv[1:] == ['--help']:
    print('--print --output-format stream-json --conversation --model --disable-slash-commands'); sys.exit(0)
text=sys.stdin.read()
def stop(*_):
    Path('stopped').write_text('interrupt acknowledged'); sys.exit(0)
signal.signal(signal.SIGINT, stop)
print(json.dumps({'event':'init','conversation_id':'00000000-0000-4000-8000-000000000001'}), flush=True)
Path('started').write_text('ready')
while True: time.sleep(0.05)
"#).unwrap();
    std::fs::set_permissions(&cli, std::fs::Permissions::from_mode(0o700)).unwrap();
    let (stop, shutdown) = tokio::sync::oneshot::channel();
    let serving = tokio::spawn(server.serve(async {
        let _ = shutdown.await;
    }));
    let client = reqwest::Client::new();
    let profile = json!({"id":"agy-shutdown","revision":1,"name":"fixture","engine":"agy","provider":"google","model":"agy-fixture","auth_ref":"cli:agy",
        "launch_spec":{"transport":"agy_json","executable":cli,"cwd_policy":"workspace","argv":[{"kind":"runtime_arguments"}]},
        "params":{"max_output_tokens":256,"reasoning_effort":null},"tool_policy":{"worker_orchestration":false,"summary_read_only":true,"unattended":"guest_vm"}});
    client.post(format!("{base}/api/v1/agent")).bearer_auth(&endpoint.token).json(&json!({"profile":profile,"expected_revision":0,"request_id":"setup","follow_worker":true})).send().await.unwrap().error_for_status().unwrap();
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
    let task_dir = fixture
        .0
        .join("workspace/tasks")
        .join(task["id"].as_str().unwrap());
    tokio::time::timeout(Duration::from_secs(5), async {
        while !fixture.0.join("workspace/started").exists() || !task_dir.join("started").exists() {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    stop.send(()).unwrap();
    tokio::time::timeout(Duration::from_secs(5), serving)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(fixture.0.join("workspace/stopped").exists());
    assert!(task_dir.join("stopped").exists());
}
