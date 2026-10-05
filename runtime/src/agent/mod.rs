pub(super) mod agy;
pub(super) mod claude;
pub(super) mod codex;
pub(super) mod driver;
pub mod migration;
pub mod routing;
pub(super) mod transcript;
// Runtime owns profile snapshots and bindings; engines own native context.
mod model;
mod storage;
use crate::*;
pub use model::*;
pub use storage::*;

/// Import once. Existing snapshots always win over a changed pi.json.
pub fn initialize(s: &Service) -> Result<()> {
    let Some(config) = &s.pi else { return Ok(()) };
    let mut inner = s.inner.lock().unwrap();
    initialize_store(&mut inner.store, &s.usage_scope, &s.data, config)?;
    Ok(())
}

pub fn configured(s: &Service) -> bool {
    s.pi.is_some()
        || current(&s.inner.lock().unwrap().store)
            .ok()
            .flatten()
            .is_some()
}

pub fn binding(s: &Service) -> Result<Binding> {
    initialize(s)?;
    current(&s.inner.lock().unwrap().store)?.ok_or_else(|| anyhow!("Agent binding missing"))
}

pub fn pi_config(s: &Service) -> Result<pi::Config> {
    let binding = binding(s)?;
    anyhow::ensure!(
        binding.state == BindingState::Active,
        "Agent binding is broken: {}",
        binding.failure.as_deref().unwrap_or("needs rebind")
    );
    anyhow::ensure!(
        binding.spec.engine == Engine::Pi,
        "Agent engine needs rebind"
    );
    let mut config = s.pi.clone().unwrap_or_default();
    config.model = binding.spec.model;
    config.max_tokens = binding.spec.params.max_output_tokens;
    Ok(config)
}

pub fn native_session(s: &Service) -> Result<(PathBuf, Option<String>)> {
    let binding = binding(s)?;
    anyhow::ensure!(
        binding.state == BindingState::Active,
        "Agent binding is broken: {}",
        binding.failure.as_deref().unwrap_or("needs rebind")
    );
    match binding.native_ref {
        NativeRef::Pi { file, session_id } => {
            if let Some(expected) = &session_id {
                if let Err(e) = validate_pi_session(&file, expected) {
                    mark_broken(&mut s.inner.lock().unwrap().store, &e.to_string())?;
                    return Err(e);
                }
            }
            Ok((file, session_id))
        }
        _ => bail!("Expected a Pi native session"),
    }
}

pub fn bind_pi_session(s: &Service, session_id: &str) -> Result<()> {
    save_pi_session(&mut s.inner.lock().unwrap().store, session_id)
}

pub fn record_turn(s: &Service, turn: &str) -> Result<()> {
    let binding = binding(s)?;
    s.inner.lock().unwrap().store.set(
        &format!("agent.turn:{turn}"),
        &json!({"epoch":binding.epoch,"spec_hash":binding.spec.spec_hash,"engine":binding.spec.engine,"provider":binding.spec.provider,"model":binding.spec.model}).to_string(),
    )
}

#[derive(Deserialize)]
pub struct InspectOwner {
    #[serde(default = "inspect_main")]
    owner: String,
}
fn inspect_main() -> String {
    "main".into()
}
pub async fn inspect(
    State(root): State<Service>,
    h: HeaderMap,
    Query(query): Query<InspectOwner>,
) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    initialize(&root).map_err(internal)?;
    let profiles = profiles(&root.inner.lock().unwrap().store).map_err(internal)?;
    let s = tasks::owner(&root, &query.owner)?;
    initialize(&s).map_err(internal)?;
    let inner = s.inner.lock().unwrap();
    let pending = inner
        .store
        .get("agent.migration.pending")
        .map_err(internal)?
        .filter(|v| !v.is_empty())
        .map(|v| serde_json::from_str::<Value>(&v))
        .transpose()
        .map_err(|e| internal(e.into()))?;
    Ok(Json(
        json!({"profiles":profiles,"bindings":bindings(&inner.store).map_err(internal)?,"pending_migration":pending,"needs_rebind":inner.store.get("agent.needs_rebind").map_err(internal)?.filter(|s|!s.is_empty())}),
    ))
}

pub(super) fn cancelled(s: &Service) -> bool {
    s.inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .is_some_and(|t| t.cancelling)
}

