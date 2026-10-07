use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Method, Request, StatusCode},
    routing::get,
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use pot_server::access::{Access, AccessConfig, admin_router, public_router};
use ring::{
    rand::SystemRandom,
    signature::{self, KeyPair},
};
use serde_json::{Value, json};
use std::os::unix::fs::PermissionsExt;
use tower::ServiceExt;
use url::Url;
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
    async fn enroll(&self, auth: &mut ClientKey) -> String {
        let (_, pair, _) = self.command(json!({"action":"pair"})).await;
        let (status, challenge, cookie) = self
            .post(
                "/access/pair/start",
                None,
                json!({"name":"Phone","public_key":auth.public()}),
            )
            .await;
        assert_eq!(status, StatusCode::OK);
        auth.id = challenge["client_id"].as_str().unwrap().into();
        let proof = json!({"code":pair["code"],"signature":auth.sign(&challenge)});
        let (status, _, session) = self
            .post("/access/pair/finish", cookie.as_deref(), proof.clone())
            .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(
            self.post("/access/pair/finish", cookie.as_deref(), proof)
                .await
                .0,
            StatusCode::BAD_REQUEST
        );
        session.unwrap()
    }
    async fn login(&self, auth: &ClientKey) -> String {
        let (status, challenge, cookie) = self
            .post("/access/login/start", None, json!({"client_id":auth.id}))
            .await;
        assert_eq!(status, StatusCode::OK);
        let proof = json!({"signature":auth.sign(&challenge)});
        let (status, _, session) = self
            .post("/access/login/finish", cookie.as_deref(), proof.clone())
            .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(
            self.post("/access/login/finish", cookie.as_deref(), proof)
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
struct ClientKey {
    key: signature::EcdsaKeyPair,
    id: String,
}
impl ClientKey {
    fn new() -> Self {
        let rng = SystemRandom::new();
        let pkcs8 = signature::EcdsaKeyPair::generate_pkcs8(
            &signature::ECDSA_P256_SHA256_FIXED_SIGNING,
            &rng,
        )
        .unwrap();
        Self {
            key: signature::EcdsaKeyPair::from_pkcs8(
                &signature::ECDSA_P256_SHA256_FIXED_SIGNING,
                pkcs8.as_ref(),
                &rng,
            )
            .unwrap(),
            id: String::new(),
        }
    }
    fn public(&self) -> String {
        URL_SAFE_NO_PAD.encode(self.key.public_key().as_ref())
    }
    fn sign(&self, v: &Value) -> String {
        URL_SAFE_NO_PAD.encode(
            self.key
                .sign(
                    &SystemRandom::new(),
                    v["challenge"].as_str().unwrap().as_bytes(),
                )
                .unwrap()
                .as_ref(),
        )
    }
}
#[tokio::test]
async fn pairing_key_login_restart_and_revocation() {
    let mut f = Fixture::new();
    let mut key = ClientKey::new();
    let session = f.enroll(&mut key).await;
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
    let bytes = std::fs::read(f.path.join("access-clients.json")).unwrap();
    let saved = String::from_utf8(bytes).unwrap();
    assert!(!saved.contains("private-root-bearer"));
    assert!(!saved.contains("__Host-pot-session"));
    assert_eq!(
        std::fs::metadata(f.path.join("access-clients.json"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    let config = AccessConfig {
        origin: Url::parse("https://pot.example/").unwrap(),
        bind: "127.0.0.1:0".parse().unwrap(),
        certificate: f.path.join("cert"),
        private_key: f.path.join("key"),
        assets: f.path.clone(),
    };
    f.access = Access::new(
        &config,
        f.path.clone(),
        "private-root-bearer".into(),
        f.access.runtime.clone(),
    )
    .unwrap();
    assert!(
        !f.access
            .valid_session(session.split('=').nth(1).unwrap(), false)
    );
    let session = f.login(&key).await;
    f.command(json!({"action":"revoke_credential","id":key.id}))
        .await;
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
    assert_eq!(
        f.post("/access/login/start", None, json!({"client_id":key.id}))
            .await
            .0,
        StatusCode::UNAUTHORIZED
    );
}
#[tokio::test]
async fn guessing_exhausts_pairing_and_expiration_rejects_proofs() {
    let f = Fixture::new();
    let key = ClientKey::new();
    f.command(json!({"action":"pair"})).await;
    for _ in 0..5 {
        let (_, challenge, cookie) = f
            .post(
                "/access/pair/start",
                None,
                json!({"public_key":key.public()}),
            )
            .await;
        assert_eq!(
            f.post(
                "/access/pair/finish",
                cookie.as_deref(),
                json!({"code":"wrong","signature":key.sign(&challenge)})
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
    assert!(f.access.inner.lock().unwrap().pairing.is_none());
    let mut key = ClientKey::new();
    f.enroll(&mut key).await;
    let (_, challenge, cookie) = f
        .post("/access/login/start", None, json!({"client_id":key.id}))
        .await;
    for c in f.access.inner.lock().unwrap().challenges.values_mut() {
        c.expires = std::time::Instant::now() - std::time::Duration::from_secs(1);
    }
    assert_eq!(
        f.post(
            "/access/login/finish",
            cookie.as_deref(),
            json!({"signature":key.sign(&challenge)})
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    f.command(json!({"action":"disable"})).await;
    assert!(!*f.access.policy.borrow());
    assert!(f.access.inner.lock().unwrap().sessions.is_empty());
}
#[tokio::test]
async fn stolen_client_id_and_cross_browser_challenge_cannot_authorize() {
    let f = Fixture::new();
    let mut owner = ClientKey::new();
    f.enroll(&mut owner).await;
    let attacker = ClientKey::new();
    let (_, challenge, cookie) = f
        .post("/access/login/start", None, json!({"client_id":owner.id}))
        .await;
    assert_eq!(
        f.post(
            "/access/login/finish",
            cookie.as_deref(),
            json!({"signature":attacker.sign(&challenge)})
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    let (_, a, ca) = f
        .post("/access/login/start", None, json!({"client_id":owner.id}))
        .await;
    let (_, _, cb) = f
        .post("/access/login/start", None, json!({"client_id":owner.id}))
        .await;
    assert_eq!(
        f.post(
            "/access/login/finish",
            cb.as_deref(),
            json!({"signature":owner.sign(&a)})
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(
        f.post(
            "/access/login/finish",
            ca.as_deref(),
            json!({"signature":owner.sign(&a)})
        )
        .await
        .0,
        StatusCode::OK
    );
}
#[tokio::test]
async fn pairing_is_one_use_and_new_code_cancels_previous() {
    let f = Fixture::new();
    let a = ClientKey::new();
    let (_, first, _) = f.command(json!({"action":"pair"})).await;
    let (_, second, _) = f.command(json!({"action":"pair"})).await;
    let (_, challenge, cookie) = f
        .post("/access/pair/start", None, json!({"public_key":a.public()}))
        .await;
    assert_eq!(
        f.post(
            "/access/pair/finish",
            cookie.as_deref(),
            json!({"code":first["code"],"signature":a.sign(&challenge)})
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let (_, challenge, cookie) = f
        .post("/access/pair/start", None, json!({"public_key":a.public()}))
        .await;
    assert_eq!(
        f.post(
            "/access/pair/finish",
            cookie.as_deref(),
            json!({"code":second["code"],"signature":a.sign(&challenge)})
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        f.post("/access/pair/start", None, json!({"public_key":a.public()}))
            .await
            .0,
        StatusCode::FORBIDDEN
    );
}

#[tokio::test]
async fn browser_boundary_rejects_csrf_root_bearer_and_private_routes() {
    let f = Fixture::new();
    assert_eq!(
        f.post("/access/pair/start", None, json!({})).await.0,
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
    let mut auth = ClientKey::new();
    let session = f.enroll(&mut auth).await;
    for path in [
        "/api/v1/providers",
        "/api/v1/authorization/status",
        "/api/v1/terminals",
        "/api/v1/access",
        "/api/v1/execution",
        "/api/v1/sandbox-resources",
        "/api/v1/desktop/sessions",
        "/api/v1/desktop/sessions/close",
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
async fn public_stream_stops_on_session_revocation() {
    use futures_util::StreamExt;
    let mut f = Fixture::new();
    let mut auth = ClientKey::new();
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
async fn push_preferences_are_installation_scoped_and_logout_removes_consent() {
    let f = Fixture::new();
    let mut auth = ClientKey::new();
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
        serde_json::from_slice(&std::fs::read(f.path.join("access-clients.json")).unwrap())
            .unwrap();
    assert!(data["subscriptions"].as_object().unwrap().is_empty());
}

#[tokio::test]
async fn sandbox_inventory_does_not_expose_private_viewer_credentials() {
    let mut f = Fixture::new();
    let mut auth = ClientKey::new();
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

#[test]
fn legacy_clients_do_not_migrate_authority_and_disabled_policy_survives() {
    let f = Fixture::new();
    std::fs::write(
        f.path.join("access-credentials.json"),
        r#"{"disabled":true,"keys":{"old":{"name":"Old phone","passkey":{}}},"subscriptions":{}}"#,
    )
    .unwrap();
    let config = AccessConfig {
        origin: Url::parse("https://pot.example/").unwrap(),
        bind: "127.0.0.1:0".parse().unwrap(),
        certificate: f.path.join("cert"),
        private_key: f.path.join("key"),
        assets: f.path.clone(),
    };
    let access = Access::new(&config, f.path.clone(), "new-root".into(), Router::new()).unwrap();
    let inner = access.inner.lock().unwrap();
    assert!(!inner.enabled);
    assert!(inner.credentials.keys.is_empty());
    assert!(inner.credentials.subscriptions.is_empty());
}
