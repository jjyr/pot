//! Injected transport port. Runtime owns task/model state; server owns Control/member credentials.
use crate::*;
use futures::future::BoxFuture;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct RemoteBinding {
    pub owner: String,
    pub run_id: String,
    pub generation: u64,
    pub environment: pot_core::EnvironmentRef,
}
pub trait RemoteEnvironment: Send + Sync {
    fn waiting_reason(&self, _owner: &str) -> Option<String> {
        None
    }
    fn ready_owner(&self, _owner: String) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }
    fn validate(&self, environment: &pot_core::EnvironmentRef) -> Result<()>;
    fn validate_management(&self, _environment: &pot_core::EnvironmentRef) -> Result<()> {
        bail!("Remote Sandbox management is unavailable")
    }
    fn manage(
        &self,
        _binding: RemoteBinding,
        _call: String,
        _args: Value,
        _cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async { bail!("Remote Sandbox management is unavailable") })
    }
    fn read_file(
        &self,
        _environment: pot_core::EnvironmentRef,
        _owner: String,
        _query: pot_protocol::FileQuery,
    ) -> BoxFuture<'_, Result<pot_protocol::FileChunk>> {
        Box::pin(async { bail!("Remote file transport unavailable") })
    }
    fn inventory(&self) -> BoxFuture<'_, Result<Value>>;
    fn install_resources(
        &self,
        _binding: RemoteBinding,
        _resources: crate::control_resources::GlobalResources,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async { bail!("Control resource delivery is unavailable") })
    }
    fn open(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<Value>>;
    fn prepare(
        &self,
        binding: RemoteBinding,
        name: String,
        args: Value,
    ) -> BoxFuture<'_, Result<Value>>;
    fn execute(
        &self,
        binding: RemoteBinding,
        call: String,
        name: String,
        args: Value,
    ) -> BoxFuture<'_, Result<Value>>;
    fn renew(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<()>>;
    fn ready(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<()>>;
    fn cancel(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<()>>;
}
#[derive(Clone)]
pub(crate) struct RemoteContext {
    pub binding: RemoteBinding,
    pub context: Value,
}
pub(crate) fn port(s: &Service) -> Result<Arc<dyn RemoteEnvironment>> {
    s.environment_service
        .remote
        .read()
        .unwrap()
        .clone()
        .ok_or_else(|| anyhow!("Remote Environment transport is not available"))
}
pub(crate) fn is_remote(s: &Service) -> bool {
    s.environment_ref
        .as_ref()
        .is_some_and(|r| !r.is_local_to(&s.runtime_id))
}
pub(crate) fn waiting_reason(s: &Service) -> Option<String> {
    port(s).ok()?.waiting_reason(&s.usage_scope)
}
pub(crate) fn key(reference: &pot_core::EnvironmentRef) -> Result<String> {
    Ok(format!("remote:{}", serde_json::to_string(reference)?))
}
pub(crate) fn from_key(key: &str) -> Result<pot_core::EnvironmentRef> {
    serde_json::from_str(
        key.strip_prefix("remote:")
            .ok_or_else(|| anyhow!("Invalid remote Environment key"))?,
    )
    .map_err(Into::into)
}
pub(crate) fn binding(s: &Service, environment: pot_core::EnvironmentRef) -> Result<RemoteBinding> {
    let inner = s.conversation.inner.lock().unwrap();
    let thread = agent::current(&inner.store)?.ok_or_else(|| anyhow!("Thread missing"))?;
    let turn = inner
        .turn
        .as_ref()
        .ok_or_else(|| anyhow!("Active turn required"))?;
    let request = inner
        .store
        .message(&turn.user_message_id)?
        .and_then(|m| m.request_id)
        .ok_or_else(|| anyhow!("Turn identity missing"))?;
    // Public request IDs need not be UUIDs. Derive a stable protocol run identity.
    use sha2::{Digest, Sha256};
    let digest = Sha256::digest(serde_json::to_vec(&(
        &s.runtime_id,
        &s.usage_scope,
        thread.generation,
        request,
    ))?);
    let bytes: [u8; 16] = digest[..16].try_into().unwrap();
    Ok(RemoteBinding {
        owner: s.usage_scope.clone(),
        run_id: uuid::Uuid::from_bytes(bytes).to_string(),
        generation: thread.generation,
        environment,
    })
}
pub(crate) async fn open(s: &Service, binding: RemoteBinding) -> Result<()> {
    let resources = crate::control_resources::snapshot(s)?;
    let remote = port(s)?;
    remote.open(binding.clone()).await?;
    let context = remote.install_resources(binding.clone(), resources).await?;
    s.remote_context.lock().unwrap().insert(
        binding.environment.clone(),
        RemoteContext { binding, context },
    );
    Ok(())
}
pub(crate) async fn start(s: &Service) -> Result<()> {
    let environment = s
        .environment_ref
        .clone()
        .ok_or_else(|| anyhow!("Remote worker binding missing"))?;
    let binding = binding(s, environment)?;
    let mut observed = None;
    loop {
        if agent::cancelled(s) || s.stopping() {
            let _ = port(s)?.cancel(binding).await;
            return Ok(());
        }
        match open(s, binding.clone()).await {
            Ok(()) => {
                if observed.is_some() {
                    s.phase(AgentPhase::Ready, None);
                }
                return Ok(());
            }
            Err(error) if waiting_reason(s).is_none() => return Err(error),
            Err(_) => {}
        }
        let waiting = waiting_reason(s);
        if waiting != observed {
            s.phase(AgentPhase::Ready, waiting.clone());
            observed = waiting;
        }
        tokio::select! {
            _ = tokio::time::sleep(std::time::Duration::from_secs(1)) => {},
            _ = s.cancel_notify.notified() => {},
            _ = s.stop_requested() => {},
        }
    }
}
pub(crate) async fn ready(s: &Service) -> Result<()> {
    if let Ok(remote) = port(s) {
        loop {
            if agent::cancelled(s) || s.stopping() {
                return Ok(());
            }
            match remote.ready_owner(s.usage_scope.clone()).await {
                Ok(()) => break,
                Err(error) if remote.waiting_reason(&s.usage_scope).is_none() => return Err(error),
                Err(_) => {}
            }
            tokio::select! {
                _ = tokio::time::sleep(std::time::Duration::from_secs(1)) => {},
                _ = s.cancel_notify.notified() => return Ok(()),
                _ = s.stop_requested() => return Ok(()),
            }
        }
    }
    let contexts = s
        .remote_context
        .lock()
        .unwrap()
        .values()
        .cloned()
        .collect::<Vec<_>>();
    for context in contexts {
        loop {
            if agent::cancelled(s) || s.stopping() {
                return Ok(());
            }
            let remote = port(s)?;
            match remote.ready(context.binding.clone()).await {
                Ok(()) => break,
                Err(error) if remote.waiting_reason(&s.usage_scope).is_none() => {
                    return Err(error);
                }
                Err(_) => {}
            }
            tokio::select! {
                _ = tokio::time::sleep(std::time::Duration::from_secs(1)) => {},
                _ = s.cancel_notify.notified() => return Ok(()),
                _ = s.stop_requested() => return Ok(()),
            }
        }
    }
    Ok(())
}
pub(crate) async fn cancel(s: &Service) -> Result<()> {
    let contexts = s
        .remote_context
        .lock()
        .unwrap()
        .values()
        .cloned()
        .collect::<Vec<_>>();
    let mut result = Ok(());
    for context in contexts {
        let cancelled = port(s)?.cancel(context.binding.clone()).await;
        if let Err(error) = cancelled {
            result = Err(error);
            continue;
        }
        s.remote_context
            .lock()
            .unwrap()
            .remove(&context.binding.environment);
    }
    result
}
impl Service {
    pub fn set_remote_environment(&self, port: Arc<dyn RemoteEnvironment>) {
        *self.environment_service.remote.write().unwrap() = Some(port);
    }
}

pub(crate) struct Renewal(tokio::task::JoinHandle<()>);
impl Drop for Renewal {
    fn drop(&mut self) {
        self.0.abort();
    }
}
pub(crate) fn renewal(s: &Service) -> Renewal {
    let service = s.clone();
    Renewal(tokio::spawn(async move {
        let mut previous = None;
        let mut renewed = std::time::Instant::now();
        loop {
            tokio::time::sleep(std::time::Duration::from_secs(1)).await;
            if service.stopping() || agent::cancelled(&service) {
                return;
            }
            let contexts = service
                .remote_context
                .lock()
                .unwrap()
                .values()
                .cloned()
                .collect::<Vec<_>>();
            let waiting = waiting_reason(&service);
            if waiting != previous {
                service.phase(AgentPhase::Ready, waiting.clone());
                previous = waiting;
            }
            if renewed.elapsed() < std::time::Duration::from_secs(5) {
                continue;
            }
            renewed = std::time::Instant::now();
            for context in contexts {
                if let Ok(port) = port(&service) {
                    let _ = port.renew(context.binding).await;
                }
            }
        }
    }))
}
