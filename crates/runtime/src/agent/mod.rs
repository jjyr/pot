pub mod preferences;
pub mod routing;
pub(super) mod runner;
pub mod settings;
pub(super) mod transcript;
// Runtime owns threads and turn state; pot-agent executes their contexts.
mod storage;
use crate::*;
pub use pot_core::*;
pub use storage::*;

/// Initialize an explicitly supplied API profile; persisted snapshots stay immutable.
pub fn initialize(s: &Service) -> Result<()> {
    let Some(config) = s.execution.as_ref().filter(|c| !c.model.is_empty()) else {
        return Ok(());
    };
    let mut inner = s.inner.lock().unwrap();
    initialize_store(&mut inner.store, &s.usage_scope, config)?;
    Ok(())
}

pub fn configured(s: &Service) -> bool {
    s.execution.as_ref().is_some_and(|c| !c.model.is_empty())
        || current(&s.inner.lock().unwrap().store)
            .ok()
            .flatten()
            .is_some()
}

pub fn thread(s: &Service) -> Result<Thread> {
    initialize(s)?;
    current(&s.inner.lock().unwrap().store)?.ok_or_else(|| anyhow!("Thread configuration missing"))
}
pub fn execution_config_for(s: &Service) -> Result<execution::Config> {
    let thread = thread(s)?;
    let data = if s.is_worker {
        s.data
            .parent()
            .and_then(|p| p.parent())
            .ok_or_else(|| anyhow!("Invalid worker data directory"))?
    } else {
        &s.data
    };
    let mut config = execution::execution_config(s.execution.clone().unwrap_or_default(), data)?;
    config.model = thread.config.model;
    config.max_tokens = thread.config.params.max_output_tokens;
    Ok(config)
}

pub fn record_turn(s: &Service, turn: &str) -> Result<()> {
    let thread = thread(s)?;
    s.inner.lock().unwrap().store.set(
        &format!("agent.turn:{turn}"),
        &json!({"generation":thread.generation,"spec_hash":thread.config.spec_hash,"provider":thread.config.provider,"model":thread.config.model}).to_string(),
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
    let pending = settings::pending(&inner.store).map_err(internal)?;
    Ok(Json(
        json!({"profiles":profiles,"thread":current(&inner.store).map_err(internal)?,"pending_config":pending}),
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
        s.inner.lock().unwrap().agent.phase != AgentPhase::Failed,
        "Agent stop was not confirmed; restart the environment before another turn"
    );
    settings::apply_pending(s, true)?;
    let spec = thread(s)?.config;
    {
        let mut inner = s.inner.lock().unwrap();
        inner.agent.model = spec.model.clone();
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
    s.inner.lock().unwrap().store.set("execution.paused", "")?;
    s.usage.lock().unwrap().clear_wait(&s.usage_scope)?;
    s.usage
        .lock()
        .unwrap()
        .interrupt_compactions(&s.usage_scope, "interrupted", now())?;
    let mut transcript = transcript::Transcript::default();
    let result = runner::execute(s, &req, &mut transcript, resuming).await;
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
    if waiting && spec.supports_checkpoint() && !cancelled(s) && !uncertain {
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
            .set("execution.paused", &serde_json::to_string(&req)?)?;
        s.phase(AgentPhase::Ready, Some("等待 Pot 额度恢复".into()));
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
            AgentPhase::Failed
        } else {
            AgentPhase::Ready
        },
        if uncertain {
            Some("Agent 未确认停止，请 Quit 后重开环境".into())
        } else {
            None
        },
    );
    Ok(())
}

#[cfg(test)]
mod integration_tests;
