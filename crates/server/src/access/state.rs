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
            assets: Self::assets_from_env(),
        }))
    }
    pub(crate) fn assets_from_env() -> PathBuf {
        std::env::var_os("POT_ACCESS_ASSETS")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                std::env::current_exe()
                    .ok()
                    .and_then(|path| path.parent().map(|parent| parent.join("mobile")))
                    .filter(|path| path.is_dir())
                    .unwrap_or_else(|| PathBuf::from("crates/web-ui/dist"))
            })
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
#[derive(Serialize, Deserialize, Default, Clone)]
pub struct Credentials {
    #[serde(default)]
    pub disabled: bool,
    pub token: String,
    #[serde(default)]
    pub web: Option<super::web::Preferences>,
    #[serde(default)]
    pub subscriptions: HashMap<String, super::push::Subscription>,
}
pub struct DesktopTicket {
    pub owner: String,
    pub expires: Instant,
    pub endpoint: Arc<std::sync::atomic::AtomicU64>,
    pub generation: u64,
}
impl DesktopTicket {
    pub fn live(&self) -> bool {
        self.expires > Instant::now()
            && self.generation == self.endpoint.load(std::sync::atomic::Ordering::Acquire)
    }
}
pub struct Inner {
    pub credentials: Credentials,
    pub enabled: bool,
    pub requests: std::collections::VecDeque<Instant>,
}
#[derive(Clone)]
pub struct Access {
    pub origin: Arc<std::sync::RwLock<String>>,
    pub tailscale: Option<Arc<super::tailscale::Tailscale>>,
    pub web: Option<Arc<super::web::Web>>,
    pub endpoint_enabled: Option<Arc<std::sync::atomic::AtomicBool>>,
    pub endpoint_epoch: Arc<std::sync::atomic::AtomicU64>,
    endpoint_snapshot: Option<u64>,
    pub assets: PathBuf,
    pub data: PathBuf,
    pub root_token: String,
    pub runtime: axum::Router,
    pub fixed_control: bool,
    pub inner: Arc<Mutex<Inner>>,
    pub notifications: Arc<Mutex<Option<super::push::NotificationSource>>>,
    pub push: Option<Arc<super::push::Push>>,
    pub policy: tokio::sync::watch::Sender<bool>,
    pub desktop_open: Arc<tokio::sync::Mutex<()>>,
    pub desktops: Arc<Mutex<HashMap<String, DesktopTicket>>>,
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
        let path = data.join("access-token.json");
        let credentials: Credentials = if path.exists() {
            serde_json::from_slice(&std::fs::read(path)?)?
        } else {
            Credentials {
                token: Self::generate_token()?,
                ..Default::default()
            }
        };
        ensure!(credentials.token.len() == 43, "Invalid saved access token");
        let enabled = !credentials.disabled;
        let access = Self {
            notifications: Arc::new(Mutex::new(None)),
            push: super::push::Push::from_env()?.map(Arc::new),
            policy: tokio::sync::watch::channel(enabled).0,
            desktop_open: Arc::new(tokio::sync::Mutex::new(())),
            desktops: Arc::new(Mutex::new(HashMap::new())),
            foreground: Arc::new(Mutex::new(HashMap::new())),
            origin: Arc::new(std::sync::RwLock::new(
                config.origin.origin().ascii_serialization(),
            )),
            tailscale: None,
            web: None,
            endpoint_enabled: None,
            endpoint_epoch: Arc::new(std::sync::atomic::AtomicU64::new(0)),
            endpoint_snapshot: None,
            assets: config.assets.clone(),
            data,
            root_token,
            runtime,
            fixed_control: false,
            inner: Arc::new(Mutex::new(Inner {
                credentials,
                enabled,
                requests: std::collections::VecDeque::new(),
            })),
        };
        access.persist(&access.inner.lock().unwrap())?;
        Ok(access)
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
        let path = self.data.join("access-token.json");
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
    pub fn origin(&self) -> String {
        self.origin.read().unwrap().clone()
    }
    pub fn generate_token() -> Result<String> {
        use base64::Engine;
        use ring::rand::{SecureRandom, SystemRandom};
        let mut bytes = [0u8; 32];
        SystemRandom::new()
            .fill(&mut bytes)
            .map_err(|_| anyhow::anyhow!("Token generation failed"))?;
        Ok(base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(bytes))
    }
    pub fn endpoint_generation(&self) -> u64 {
        self.endpoint_snapshot.unwrap_or_else(|| {
            self.endpoint_epoch
                .load(std::sync::atomic::Ordering::Acquire)
        })
    }
    pub fn capture_endpoint(&mut self) {
        self.endpoint_snapshot = Some(
            self.endpoint_epoch
                .load(std::sync::atomic::Ordering::Acquire),
        );
    }
    pub fn invalidate_endpoint(&self) {
        self.endpoint_epoch
            .fetch_add(1, std::sync::atomic::Ordering::AcqRel);
    }
    pub fn endpoint_active(&self) -> bool {
        if self.endpoint_snapshot.is_some_and(|generation| {
            generation
                != self
                    .endpoint_epoch
                    .load(std::sync::atomic::Ordering::Acquire)
        }) {
            return false;
        }
        self.endpoint_enabled
            .as_ref()
            .is_none_or(|enabled| enabled.load(std::sync::atomic::Ordering::Acquire))
    }
    pub fn valid_token(&self, token: &str) -> bool {
        if !self.endpoint_active() {
            return false;
        }
        let inner = self.inner.lock().unwrap();
        inner.enabled
            && token.len() == inner.credentials.token.len()
            && token
                .as_bytes()
                .iter()
                .zip(inner.credentials.token.as_bytes())
                .fold(0u8, |diff, (a, b)| diff | (a ^ b))
                == 0
    }
    pub fn generation(&self) -> String {
        Self::token_generation(&self.inner.lock().unwrap().credentials.token)
    }
    pub fn token_generation(token: &str) -> String {
        use base64::Engine;
        base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(ring::digest::digest(
            &ring::digest::SHA256,
            token.as_bytes(),
        ))
    }
    // Stateless, token-signed browser cookies support navigation/WebSockets that cannot set headers.
    pub fn browser_cookie(&self) -> String {
        use base64::Engine;
        let id = Uuid::new_v4().to_string();
        let inner = self.inner.lock().unwrap();
        let key = ring::hmac::Key::new(ring::hmac::HMAC_SHA256, inner.credentials.token.as_bytes());
        format!(
            "{}.{}",
            id,
            base64::engine::general_purpose::URL_SAFE_NO_PAD
                .encode(ring::hmac::sign(&key, id.as_bytes()))
        )
    }
    pub fn valid_session(&self, value: &str, _: bool) -> bool {
        if !self.endpoint_active() {
            return false;
        }
        use base64::Engine;
        let Some((id, mac)) = value.split_once('.') else {
            return false;
        };
        if Uuid::parse_str(id).is_err() {
            return false;
        }
        let Ok(mac) = base64::engine::general_purpose::URL_SAFE_NO_PAD.decode(mac) else {
            return false;
        };
        let inner = self.inner.lock().unwrap();
        let key = ring::hmac::Key::new(ring::hmac::HMAC_SHA256, inner.credentials.token.as_bytes());
        inner.enabled && ring::hmac::verify(&key, id.as_bytes(), &mac).is_ok()
    }
    pub fn session_key(&self, id: &str, touch: bool) -> Option<String> {
        self.valid_session(id, touch).then(|| self.generation())
    }
    pub fn authorization(&self, headers: &axum::http::HeaderMap) -> Option<String> {
        if let Some(value) = headers.get("authorization") {
            let token = value.to_str().ok()?.strip_prefix("Bearer ")?;
            return self.valid_token(token).then(|| token.to_owned());
        }
        cookie(headers, SESSION_COOKIE).filter(|id| self.valid_session(id, false))
    }
    pub fn valid_authorization(&self, proof: &str) -> bool {
        self.valid_token(proof) || self.valid_session(proof, false)
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
        h.append("cookie", "other=ignored".to_owned().parse().unwrap());
        h.append(
            "cookie",
            format!("{SESSION_COOKIE}=session").parse().unwrap(),
        );
        assert_eq!(cookie(&h, SESSION_COOKIE).as_deref(), Some("session"));
    }
}
