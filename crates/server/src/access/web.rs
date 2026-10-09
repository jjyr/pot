//! Independently managed LAN and tailnet browser listeners for the embedded App.
use super::Access;
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    net::Ipv4Addr,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};

#[derive(Clone, Copy, Default, Serialize, Deserialize)]
pub struct Preferences {
    pub lan: bool,
    pub tailscale: bool,
    pub https: bool,
}
struct LanListener {
    origin: String,
    handle: axum_server::Handle<std::net::SocketAddr>,
    active: Arc<AtomicBool>,
    epoch: Arc<std::sync::atomic::AtomicU64>,
}
impl LanListener {
    fn stop(self) {
        self.epoch.fetch_add(1, Ordering::AcqRel);
        self.active.store(false, Ordering::Release);
        self.handle.shutdown();
    }
}
struct State {
    preferences: Preferences,
    lan: Vec<LanListener>,
    error: String,
}
pub struct Web {
    #[cfg(test)]
    lan_override: Option<(Vec<Ipv4Addr>, u16)>,
    state: tokio::sync::Mutex<State>,
}
impl Web {
    pub fn new(preferences: Preferences) -> Arc<Self> {
        Arc::new(Self {
            #[cfg(test)]
            lan_override: None,
            state: tokio::sync::Mutex::new(State {
                preferences,
                lan: Vec::new(),
                error: String::new(),
            }),
        })
    }
    fn lan_binding(&self) -> Result<(Vec<Ipv4Addr>, u16)> {
        #[cfg(test)]
        if let Some(value) = &self.lan_override {
            return Ok(value.clone());
        }
        Ok((lan_addresses()?, 17701))
    }
    pub async fn status(&self, access: &Access) -> Value {
        let state = self.state.lock().await;
        let origin = access.origin();
        json!({"lan":state.preferences.lan,"tailscale":state.preferences.tailscale,"https":state.preferences.https,
            "lan_urls":state.lan.iter().map(|listener|&listener.origin).collect::<Vec<_>>(),
            "tailscale_url":if state.preferences.tailscale && origin != "http://localhost" { origin } else { String::new() },"error":state.error})
    }
    pub async fn configure(&self, access: &Access, action: &str, enabled: bool) -> Result<()> {
        let mut state = self.state.lock().await;
        let mut prefs = state.preferences;
        match action {
            "lan" => prefs.lan = enabled,
            "tailscale" => prefs.tailscale = enabled,
            "https" => prefs.https = enabled,
            _ => anyhow::bail!("Unknown Web UI setting"),
        }
        self.apply(access, &mut state, prefs, action != "lan").await
    }
    async fn apply(
        &self,
        access: &Access,
        state: &mut State,
        prefs: Preferences,
        update_tailnet: bool,
    ) -> Result<()> {
        let mut started = Vec::new();
        if prefs.lan && state.lan.is_empty() && !update_tailnet {
            let (addresses, port) = self.lan_binding()?;
            ensure!(
                !addresses.is_empty(),
                "No private IPv4 LAN address is available"
            );
            // Bind every address before starting any of them, so conflicts leave the old state intact.
            let listeners = addresses
                .into_iter()
                .map(|ip| {
                    let listener = std::net::TcpListener::bind((ip, port))?;
                    listener.set_nonblocking(true)?;
                    Ok((format!("http://{}", listener.local_addr()?), listener))
                })
                .collect::<Result<Vec<_>>>()?;
            for (origin, listener) in listeners {
                let mut lan = access.clone();
                lan.origin = Arc::new(std::sync::RwLock::new(origin.clone()));
                let active = Arc::new(AtomicBool::new(true));
                lan.endpoint_enabled = Some(active.clone());
                let epoch = Arc::new(std::sync::atomic::AtomicU64::new(0));
                lan.endpoint_epoch = epoch.clone();
                let handle = axum_server::Handle::new();
                let serving = handle.clone();
                tokio::spawn(async move {
                    if let Ok(server) = axum_server::from_tcp(listener) {
                        let _ = server
                            .handle(serving)
                            .serve(super::public_router(lan).into_make_service())
                            .await;
                    }
                });
                started.push(LanListener {
                    origin,
                    handle,
                    active,
                    epoch,
                });
            }
        }
        if update_tailnet && prefs.tailscale {
            let ts = access
                .tailscale
                .as_ref()
                .ok_or_else(|| anyhow::anyhow!("Tailscale unavailable"))?;
            match ts.set_web(true, prefs.https).await {
                Ok(origin) => *access.origin.write().unwrap() = origin,
                Err(error) => {
                    for listener in started {
                        listener.stop();
                    }
                    return Err(error);
                }
            }
        } else if update_tailnet && let Some(ts) = &access.tailscale {
            ts.set_web(false, prefs.https).await?;
            *access.origin.write().unwrap() = "http://localhost".into();
        }
        let result = {
            let mut inner = access.inner.lock().unwrap();
            let old = inner.credentials.clone();
            inner.credentials.web = Some(prefs);
            inner.credentials.disabled = !(prefs.lan || prefs.tailscale);
            if let Err(error) = access.persist(&inner) {
                inner.credentials = old;
                Err(error)
            } else {
                inner.enabled = !inner.credentials.disabled;
                access.policy.send_replace(inner.enabled);
                Ok(())
            }
        };
        if let Err(error) = result {
            for listener in started {
                listener.stop();
            }
            // Return the tailnet listener to its previously saved mode.
            if let Some(ts) = &access.tailscale
                && let Ok(origin) = ts
                    .set_web(state.preferences.tailscale, state.preferences.https)
                    .await
            {
                *access.origin.write().unwrap() = origin;
            }
            return Err(error);
        }
        if !prefs.lan {
            for listener in state.lan.drain(..) {
                listener.stop();
            }
        }
        if update_tailnet && let Some(active) = &access.endpoint_enabled {
            if state.preferences.tailscale != prefs.tailscale
                || state.preferences.https != prefs.https
            {
                access.invalidate_endpoint();
            }
            active.store(prefs.tailscale, Ordering::Release);
        }
        state.lan.extend(started);
        state.preferences = prefs;
        state.error.clear();
        Ok(())
    }
    pub async fn restore(&self, access: &Access) {
        let mut state = self.state.lock().await;
        let prefs = state.preferences;
        // LAN restores even when tailnet login/certificates are unavailable.
        let lan = Preferences {
            tailscale: false,
            ..prefs
        };
        if let Err(error) = self.apply(access, &mut state, lan, false).await {
            state.error = error.to_string();
        }
        state.preferences = prefs;
        // Preserve saved intent; the background tailnet loop restores that endpoint later.
        let mut inner = access.inner.lock().unwrap();
        inner.credentials.web = Some(prefs);
        let _ = access.persist(&inner);
    }
    pub async fn connect(&self, access: &Access) -> Result<()> {
        let _state = self.state.lock().await;
        access
            .tailscale
            .as_ref()
            .ok_or_else(|| anyhow::anyhow!("Tailscale unavailable"))?
            .connect()
            .await
    }
    pub async fn maintain_tailnet(&self, access: &Access) {
        let mut state = self.state.lock().await;
        let Some(ts) = &access.tailscale else {
            return;
        };
        if !ts.configured() {
            return;
        }
        if let Err(error) = ts.connect().await {
            state.error = error.to_string();
            return;
        }
        if !state.preferences.tailscale {
            return;
        }
        if ts.status().await["state"] != "Running" {
            if let Some(active) = &access.endpoint_enabled
                && active.swap(false, Ordering::AcqRel)
            {
                access.invalidate_endpoint();
            }
            return;
        }
        if access.endpoint_active() {
            return;
        }
        let prefs = state.preferences;
        if let Err(error) = self.apply(access, &mut state, prefs, true).await {
            if let Some(active) = &access.endpoint_enabled {
                active.store(false, Ordering::Release);
            }
            state.error = error.to_string();
        }
    }
    pub async fn disconnect(&self, access: &Access) -> Result<()> {
        let mut state = self.state.lock().await;
        access.invalidate_endpoint();
        if let Some(active) = &access.endpoint_enabled {
            active.store(false, Ordering::Release);
        }
        if let Some(ts) = &access.tailscale {
            ts.disconnect().await?;
        }
        *access.origin.write().unwrap() = "http://localhost".into();
        // Keep the chosen Web UI mode for reconnection; LAN keeps its own listeners.
        state.error.clear();
        Ok(())
    }
    pub async fn shutdown(&self) {
        let mut state = self.state.lock().await;
        for listener in state.lan.drain(..) {
            listener.stop();
        }
    }
}
fn lan_addresses() -> Result<Vec<Ipv4Addr>> {
    let mut first = std::ptr::null_mut();
    ensure!(
        unsafe { libc::getifaddrs(&mut first) } == 0,
        "Could not read network interfaces"
    );
    let mut addresses = Vec::new();
    let mut item = first;
    while !item.is_null() {
        let interface = unsafe { &*item };
        if !interface.ifa_addr.is_null()
            && interface.ifa_flags & libc::IFF_UP as u32 != 0
            && unsafe { (*interface.ifa_addr).sa_family as i32 } == libc::AF_INET
        {
            let address = unsafe { &*(interface.ifa_addr as *const libc::sockaddr_in) };
            let ip = Ipv4Addr::from(address.sin_addr.s_addr.to_ne_bytes());
            if ip.is_private() && !addresses.contains(&ip) {
                addresses.push(ip);
            }
        }
        item = interface.ifa_next;
    }
    unsafe { libc::freeifaddrs(first) };
    Ok(addresses)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::access::AccessConfig;
    use axum::{Router, routing::get};
    use tower::ServiceExt;

    fn fixture() -> (Access, std::path::PathBuf) {
        let dir = std::env::temp_dir().join(format!("pot-web-modes-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&dir).unwrap();
        let config = AccessConfig {
            origin: "http://localhost".parse().unwrap(),
            bind: "127.0.0.1:0".parse().unwrap(),
            certificate: Default::default(),
            private_key: Default::default(),
            assets: dir.clone(),
        };
        let access = Access::new(
            &config,
            dir.clone(),
            "owner".into(),
            Router::new().route(
                "/api/v1/snapshot",
                get(|| async { axum::Json(json!({"ok":true})) }),
            ),
        )
        .unwrap();
        (access, dir)
    }
    #[tokio::test]
    async fn lan_token_cookie_rotation_and_stop_are_independent_of_tailnet() {
        let (mut access, dir) = fixture();
        access.endpoint_enabled = Some(Arc::new(AtomicBool::new(false)));
        let web = Web {
            lan_override: Some((vec![Ipv4Addr::LOCALHOST], 0)),
            state: tokio::sync::Mutex::new(State {
                preferences: Default::default(),
                lan: Vec::new(),
                error: String::new(),
            }),
        };
        web.restore(&access).await;
        assert!(!access.inner.lock().unwrap().enabled);
        web.configure(&access, "lan", true).await.unwrap();
        let url = web.status(&access).await["lan_urls"][0]
            .as_str()
            .unwrap()
            .to_owned();
        let token = access.inner.lock().unwrap().credentials.token.clone();
        let client = reqwest::Client::builder().no_proxy().build().unwrap();
        let response = client
            .post(format!("{url}/access/token"))
            .header("origin", &url)
            .bearer_auth(&token)
            .json(&json!({}))
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), 200);
        let cookie = response.headers()["set-cookie"]
            .to_str()
            .unwrap()
            .to_owned();
        assert!(cookie.starts_with("pot-session="));
        assert!(!cookie.contains("Secure"));
        assert!(!response.headers().contains_key("strict-transport-security"));
        let cookie = cookie.split(';').next().unwrap();
        assert_eq!(
            client
                .get(format!("{url}/api/v1/snapshot"))
                .header("cookie", cookie)
                .send()
                .await
                .unwrap()
                .status(),
            200
        );
        assert_eq!(
            client
                .post(format!("{url}/access/token"))
                .header("origin", "http://other.test")
                .bearer_auth(&token)
                .json(&json!({}))
                .send()
                .await
                .unwrap()
                .status(),
            403
        );
        // The internal tailnet transport remains unauthorized while LAN works.
        assert!(!access.valid_token(&token));
        let mut old_tailnet_stream = access.clone();
        old_tailnet_stream.endpoint_enabled = Some(Arc::new(AtomicBool::new(true)));
        old_tailnet_stream.capture_endpoint();
        assert!(old_tailnet_stream.valid_token(&token));
        access.invalidate_endpoint();
        assert!(!old_tailnet_stream.valid_token(&token));
        web.configure(&access, "tailscale", true).await.unwrap_err();
        assert_eq!(
            client
                .get(format!("{url}/api/v1/snapshot"))
                .bearer_auth(&token)
                .send()
                .await
                .unwrap()
                .status(),
            200
        );
        let response = super::super::admin_router(access.clone())
            .oneshot(
                axum::http::Request::builder()
                    .method("POST")
                    .uri("/api/v1/access")
                    .header("authorization", "Bearer owner")
                    .header("content-type", "application/json")
                    .body(axum::body::Body::from(r#"{"action":"regenerate_token"}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), 200);
        assert_eq!(
            client
                .get(format!("{url}/api/v1/snapshot"))
                .header("cookie", cookie)
                .send()
                .await
                .unwrap()
                .status(),
            401
        );
        // Keep shared access enabled to emulate another listener remaining available.
        let lan_guard = web.state.lock().await.lan[0].active.clone();
        web.configure(&access, "lan", false).await.unwrap();
        assert!(!lan_guard.load(Ordering::Acquire));
        let saved: super::super::state::Credentials =
            serde_json::from_slice(&std::fs::read(dir.join("access-token.json")).unwrap()).unwrap();
        assert!(!saved.web.unwrap().lan);
        assert_eq!(saved.token, access.inner.lock().unwrap().credentials.token);
        web.shutdown().await;
        std::fs::remove_dir_all(dir).unwrap();
    }
}
