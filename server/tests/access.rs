use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Method, Request, StatusCode},
    routing::get,
};
use pot_server::access::{Access, AccessConfig, admin_router, public_router};
use serde_json::{Value, json};
use std::os::unix::fs::PermissionsExt;
use tower::ServiceExt;
use webauthn_authenticator_rs::{WebauthnAuthenticator, softpasskey::SoftPasskey};
use webauthn_rs::prelude::*;
struct Fixture {
    access: Access,
    path: std::path::PathBuf,
}
impl Fixture {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!("pot-access-test-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&path).unwrap();
        let config = AccessConfig {
            origin: Url::parse("https://pot.example/").unwrap(),
            bind: "127.0.0.1:0".parse().unwrap(),
            certificate: path.join("cert"),
            private_key: path.join("key"),
            assets: path.clone(),
        };
        let runtime = Router::new().route(
            "/api/v1/snapshot",
            get(|| async { axum::Json(json!({"conversation_id":"main","messages":[]})) }),
        );
        let access =
            Access::new(&config, path.clone(), "private-root-bearer".into(), runtime).unwrap();
        Self { access, path }
    }
    async fn request(
        &self,
        admin: bool,
        method: Method,
        path: &str,
        cookie: Option<&str>,
        body: Value,
        origin: Option<&str>,
    ) -> (StatusCode, Value, Option<String>) {
        let router = if admin {
            admin_router(self.access.clone())
        } else {
            public_router(self.access.clone())
        };
        let mut req = Request::builder()
            .method(method)
            .uri(path)
            .header("content-type", "application/json");
        if admin {
            req = req.header("authorization", "Bearer private-root-bearer");
        }
        if let Some(cookie) = cookie {
            req = req.header("cookie", cookie);
        }
        if let Some(origin) = origin {
            req = req.header("origin", origin);
        }
        let response = router
            .oneshot(req.body(Body::from(body.to_string())).unwrap())
            .await
            .unwrap();
        let status = response.status();
        let cookie = response
            .headers()
            .get("set-cookie")
            .map(|h| h.to_str().unwrap().split(';').next().unwrap().to_string());
        let bytes = to_bytes(response.into_body(), 1024 * 1024).await.unwrap();
        let body = serde_json::from_slice(&bytes).unwrap_or(Value::Null);
        (status, body, cookie)
    }
    async fn post(
        &self,
        path: &str,
        cookie: Option<&str>,
        body: Value,
    ) -> (StatusCode, Value, Option<String>) {
        self.request(
            false,
            Method::POST,
            path,
            cookie,
            body,
            Some("https://pot.example"),
        )
        .await
    }
    async fn command(&self, v: Value) -> (StatusCode, Value, Option<String>) {
        self.request(true, Method::POST, "/api/v1/access", None, v, None)
            .await
    }
    async fn enroll(&self, auth: &mut WebauthnAuthenticator<SoftPasskey>) -> String {
        assert_eq!(
            self.command(json!({"action":"pair"})).await.0,
            StatusCode::OK
        );
        let (_, _, cookie) = self
            .post("/access/enroll", None, json!({"name":"Phone"}))
            .await;
        let cookie = cookie.unwrap();
        assert_eq!(
            self.post("/access/register/start", Some(&cookie), json!({}))
                .await
                .0,
            StatusCode::FORBIDDEN
        );
        let (_, status, _) = self
            .request(true, Method::GET, "/api/v1/access", None, Value::Null, None)
            .await;
        let id = status["pending"][0]["id"].as_str().unwrap();
        self.command(json!({"action":"approve","id":id})).await;
        let (status, options, _) = self
            .post("/access/register/start", Some(&cookie), json!({}))
            .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(
            options["publicKey"]["authenticatorSelection"]["userVerification"],
            "required"
        );
        let response = auth
            .do_registration(
                Url::parse("https://pot.example").unwrap(),
                serde_json::from_value(options).unwrap(),
            )
            .unwrap();
        let (status, _, session) = self
            .post("/access/register/finish", Some(&cookie), json!(response))
            .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(
            self.post("/access/register/finish", Some(&cookie), json!(response))
                .await
                .0,
            StatusCode::BAD_REQUEST
        );
        session.unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.path);
    }
}
#[tokio::test]
async fn real_passkey_enrollment_login_revocation_and_persistence() {
    let f = Fixture::new();
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    let session = f.enroll(&mut auth).await;
    assert_eq!(
        f.request(
            false,
            Method::GET,
            "/api/v1/snapshot",
            Some(&session),
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        std::fs::metadata(f.path.join("access-credentials.json"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    let saved = std::fs::read_to_string(f.path.join("access-credentials.json")).unwrap();
    assert!(!saved.contains("private-root-bearer"));
    assert!(!saved.contains("__Host-pot-session"));
    assert_eq!(
        f.post("/access/logout", Some(&session), json!({})).await.0,
        StatusCode::OK
    );
    assert_eq!(
        f.request(
            false,
            Method::GET,
            "/api/v1/snapshot",
            Some(&session),
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    let (status, options, cookie) = f.post("/access/login/start", None, json!({})).await;
    assert_eq!(status, StatusCode::OK);
    let assertion = auth
        .do_authentication(
            Url::parse("https://pot.example").unwrap(),
            serde_json::from_value(options).unwrap(),
        )
        .unwrap();
    let (status, _, session) = f
        .post("/access/login/finish", cookie.as_deref(), json!(assertion))
        .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(
        f.post("/access/login/finish", cookie.as_deref(), json!(assertion))
            .await
            .0,
        StatusCode::BAD_REQUEST
    );
    let (_, admin, _) = f
        .request(true, Method::GET, "/api/v1/access", None, Value::Null, None)
        .await;
    f.command(json!({"action":"revoke_credential","id":admin["credentials"][0]["id"]}))
        .await;
    assert_eq!(
        f.request(
            false,
            Method::GET,
            "/api/v1/snapshot",
            session.as_deref(),
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        f.post("/access/login/start", None, json!({})).await.0,
        StatusCode::UNAUTHORIZED
    );
    assert!(f.access.inner.lock().unwrap().sessions.is_empty());
}
#[tokio::test]
async fn browser_boundary_rejects_csrf_root_bearer_and_private_routes() {
    let f = Fixture::new();
    assert_eq!(
        f.post("/access/enroll", None, json!({})).await.0,
        StatusCode::FORBIDDEN
    );
    for origin in [
        None,
        Some("https://attacker.example"),
        Some("https://pot.example.evil"),
    ] {
        assert_eq!(
            f.request(
                false,
                Method::POST,
                "/access/login/start",
                None,
                json!({}),
                origin
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
    assert_eq!(
        f.request(
            true,
            Method::GET,
            "/api/v1/access",
            None,
            Value::Null,
            Some("https://pot.example")
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    let session = f.enroll(&mut auth).await;
    for path in [
        "/api/v1/providers",
        "/api/v1/authorization/status",
        "/api/v1/terminals",
        "/api/v1/access",
        "/api/v1/execution",
    ] {
        assert_eq!(
            f.request(false, Method::GET, path, Some(&session), Value::Null, None)
                .await
                .0,
            StatusCode::FORBIDDEN,
            "{path}"
        );
    }
    assert_eq!(
        f.post(
            "/api/v1/sandboxes",
            Some(&session),
            json!({"action":"stop"})
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    f.command(json!({"action":"disable"})).await;
    assert_eq!(
        f.request(
            false,
            Method::GET,
            "/access/status",
            Some(&session),
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    f.command(json!({"action":"enable"})).await;
    assert_eq!(
        f.request(
            false,
            Method::GET,
            "/api/v1/snapshot",
            Some(&session),
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
}
#[tokio::test]
async fn enrollment_cannot_be_claimed_by_another_browser_or_replayed() {
    let f = Fixture::new();
    f.command(json!({"action":"pair"})).await;
    let (_, _, first) = f
        .post("/access/enroll", None, json!({"name":"Owner"}))
        .await;
    let (_, _, other) = f
        .post("/access/enroll", None, json!({"name":"Other"}))
        .await;
    let (_, status, _) = f
        .request(true, Method::GET, "/api/v1/access", None, Value::Null, None)
        .await;
    let id = status["pending"]
        .as_array()
        .unwrap()
        .iter()
        .find(|v| v["name"] == "Owner")
        .unwrap()["id"]
        .clone();
    f.command(json!({"action":"approve","id":id})).await;
    assert_eq!(
        f.post("/access/register/start", other.as_deref(), json!({}))
            .await
            .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        f.post("/access/register/start", first.as_deref(), json!({}))
            .await
            .0,
        StatusCode::OK
    );
    assert_eq!(
        f.post("/access/register/start", first.as_deref(), json!({}))
            .await
            .0,
        StatusCode::BAD_REQUEST
    );
    f.command(json!({"action":"close_enrollment"})).await;
    assert_eq!(
        f.post("/access/enroll", None, json!({})).await.0,
        StatusCode::FORBIDDEN
    );
}
#[test]
fn origin_configuration_fails_closed() {
    for value in [
        "http://pot.example/",
        "https://192.168.0.1/",
        "https://user@pot.example/",
        "https://pot.example/path",
        "https://pot.example/?token=x",
    ] {
        assert!(
            AccessConfig::validate_origin(&Url::parse(value).unwrap()).is_err(),
            "{value}"
        );
    }
    assert!(
        AccessConfig::validate_origin(&Url::parse("https://pot.example:17701/").unwrap()).is_ok()
    );
}

#[tokio::test]
async fn expires_challenges_and_sessions_and_persists_disabled_policy() {
    use std::time::{Duration, Instant};
    let f = Fixture::new();
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    let session = f.enroll(&mut auth).await;
    let (_, options, ceremony) = f.post("/access/login/start", None, json!({})).await;
    let assertion = auth
        .do_authentication(
            Url::parse("https://pot.example").unwrap(),
            serde_json::from_value(options).unwrap(),
        )
        .unwrap();
    for c in f.access.inner.lock().unwrap().challenges.values_mut() {
        c.expires = Instant::now() - Duration::from_secs(1);
    }
    assert_eq!(
        f.post(
            "/access/login/finish",
            ceremony.as_deref(),
            json!(assertion)
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    for s in f.access.inner.lock().unwrap().sessions.values_mut() {
        s.created = Instant::now() - Duration::from_secs(43201);
    }
    assert_eq!(
        f.request(
            false,
            Method::GET,
            "/api/v1/snapshot",
            Some(&session),
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    f.command(json!({"action":"disable"})).await;
    let cfg = AccessConfig {
        origin: Url::parse("https://pot.example/").unwrap(),
        bind: "127.0.0.1:0".parse().unwrap(),
        certificate: f.path.join("cert"),
        private_key: f.path.join("key"),
        assets: f.path.clone(),
    };
    let restarted = Access::new(&cfg, f.path.clone(), "new-root".into(), Router::new()).unwrap();
    assert!(!restarted.inner.lock().unwrap().enabled);
    assert_eq!(restarted.inner.lock().unwrap().credentials.keys.len(), 1);
    assert!(restarted.inner.lock().unwrap().sessions.is_empty());
    assert!(!*restarted.policy.borrow());
}
#[tokio::test]
async fn invalid_signature_and_client_data_never_issue_session() {
    let f = Fixture::new();
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    f.enroll(&mut auth).await;
    f.access.inner.lock().unwrap().sessions.clear();
    let (_, options, cookie) = f.post("/access/login/start", None, json!({})).await;
    let assertion = auth
        .do_authentication(
            Url::parse("https://pot.example").unwrap(),
            serde_json::from_value(options).unwrap(),
        )
        .unwrap();
    let mut bad = json!(assertion);
    bad["response"]["signature"] = json!("AAAA");
    assert_eq!(
        f.post("/access/login/finish", cookie.as_deref(), bad)
            .await
            .0,
        StatusCode::BAD_REQUEST
    );
    assert!(f.access.inner.lock().unwrap().sessions.is_empty());
    // A signature over a different WebAuthn origin must fail even with an exact HTTP Origin.
    let (_, options, cookie) = f.post("/access/login/start", None, json!({})).await;
    let assertion = auth
        .do_authentication(
            Url::parse("https://pot.example").unwrap(),
            serde_json::from_value(options).unwrap(),
        )
        .unwrap();
    let mut bad = json!(assertion);
    bad["response"]["clientDataJSON"] = json!("AAAA");
    assert_eq!(
        f.post("/access/login/finish", cookie.as_deref(), bad)
            .await
            .0,
        StatusCode::BAD_REQUEST
    );
    assert!(f.access.inner.lock().unwrap().sessions.is_empty());
}
#[tokio::test]
async fn public_stream_stops_on_session_revocation() {
    use futures_util::StreamExt;
    let mut f = Fixture::new();
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    let session = f.enroll(&mut auth).await;
    f.access.runtime=Router::new().route("/api/v1/events",get(||async{
        let stream=async_stream::stream! {loop {tokio::time::sleep(std::time::Duration::from_millis(20)).await;yield Ok::<_,std::io::Error>("data: event\n\n");}};
        axum::response::Response::new(Body::from_stream(stream))
    }));
    let response = public_router(f.access.clone())
        .oneshot(
            Request::builder()
                .uri("/api/v1/events")
                .header("cookie", &session)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let mut stream = response.into_body().into_data_stream();
    assert!(stream.next().await.is_some());
    f.access.inner.lock().unwrap().sessions.clear();
    assert!(
        tokio::time::timeout(std::time::Duration::from_secs(2), stream.next())
            .await
            .unwrap()
            .is_none()
    );
}

#[tokio::test]
async fn unsigned_client_uv_preference_cannot_weaken_server_policy() {
    let f = Fixture::new();
    f.command(json!({"action":"pair"})).await;
    let (_, _, cookie) = f
        .post("/access/enroll", None, json!({"name":"No UV"}))
        .await;
    let (_, state, _) = f
        .request(true, Method::GET, "/api/v1/access", None, Value::Null, None)
        .await;
    f.command(json!({"action":"approve","id":state["pending"][0]["id"]}))
        .await;
    let (_, mut options, _) = f
        .post("/access/register/start", cookie.as_deref(), json!({}))
        .await;
    options["publicKey"]["authenticatorSelection"]["userVerification"] = json!("discouraged");
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(false));
    let response = auth
        .do_registration(
            Url::parse("https://pot.example").unwrap(),
            serde_json::from_value(options).unwrap(),
        )
        .unwrap();
    assert_eq!(
        f.post(
            "/access/register/finish",
            cookie.as_deref(),
            json!(response)
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert!(f.access.inner.lock().unwrap().credentials.keys.is_empty());
    assert!(f.access.inner.lock().unwrap().sessions.is_empty());
}

#[tokio::test]
async fn push_preferences_are_installation_scoped_and_logout_removes_consent() {
    let f = Fixture::new();
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    let session = f.enroll(&mut auth).await;
    for path in [
        "/access/push",
        "/access/push/preferences",
        "/access/push/remove",
    ] {
        assert_eq!(
            f.post(path, None, json!({})).await.0,
            StatusCode::UNAUTHORIZED
        );
    }
    {
        let mut i = f.access.inner.lock().unwrap();
        let key = i.credentials.keys.keys().next().unwrap().clone();
        i.credentials.subscriptions.insert("installation".into(),serde_json::from_value(json!({"credential":key,"info":{"endpoint":"https://fcm.googleapis.com/test","keys":{"p256dh":"fixture","auth":"fixture"}},"locale":"en","private_preview":false,"sequence":12,"failures":0,"retry_after_ms":0})).unwrap());
        f.access.persist(&i).unwrap();
    }
    let browser = format!("{session}; __Host-pot-installation=installation");
    assert_eq!(
        f.post(
            "/access/push/preferences",
            Some(&session),
            json!({"locale":"zh-CN"})
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(
        f.post(
            "/access/push/preferences",
            Some(&browser),
            json!({"locale":"zh-CN"})
        )
        .await
        .0,
        StatusCode::OK
    );
    {
        let i = f.access.inner.lock().unwrap();
        assert_eq!(i.credentials.subscriptions["installation"].locale, "zh-CN");
        assert_eq!(i.credentials.subscriptions["installation"].sequence, 12);
    }
    assert_eq!(
        f.post("/access/logout", Some(&browser), json!({})).await.0,
        StatusCode::OK
    );
    assert!(
        f.access
            .inner
            .lock()
            .unwrap()
            .credentials
            .subscriptions
            .is_empty()
    );
    let data: Value =
        serde_json::from_slice(&std::fs::read(f.path.join("access-credentials.json")).unwrap())
            .unwrap();
    assert!(data["subscriptions"].as_object().unwrap().is_empty());
}

#[tokio::test]
async fn sandbox_inventory_does_not_expose_private_viewer_credentials() {
    let mut f = Fixture::new();
    let mut auth = WebauthnAuthenticator::new(SoftPasskey::new(true));
    let session = f.enroll(&mut auth).await;
    f.access.runtime=Router::new().route("/api/v1/sandboxes",get(||async{axum::Json(json!({"selected":"default","future_secret":"root-secret","items":[{"id":"default","name":"Sandbox","os":"linux","config":{"cpus":4,"private_key":"config-secret"},"state":{"phase":"running","generation":9,"ip":"192.168.64.2","viewer":"http://192.168.64.2:17681/#broker-secret","future_token":"another-secret"}}]}))}));
    let (code, body, _) = f
        .request(
            false,
            Method::GET,
            "/api/v1/sandboxes",
            Some(&session),
            Value::Null,
            None,
        )
        .await;
    assert_eq!(code, StatusCode::OK);
    assert_eq!(body["items"][0]["state"]["phase"], "running");
    assert_eq!(body["items"][0]["state"]["generation"], 9);
    for secret in [
        "root-secret",
        "config-secret",
        "broker-secret",
        "another-secret",
        "192.168.64.2",
        "viewer",
    ] {
        assert!(!body.to_string().contains(secret));
    }
}