pub async fn run(s: &Service, mut req: SendMessage) -> Result<()> {
    anyhow::ensure!(
        s.inner.lock().unwrap().engine.phase != EnginePhase::Failed,
        "Agent stop was not confirmed; restart the environment before another turn"
    );
    migration::apply_pending(s, true)?;
    let spec = binding(s)?.spec;
    {
        let mut inner = s.inner.lock().unwrap();
        inner.engine.engine = format!("{:?} · {}", spec.engine, spec.model);
        inner.engine.version = None;
    }
    let mut user = s
        .inner
        .lock()
        .unwrap()
        .store
        .duplicate(&req.request_id)?
        .ok_or_else(|| anyhow!("Missing accepted message"))?;
    let resuming = usage::paused(s)?.is_some_and(|p| p.request_id == req.request_id);
    if resuming {
        req.text = "Pot allowance is available again. Continue the unfinished user request from the saved session. Preserve completed tool results; do not repeat completed external actions.".into();
    }
    req.text = migration::bootstrap(s, &req)?;
    s.inner.lock().unwrap().store.set("pi.paused", "")?;
    s.usage.lock().unwrap().clear_wait(&s.usage_scope)?;
    s.usage
        .lock()
        .unwrap()
        .interrupt_compactions(&s.usage_scope, "interrupted", now())?;
    let mut transcript = transcript::Transcript::default();
    let result = driver::execute(s, &req, &mut transcript, resuming).await;
    let interrupted_state = if cancelled(s) {
        "cancelled"
    } else {
        "interrupted"
    };
    s.usage
        .lock()
        .unwrap()
        .interrupt_compactions(&s.usage_scope, interrupted_state, now())?;
    let waiting = s.usage.lock().unwrap().waiting(&s.usage_scope)?.is_some();
    let uncertain = result.as_ref().err().is_some_and(|e| {
        e.to_string().contains("未确认停止") || e.to_string().contains("stop unconfirmed")
    });
    if waiting && spec.capabilities.safe_checkpoint_resume && !cancelled(s) && !uncertain {
        transcript.finish(s, MessageStatus::Failed, "\n等待 Pot 额度恢复")?;
        if let Some(m) = transcript.assistant.as_mut() {
            m.text = "等待 Pot Token 额度恢复。可在此会话的用量详情中继续或取消。".into();
            s.save(m.clone())?;
        }
        user.status = MessageStatus::Accepted;
        s.save(user)?;
        s.inner
            .lock()
            .unwrap()
            .store
            .set("pi.paused", &serde_json::to_string(&req)?)?;
        s.phase(EnginePhase::Ready, Some("等待 Pot 额度恢复".into()));
        return Ok(());
    }
    s.usage.lock().unwrap().clear_wait(&s.usage_scope)?;
    let status = if cancelled(s) && result.is_ok() {
        MessageStatus::Cancelled
    } else if result.is_err() {
        MessageStatus::Failed
    } else {
        MessageStatus::Complete
    };
    let detail = result
        .as_ref()
        .err()
        .map(|e| format!("\n{e}"))
        .unwrap_or_default();
    transcript.finish(s, status, &detail)?;
    user.status = status;
    if req.request_id.starts_with(orchestration::RESULT_PREFIX) {
        user.text = if status == MessageStatus::Complete {
            "后台任务结果已整理。"
        } else {
            "后台任务汇总未完成，原始结果保留在任务报告中。"
        }
        .into();
    }
    s.save(user)?;
    let uncertain = result.as_ref().err().is_some_and(|e| {
        e.to_string().contains("未确认停止") || e.to_string().contains("stop unconfirmed")
    });
    if let Err(e) = result {
        s.save(Message {
            id: id(),
            conversation_id: "main".into(),
            role: Role::System,
            text: format!("任务未完成：{e}"),
            status,
            created_at_ms: now(),
            request_id: None,
        })?;
    }
    // A failed request is recoverable; keep the composer usable for a correction.
    s.phase(
        if uncertain {
            EnginePhase::Failed
        } else {
            EnginePhase::Ready
        },
        if uncertain {
            Some("Agent 未确认停止，请 Quit 后重开环境".into())
        } else {
            None
        },
    );
    Ok(())
}
