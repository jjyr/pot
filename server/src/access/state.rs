use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use std::{
    collections::HashMap,
    path::PathBuf,
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
use url::Url;
use uuid::Uuid;

pub const SESSION_COOKIE: &str = "__Host-pot-session";
pub const CEREMONY_COOKIE: &str = "__Host-pot-ceremony";
const REQUEST_TTL: Duration = Duration::from_secs(300);
const SESSION_IDLE: Duration = Duration::from_secs(1800);
const SESSION_MAX: Duration = Duration::from_secs(43200);

#[derive(Clone, Debug)]
pub struct AccessConfig {
    pub origin: Url,
    pub bind: std::net::SocketAddr,
    pub certificate: PathBuf,
    pub private_key: PathBuf,
    pub assets: PathBuf,
}
impl AccessConfig {
    pub fn from_env() -> Result<Option<Self>> {
        let Ok(origin) = std::env::var("POT_ACCESS_ORIGIN") else {
            return Ok(None);
        };
        let origin = Url::parse(&origin)?;
        Self::validate_origin(&origin)?;
        let bind: std::net::SocketAddr = std::env::var("POT_ACCESS_BIND")
            .unwrap_or_else(|_| "127.0.0.1:17701".into())
            .parse()?;
        ensure!(
            origin.scheme() == "https" || bind.ip().is_loopback(),
            "Local HTTP must bind loopback"
        );
        Ok(Some(Self {
            origin,
            bind,
            certificate: std::env::var("POT_ACCESS_CERT").unwrap_or_default().into(),
            private_key: std::env::var("POT_ACCESS_KEY").unwrap_or_default().into(),
            assets: std::env::var_os("POT_ACCESS_ASSETS")
                .map(PathBuf::from)
                .unwrap_or_else(|| {
                    std::env::current_exe()
                        .ok()
                        .and_then(|path| path.parent().map(|parent| parent.join("mobile")))
                        .filter(|path| path.is_dir())
                        .unwrap_or_else(|| PathBuf::from("mobile/dist"))
                }),
        }))
    }
    pub fn validate_origin(origin: &Url) -> Result<()> {
        ensure!(
            (origin.scheme() == "https" && origin.domain().is_some())
                || (origin.scheme() == "http" && origin.host_str() == Some("localhost")),
            "Remote access requires a stable HTTPS domain; HTTP is allowed only on localhost"
        );
        ensure!(
            origin.username().is_empty()
                && origin.password().is_none()
                && origin.path() == "/"
                && origin.query().is_none()
                && origin.fragment().is_none(),
            "Origin cannot contain credentials, path, query or fragment"
        );
        Ok(())
    }
}
#[derive(Serialize, Deserialize, Default)]
pub struct Credentials {
    #[serde(default)]
    pub disabled: bool,
    pub keys: HashMap<String, Credential>,
    #[serde(default)]
    pub subscriptions: HashMap<String, super::push::Subscription>,
}
#[derive(Serialize, Deserialize)]
pub struct Credential {
    pub name: String,
    pub public_key: String,
}
pub struct Pairing {
    pub digest: Vec<u8>,
    pub expires: Instant,
    pub attempts: u8,
}
pub struct Challenge {
    pub expires: Instant,
    pub message: String,
    pub key: String,
    pub name: String,
    pub public_key: String,
    pub pairing: bool,
}
pub struct Session {
    pub key: String,
    pub created: Instant,
    pub touched: Instant,
}
pub struct Inner {
    pub credentials: Credentials,
    pub pairing: Option<Pairing>,
    pub challenges: HashMap<String, Challenge>,
    pub sessions: HashMap<String, Session>,
    pub enabled: bool,
    pub requests: std::collections::VecDeque<Instant>,
}
#[derive(Clone)]
pub struct Access {
    pub origin: String,
    pub assets: PathBuf,
    pub data: PathBuf,
    pub root_token: String,
    pub runtime: axum::Router,
    pub inner: Arc<Mutex<Inner>>,
    pub notifications: Option<pot_runtime::Service>,
    pub push: Option<Arc<super::push::Push>>,
    pub policy: tokio::sync::watch::Sender<bool>,
    pub desktop_open: Arc<tokio::sync::Mutex<()>>,
    pub desktops: Arc<Mutex<HashMap<String, (String, Instant)>>>,
    pub foreground: Arc<Mutex<HashMap<String, Instant>>>,
}
impl Access {
    pub fn new(
        config: &AccessConfig,
        data: PathBuf,
        root_token: String,
        runtime: axum::Router,
    ) -> Result<Self> {
        AccessConfig::validate_origin(&config.origin)?;
        let path = data.join("access-clients.json");
        let credentials: Credentials = if path.exists() {
            serde_json::from_slice(&std::fs::read(path)?)?
        } else {
            // Preserve a previously disabled endpoint, but never migrate old client authority.
            let legacy = data.join("access-credentials.json");
            let disabled = if legacy.exists() {
                serde_json::from_slice::<serde_json::Value>(&std::fs::read(legacy)?)?["disabled"]
                    .as_bool()
                    .unwrap_or(false)
            } else {
                false
            };
            Credentials {
                disabled,
                ..Default::default()
            }
        };
        let enabled = !credentials.disabled;
        Ok(Self {
            notifications: None,
            push: super::push::Push::from_env()?.map(Arc::new),
            policy: tokio::sync::watch::channel(enabled).0,
            desktop_open: Arc::new(tokio::sync::Mutex::new(())),
            desktops: Arc::new(Mutex::new(HashMap::new())),
            foreground: Arc::new(Mutex::new(HashMap::new())),
            origin: config.origin.origin().ascii_serialization(),
            assets: config.assets.clone(),
            data,
            root_token,
            runtime,
            inner: Arc::new(Mutex::new(Inner {
                credentials,
                pairing: None,
                challenges: HashMap::new(),
                sessions: HashMap::new(),
                enabled,
                requests: std::collections::VecDeque::new(),
            })),
        })
    }
    pub fn rate_limit(&self) -> bool {
        let mut i = self.inner.lock().unwrap();
        let now = Instant::now();
        while i
            .requests
            .front()
            .is_some_and(|t| now.duration_since(*t) >= Duration::from_secs(60))
        {
            i.requests.pop_front();
        }
        if i.requests.len() >= 60 {
            return false;
        }
        i.requests.push_back(now);
        true
    }
    pub fn persist(&self, inner: &Inner) -> Result<()> {
        use std::{io::Write, os::unix::fs::OpenOptionsExt};
        let path = self.data.join("access-clients.json");
        let temporary = path.with_extension("tmp");
        let mut f = std::fs::OpenOptions::new()
            .create(true)
            .truncate(true)
            .write(true)
            .mode(0o600)
            .open(&temporary)?;
        f.write_all(&serde_json::to_vec(&inner.credentials)?)?;
        f.sync_all()?;
        std::fs::rename(temporary, path)?;
        std::fs::File::open(&self.data)?.sync_all()?;
        Ok(())
    }
    pub fn cleanup(inner: &mut Inner) {
        let now = Instant::now();
        if inner.pairing.as_ref().is_some_and(|p| p.expires <= now) {
            inner.pairing = None;
        }
        inner.challenges.retain(|_, p| p.expires > now);
        inner.sessions.retain(|_, s| {
            now.duration_since(s.created) < SESSION_MAX
                && now.duration_since(s.touched) < SESSION_IDLE
        });
    }
    pub fn valid_session(&self, id: &str, touch: bool) -> bool {
        self.session_key(id, touch).is_some()
    }
    pub fn session_key(&self, id: &str, touch: bool) -> Option<String> {
        let mut inner = self.inner.lock().unwrap();
        Self::cleanup(&mut inner);
        if !inner.enabled {
            return None;
        }
        let key = inner.sessions.get(id).map(|s| s.key.clone());
        if !key
            .as_ref()
            .is_some_and(|k| inner.credentials.keys.contains_key(k))
        {
            return None;
        }
        if touch {
            inner.sessions.get_mut(id).unwrap().touched = Instant::now();
        }
        key
    }
    pub fn session(inner: &mut Inner, key: String) -> String {
        Self::cleanup(inner);
        // Bound memory even for repeated successful logins.
        if inner.sessions.len() >= 128
            && let Some(oldest) = inner
                .sessions
                .iter()
                .min_by_key(|(_, s)| s.created)
                .map(|(id, _)| id.clone())
        {
            inner.sessions.remove(&oldest);
        }
        let id = Uuid::new_v4().to_string();
        let now = Instant::now();
        inner.sessions.insert(
            id.clone(),
            Session {
                key,
                created: now,
                touched: now,
            },
        );
        id
    }
    pub fn expiry() -> Instant {
        Instant::now() + REQUEST_TTL
    }
}
pub fn cookie(headers: &axum::http::HeaderMap, name: &str) -> Option<String> {
    // HTTP/2 clients may split Cookie across several header fields (RFC 9113).
    headers
        .get_all("cookie")
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(';'))
        .find_map(|item| {
            let (k, v) = item.trim().split_once('=')?;
            (k == name).then(|| v.to_owned())
        })
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn split_http2_cookie_headers_are_all_parsed() {
        let mut h = axum::http::HeaderMap::new();
        h.append(
            "cookie",
            format!("{CEREMONY_COOKIE}=challenge").parse().unwrap(),
        );
        h.append(
            "cookie",
            format!("{SESSION_COOKIE}=session").parse().unwrap(),
        );
        assert_eq!(cookie(&h, SESSION_COOKIE).as_deref(), Some("session"));
    }
}
