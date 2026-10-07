//! Desktop tickets and control ownership, independent of command scopes.
use super::*;
struct DesktopAttempt<'a> {
    manager: &'a Manager,
    session: &'a str,
    armed: bool,
}
impl Drop for DesktopAttempt<'_> {
    fn drop(&mut self) {
        if self.armed {
            self.manager.desktop_disconnected(self.session);
        }
    }
}
impl Manager {
    pub async fn open_desktop(self: &Arc<Self>, target: &str) -> Result<crate::DesktopSession> {
        let target = self.assignment(target)?;
        let capabilities = self.provider(&target)?.desktop_capabilities();
        anyhow::ensure!(
            capabilities.viewing && capabilities.protocol == Some(crate::DesktopProtocol::Rfb38),
            "Desktop capability unavailable"
        );
        let _lease = self.acquire(&target).await?;
        let _lifecycle = self.lifecycle.lock().await;
        self.ready(&target, &|| false).await?;
        self.provider(&target)?
            .prepare_desktop(&self.context(&target))
            .await?;
        let generation = self.helper(&target, "status").await?["generation"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing VM generation"))?;
        let session = crate::DesktopSession {
            id: id(),
            sandbox_id: target,
            capabilities,
            expires_in_seconds: crate::desktop::LIFETIME.as_secs(),
        };
        let mut viewers = self.viewers.lock().unwrap();
        viewers.retain(|_, v| v.connected || v.expires > std::time::Instant::now());
        anyhow::ensure!(viewers.len() < 64, "Desktop session limit");
        viewers.insert(
            session.id.clone(),
            crate::desktop::Viewer {
                session: session.clone(),
                expires: std::time::Instant::now() + crate::desktop::LIFETIME,
                generation,
                connected: false,
                control: false,
                revision: 0,
                release_pending: false,
                revoked: false,
            },
        );
        Ok(session)
    }
    pub fn desktop_session(&self, session: &str) -> Result<crate::DesktopSession> {
        let viewers = self.viewers.lock().unwrap();
        let v = viewers
            .get(session)
            .ok_or_else(|| anyhow!("Desktop session revoked"))?;
        anyhow::ensure!(
            !v.revoked && v.expires > std::time::Instant::now(),
            "Desktop session expired"
        );
        Ok(v.session.clone())
    }
    pub async fn desktop_serialization(&self) -> tokio::sync::MutexGuard<'_, ()> {
        self.desktop_io.lock().await
    }
    pub async fn desktop_control(&self, session: &str, acquire: bool) -> Result<()> {
        let wait = {
            let _guard = self.desktop_serialization().await;
            self.desktop_session(session)?;
            let mut viewers = self.viewers.lock().unwrap();
            let v = &viewers[session];
            anyhow::ensure!(
                !acquire || (v.connected && v.session.capabilities.input),
                "Desktop input unavailable"
            );
            let target = v.session.sandbox_id.clone();
            anyhow::ensure!(
                !acquire
                    || !viewers.iter().any(|(key, v)| key != session
                        && v.session.sandbox_id == target
                        && (v.control || v.release_pending)
                        && v.connected),
                "Another viewer controls this desktop"
            );
            let v = viewers.get_mut(session).unwrap();
            if !acquire && v.control && v.connected {
                v.release_pending = true;
            }
            v.control = acquire;
            v.revision += 1;
            v.release_pending
        };
        if wait {
            tokio::time::timeout(Duration::from_secs(7), async {
                loop {
                    if !self
                        .viewers
                        .lock()
                        .unwrap()
                        .get(session)
                        .is_some_and(|v| v.release_pending)
                    {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
            })
            .await
            .map_err(|_| anyhow!("Desktop input release unconfirmed; disconnect viewer"))?;
            anyhow::ensure!(
                self.viewers
                    .lock()
                    .unwrap()
                    .get(session)
                    .is_some_and(|v| v.connected && !v.revoked),
                "Desktop disconnected before input release confirmation"
            );
        }
        Ok(())
    }
    pub fn desktop_released(&self, session: &str, revision: u64) {
        if let Some(v) = self.viewers.lock().unwrap().get_mut(session)
            && v.revision == revision
        {
            v.release_pending = false;
        }
    }
    pub fn desktop_revision(&self, session: &str) -> Result<u64> {
        self.desktop_session(session)?;
        Ok(self.viewers.lock().unwrap()[session].revision)
    }
    pub async fn desktop_validate(&self, session: &str) -> Result<()> {
        let ticket = self.desktop_session(session)?;
        let state = self.helper(&ticket.sandbox_id, "status").await?;
        let viewers = self.viewers.lock().unwrap();
        let v = viewers
            .get(session)
            .ok_or_else(|| anyhow!("Desktop revoked"))?;
        anyhow::ensure!(
            state["phase"] == "running" && state["generation"].as_u64() == Some(v.generation),
            "Desktop VM stopped or generation changed"
        );
        Ok(())
    }
    pub fn desktop_has_control(&self, session: &str) -> Result<bool> {
        self.desktop_session(session)?;
        Ok(self.viewers.lock().unwrap()[session].control)
    }
    pub fn desktop_disconnected(&self, session: &str) {
        let mut viewers = self.viewers.lock().unwrap();
        if let Some(v) = viewers.get_mut(session) {
            if v.revoked {
                viewers.remove(session);
            } else {
                v.release_pending = false;
                v.connected = false;
                v.control = false;
                v.revision += 1;
            }
        }
    }
    pub fn close_desktop(&self, session: &str) {
        let mut viewers = self.viewers.lock().unwrap();
        if let Some(v) = viewers.get_mut(session) {
            if v.connected {
                v.revoked = true;
            } else {
                viewers.remove(session);
            }
        }
    }
    pub(super) fn revoke_desktops(&self, target: &str) {
        let ids: Vec<_> = self
            .viewers
            .lock()
            .unwrap()
            .iter()
            .filter(|(_, v)| v.session.sandbox_id == target)
            .map(|(id, _)| id.clone())
            .collect();
        for id in ids {
            self.close_desktop(&id);
        }
    }
    pub async fn desktop_stream(&self, session: &str) -> Result<crate::DesktopStream> {
        let ticket = self.desktop_session(session)?;
        let _lifecycle = self.lifecycle.lock().await;
        let state = self.helper(&ticket.sandbox_id, "status").await?;
        {
            let mut viewers = self.viewers.lock().unwrap();
            let v = viewers
                .get_mut(session)
                .ok_or_else(|| anyhow!("Desktop session revoked"))?;
            anyhow::ensure!(
                !v.connected
                    && v.expires > std::time::Instant::now()
                    && state["phase"] == "running"
                    && state["generation"].as_u64() == Some(v.generation),
                "Desktop unavailable or generation changed"
            );
            v.connected = true;
        }
        let mut attempt = DesktopAttempt {
            manager: self,
            session,
            armed: true,
        };
        let result = tokio::time::timeout(
            Duration::from_secs(30),
            self.provider(&ticket.sandbox_id)?
                .open_desktop(&self.context(&ticket.sandbox_id)),
        )
        .await;
        match result {
            Ok(Ok(stream)) => {
                attempt.armed = false;
                Ok(stream)
            }
            other => {
                self.desktop_disconnected(session);
                self.close_desktop(session);
                match other {
                    Ok(Err(e)) => Err(e),
                    Err(e) => Err(e.into()),
                    _ => unreachable!(),
                }
            }
        }
    }
}

impl Manager {
    pub fn desktop_capabilities(&self, target: &str) -> Result<crate::DesktopCapabilities> {
        Ok(self
            .provider(&self.assignment(target)?)?
            .desktop_capabilities())
    }
    pub async fn desktop_browser(&self, session: &str) -> Result<()> {
        let _guard = self.desktop_serialization().await;
        let ticket = self.desktop_session(session)?;
        anyhow::ensure!(
            ticket.capabilities.browser && self.desktop_has_control(session)?,
            "Desktop control required"
        );
        tokio::time::timeout(
            Duration::from_secs(10),
            self.provider(&ticket.sandbox_id)?
                .desktop_browser(&self.context(&ticket.sandbox_id)),
        )
        .await??;
        Ok(())
    }
    pub async fn desktop_metrics(&self, session: &str) -> Result<Value> {
        let ticket = self.desktop_session(session)?;
        let metrics = self
            .provider(&ticket.sandbox_id)?
            .desktop_metrics(&self.context(&ticket.sandbox_id))
            .await?;
        let mut safe = serde_json::Map::new();
        for key in [
            "image",
            "image_bytes",
            "cpu_percent",
            "cpus",
            "memory_used",
            "memory_total",
            "memory_gib",
            "disk_used",
            "disk_total",
            "workspace_bytes",
            "uptime_seconds",
        ] {
            if let Some(v) = metrics.get(key).filter(|v| {
                v.is_number() || (key == "image" && v.as_str().is_some_and(|s| s.len() <= 256))
            }) {
                safe.insert(key.into(), v.clone());
            }
        }
        safe.insert("os".into(), json!(self.describe(&ticket.sandbox_id)?.os));
        safe.insert("sandbox_id".into(), json!(ticket.sandbox_id));
        Ok(Value::Object(safe))
    }
}
