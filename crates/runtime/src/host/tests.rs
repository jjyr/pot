use super::*;
use futures::future::BoxFuture;
use pot_sandbox::{Capabilities, Provider, ProviderContext, ProviderDescriptor};
use std::{
    sync::atomic::{AtomicUsize, Ordering},
    time::Duration,
};

#[derive(Default)]
struct Executor {
    starts: AtomicUsize,
    dispatches: AtomicUsize,
}
impl Provider for Executor {
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: "fixture.executor".into(),
            os: "linux".into(),
            host_os: "fixture".into(),
            engine: "fixture".into(),
            capabilities: Capabilities {
                shell: true,
                files: true,
                desktop: false,
                shared_execution: true,
            },
        }
    }
    fn workspace(&self, root: &Path, target: &str) -> PathBuf {
        root.join("sandboxes").join(target).join("workspace")
    }
    fn guest_workspace(&self) -> &str {
        "/data/workspace"
    }
    fn lifecycle<'a>(
        &'a self,
        _: &'a ProviderContext,
        method: &'a str,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            if method == "start" {
                self.starts.fetch_add(1, Ordering::SeqCst);
            }
            Ok(json!({"phase": "running", "generation": 1}))
        })
    }
    fn health<'a>(&'a self, _: &'a ProviderContext) -> BoxFuture<'a, Result<bool>> {
        Box::pin(async { Ok(true) })
    }
    fn request<'a>(
        &'a self,
        _: &'a ProviderContext,
        route: &'a str,
        _: &'a Value,
        _: Duration,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            if route == "execute" {
                self.dispatches.fetch_add(1, Ordering::SeqCst);
                Ok(
                    json!({"result": {"stdout":"environment-only", "session_id":"fixture-session"}, "events":[]}),
                )
            } else {
                Ok(json!({"settled":true}))
            }
        })
    }
    fn validate_config(&self, _: &Value) -> Result<()> {
        Ok(())
    }
}

struct Node {
    backend: Backend,
    url: String,
    token: String,
    stop: tokio::sync::oneshot::Sender<()>,
    serving: tokio::task::JoinHandle<()>,
}
impl Node {
    async fn start(root: &Path, enable_host: bool, providers: Vec<Arc<dyn Provider>>) -> Self {
        let mut backend = initialize(RuntimeConfig {
            data: root.join("product"),
            workspace: root.join("workspace"),
        })
        .await
        .unwrap();
        if enable_host {
            backend.enable_host_resource();
        }
        if !providers.is_empty() {
            let provider_id = providers[0].descriptor().id;
            let manager = backend.register_environment_providers(providers).unwrap();
            manager
                .management(
                    &json!({"action":"create","sandbox_id":"default","provider":provider_id}),
                    false,
                    &|| false,
                    &|_, action| action(),
                )
                .await
                .unwrap();
        }
        backend.activate().unwrap();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let token = backend.endpoint(0).token;
        let routes = backend.host().router();
        let (stop, stopped) = tokio::sync::oneshot::channel();
        let serving = tokio::spawn(async move {
            axum::serve(listener, routes)
                .with_graceful_shutdown(async {
                    let _ = stopped.await;
                })
                .await
                .unwrap();
        });
        Self {
            backend,
            url,
            token,
            stop,
            serving,
        }
    }
    async fn get(&self, path: &str) -> reqwest::Response {
        reqwest::Client::new()
            .get(format!("{}{path}", self.url))
            .bearer_auth(&self.token)
            .send()
            .await
            .unwrap()
    }
    async fn post(&self, path: &str, body: Value) -> reqwest::Response {
        reqwest::Client::new()
            .post(format!("{}{path}", self.url))
            .bearer_auth(&self.token)
            .json(&body)
            .send()
            .await
            .unwrap()
    }
    async fn close(mut self) {
        self.backend.shutdown().await;
        let _ = self.stop.send(());
        self.serving.await.unwrap();
    }
}
fn root() -> PathBuf {
    std::env::temp_dir().join(format!("pot-resources-{}", id()))
}

