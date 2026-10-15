//! Token boundary, persistence, rotation and streaming authority.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
    routing::get,
};
use pot_server::access::{Access, AccessConfig, admin_router, public_router};
use serde_json::{Value, json};
use tower::ServiceExt;
use url::Url;
struct Fixture {
    a: Access,
    path: std::path::PathBuf,
}
impl Fixture {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!("pot-token-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&path).unwrap();
        let cfg = AccessConfig {
            origin: Url::parse("https://pot.test/").unwrap(),
            bind: "127.0.0.1:0".parse().unwrap(),
            certificate: Default::default(),
            private_key: Default::default(),
            assets: path.clone(),
        };
        let runtime = Router::new().route(
            "/api/v1/snapshot",
            get(|| async { axum::Json(json!({"ok":true})) }),
        );
        let a = Access::new(&cfg, path.clone(), "root-only".into(), runtime).unwrap();
        Self { a, path }
    }
    async fn request(
        &self,
        admin: bool,
        path: &str,
        body: Option<Value>,
        auth: Option<&str>,
        cookie: Option<&str>,
    ) -> (StatusCode, Value, String) {
        let mut req = Request::builder().uri(path);
        if let Some(auth) = auth {
            req = req.header("authorization", format!("Bearer {auth}"));
        }
        if let Some(cookie) = cookie {
            req = req.header("cookie", cookie);
        }
        if body.is_some() {
            req = req
                .method("POST")
                .header("content-type", "application/json");
            if !admin {
                req = req.header("origin", "https://pot.test");
            }
        }
        let router = if admin {
            admin_router(self.a.clone())
        } else {
            public_router(self.a.clone())
        };
        let response = router
            .oneshot(
                req.body(Body::from(body.map(|v| v.to_string()).unwrap_or_default()))
                    .unwrap(),
            )
            .await
            .unwrap();
        let cookie = response
            .headers()
            .get("set-cookie")
            .map(|v| v.to_str().unwrap().split(';').next().unwrap().to_owned())
            .unwrap_or_default();
        let status = response.status();
        let bytes = to_bytes(response.into_body(), 1024 * 1024).await.unwrap();
        (
            status,
            serde_json::from_slice(&bytes).unwrap_or(Value::Null),
            cookie,
        )
    }
    async fn command(&self, action: &str) -> Value {
        let (status, v, _) = self
            .request(
                true,
                "/api/v1/access",
                Some(json!({"action":action})),
                Some("root-only"),
                None,
            )
            .await;
        assert_eq!(status, 200);
        v
    }
    async fn token(&self) -> String {
        self.command("token").await["token"]
            .as_str()
            .unwrap()
            .into()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.path);
    }
}
#[tokio::test]
async fn token_headers_cookies_rotation_and_private_routes() {
    let f = Fixture::new();
    let token = f.token().await;
    assert_eq!(token.len(), 43);
    assert_eq!(
        f.request(false, "/api/v1/snapshot", None, None, None)
            .await
            .0,
        401
    );
    assert_eq!(
        f.request(false, "/api/v1/snapshot", None, Some("root-only"), None)
            .await
            .0,
        401
    );
    assert_eq!(
        f.request(false, "/api/v1/snapshot", None, Some(&token), None)
            .await
            .0,
        200
    );
    let (status, _, cookie) = f
        .request(false, "/access/token", Some(json!({})), Some(&token), None)
        .await;
    assert_eq!(status, 200);
    assert_eq!(
        f.request(false, "/api/v1/snapshot", None, None, Some(&cookie))
            .await
            .0,
        200
    );
    for path in ["/api/v1/access", "/api/v1/terminals"] {
        assert_eq!(
            f.request(false, path, None, Some(&token), None).await.0,
            403
        );
    }
    // Shared settings is authenticated and reaches the backend. This fixture
    // has no provider route, so a forwarded request returns 404 rather than 403.
    assert_eq!(
        f.request(false, "/api/v1/providers", None, None, None)
            .await
            .0,
        401
    );
    assert_eq!(
        f.request(false, "/api/v1/providers", None, Some(&token), None)
            .await
            .0,
        404
    );
    assert_eq!(
        f.request(
            false,
            "/api/v1/assistant-mail",
            Some(json!({"operation":"pending","target_control_id":"other"})),
            Some(&token),
            None
        )
        .await
        .0,
        403
    );
    let fresh = f.command("regenerate_token").await;
    assert_ne!(fresh["token"], token);
    assert_eq!(
        f.request(false, "/api/v1/snapshot", None, Some(&token), None)
            .await
            .0,
        401
    );
    assert_eq!(
        f.request(false, "/api/v1/snapshot", None, None, Some(&cookie))
            .await
            .0,
        401
    );
}
#[tokio::test]
async fn private_token_persists_and_old_client_files_grant_no_authority() {
    use std::os::unix::fs::PermissionsExt;
    let f = Fixture::new();
    let token = f.token().await;
    assert_eq!(
        std::fs::metadata(f.path.join("access-token.json"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    let cfg = AccessConfig {
        origin: Url::parse("https://pot.test").unwrap(),
        bind: "127.0.0.1:0".parse().unwrap(),
        certificate: Default::default(),
        private_key: Default::default(),
        assets: f.path.clone(),
    };
    let a = Access::new(&cfg, f.path.clone(), "other-root".into(), Router::new()).unwrap();
    assert!(a.valid_token(&token));
    f.command("disable").await;
    let a = Access::new(&cfg, f.path.clone(), "other-root".into(), Router::new()).unwrap();
    assert!(!a.valid_token(&token));
    f.command("enable").await;
    assert!(f.a.valid_token(&token));
    let signed = f.a.browser_cookie();
    let restarted = Access::new(&cfg, f.path.clone(), "other-root".into(), Router::new()).unwrap();
    assert!(restarted.valid_session(&signed, false));
    std::fs::remove_file(f.path.join("access-token.json")).unwrap();
    std::fs::write(
        f.path.join("access-clients.json"),
        r#"{"keys":{"old":{"name":"Old browser","public_key":"obsolete"}}}"#,
    )
    .unwrap();
    let fresh = Access::new(&cfg, f.path.clone(), "other-root".into(), Router::new()).unwrap();
    assert!(!fresh.valid_token(&token));
    assert!(!fresh.valid_session(&signed, false));
}
#[tokio::test]
async fn token_rotation_closes_streams() {
    use futures_util::StreamExt;
    let mut f = Fixture::new();
    let token = f.token().await;
    f.a.runtime=Router::new().route("/api/v1/events",get(||async{let stream=async_stream::stream!{loop{tokio::time::sleep(std::time::Duration::from_millis(20)).await;yield Ok::<_,std::io::Error>("data: event\n\n");}};axum::response::Response::new(Body::from_stream(stream))}));
    let response = public_router(f.a.clone())
        .oneshot(
            Request::builder()
                .uri("/api/v1/events")
                .header("authorization", format!("Bearer {token}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), 200);
    let mut stream = response.into_body().into_data_stream();
    assert!(stream.next().await.is_some());
    f.command("regenerate_token").await;
    assert!(
        tokio::time::timeout(std::time::Duration::from_secs(2), stream.next())
            .await
            .unwrap()
            .is_none()
    );
}
#[tokio::test]
async fn origin_boundary_and_public_shell() {
    let f = Fixture::new();
    let token = f.token().await;
    let req = Request::builder()
        .uri("/access/token")
        .method("POST")
        .header("origin", "https://evil.test")
        .header("authorization", format!("Bearer {token}"))
        .body(Body::empty())
        .unwrap();
    assert_eq!(
        public_router(f.a.clone())
            .oneshot(req)
            .await
            .unwrap()
            .status(),
        403
    );
    assert_eq!(
        f.request(false, "/access/status", None, None, None).await.1["authenticated"],
        false
    );
    for origin in [
        "http://remote.test",
        "http://100.64.0.20:17701",
        "https://100.64.0.20:17701",
        "http://[::1]:17701",
    ] {
        assert!(AccessConfig::validate_origin(&Url::parse(origin).unwrap()).is_ok());
    }
    for origin in [
        "ftp://remote.test",
        "https://user@pot.test",
        "https://pot.test/?token=x",
    ] {
        assert!(AccessConfig::validate_origin(&Url::parse(origin).unwrap()).is_err());
    }
}

#[tokio::test]
async fn public_inventory_keeps_broker_secrets_private() {
    let mut f = Fixture::new();
    let token = f.token().await;
    f.a.runtime=Router::new().route("/api/v1/sandboxes",get(||async{axum::Json(json!({"selected":"default","future_secret":"root-secret","items":[{"id":"default","name":"Sandbox","config":{"cpus":4,"private_key":"config-secret"},"state":{"phase":"running","generation":9,"ip":"192.168.64.2","viewer":"http://192.168.64.2/#broker-secret"}}]}))}));
    let (status, data, _) = f
        .request(false, "/api/v1/sandboxes", None, Some(&token), None)
        .await;
    assert_eq!(status, 200);
    assert_eq!(data["items"][0]["state"]["phase"], "running");
    for secret in [
        "root-secret",
        "config-secret",
        "broker-secret",
        "192.168.64.2",
    ] {
        assert!(!data.to_string().contains(secret));
    }
}

#[cfg(feature = "web-ui")]
#[tokio::test]
async fn embedded_web_works_without_an_asset_directory() {
    let f = Fixture::new();
    // The fixture has no frontend files: every response must come from the binary.
    for (path, mime) in [
        ("/", "text/html; charset=utf-8"),
        ("/pkg/pot_web_ui.js", "text/javascript"),
        ("/pkg/pot_web_ui_bg.wasm", "application/wasm"),
        ("/font.ttf", "font/ttf"),
        ("/locales.json", "application/json"),
        ("/icon.png", "image/png"),
    ] {
        let encoded = public_router(f.a.clone())
            .oneshot(
                Request::builder()
                    .uri(path)
                    .header("accept-encoding", "br")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(encoded.status(), StatusCode::OK, "{path}");
        assert_eq!(encoded.headers()["content-type"], mime);
        assert_eq!(encoded.headers()["content-encoding"], "br");
        let bytes = to_bytes(encoded.into_body(), 32 * 1024 * 1024)
            .await
            .unwrap();
        let mut decoded = Vec::new();
        std::io::Read::read_to_end(
            &mut brotli_decompressor::Decompressor::new(&bytes[..], 4096),
            &mut decoded,
        )
        .unwrap();
        let identity = public_router(f.a.clone())
            .oneshot(
                Request::builder()
                    .uri(path)
                    .header("accept-encoding", "br;q=0, identity")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(identity.status(), StatusCode::OK);
        assert!(identity.headers().get("content-encoding").is_none());
        assert_eq!(
            to_bytes(identity.into_body(), 32 * 1024 * 1024)
                .await
                .unwrap()
                .as_ref(),
            decoded
        );
    }
}
