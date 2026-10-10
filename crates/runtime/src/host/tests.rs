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
    async fn start(
        root: &Path,
        composition: Composition,
        providers: Vec<Arc<dyn Provider>>,
    ) -> Self {
        let backend = initialize_composition(
            RuntimeConfig {
                data: root.join("product"),
                workspace: root.join("workspace"),
            },
            composition,
            providers,
        )
        .await
        .unwrap();
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
    std::env::temp_dir().join(format!("pot-composition-{}", id()))
}

#[tokio::test]
async fn bare_startup_is_idle_and_identity_survives_restart() {
    let root = root();
    let node = Node::start(&root, Composition::Bare, vec![]).await;
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
    let node = Node::start(&root, Composition::Bare, vec![]).await;
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
    let node = Node::start(&root, Composition::Environment, vec![executor.clone()]).await;
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

#[test]
fn composition_rollout_preserves_existing_roots_and_rejects_future_versions() {
    let root = root();
    let data = root.join("product");
    std::fs::create_dir_all(&data).unwrap();
    assert_eq!(Composition::load(&data).unwrap(), Composition::Bare);
    std::fs::write(data.join("pot-v2.sqlite"), b"existing data").unwrap();
    assert_eq!(Composition::load(&data).unwrap(), Composition::Assistant);
    execution::private_json(
        &data.join("runtime-composition.json"),
        &json!({"schema_version":1,"preset":"environment"}),
    )
    .unwrap();
    assert_eq!(Composition::load(&data).unwrap(), Composition::Environment);
    execution::private_json(
        &data.join("runtime-composition.json"),
        &json!({"schema_version":2,"preset":"environment"}),
    )
    .unwrap();
    assert!(Composition::load(&data).is_err());
    assert_eq!(
        std::fs::read(data.join("pot-v2.sqlite")).unwrap(),
        b"existing data"
    );
    std::fs::remove_dir_all(root).unwrap();
}