#[tokio::test]
async fn host_only_executes_and_restores_receipts_without_sandbox_or_agent() {
    let root = root();
    let node = Node::start(&root, true, vec![]).await;
    assert!(node.backend.state().is_err());
    let description: Value = node.get("/api/v1/runtime").await.json().await.unwrap();
    assert_eq!(
        description["capabilities"],
        json!(["runtime", "host_execution"])
    );
    assert_eq!(
        node.get("/api/v1/sandboxes").await.status(),
        StatusCode::NOT_FOUND
    );
    let inventory: Value = node.get("/api/v1/environments").await.json().await.unwrap();
    assert_eq!(inventory["items"].as_array().unwrap().len(), 1);
    let run = id();
    let op = json!({"run_id":run,"call_id":"write","environment_ref":inventory["items"][0]["environment_ref"],"name":"exec_command","args":{"cmd":"printf once >> result.txt"}});
    let response = node.post("/api/v1/execution", op.clone()).await;
    assert_eq!(response.status(), StatusCode::OK);
    let first: Value = response.json().await.unwrap();
    assert!(first.get("error").is_none(), "{first}");
    let duplicate: Value = node
        .post("/api/v1/execution", op.clone())
        .await
        .json()
        .await
        .unwrap();
    assert_eq!(first, duplicate);
    let receipt: Value = node
        .get(&format!(
            "/api/v1/environment/operations?run_id={run}&call_id=write"
        ))
        .await
        .json()
        .await
        .unwrap();
    assert_eq!(receipt["state"], "completed");
    assert_eq!(receipt["result"], first);
    let mut conflict = op.clone();
    conflict["args"]["cmd"] = json!("printf bad >> result.txt");
    assert_eq!(
        node.post("/api/v1/execution", conflict).await.status(),
        StatusCode::CONFLICT
    );
    node.close().await;
    let node = Node::start(&root, true, vec![]).await;
    assert_eq!(
        node.post("/api/v1/execution", op.clone())
            .await
            .json::<Value>()
            .await
            .unwrap(),
        first
    );
    assert_eq!(
        std::fs::read_to_string(
            root.join("product/host-runs")
                .join(&run)
                .join("workspace/result.txt")
        )
        .unwrap(),
        "once"
    );
    assert!(!root.join("product/sandboxes").exists());
    assert!(!root.join("product/pot-v2.sqlite").exists());
    // A persisted acceptance without a final receipt must never replay effects.
    let receipt_path = std::fs::read_dir(root.join("product/host-operations"))
        .unwrap()
        .next()
        .unwrap()
        .unwrap()
        .path();
    let mut interrupted: Value =
        serde_json::from_slice(&std::fs::read(&receipt_path).unwrap()).unwrap();
    interrupted["result"] = Value::Null;
    execution::private_json(&receipt_path, &interrupted).unwrap();
    let receipt: Value = node
        .get(&format!(
            "/api/v1/environment/operations?run_id={run}&call_id=write"
        ))
        .await
        .json()
        .await
        .unwrap();
    assert_eq!(receipt["state"], "uncertain");
    assert_eq!(
        node.post("/api/v1/execution", op).await.status(),
        StatusCode::CONFLICT
    );
    assert_eq!(
        std::fs::read_to_string(
            root.join("product/host-runs")
                .join(&run)
                .join("workspace/result.txt")
        )
        .unwrap(),
        "once"
    );
    assert_eq!(
        node.post("/api/v1/environment/cancel", json!({"run_id":run}))
            .await
            .status(),
        StatusCode::OK
    );
    node.close().await;
    std::fs::remove_dir_all(root).unwrap();
}

