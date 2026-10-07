//! Saved endpoints contain no private keys. Each immutable transport owns its login session.
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD as B64};
use ring::{
    rand::SystemRandom,
    signature::{self, KeyPair},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex, OnceLock},
    time::Duration,
};

pub const LOCAL: &str = "local";
#[derive(Clone, Serialize, Deserialize)]
pub struct Connection {
    pub id: String,
    pub name: String,
    pub address: String,
    pub server_id: String,
}
#[derive(Default, Serialize, Deserialize)]
struct Saved {
    connections: Vec<Connection>,
}
#[derive(Serialize, Deserialize)]
struct Secret {
    client_id: String,
    pkcs8: String,
}
pub struct Remote {
    pub connection: Connection,
    http: reqwest::blocking::Client,
    secret: Secret,
    session: Mutex<String>,
}
#[derive(Clone)]
pub enum Target {
    Local,
    Remote(Arc<Remote>),
}
impl Target {
    pub fn id(&self) -> &str {
        match self {
            Self::Local => LOCAL,
            Self::Remote(r) => &r.connection.id,
        }
    }
    pub fn request(
        &self,
        path: &str,
        body: Option<Value>,
        timeout: Duration,
    ) -> Result<(u16, Vec<u8>), String> {
        match self {
            Self::Local => crate::backend::local_request(path, body, timeout),
            Self::Remote(r) => r.request(path, body, timeout),
        }
    }
}
fn error(key: &str) -> String {
    crate::i18n::text(key).into()
}
pub fn normalize(address: &str) -> Result<String, String> {
    let url = url::Url::parse(address.trim()).map_err(|_| error("servers.invalid-address"))?;
    pot_server::access::AccessConfig::validate_origin(&url)
        .map_err(|_| error("servers.invalid-address"))?;
    Ok(url.origin().ascii_serialization())
}
fn http() -> Result<reqwest::blocking::Client, String> {
    reqwest::blocking::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(10))
        .build()
        .map_err(|e| e.to_string())
}
fn fetch(
    http: &reqwest::blocking::Client,
    address: &str,
    path: &str,
    body: Option<Value>,
    cookie: &str,
    timeout: Duration,
) -> Result<(u16, Vec<u8>, String), String> {
    let mut request = if body.is_some() {
        http.post(format!("{address}{path}"))
    } else {
        http.get(format!("{address}{path}"))
    };
    request = request.timeout(timeout).header("origin", address);
    if !cookie.is_empty() {
        request = request.header("cookie", cookie);
    }
    if let Some(body) = body {
        request = request.json(&body);
    }
    let response = request.send().map_err(|_| error("servers.unreachable"))?;
    let status = response.status().as_u16();
    let cookie = response
        .headers()
        .get_all("set-cookie")
        .iter()
        .filter_map(|h| h.to_str().ok())
        .filter_map(|h| h.split(';').next())
        .collect::<Vec<_>>()
        .join("; ");
    let bytes = response
        .bytes()
        .map_err(|_| error("servers.unreachable"))?
        .to_vec();
    Ok((status, bytes, cookie))
}
fn json_reply(reply: (u16, Vec<u8>, String)) -> Result<(Value, String), String> {
    let (status, bytes, cookie) = reply;
    let value: Value = serde_json::from_slice(&bytes).map_err(|_| error("servers.incompatible"))?;
    if status >= 400 {
        return Err(match value["code"].as_str().unwrap_or("") {
            "unknown_client" | "client_revoked" | "login_required" | "session_expired" => {
                error("servers.repair-required")
            }
            "enrollment_closed" => error("servers.pairing-closed"),
            "invalid_pairing_code" => error("servers.invalid-code"),
            _ => value["message"]
                .as_str()
                .or(value["code"].as_str())
                .unwrap_or("Server rejected request")
                .into(),
        });
    }
    Ok((value, cookie))
}
fn inspect(
    http: &reqwest::blocking::Client,
    address: &str,
    expected: Option<&str>,
) -> Result<String, String> {
    let (value, _) = json_reply(fetch(
        http,
        address,
        "/access/status",
        None,
        "",
        Duration::from_secs(10),
    )?)?;
    if value["api_version"] != pot_protocol::API_VERSION {
        return Err(error("servers.incompatible"));
    }
    let id = value["server_id"]
        .as_str()
        .filter(|id| !id.is_empty())
        .ok_or_else(|| error("servers.incompatible"))?;
    if expected.is_some_and(|expected| expected != id) {
        return Err(error("servers.identity-changed"));
    }
    Ok(id.into())
}
fn key(secret: &Secret) -> Result<signature::EcdsaKeyPair, String> {
    let bytes = B64
        .decode(&secret.pkcs8)
        .map_err(|_| error("servers.repair-required"))?;
    signature::EcdsaKeyPair::from_pkcs8(
        &signature::ECDSA_P256_SHA256_FIXED_SIGNING,
        &bytes,
        &SystemRandom::new(),
    )
    .map_err(|_| error("servers.repair-required"))
}
fn proof(secret: &Secret, challenge: &Value, server_id: &str) -> Result<String, String> {
    if challenge["server_id"] != server_id {
        return Err(error("servers.identity-changed"));
    }
    let message = challenge["challenge"]
        .as_str()
        .ok_or_else(|| error("servers.incompatible"))?;
    if !message.ends_with(&format!("\n{server_id}")) {
        return Err(error("servers.identity-changed"));
    }
    Ok(B64.encode(
        key(secret)?
            .sign(&SystemRandom::new(), message.as_bytes())
            .map_err(|_| error("servers.repair-required"))?
            .as_ref(),
    ))
}
impl Remote {
    fn login(&self, session: &mut String) -> Result<(), String> {
        let address = &self.connection.address;
        inspect(&self.http, address, Some(&self.connection.server_id))?;
        let (challenge, cookie) = json_reply(fetch(
            &self.http,
            address,
            "/access/login/start",
            Some(json!({"client_id":self.secret.client_id})),
            "",
            Duration::from_secs(10),
        )?)?;
        let signature = proof(&self.secret, &challenge, &self.connection.server_id)?;
        let (_, cookie) = json_reply(fetch(
            &self.http,
            address,
            "/access/login/finish",
            Some(json!({"signature":signature})),
            &cookie,
            Duration::from_secs(10),
        )?)?;
        *session = cookie;
        Ok(())
    }
    fn request(
        &self,
        path: &str,
        body: Option<Value>,
        timeout: Duration,
    ) -> Result<(u16, Vec<u8>), String> {
        let cookie = {
            let mut session = self.session.lock().unwrap();
            if session.is_empty() {
                self.login(&mut session)?;
            }
            session.clone()
        };
        let (status, bytes, _) = fetch(
            &self.http,
            &self.connection.address,
            path,
            body.clone(),
            &cookie,
            timeout,
        )?;
        if status != 401 {
            return Ok((status, bytes));
        }
        let cookie = {
            let mut session = self.session.lock().unwrap();
            if *session == cookie {
                session.clear();
                self.login(&mut session)?;
            }
            session.clone()
        };
        let (status, bytes, _) = fetch(
            &self.http,
            &self.connection.address,
            path,
            body,
            &cookie,
            timeout,
        )?;
        Ok((status, bytes))
    }
    pub fn desktop(&self, path: &str) -> Result<Value, String> {
        let cookie = self.session.lock().unwrap().clone();
        if !path.starts_with("/desktop/") || path.contains("..") {
            return Err(error("servers.incompatible"));
        }
        Ok(
            json!({"url":format!("{}{path}?native=1",self.connection.address),"cookie":cookie,"origin":self.connection.address}),
        )
    }
}
struct Registry {
    saved: Saved,
    targets: BTreeMap<String, Target>,
}
static REGISTRY: OnceLock<Mutex<Registry>> = OnceLock::new();
fn registry() -> &'static Mutex<Registry> {
    REGISTRY.get_or_init(|| {
        let path = storage_root().join("connections.json");
        let saved = match std::fs::read(&path) {
            Ok(bytes) => serde_json::from_slice(&bytes).unwrap_or_default(),
            Err(_) => Saved::default(),
        };
        Mutex::new(Registry {
            saved,
            targets: BTreeMap::new(),
        })
    })
}
pub fn list() -> Vec<Connection> {
    registry().lock().unwrap().saved.connections.clone()
}
pub fn target(id: &str) -> Result<Target, String> {
    if id == LOCAL {
        return Ok(Target::Local);
    }
    let mut registry = registry().lock().unwrap();
    if let Some(target) = registry.targets.get(id) {
        return Ok(target.clone());
    }
    let connection = registry
        .saved
        .connections
        .iter()
        .find(|c| c.id == id)
        .cloned()
        .ok_or_else(|| error("servers.repair-required"))?;
    let secret =
        serde_json::from_str(&keychain(id, None)?).map_err(|_| error("servers.repair-required"))?;
    let target = Target::Remote(Arc::new(Remote {
        connection,
        secret,
        http: http()?,
        session: Mutex::new(String::new()),
    }));
    registry.targets.insert(id.into(), target.clone());
    Ok(target)
}
fn persist(saved: &Saved) -> Result<(), String> {
    std::fs::create_dir_all(storage_root()).map_err(|e| e.to_string())?;
    pot_runtime_private_json(&storage_root().join("connections.json"), saved)
}
fn pot_runtime_private_json(path: &std::path::Path, value: &impl Serialize) -> Result<(), String> {
    // Reuse the Server's durable private-file primitive through its runtime dependency.
    pot_server::save_private_json(
        path,
        &serde_json::to_value(value).map_err(|e| e.to_string())?,
    )
    .map_err(|e| e.to_string())
}
pub fn pair(
    address: &str,
    code: &str,
    device: &str,
    name: &str,
    existing: Option<&str>,
) -> Result<String, String> {
    let address = normalize(address)?;
    let http = http()?;
    let server_id = inspect(&http, &address, None)?;
    if let Some(id) = existing {
        let registry = registry().lock().unwrap();
        let connection = registry
            .saved
            .connections
            .iter()
            .find(|c| c.id == id)
            .ok_or_else(|| error("servers.repair-required"))?;
        if connection.server_id != server_id {
            return Err(error("servers.identity-changed"));
        }
    }
    let pkcs8 = signature::EcdsaKeyPair::generate_pkcs8(
        &signature::ECDSA_P256_SHA256_FIXED_SIGNING,
        &SystemRandom::new(),
    )
    .map_err(|e| e.to_string())?;
    let mut secret = Secret {
        client_id: String::new(),
        pkcs8: B64.encode(pkcs8.as_ref()),
    };
    // Persist the private key before consuming the one-use code, including recovery from a lost finish response.
    let id = existing
        .map(str::to_owned)
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());
    let (challenge, cookie) = json_reply(fetch(
        &http,
        &address,
        "/access/pair/start",
        Some(json!({"public_key":B64.encode(key(&secret)?.public_key().as_ref()),"name":device})),
        "",
        Duration::from_secs(10),
    )?)?;
    secret.client_id = challenge["client_id"]
        .as_str()
        .ok_or_else(|| error("servers.incompatible"))?
        .into();
    let signature = proof(&secret, &challenge, &server_id)?;
    // Keep a new key under a temporary account until pairing succeeds, so failed re-pairing preserves the old key.
    let pending = format!("{id}-pending");
    keychain(&pending, Some(&serde_json::to_string(&secret).unwrap()))?;
    let finish = json_reply(fetch(
        &http,
        &address,
        "/access/pair/finish",
        Some(json!({"signature":signature,"code":code.trim()})),
        &cookie,
        Duration::from_secs(10),
    )?);
    let session = match finish {
        Ok((_, session)) => session,
        Err(e) => {
            let candidate = Remote {
                connection: Connection {
                    id: id.clone(),
                    name: name.into(),
                    address: address.clone(),
                    server_id: server_id.clone(),
                },
                http: http.clone(),
                secret: serde_json::from_value(serde_json::to_value(&secret).unwrap()).unwrap(),
                session: Mutex::new(String::new()),
            };
            let mut session = String::new();
            if candidate.login(&mut session).is_err() {
                let _ = keychain(&pending, Some(""));
                return Err(e);
            }
            session
        }
    };
    keychain(&id, Some(&serde_json::to_string(&secret).unwrap()))?;
    let mut registry = registry().lock().unwrap();
    let connection = Connection {
        id: id.clone(),
        name: if name.trim().is_empty() {
            address.clone()
        } else {
            name.trim().into()
        },
        address,
        server_id,
    };
    let mut saved = Saved {
        connections: registry.saved.connections.clone(),
    };
    saved.connections.retain(|c| c.id != id);
    saved.connections.push(connection.clone());
    persist(&saved)?;
    registry.saved = saved;
    registry.targets.insert(
        id.clone(),
        Target::Remote(Arc::new(Remote {
            connection,
            http,
            secret,
            session: Mutex::new(session),
        })),
    );
    let _ = keychain(&pending, Some(""));
    Ok(id)
}
pub fn edit(id: &str, name: &str, address: &str) -> Result<(), String> {
    let address = normalize(address)?;
    let original = list()
        .into_iter()
        .find(|c| c.id == id)
        .ok_or_else(|| error("servers.repair-required"))?;
    if address != original.address {
        inspect(&http()?, &address, Some(&original.server_id))?;
    }
    let mut registry = registry().lock().unwrap();
    let mut saved = Saved {
        connections: registry.saved.connections.clone(),
    };
    let connection = saved.connections.iter_mut().find(|c| c.id == id).unwrap();
    connection.name = if name.trim().is_empty() {
        address.clone()
    } else {
        name.trim().into()
    };
    connection.address = address;
    persist(&saved)?;
    registry.saved = saved;
    registry.targets.remove(id);
    Ok(())
}
pub fn remove(id: &str) -> Result<(), String> {
    let mut registry = registry().lock().unwrap();
    let saved = Saved {
        connections: registry
            .saved
            .connections
            .iter()
            .filter(|c| c.id != id)
            .cloned()
            .collect(),
    };
    persist(&saved)?;
    registry.saved = saved;
    registry.targets.remove(id);
    keychain(id, Some(""))?;
    Ok(())
}
#[cfg(not(test))]
fn keychain(id: &str, value: Option<&str>) -> Result<String, String> {
    use std::ffi::{CStr, CString, c_char};
    unsafe extern "C" {
        fn pot_keychain(id: *const c_char, value: *const c_char) -> *mut c_char;
        fn pot_backend_free(value: *mut c_char);
    }
    let id = CString::new(id).map_err(|e| e.to_string())?;
    let value = value
        .map(CString::new)
        .transpose()
        .map_err(|e| e.to_string())?;
    let result = unsafe {
        pot_keychain(
            id.as_ptr(),
            value.as_ref().map_or(std::ptr::null(), |v| v.as_ptr()),
        )
    };
    let text = unsafe { CStr::from_ptr(result) }
        .to_string_lossy()
        .into_owned();
    unsafe {
        pot_backend_free(result);
    }
    let value: Value = serde_json::from_str(&text).map_err(|_| error("servers.keychain-error"))?;
    if !value["error"].is_null() {
        return Err(error("servers.keychain-error"));
    }
    Ok(value["value"].as_str().unwrap_or_default().into())
}
#[cfg(test)]
fn keychain(id: &str, value: Option<&str>) -> Result<String, String> {
    static KEYS: OnceLock<Mutex<BTreeMap<String, String>>> = OnceLock::new();
    let mut keys = KEYS.get_or_init(Default::default).lock().unwrap();
    if let Some(value) = value {
        keys.insert(id.into(), value.into());
    }
    Ok(keys.get(id).cloned().unwrap_or_default())
}

