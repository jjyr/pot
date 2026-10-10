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
        *host = Some(Host { client, stop, task });
    }
    let host = host.as_ref().unwrap();
    if host.task.is_finished() {
        return Err("Embedded backend stopped; reopen Pot".into());
    }
    Ok(host.client.clone())
}
pub fn ready() -> Result<(), String> {
    client().map(|_| ())
}
fn request(path: &str, body: Option<Value>, timeout: Duration) -> Result<Response, String> {
    let client = client()?;
    let selected = SELECTED_ASSISTANT.lock().unwrap().clone();
    let path = if !selected.is_empty()
        && selected != "main"
        && path.starts_with("/api/v1/")
        && !path.starts_with("/api/v1/assistants")
        && !path.starts_with("/api/v1/assistant-tasks")
        && path != "/api/v1/runtime"
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
        Ok(Response {
            status: Status(reply.status.as_u16()),
            body: Body(reply.body),
        })
    })
}
fn shutdown() {
    let mut host = HOST.lock().unwrap();
    if let Some(host) = host.take() {
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
        let reply = request(
            &format!("/{}", path.trim_start_matches('/')),
            body,
            Duration::from_secs(120),
        )?;
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
    timeout: Duration,
}
impl Requests {
    pub fn config_builder() -> Self {
        Self {
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
        Builder::new(&self.scoped(url), self.timeout)
    }
    pub fn post(&self, url: &str) -> Builder {
        Builder::new(&self.scoped(url), self.timeout)
    }
}
pub struct Builder {
    url: url::Url,
    status_error: bool,
    timeout: Duration,
}
impl Builder {
    fn new(path: &str, timeout: Duration) -> Self {
        Self {
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
        let response = request(&path, body, self.timeout)?;
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