#[tokio::test]
async fn host_cancel_cleans_only_its_run_and_prevents_new_effects() {
    let root = root();
    let node = Node::start(&root, true, vec![]).await;
    let environment = pot_core::EnvironmentRef::host(node.backend.host().runtime_id());
    let run = id();
    let sibling = id();
    let start = |run: &str| json!({"run_id":run,"call_id":"start","environment_ref":environment,"name":"exec_command","args":{"cmd":"sleep 60","yield_time_ms":1}});
    let first: Value = node
        .post("/api/v1/execution", start(&run))
        .await
        .json()
        .await
        .unwrap();
    let second: Value = node
        .post("/api/v1/execution", start(&sibling))
        .await
        .json()
        .await
        .unwrap();
    assert!(first["result"]["session_id"].is_string(), "{first}");
    assert!(second["result"]["session_id"].is_string(), "{second}");
    assert_eq!(
        node.post("/api/v1/environment/cancel", json!({"run_id":run}))
            .await
            .status(),
        StatusCode::OK
    );
    let poll = |run: &str, session: &Value| json!({"run_id":run,"call_id":"poll","environment_ref":environment,"name":"write_stdin","args":{"session_id":session,"yield_time_ms":1}});
    assert_eq!(
        node.post(
            "/api/v1/execution",
            poll(&run, &first["result"]["session_id"])
        )
        .await
        .status(),
        StatusCode::CONFLICT
    );
    let live: Value = node
        .post(
            "/api/v1/execution",
            poll(&sibling, &second["result"]["session_id"]),
        )
        .await
        .json()
        .await
        .unwrap();
    assert!(live.get("error").is_none(), "{live}");
    assert_eq!(live["result"]["session_id"], second["result"]["session_id"]);
    let foreign = json!({"run_id":sibling,"call_id":"foreign","environment_ref":environment,"name":"write_stdin","args":{"session_id":first["result"]["session_id"],"yield_time_ms":1}});
    let denied: Value = node
        .post("/api/v1/execution", foreign)
        .await
        .json()
        .await
        .unwrap();
    assert_eq!(denied["isError"], true);
    assert_eq!(
        node.post("/api/v1/environment/cancel", json!({"run_id":sibling}))
            .await
            .status(),
        StatusCode::OK
    );
    node.close().await;
    std::fs::remove_dir_all(root).unwrap();
}

#[tokio::test]
async fn bare_startup_is_idle_and_identity_survives_restart() {
    let root = root();
    let node = Node::start(&root, false, vec![]).await;
    assert!(node.backend.state().is_err());
    let first: Value = node.get("/api/v1/runtime").await.json().await.unwrap();
    assert_eq!(first["capabilities"], json!(["runtime"]));
    assert_eq!(
        node.get("/api/v1/messages").await.status(),
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        node.get("/api/v1/sandboxes").await.status(),
        StatusCode::NOT_FOUND
    );
    assert!(!root.join("workspace").exists());
    let mut files: Vec<_> = std::fs::read_dir(root.join("product"))
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .collect();
    files.sort();
    assert_eq!(files, ["instance.lock", "runtime-identity.json"]);
    assert_eq!(
        reqwest::Client::new()
            .get(format!("{}/api/v1/runtime", node.url))
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::UNAUTHORIZED
    );
    assert!(node.backend.activate().is_err());
    node.close().await;
    let node = Node::start(&root, false, vec![]).await;
    let second: Value = node.get("/api/v1/runtime").await.json().await.unwrap();
    assert_eq!(first["runtime_id"], second["runtime_id"]);
    assert_ne!(first["boot_id"], second["boot_id"]);
    node.close().await;
    std::fs::remove_dir_all(root).unwrap();
}