#[cfg(not(test))]
fn storage_root() -> std::path::PathBuf {
    crate::client::root()
}
#[cfg(test)]
fn storage_root() -> std::path::PathBuf {
    static ROOT: OnceLock<std::path::PathBuf> = OnceLock::new();
    ROOT.get_or_init(|| {
        std::env::temp_dir().join(format!("pot-client-test-{}", uuid::Uuid::new_v4()))
    })
    .clone()
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        Json, Router,
        extract::State,
        routing::{get, post},
    };
    use pot_server::access::{Access, AccessConfig};
    struct Fixture {
        address: String,
        code: String,
        access: Access,
        stop: Option<tokio::sync::oneshot::Sender<()>>,
        thread: Option<std::thread::JoinHandle<()>>,
        path: std::path::PathBuf,
    }
    impl Fixture {
        fn new(label: &str) -> Self {
            let path =
                std::env::temp_dir().join(format!("pot-multi-server-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&path).unwrap();
            let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            listener.set_nonblocking(true).unwrap();
            let address = format!("http://localhost:{}", listener.local_addr().unwrap().port());
            let runtime = Router::new()
                .route(
                    "/api/v1/snapshot",
                    get(|State(label): State<String>| async move { Json(json!({"server":label})) }),
                )
                .route(
                    "/api/v1/providers",
                    post(
                        |State(label): State<String>, Json(body): Json<Value>| async move {
                            Json(json!({"server":label,"saved":body}))
                        },
                    ),
                )
                .route(
                    "/api/v1/messages",
                    post(
                        |State(label): State<String>, Json(body): Json<Value>| async move {
                            tokio::time::sleep(Duration::from_millis(100)).await;
                            Json(json!({"server":label,"request_id":body["request_id"]}))
                        },
                    ),
                )
                .with_state(label.to_owned());
            let config = AccessConfig {
                origin: url::Url::parse(&address).unwrap(),
                bind: "127.0.0.1:0".parse().unwrap(),
                certificate: path.join("cert"),
                private_key: path.join("key"),
                assets: path.clone(),
            };
            let access =
                Access::new(&config, path.clone(), "fixture-root".into(), runtime).unwrap();
            let service = access.clone();
            let (stop, rx) = tokio::sync::oneshot::channel();
            let (sender, receiver) = std::sync::mpsc::channel();
            let thread = std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async move {
                    use tower::ServiceExt;
                    let req = axum::http::Request::builder()
                        .method("POST")
                        .uri("/api/v1/access")
                        .header("authorization", "Bearer fixture-root")
                        .header("content-type", "application/json")
                        .body(axum::body::Body::from(r#"{"action":"pair"}"#))
                        .unwrap();
                    let response = pot_server::access::admin_router(service.clone())
                        .oneshot(req)
                        .await
                        .unwrap();
                    let bytes = axum::body::to_bytes(response.into_body(), 1024 * 1024)
                        .await
                        .unwrap();
                    let value: Value = serde_json::from_slice(&bytes).unwrap();
                    let listener = tokio::net::TcpListener::from_std(listener).unwrap();
                    sender
                        .send(value["code"].as_str().unwrap().to_owned())
                        .unwrap();
                    axum::serve(listener, pot_server::access::public_router(service))
                        .with_graceful_shutdown(async {
                            let _ = rx.await;
                        })
                        .await
                        .unwrap();
                });
            });
            let code = receiver.recv().unwrap();
            Self {
                address,
                code,
                access,
                stop: Some(stop),
                thread: Some(thread),
                path,
            }
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = self.stop.take().unwrap().send(());
            self.thread.take().unwrap().join().unwrap();
            let _ = std::fs::remove_dir_all(&self.path);
        }
    }
    fn reply(target: &Target, path: &str, body: Option<Value>) -> Value {
        let (status, bytes) = target.request(path, body, Duration::from_secs(3)).unwrap();
        assert_eq!(status, 200);
        serde_json::from_slice(&bytes).unwrap()
    }
    #[test]
    fn two_real_servers_keep_authentication_writes_and_recovery_separate() {
        let a = Fixture::new("A");
        let b = Fixture::new("B");
        let aid = pair(&a.address, &a.code, "Test Mac", "Home", None).unwrap();
        let bid = pair(&b.address, &b.code, "Test Mac", "Cloud", None).unwrap();
        let at = target(&aid).unwrap();
        let bt = target(&bid).unwrap();
        assert_eq!(reply(&at, "/api/v1/snapshot", None)["server"], "A");
        assert_eq!(reply(&bt, "/api/v1/snapshot", None)["server"], "B");
        let captured = at.clone();
        let pending = std::thread::spawn(move || {
            reply(
                &captured,
                "/api/v1/messages",
                Some(json!({"request_id":"same-id"})),
            )
        });
        let config = reply(&bt, "/api/v1/providers", Some(json!({"name":"B-only"})));
        assert_eq!(config["server"], "B");
        assert_eq!(config["saved"]["name"], "B-only");
        let accepted = pending.join().unwrap();
        assert_eq!(accepted["server"], "A");
        assert_eq!(accepted["request_id"], "same-id");
        a.access.inner.lock().unwrap().sessions.clear();
        assert_eq!(reply(&at, "/api/v1/snapshot", None)["server"], "A");
        // Recreate the client from saved metadata/Keychain and perform a fresh signed login.
        registry().lock().unwrap().targets.remove(&aid);
        assert_eq!(
            reply(&target(&aid).unwrap(), "/api/v1/snapshot", None)["server"],
            "A"
        );
        assert!(edit(&aid, "Wrong address", &b.address).is_err());
        a.access.inner.lock().unwrap().credentials.keys.clear();
        a.access.inner.lock().unwrap().sessions.clear();
        assert!(
            at.request("/api/v1/snapshot", None, Duration::from_secs(3))
                .unwrap_err()
                .contains(&crate::i18n::text("servers.repair-required").to_string())
        );
        assert_eq!(reply(&bt, "/api/v1/snapshot", None)["server"], "B");
        let bytes = std::fs::read(storage_root().join("connections.json")).unwrap();
        let text = String::from_utf8(bytes).unwrap();
        assert!(!text.contains("pkcs8"));
        assert!(!text.contains("fixture-root"));
        assert!(!text.contains("__Host-pot-session"));
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            std::fs::metadata(storage_root().join("connections.json"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
        remove(&aid).unwrap();
        remove(&bid).unwrap();
        assert!(list().is_empty());
        let _ = std::fs::remove_dir_all(storage_root());
    }
    #[test]
    fn endpoints_do_not_accept_credentials_paths_queries_or_unencrypted_remote_hosts() {
        for address in [
            "http://example.com",
            "https://user:pass@example.com",
            "https://example.com/path",
            "https://example.com?token=secret",
            "https://example.com/#secret",
        ] {
            assert!(normalize(address).is_err(), "{address}");
        }
        assert_eq!(
            normalize(" https://example.com/ ").unwrap(),
            "https://example.com"
        );
        assert_eq!(
            normalize("http://localhost:1234").unwrap(),
            "http://localhost:1234"
        );
    }
}
