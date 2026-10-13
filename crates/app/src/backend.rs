//! App-owned backend. Local requests stay in memory; the Swift adapter uses the same dispatch.
use serde::de::DeserializeOwned;
use serde_json::Value;
use std::{
    ffi::{CStr, CString, c_char},
    sync::{Mutex, OnceLock},
    time::Duration,
};
struct Host {
    client: pot_server::ClientTransport,
    relay: tokio::task::JoinHandle<()>,
    stop: tokio::sync::oneshot::Sender<()>,
    task: tokio::task::JoinHandle<anyhow_result::Result>,
}
// Avoid exposing any backend token or endpoint to either renderer.
mod anyhow_result {
    pub type Result = std::result::Result<(), Box<dyn std::error::Error + Send + Sync>>;
}
static SELECTED_ASSISTANT: Mutex<String> = Mutex::new(String::new());
pub fn select_assistant(id: &str) {
    *SELECTED_ASSISTANT.lock().unwrap() = id.into();
}
static RUNTIME: OnceLock<tokio::runtime::Runtime> = OnceLock::new();
static HOST: Mutex<Option<Host>> = Mutex::new(None);
static EXIT_OWNER: Mutex<Option<pot_server::ClientTransport>> = Mutex::new(None);
static STOPPED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);
fn runtime() -> &'static tokio::runtime::Runtime {
    RUNTIME.get_or_init(|| tokio::runtime::Runtime::new().expect("backend runtime"))
}
fn client() -> Result<pot_server::ClientTransport, String> {
    let mut host = HOST.lock().unwrap();
    if STOPPED.load(std::sync::atomic::Ordering::SeqCst) {
        return Err("Pot is shutting down".into());
    }
    if host.is_none() {
        let mut config = pot_server::ServerConfig::from_env();
        config.runtime.data = crate::client::root().join("product");
        config.runtime.workspace = crate::client::root().join("agent-workspace");
        let server = runtime()
            .block_on(pot_server::Server::embedded(config))
            .map_err(|e| e.to_string())?;
        let client = pot_server::ClientTransport::Local(server.local_client());
        let (stop, rx) = tokio::sync::oneshot::channel();
        let task = runtime().spawn(async move {
            server
                .serve(async {
                    let _ = rx.await;
                })
                .await
                .map_err(|e| e.into())
        });
        let relay = runtime().spawn(crate::relay::run(client.clone()));
        *host = Some(Host {
            client,
            relay,
            stop,
            task,
        });
    }
    let host = host.as_ref().unwrap();
    if host.task.is_finished() {
        return Err("Embedded backend stopped; reopen Pot".into());
    }
    Ok(host.client.clone())
}
#[derive(Clone, serde::Serialize, serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct ControlConfig {
    url: String,
    token: String,
    ca_pem: Option<String>,
    runtime_id: String,
}
static REMOTE_MODE: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);
static MODE_GENERATION: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
fn control_file() -> std::path::PathBuf {
    crate::client::root().join("control-client.json")
}
fn control_config() -> Result<ControlConfig, String> {
    serde_json::from_slice(
        &std::fs::read(control_file()).map_err(|_| "Configure a Control first".to_string())?,
    )
    .map_err(|e| format!("Invalid saved Control: {e}"))
}
pub fn mode() -> &'static str {
    if REMOTE_MODE.load(std::sync::atomic::Ordering::SeqCst) {
        "remote"
    } else {
        "local"
    }
}
pub fn control_projection() -> Value {
    match control_config() {
        Ok(c) => {
            serde_json::json!({"configured":true,"url":c.url,"runtime_id":c.runtime_id,"mode":mode()})
        }
        Err(_) => serde_json::json!({"configured":false,"mode":mode()}),
    }
}
pub fn toggle_mode() -> Result<(), String> {
    if mode() == "local" {
        control_config()?;
    }
    REMOTE_MODE.fetch_xor(true, std::sync::atomic::Ordering::SeqCst);
    MODE_GENERATION.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
    *SELECTED_ASSISTANT.lock().unwrap() = String::new();
    Ok(())
}
fn remote_client(c: &ControlConfig) -> Result<pot_server::ClientTransport, String> {
    pot_server::RemoteClient::with_ca(&c.url, c.token.clone(), c.ca_pem.as_deref())
        .map(pot_server::ClientTransport::Remote)
        .map_err(|e| e.to_string())
}
pub fn configure_control(body: Value) -> Result<Value, String> {
    let _guard = CONTROL_CONFIGURATION.lock().unwrap();
    if control_file().exists() {
        return Err("This App already has a Control".into());
    }
    let mut config:ControlConfig=serde_json::from_value(serde_json::json!({"url":body["url"],"token":body["token"],"ca_pem":body["ca_pem"],"runtime_id":""})).map_err(|e|e.to_string())?;
    validate_control(&mut config)?;
    save_control(&control_file(), &config)?;
    Ok(control_projection())
}
fn validate_control(config: &mut ControlConfig) -> Result<(), String> {
    let remote = remote_client(config)?;
    let metadata = runtime().block_on(async {
        let reply = remote
            .request("/api/v1/runtime/control", None)
            .await
            .map_err(|e| e.to_string())?;
        if !reply.status.is_success() {
            return Err("Control rejected client credential".into());
        }
        serde_json::from_slice::<Value>(&reply.body).map_err(|e| e.to_string())
    })?;
    if metadata["role"] != "control" {
        return Err("Address must refer to a standalone Control".into());
    }
    config.runtime_id = metadata["identity"]["runtime_id"]
        .as_str()
        .ok_or("Control identity missing")?
        .to_owned();
    uuid::Uuid::parse_str(&config.runtime_id)
        .map_err(|_| "Invalid Control identity".to_string())?;
    Ok(())
}
fn save_control(path: &std::path::Path, config: &ControlConfig) -> Result<(), String> {
    use std::{io::Write, os::unix::fs::OpenOptionsExt};
    std::fs::create_dir_all(path.parent().unwrap()).map_err(|e| e.to_string())?;
    let temp = path.with_extension(format!("{}.tmp", uuid::Uuid::new_v4()));
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&temp)
        .map_err(|e| e.to_string())?;
    let result = (|| {
        file.write_all(&serde_json::to_vec(&config).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())?;
        file.sync_all().map_err(|e| e.to_string())?;
        std::fs::rename(&temp, path).map_err(|e| e.to_string())?;
        std::fs::File::open(path.parent().unwrap())
            .and_then(|d| d.sync_all())
            .map_err(|e| e.to_string())
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result?;
    Ok(())
}
static CONTROL_CONFIGURATION: Mutex<()> = Mutex::new(());
#[unsafe(no_mangle)]
pub extern "C" fn pot_backend_remote_mode() -> bool {
    mode() == "remote"
}
#[unsafe(no_mangle)]
pub extern "C" fn pot_backend_mode_generation() -> u64 {
    MODE_GENERATION.load(std::sync::atomic::Ordering::SeqCst)
}

pub fn ready() -> Result<(), String> {
    client().map(|_| ())
}
fn request_mode(
    mode: &str,
    path: &str,
    body: Option<Value>,
    timeout: Duration,
) -> Result<Response, String> {
    let control = if mode == "remote" {
        Some(control_config()?)
    } else {
        None
    };
    let client = if let Some(config) = &control {
        remote_client(config)?
    } else {
        client()?
    };
    let selected = SELECTED_ASSISTANT.lock().unwrap().clone();
    let path = if !selected.is_empty()
        && selected != "main"
        && path.starts_with("/api/v1/")
        && !path.starts_with("/api/v1/assistants")
        && !path.starts_with("/api/v1/assistant-tasks")
        && path != pot_protocol::message::API
        && path != "/api/v1/runtime"
        && !path.starts_with("/api/v1/runtime/")
        && path != "/api/v1/machines"
        && path != "/api/v1/machine-connections"
    {
        format!("/api/v1/assistants/{selected}/{}", &path[8..])
    } else {
        path.into()
    };
    runtime().block_on(async {
        let reply = tokio::time::timeout(timeout, client.request(&path, body))
            .await
            .map_err(|_| "Backend request timed out".to_string())?
            .map_err(|e| e.to_string())?;
        if path == "/api/v1/runtime"
            && reply.status.is_success()
            && let Some(config) = &control
        {
            let directory: Value =
                serde_json::from_slice(&reply.body).map_err(|e| e.to_string())?;
            if directory["runtime_id"].as_str() != Some(config.runtime_id.as_str()) {
                return Err("Configured Control identity changed".into());
            }
        }
        Ok(Response {
            status: Status(reply.status.as_u16()),
            body: Body(reply.body),
        })
    })
}
fn shutdown() {
    let mut host = HOST.lock().unwrap();
    if let Some(host) = host.take() {
        host.relay.abort();
        let _ = host.stop.send(());
        let _ = runtime().block_on(host.task);
    }
}
#[unsafe(no_mangle)]
pub unsafe extern "C" fn pot_backend_request(
    path: *const c_char,
    body: *const c_char,
) -> *mut c_char {
    let result: Result<Value, String> = (|| {
        // Swift supplies valid NUL-terminated strings for the duration of this call.
        let path = unsafe { CStr::from_ptr(path) }
            .to_str()
            .map_err(|e| e.to_string())?;
        let text = unsafe { CStr::from_ptr(body) }
            .to_str()
            .map_err(|e| e.to_string())?;
        let body = if text.is_empty() {
            None
        } else {
            Some(serde_json::from_str(text).map_err(|e| e.to_string())?)
        };
        let path = format!("/{}", path.trim_start_matches('/'));
        let (request_mode_name, path) = if let Some(p) = path.strip_prefix("/_client/remote/") {
            ("remote", format!("/{p}"))
        } else if let Some(p) = path.strip_prefix("/_client/local/") {
            ("local", format!("/{p}"))
        } else {
            (mode(), path)
        };
        let reply = request_mode(request_mode_name, &path, body, Duration::from_secs(120))?;
        let value: Value = serde_json::from_slice(&reply.body.0).map_err(|e| e.to_string())?;
        if reply.status.0 >= 400 {
            return Err(value["message"]
                .as_str()
                .or(value["code"].as_str())
                .unwrap_or("Backend request rejected")
                .into());
        }
        Ok(value)
    })();
    let value = match result {
        Ok(value) => serde_json::json!({"value":value}),
        Err(e) => serde_json::json!({"error":e}),
    };
    CString::new(value.to_string()).unwrap().into_raw()
}
#[unsafe(no_mangle)]
pub unsafe extern "C" fn pot_backend_free(text: *mut c_char) {
    if !text.is_null() {
        drop(unsafe { CString::from_raw(text) });
    }
}
#[unsafe(no_mangle)]
pub extern "C" fn pot_backend_shutdown() {
    STOPPED.store(true, std::sync::atomic::Ordering::SeqCst);
    *EXIT_OWNER.lock().unwrap() = HOST.lock().unwrap().as_ref().map(|h| h.client.clone());
    shutdown();
}
#[unsafe(no_mangle)]
pub extern "C" fn pot_backend_restart() {
    shutdown();
}

// Request construction is shared with the old cached client, but has no network transport.
pub struct Requests {
    owner: Option<String>,
    mode: String,
    timeout: Duration,
}
impl Requests {
    pub fn for_mode(mode: &str) -> Self {
        Self {
            mode: mode.into(),
            owner: Some({
                let selected = SELECTED_ASSISTANT.lock().unwrap();
                if selected.is_empty() {
                    "main".into()
                } else {
                    selected.clone()
                }
            }),
            timeout: Duration::from_secs(120),
        }
    }
    pub fn timeout_global(mut self, timeout: Option<Duration>) -> Self {
        if let Some(timeout) = timeout {
            self.timeout = timeout;
        }
        self
    }
    pub fn build(self) -> Self {
        self
    }
    pub fn new_agent(self) -> Self {
        self
    }
    pub fn scope(mut self, owner: &str) -> Self {
        self.owner = Some(owner.into());
        self
    }
    fn scoped(&self, path: &str) -> String {
        if path.starts_with("/api/v1/assistants")
            || path.starts_with("/api/v1/assistant-tasks")
            || path == "/api/v1/runtime"
            || path.starts_with("/api/v1/runtime/")
            || path == "/api/v1/machines"
            || path == "/api/v1/machine-connections"
        {
            return path.into();
        }
        self.owner
            .as_ref()
            .map(|owner| {
                format!(
                    "/api/v1/assistants/{owner}/{}",
                    path.strip_prefix("/api/v1/").unwrap_or(path)
                )
            })
            .unwrap_or_else(|| path.into())
    }
    pub fn get(&self, url: &str) -> Builder {
        Builder::new(&self.scoped(url), self.timeout, self.mode.clone())
    }
    pub fn post(&self, url: &str) -> Builder {
        Builder::new(&self.scoped(url), self.timeout, self.mode.clone())
    }
}
pub struct Builder {
    url: url::Url,
    mode: String,
    status_error: bool,
    timeout: Duration,
}
impl Builder {
    fn new(path: &str, timeout: Duration, mode: String) -> Self {
        Self {
            mode,
            url: url::Url::parse(&format!("http://embedded{path}")).expect("local API path"),
            timeout,
            status_error: true,
        }
    }
    pub fn query(mut self, name: &str, value: impl AsRef<str>) -> Self {
        self.url.query_pairs_mut().append_pair(name, value.as_ref());
        self
    }
    pub fn config(self) -> Self {
        self
    }
    pub fn http_status_as_error(mut self, enabled: bool) -> Self {
        self.status_error = enabled;
        self
    }
    pub fn build(self) -> Self {
        self
    }
    fn dispatch(self, body: Option<Value>) -> Result<Response, String> {
        let path = format!(
            "{}{}",
            self.url.path(),
            self.url
                .query()
                .map(|q| format!("?{q}"))
                .unwrap_or_default()
        );
        let response = request_mode(&self.mode, &path, body, self.timeout)?;
        if self.status_error && response.status.0 >= 400 {
            return Err(serde_json::from_slice::<Value>(&response.body.0)
                .ok()
                .and_then(|v| {
                    v["message"]
                        .as_str()
                        .or(v["code"].as_str())
                        .map(str::to_owned)
                })
                .unwrap_or_else(|| format!("Backend rejected request ({})", response.status.0)));
        }
        Ok(response)
    }
    pub fn call(self) -> Result<Response, String> {
        self.dispatch(None)
    }
    pub fn send_json(self, value: Value) -> Result<Response, String> {
        self.dispatch(Some(value))
    }
}
pub struct Status(u16);
impl Status {
    pub fn is_client_error(&self) -> bool {
        (400..500).contains(&self.0)
    }
    pub fn is_server_error(&self) -> bool {
        self.0 >= 500
    }
}
pub struct Response {
    status: Status,
    body: Body,
}
impl Response {
    pub fn status(&self) -> &Status {
        &self.status
    }
    pub fn body_mut(&mut self) -> &mut Body {
        &mut self.body
    }
}
pub struct Body(Vec<u8>);
impl Body {
    pub fn read_json<T: DeserializeOwned>(&mut self) -> Result<T, serde_json::Error> {
        serde_json::from_slice(&self.0)
    }
    #[cfg(test)]
    pub fn builder() -> BodyBuilder {
        BodyBuilder
    }
}
#[cfg(test)]
pub struct BodyBuilder;
#[cfg(test)]
impl BodyBuilder {
    pub fn data(self, s: impl AsRef<str>) -> Body {
        Body(s.as_ref().as_bytes().to_vec())
    }
}

pub(crate) fn relay_remote() -> Option<crate::relay::Connection> {
    let config = control_config().ok()?;
    Some(crate::relay::Connection {
        client: remote_client(&config).ok()?,
        id: config.runtime_id,
    })
}

#[cfg(test)]
mod control_tests {
    use super::*;
    #[test]
    fn control_requires_authenticated_control_role_and_keeps_private_configuration() {
        use std::io::{Read, Write};
        use std::os::unix::fs::PermissionsExt;
        for role in ["local", "control"] {
            let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            let url = format!("http://{}", listener.local_addr().unwrap());
            let id = uuid::Uuid::new_v4().to_string();
            let expected = id.clone();
            let server = std::thread::spawn(move || {
                let (mut socket, _) = listener.accept().unwrap();
                let mut buffer = [0; 8192];
                let count = socket.read(&mut buffer).unwrap();
                let request = String::from_utf8_lossy(&buffer[..count]);
                assert!(request.starts_with("GET /api/v1/runtime/control "));
                assert!(
                    request
                        .to_lowercase()
                        .contains("authorization: bearer private-client-token")
                );
                let body =
                    serde_json::json!({"role":role,"identity":{"runtime_id":expected}}).to_string();
                write!(socket,"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",body.len(),body).unwrap();
            });
            let mut config = ControlConfig {
                url,
                token: "private-client-token".into(),
                ca_pem: None,
                runtime_id: String::new(),
            };
            let result = validate_control(&mut config);
            server.join().unwrap();
            assert_eq!(result.is_ok(), role == "control");
            if role == "control" {
                assert_eq!(config.runtime_id, id);
                let root = std::env::temp_dir()
                    .join(format!("pot-client-control-{}", uuid::Uuid::new_v4()));
                let path = root.join("control.json");
                save_control(&path, &config).unwrap();
                assert_eq!(
                    std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                    0o600
                );
                let stored: ControlConfig =
                    serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
                assert_eq!(stored.runtime_id, id);
                std::fs::remove_dir_all(root).unwrap();
            }
        }
    }
    #[test]
    fn request_builders_capture_mode_and_owner_independently() {
        let local = Requests::for_mode("local")
            .scope("same-owner")
            .get("/api/v1/tasks");
        let remote = Requests::for_mode("remote")
            .scope("same-owner")
            .get("/api/v1/tasks");
        assert_eq!(local.mode, "local");
        assert_eq!(remote.mode, "remote");
        assert_eq!(local.url.path(), "/api/v1/assistants/same-owner/tasks");
        assert_eq!(remote.url.path(), local.url.path());
        assert_eq!(
            Requests::for_mode("remote")
                .scope("owner")
                .get("/api/v1/runtime/control")
                .url
                .path(),
            "/api/v1/runtime/control"
        );
    }
}