#[tokio::test]
async fn environment_executes_without_agent_and_replays_receipts_not_effects() {
    let root = root();
    let executor = Arc::new(Executor::default());
    let node = Node::start(&root, true, vec![executor.clone()]).await;
    assert!(node.backend.state().is_err());
    assert_eq!(
        node.get("/api/v1/tasks").await.status(),
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        node.post("/api/v1/sandboxes", json!({"action":"set_mounts"}))
            .await
            .status(),
        StatusCode::FORBIDDEN
    );
    let run = id();
    let op = json!({"run_id":run,"call_id":"command","sandbox_id":"default","name":"exec_command","args":{"cmd":"true"}});
    let mut invalid = op.clone();
    invalid["args"] = json!({});
    assert_eq!(
        node.post("/api/v1/execution", invalid).await.status(),
        StatusCode::BAD_REQUEST
    );
    let mut invalid_id = op.clone();
    invalid_id["call_id"] = json!("");
    assert_eq!(
        node.post("/api/v1/execution", invalid_id).await.status(),
        StatusCode::BAD_REQUEST
    );
    assert_eq!(executor.starts.load(Ordering::SeqCst), 0);
    for _ in 0..2 {
        let response = node.post("/api/v1/execution", op.clone()).await;
        assert_eq!(response.status(), StatusCode::OK);
        let value: Value = response.json().await.unwrap();
        assert_eq!(value["result"]["stdout"], "environment-only");
    }
    assert_eq!(executor.dispatches.load(Ordering::SeqCst), 1);
    let mut conflicting = op.clone();
    conflicting["args"]["cmd"] = json!("different");
    assert_eq!(
        node.post("/api/v1/execution", conflicting).await.status(),
        StatusCode::CONFLICT
    );
    let response = node
        .post("/api/v1/environment/cancel", json!({"run_id":run}))
        .await;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        node.post("/api/v1/execution", op.clone()).await.status(),
        StatusCode::CONFLICT
    );
    let mut other = op;
    other["run_id"] = json!(id());
    assert_eq!(
        node.post("/api/v1/execution", other).await.status(),
        StatusCode::OK
    );
    assert_eq!(executor.dispatches.load(Ordering::SeqCst), 2);
    for file in [
        "pot-v2.sqlite",
        "usage-v2.sqlite",
        "providers.json",
        "execution-limits.json",
    ] {
        assert!(
            !root.join("product").join(file).exists(),
            "unexpected {file}"
        );
    }
    node.close().await;
    std::fs::remove_dir_all(root).unwrap();
}

#[tokio::test]
async fn neutral_startup_ignores_obsolete_preset_without_opening_product_data() {
    let root = root();
    let data = root.join("product");
    std::fs::create_dir_all(&data).unwrap();
    std::fs::write(data.join("pot-v2.sqlite"), b"existing data").unwrap();
    execution::private_json(
        &data.join("runtime-composition.json"),
        &json!({"schema_version":1,"preset":"assistant"}),
    )
    .unwrap();
    let mut backend = initialize(RuntimeConfig {
        data: data.clone(),
        workspace: root.join("workspace"),
    })
    .await
    .unwrap();
    assert_eq!(
        backend.host().describe()["capabilities"],
        json!(["runtime"])
    );
    assert!(backend.optional_state().is_none());
    assert_eq!(
        std::fs::read(data.join("pot-v2.sqlite")).unwrap(),
        b"existing data"
    );
    backend.shutdown().await;
    std::fs::remove_dir_all(root).unwrap();
}

#[tokio::test]
async fn control_cancel_closes_sandbox_scope_and_preserves_other_owner() {
    let root = root();
    let node = Node::start(&root, true, vec![Arc::new(Executor::default())]).await;
    let host = node.backend.host();
    let target = pot_core::EnvironmentRef::sandbox(&host.runtime_id, "default");
    for scope in ["control-a", "control-b"] {
        host.execute_controlled(
            ControlOperation {
                resources_revision: None,
                scope: scope.into(),
                workspace_scope: scope.into(),
                run_id: id(),
                generation: 1,
                target: target.clone(),
                call_id: "command".into(),
                name: "exec_command".into(),
                args: json!({"cmd":"true"}),
            },
            Arc::new(|| false),
        )
        .await
        .unwrap();
    }
    let manager = node.backend.environment_manager().unwrap();
    assert_eq!(manager.inventory().await["items"][0]["active_leases"], 2);
    host.cancel_controlled("control-a", &target.target)
        .await
        .unwrap();
    assert_eq!(manager.inventory().await["items"][0]["active_leases"], 1);
    host.cancel_controlled("control-b", &target.target)
        .await
        .unwrap();
    assert_eq!(manager.inventory().await["items"][0]["active_leases"], 0);
    assert_eq!(
        manager.inventory().await["items"][0]["unsettled_operations"],
        0
    );
    node.close().await;
    std::fs::remove_dir_all(root).unwrap();
}
