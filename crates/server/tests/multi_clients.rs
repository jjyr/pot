//! Real HTTP clients and production Runtime/Access; no model credentials or containers.
use pot_runtime::{Backend, RuntimeConfig};
use pot_server::access::{Access, AccessConfig, admin_router, public_router};
use reqwest::{Client, Response};
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    time::Duration,
};

struct Host {
    backend: Backend,
    access: Access,
    public: String,
    admin: String,
    token: String,
    stops: Vec<tokio::sync::oneshot::Sender<()>>,
    servers: Vec<tokio::task::JoinHandle<()>>,
}
impl Host {
    async fn start(root: &Path) -> Self {
        Self::start_with(
            root,
            std::sync::Arc::new(
                pot_sandbox_macos_apple_container::MacOSAppleContainerProvider::new().unwrap(),
            ),
        )
        .await
    }
    async fn start_with(root: &Path, provider: std::sync::Arc<dyn pot_sandbox::Provider>) -> Self {
        let backend = pot_runtime::initialize_with_providers(
            RuntimeConfig {
                data: root.join("product"),
                workspace: root.join("workspace"),
            },
            vec![provider],
        )
        .await
        .unwrap();
        let token = backend.endpoint(0).token;
        let cfg = AccessConfig {
            origin: url::Url::parse("https://pot.test/").unwrap(),
            bind: "127.0.0.1:0".parse().unwrap(),
            certificate: root.join("cert"),
            private_key: root.join("key"),
            assets: root.to_path_buf(),
        };
        let routes = pot_server::router(backend.state());
        let access =
            Access::new(&cfg, root.join("product"), token.clone(), routes.clone()).unwrap();
        backend.activate().unwrap();
        let mut stops = vec![];
        let mut servers = vec![];
        let mut addresses = vec![];
        for router in [
            public_router(access.clone()),
            routes.merge(admin_router(access.clone())),
        ] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            addresses.push(format!("http://{}", listener.local_addr().unwrap()));
            let (tx, rx) = tokio::sync::oneshot::channel();
            stops.push(tx);
            servers.push(tokio::spawn(async move {
                axum::serve(listener, router)
                    .with_graceful_shutdown(async {
                        let _ = rx.await;
                    })
                    .await
                    .unwrap();
            }));
        }
        let mut host = Self {
            backend,
            access,
            public: addresses.remove(0),
            admin: addresses.remove(0),
            token,
            stops,
            servers,
        };
        let port = url::Url::parse(&host.admin).unwrap().port().unwrap();
        pot_sandbox::private_json(
            &root.join("product/runtime.json"),
            &json!(host.backend.endpoint(port)),
        )
        .unwrap();
        if !root.join("product/providers.json").exists() {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let endpoint = format!("http://{}/model", listener.local_addr().unwrap());
            let model = axum::Router::new().route("/model",axum::routing::post(|axum::Json(input):axum::Json<Value>| async move {
                let stream = async_stream::stream! {
                    if input.to_string().contains("cancel this") { std::future::pending::<()>().await; }
                    tokio::time::sleep(Duration::from_millis(100)).await;
                    yield Ok::<_,std::convert::Infallible>(axum::response::sse::Event::default().data(json!({"choices":[{"delta":{"content":"fixture reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}).to_string()));
                    yield Ok(axum::response::sse::Event::default().data("[DONE]"));
                };
                axum::response::sse::Sse::new(stream)
            }));
            let (tx, rx) = tokio::sync::oneshot::channel();
            host.stops.push(tx);
            host.servers.push(tokio::spawn(async move {
                axum::serve(listener, model)
                    .with_graceful_shutdown(async {
                        let _ = rx.await;
                    })
                    .await
                    .unwrap();
            }));
            host.admin("/api/v1/providers",Some(json!({"request_id":uuid::Uuid::new_v4().to_string(),"expected_revision":0,"endpoint":endpoint,"model":"fixture","protocol":"openai_chat","api_key":"fixture"}))).await.error_for_status().unwrap();
        }
        host
    }

    async fn admin(&self, path: &str, body: Option<Value>) -> Response {
        let client = Client::builder().no_proxy().build().unwrap();
        let url = format!("{}{path}", self.admin);
        let req = if let Some(body) = body {
            client.post(url).json(&body)
        } else {
            client.get(url)
        };
        req.bearer_auth(&self.token).send().await.unwrap()
    }
    async fn command(&self, body: Value) -> Value {
        self.admin("/api/v1/access", Some(body))
            .await
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap()
    }
    async fn stop(mut self) {
        self.backend.shutdown().await;
        // Revoke ephemeral sessions before draining open public streams.
        self.access.inner.lock().unwrap().enabled = false;
        for stop in self.stops.drain(..) {
            let _ = stop.send(());
        }
        for server in self.servers.drain(..) {
            tokio::time::timeout(Duration::from_secs(5), server)
                .await
                .unwrap()
                .unwrap();
        }
    }
}
struct Browser {
    http: Client,
    token: String,
    session: String,
}
impl Browser {
    fn new() -> Self {
        Self {
            http: Client::builder().no_proxy().build().unwrap(),
            token: String::new(),
            session: String::new(),
        }
    }
    async fn request(&self, h: &Host, path: &str, body: Option<Value>) -> Response {
        let url = format!("{}{path}", h.public);
        let req = if let Some(body) = body {
            self.http
                .post(url)
                .header("origin", "https://pot.test")
                .json(&body)
        } else {
            self.http.get(url)
        };
        req.header("cookie", &self.session).send().await.unwrap()
    }
    async fn configure(&mut self, h: &Host) {
        self.token = h.command(json!({"action":"token"})).await["token"]
            .as_str()
            .unwrap()
            .to_owned();
        self.login(h).await;
    }
    async fn login(&mut self, h: &Host) {
        let response = self
            .http
            .post(format!("{}/access/token", h.public))
            .header("origin", "https://pot.test")
            .bearer_auth(&self.token)
            .json(&json!({}))
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap();
        self.session = cookie_of(&response);
    }
    async fn snapshot(&self, h: &Host) -> Value {
        self.request(h, "/api/v1/snapshot", None)
            .await
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap()
    }
}
fn cookie_of(response: &Response) -> String {
    response.headers()["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .into()
}

struct Root(PathBuf);
impl Root {
    fn new() -> Self {
        Self(std::env::temp_dir().join(format!("pot-clients-{}", uuid::Uuid::new_v4())))
    }
}
impl Drop for Root {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}
async fn contains(stream: &mut Response, needle: &str) {
    tokio::time::timeout(Duration::from_secs(5), async {
        let mut data = String::new();
        loop {
            let chunk = stream
                .chunk()
                .await
                .unwrap()
                .expect("stream ended before expected update");
            data.push_str(&String::from_utf8_lossy(&chunk));
            if data.contains(needle) {
                break;
            }
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn token_survives_restart_and_rotation_invalidates_all_clients() {
    let root = Root::new();
    let h = Host::start(&root.0).await;
    let mut a = Browser::new();
    let mut b = Browser::new();
    a.configure(&h).await;
    b.configure(&h).await;
    assert_eq!(a.token, b.token);
    assert_ne!(a.session, b.session);
    assert_eq!(a.snapshot(&h).await, b.snapshot(&h).await);
    h.stop().await;
    let h = Host::start(&root.0).await;
    assert!(
        a.request(&h, "/api/v1/snapshot", None)
            .await
            .status()
            .is_success()
    );
    assert!(
        b.request(&h, "/api/v1/snapshot", None)
            .await
            .status()
            .is_success()
    );
    h.command(json!({"action":"regenerate_token"})).await;
    for browser in [&a, &b] {
        assert_eq!(
            browser.request(&h, "/api/v1/snapshot", None).await.status(),
            401
        );
    }
    a.configure(&h).await;
    assert!(
        a.request(&h, "/api/v1/snapshot", None)
            .await
            .status()
            .is_success()
    );
    h.stop().await;
}

#[tokio::test]
async fn concurrent_writes_deduplicate_globally_and_all_clients_observe_durable_state() {
    let root = Root::new();
    let h = Host::start(&root.0).await;
    let mut a = Browser::new();
    let mut b = Browser::new();
    a.configure(&h).await;
    b.configure(&h).await;
    let request = json!({"request_id":"same-write","conversation_id":"main","text":"exactly-once"});
    let (ra, rb) = tokio::join!(
        a.request(&h, "/api/v1/messages", Some(request.clone())),
        b.request(&h, "/api/v1/messages", Some(request.clone()))
    );
    let receipt: Value = ra.error_for_status().unwrap().json().await.unwrap();
    let rb: Value = rb.error_for_status().unwrap().json().await.unwrap();
    assert_eq!(receipt["message_id"], rb["message_id"]);
    assert_ne!(receipt["duplicate"], rb["duplicate"]);
    assert_eq!(
        b.request(
            &h,
            "/api/v1/messages",
            Some(json!({"request_id":"same-write","conversation_id":"main","text":"conflicting"}))
        )
        .await
        .status()
        .as_u16(),
        409
    );
    idle(&a, &h).await;
    let a_write = json!({"request_id":"a-write","conversation_id":"main","text":"from-a"});
    let b_write = json!({"request_id":"b-write","conversation_id":"main","text":"from-b"});
    let (ra, rb) = tokio::join!(
        a.request(&h, "/api/v1/messages", Some(a_write.clone())),
        b.request(&h, "/api/v1/messages", Some(b_write.clone()))
    );
    // Main has one active turn. Concurrent distinct writes are explicitly busy,
    // never silently dropped or accepted without a durable receipt.
    let accepted_a = ra.status().is_success();
    let accepted_b = rb.status().is_success();
    assert!(accepted_a || accepted_b);
    for (browser, response, request, accepted) in
        [(&a, ra, a_write, accepted_a), (&b, rb, b_write, accepted_b)]
    {
        if !accepted {
            assert_eq!(response.status().as_u16(), 409);
            assert_eq!(response.json::<Value>().await.unwrap()["code"], "busy");
            idle(&a, &h).await;
            browser
                .request(&h, "/api/v1/messages", Some(request))
                .await
                .error_for_status()
                .unwrap();
        }
    }
    idle(&a, &h).await;
    for browser in [&a, &b] {
        let snapshot = browser.snapshot(&h).await;
        for text in ["exactly-once", "from-a", "from-b"] {
            assert_eq!(
                snapshot["messages"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .filter(|m| m["role"] == "user" && m["text"] == text)
                    .count(),
                1
            );
        }
    }
    let admin: Value = h
        .admin("/api/v1/snapshot", None)
        .await
        .json()
        .await
        .unwrap();
    assert_eq!(admin["messages"], a.snapshot(&h).await["messages"]);
    h.stop().await;
    let h = Host::start(&root.0).await;
    tokio::join!(a.login(&h), b.login(&h));
    let retry: Value = b
        .request(&h, "/api/v1/messages", Some(request))
        .await
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(retry["message_id"], receipt["message_id"]);
    assert_eq!(retry["duplicate"], true);
    assert_eq!(
        a.snapshot(&h).await["messages"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|m| m["text"] == "exactly-once")
            .count(),
        1
    );
    h.stop().await;
}

#[tokio::test]
async fn simultaneous_streams_disconnect_reconnect_and_token_rotation() {
    let root = Root::new();
    let h = Host::start(&root.0).await;
    let mut a = Browser::new();
    let mut b = Browser::new();
    a.configure(&h).await;
    b.configure(&h).await;
    let mut sa = a
        .request(&h, "/api/v1/events?after=0", None)
        .await
        .error_for_status()
        .unwrap();
    let mut sb = b
        .request(&h, "/api/v1/events?after=0", None)
        .await
        .error_for_status()
        .unwrap();
    tokio::join!(
        contains(&mut sa, "event: snapshot"),
        contains(&mut sb, "event: snapshot")
    );
    // An unread third stream must not block other subscribers.
    let slow = b
        .request(&h, "/api/v1/events?after=0", None)
        .await
        .error_for_status()
        .unwrap();
    let write = a
        .request(
            &h,
            "/api/v1/messages",
            Some(
                json!({"request_id":"stream-one","conversation_id":"main","text":"broadcast-one"}),
            ),
        )
        .await;
    assert!(write.status().is_success());
    tokio::join!(
        contains(&mut sa, "broadcast-one"),
        contains(&mut sb, "broadcast-one")
    );
    drop(sb);
    drop(slow);
    let mut sb = b
        .request(&h, "/api/v1/events?after=0", None)
        .await
        .error_for_status()
        .unwrap();
    contains(&mut sb, "broadcast-one").await;
    idle(&b, &h).await;
    h.command(json!({"action":"regenerate_token"})).await;
    tokio::time::timeout(Duration::from_secs(5), async {
        while sa.chunk().await.unwrap().is_some() {}
    })
    .await
    .unwrap();
    tokio::time::timeout(Duration::from_secs(2), async {
        while sb.chunk().await.unwrap().is_some() {}
    })
    .await
    .unwrap();
    b.configure(&h).await;
    assert!(
        b.request(
            &h,
            "/api/v1/messages",
            Some(
                json!({"request_id":"stream-two","conversation_id":"main","text":"broadcast-two"})
            )
        )
        .await
        .status()
        .is_success()
    );
    h.stop().await;
}

async fn idle(browser: &Browser, host: &Host) {
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            if browser.snapshot(host).await["turn"].is_null() {
                break;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn another_client_cancels_main_and_interrupts_tasks_and_both_observe_the_same_outcome() {
    let root = Root::new();
    let h = Host::start(&root.0).await;
    let mut a = Browser::new();
    let mut b = Browser::new();
    a.configure(&h).await;
    b.configure(&h).await;
    let receipt:Value=a.request(&h,"/api/v1/messages",Some(json!({"request_id":"cancel-main","conversation_id":"main","text":"cancel this turn"}))).await.error_for_status().unwrap().json().await.unwrap();
    assert_eq!(
        b.request(
            &h,
            "/api/v1/turn/cancel",
            Some(json!({"request_id":"wrong-cancel","user_message_id":"wrong-turn"}))
        )
        .await
        .status()
        .as_u16(),
        409
    );
    b.request(
        &h,
        "/api/v1/turn/cancel",
        Some(json!({"request_id":"cancel-turn","user_message_id":receipt["message_id"]})),
    )
    .await
    .error_for_status()
    .unwrap();
    idle(&a, &h).await;
    assert_eq!(
        a.snapshot(&h).await["messages"],
        b.snapshot(&h).await["messages"]
    );
    let task:Value=h.admin("/api/v1/tasks",Some(json!({"request_id":"client-task","text":"cancel this task","profile":"apot-default"}))).await.error_for_status().unwrap().json().await.unwrap();
    let id = task["id"].as_str().unwrap();
    let cancel = b
        .request(
            &h,
            &format!("/api/v1/tasks/{id}/interrupt"),
            Some(json!({})),
        )
        .await;
    let status = cancel.status();
    let value = cancel.text().await.unwrap();
    assert!(status.is_success(), "{status}: {value}");
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            let task: Value = a
                .request(&h, "/api/v1/tasks", None)
                .await
                .error_for_status()
                .unwrap()
                .json()
                .await
                .unwrap();
            if task
                .as_array()
                .unwrap()
                .iter()
                .any(|t| t["id"] == id && t["phase"] == "interrupted")
            {
                break;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    let detail = a
        .request(&h, &format!("/api/v1/tasks/{id}"), None)
        .await
        .error_for_status()
        .unwrap()
        .json::<Value>()
        .await
        .unwrap();
    assert_eq!(detail["conversation_id"], id);
    let ta: Value = a
        .request(&h, "/api/v1/tasks", None)
        .await
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
    let tb: Value = b
        .request(&h, "/api/v1/tasks", None)
        .await
        .error_for_status()
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(ta, tb);
    h.stop().await;
}

struct Display;
impl pot_sandbox::Provider for Display {
    fn descriptor(&self) -> pot_sandbox::ProviderDescriptor {
        pot_sandbox::ProviderDescriptor {
            id: "test.display".into(),
            host_os: "test".into(),
            engine: "test".into(),
            os: "linux".into(),
            capabilities: pot_sandbox::Capabilities {
                shell: false,
                files: false,
                desktop: true,
                shared_execution: true,
            },
        }
    }
    fn desktop_capabilities(&self) -> pot_sandbox::DesktopCapabilities {
        pot_sandbox::DesktopCapabilities {
            viewing: true,
            input: true,
            clipboard: true,
            browser: false,
            protocol: Some(pot_sandbox::DesktopProtocol::Rfb38),
        }
    }
    fn workspace(&self, root: &Path, _: &str) -> PathBuf {
        root.join("workspace")
    }
    fn guest_workspace(&self) -> &str {
        "/data/workspace"
    }
    fn lifecycle<'a>(
        &'a self,
        _: &'a pot_sandbox::ProviderContext,
        _: &'a str,
    ) -> futures::future::BoxFuture<'a, anyhow::Result<Value>> {
        Box::pin(async { Ok(json!({"phase":"running","generation":1})) })
    }
    fn health<'a>(
        &'a self,
        _: &'a pot_sandbox::ProviderContext,
    ) -> futures::future::BoxFuture<'a, anyhow::Result<bool>> {
        Box::pin(async { Ok(true) })
    }
    fn request<'a>(
        &'a self,
        _: &'a pot_sandbox::ProviderContext,
        _: &'a str,
        _: &'a Value,
        _: Duration,
    ) -> futures::future::BoxFuture<'a, anyhow::Result<Value>> {
        Box::pin(async { Ok(json!({"settled":true})) })
    }
    fn validate_config(&self, _: &Value) -> anyhow::Result<()> {
        Ok(())
    }
    fn prepare_desktop<'a>(
        &'a self,
        _: &'a pot_sandbox::ProviderContext,
    ) -> futures::future::BoxFuture<'a, anyhow::Result<()>> {
        Box::pin(async { Ok(()) })
    }
    fn open_desktop<'a>(
        &'a self,
        _: &'a pot_sandbox::ProviderContext,
    ) -> futures::future::BoxFuture<'a, anyhow::Result<pot_sandbox::DesktopStream>> {
        Box::pin(async {
            use tokio::io::{AsyncReadExt, AsyncWriteExt};
            let (client, mut server) = tokio::io::duplex(65536);
            tokio::spawn(async move {
                server.write_all(b"RFB 003.008\n").await.unwrap();
                let mut buf = [0; 1024];
                while let Ok(n) = server.read(&mut buf).await {
                    if n == 0 {
                        break;
                    }
                }
            });
            Ok(Box::pin(client) as pot_sandbox::DesktopStream)
        })
    }
}
#[tokio::test]
async fn desktop_tickets_are_browser_bound_and_rotation_disconnects_every_owner() {
    use futures::StreamExt;
    use tokio_tungstenite::tungstenite::client::IntoClientRequest;
    let root = Root::new();
    let host = Host::start_with(&root.0, std::sync::Arc::new(Display)).await;
    let mut a = Browser::new();
    let mut b = Browser::new();
    a.configure(&host).await;
    b.configure(&host).await;
    std::fs::create_dir_all(root.0.join("desktop-assets/core")).unwrap();
    std::fs::write(
        root.0.join("desktop-assets/core/rfb.js"),
        "export default class RFB {};",
    )
    .unwrap();
    assert_eq!(
        Browser::new()
            .request(&host, "/desktop-assets/core/rfb.js", None)
            .await
            .status(),
        401
    );
    assert_eq!(
        a.request(&host, "/desktop-assets/core/rfb.js", None)
            .await
            .status(),
        200
    );
    let mut paths = vec![];
    for client in [&a, &b] {
        let response = client
            .request(&host, "/api/v1/desktop/sessions", Some(json!({})))
            .await;
        assert_eq!(response.status(), 200);
        let opened: Value = response.json().await.unwrap();
        paths.push(opened["path"].as_str().unwrap().to_owned());
    }
    assert_eq!(
        b.request(&host, &format!("{}api/status", paths[0]), None)
            .await
            .status(),
        403
    );
    assert_eq!(
        b.request(
            &host,
            &format!("{}api/control", paths[0]),
            Some(json!({"action":"take"}))
        )
        .await
        .status(),
        403
    );
    assert_eq!(a.request(&host, &paths[0], None).await.status(), 200);
    let mut cross_site = format!("{}{}rfb", host.public.replace("http:", "ws:"), paths[0])
        .into_client_request()
        .unwrap();
    cross_site
        .headers_mut()
        .insert("Cookie", a.session.parse().unwrap());
    cross_site
        .headers_mut()
        .insert("Origin", "https://attacker.invalid".parse().unwrap());
    assert!(tokio_tungstenite::connect_async(cross_site).await.is_err());
    for asset in ["i18n.js", "locales.json"] {
        assert_eq!(
            b.request(&host, &format!("{}{asset}", paths[0]), None)
                .await
                .status(),
            403
        );
        let response = a
            .request(&host, &format!("{}{asset}", paths[0]), None)
            .await;
        assert_eq!(response.status(), 200);
        assert_eq!(response.headers()["cache-control"], "no-store");
        let body = response.text().await.unwrap();
        if asset == "locales.json" {
            let catalogs: Value = serde_json::from_str(&body).unwrap();
            assert_eq!(catalogs["en"]["web.take-over"], "Take over");
            assert!(catalogs["en"].get("web.configure-token").is_none());
        } else {
            assert!(body.contains("potLocalizationReady"));
        }
    }
    let page = a
        .request(&host, &paths[0], None)
        .await
        .text()
        .await
        .unwrap();
    assert!(page.contains("./i18n.js"));
    assert!(!page.contains("potTranslations"));
    let mut sockets = vec![];
    for (client, path) in [(&a, &paths[0]), (&b, &paths[1])] {
        let mut request = format!("{}{}rfb", host.public.replace("http:", "ws:"), path)
            .into_client_request()
            .unwrap();
        request
            .headers_mut()
            .insert("Cookie", client.session.parse().unwrap());
        request
            .headers_mut()
            .insert("Origin", "https://pot.test".parse().unwrap());
        let (mut socket, _) = tokio_tungstenite::connect_async(request).await.unwrap();
        assert_eq!(
            socket.next().await.unwrap().unwrap().into_data(),
            b"RFB 003.008\n".as_slice()
        );
        sockets.push(socket);
    }
    assert_eq!(
        a.request(
            &host,
            &format!("{}api/control", paths[0]),
            Some(json!({"action":"take"}))
        )
        .await
        .status(),
        200
    );
    assert_eq!(
        b.request(
            &host,
            &format!("{}api/control", paths[1]),
            Some(json!({"action":"take"}))
        )
        .await
        .status(),
        409
    );
    host.command(json!({"action":"regenerate_token"})).await;
    for socket in &mut sockets {
        let result = tokio::time::timeout(Duration::from_secs(3), socket.next())
            .await
            .unwrap();
        assert!(result.is_none() || result.is_some_and(|r| r.is_err() || r.unwrap().is_close()));
    }
    for (browser, path) in [(&a, &paths[0]), (&b, &paths[1])] {
        assert_eq!(
            browser
                .request(&host, &format!("{path}api/status"), None)
                .await
                .status(),
            401
        );
    }
    for asset in ["i18n.js", "locales.json"] {
        assert_eq!(
            a.request(&host, &format!("{}{asset}", paths[0]), None)
                .await
                .status(),
            401
        );
    }
    b.configure(&host).await;
    assert_eq!(
        b.request(&host, &format!("{}api/status", paths[1]), None)
            .await
            .status(),
        403
    );
    host.stop().await;
}
