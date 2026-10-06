use crate::store::Store;
use anyhow::{Result, anyhow, ensure};
use pot_core::*;
use rusqlite::params;
use std::path::Path;

pub const PROFILES: &str = "agent.profiles.v1";
pub const BINDINGS: &str = "agent.bindings.v1";

pub fn profiles(store: &Store) -> Result<Vec<Profile>> {
    store
        .get(PROFILES)?
        .map(|v| Ok(serde_json::from_str(&v)?))
        .unwrap_or_else(|| Ok(vec![]))
}
pub fn bindings(store: &Store) -> Result<Vec<Binding>> {
    let bindings: Vec<Binding> = store
        .get(BINDINGS)?
        .map(|v| serde_json::from_str(&v))
        .transpose()?
        .unwrap_or_default();
    let mut epochs = std::collections::BTreeSet::new();
    let mut owner: Option<&str> = None;
    for b in &bindings {
        ensure!(
            b.schema_version == SCHEMA_VERSION,
            "Unsupported binding schema"
        );
        ensure!(
            b.epoch > 0 && epochs.insert(b.epoch),
            "Duplicate binding epoch"
        );
        ensure!(
            owner.is_none_or(|owner| owner == b.pot_owner),
            "Mixed binding owners"
        );
        owner = Some(&b.pot_owner);
        b.spec.validate()?;
        ensure!(
            matches!(
                (&b.spec.engine, &b.native_ref),
                (Engine::Pi, NativeRef::Pi { .. })
                    | (Engine::Codex, NativeRef::Codex { .. })
                    | (Engine::Claude, NativeRef::Claude { .. })
                    | (Engine::Agy, NativeRef::Agy { .. })
                    | (Engine::Grok, NativeRef::Grok { .. })
            ),
            "Engine and native session identity mismatch"
        );
    }
    ensure!(
        bindings
            .iter()
            .filter(|b| b.state != BindingState::Archived)
            .count()
            <= 1,
        "Multiple current bindings"
    );
    ensure!(
        bindings.is_empty() || bindings.iter().any(|b| b.state != BindingState::Archived),
        "No current binding"
    );
    Ok(bindings)
}
pub fn current(store: &Store) -> Result<Option<Binding>> {
    Ok(bindings(store)?
        .into_iter()
        .find(|b| b.state != BindingState::Archived))
}
pub fn set(tx: &rusqlite::Transaction<'_>, key: &str, value: &str) -> Result<()> {
    tx.execute(
        "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
        params![key, value],
    )?;
    Ok(())
}
pub fn initialize_store(
    store: &mut Store,
    owner: &str,
    data: &Path,
    config: &impl LegacyProfileConfig,
) -> Result<()> {
    if let Some(binding) = current(store)? {
        ensure!(binding.pot_owner == owner, "Binding owner mismatch");
        return Ok(());
    }
    let profile = Profile::legacy(config);
    let spec = profile.resolve()?;
    let legacy = store.get("pi.session")?;
    let file = data.join("pi-session.jsonl");
    let failure = legacy
        .as_ref()
        .and_then(|id| validate_pi_session(&file, id).err())
        .map(|e| e.to_string());
    let binding = Binding {
        pot_owner: owner.into(),
        epoch: 1,
        spec,
        native_ref: NativeRef::Pi {
            file,
            session_id: legacy,
        },
        schema_version: SCHEMA_VERSION,
        state: if failure.is_some() {
            BindingState::Broken
        } else {
            BindingState::Active
        },
        model_switches: vec![],
        handoff_ref: None,
        failure,
    };
    let tx = store.0.transaction()?;
    set(&tx, PROFILES, &serde_json::to_string(&vec![profile])?)?;
    set(&tx, BINDINGS, &serde_json::to_string(&vec![binding])?)?;
    // Deliberately preserve pi.session, pi.paused, pi.usage and task records.
    tx.commit()?;
    Ok(())
}

pub fn validate_pi_session(file: &Path, expected: &str) -> Result<()> {
    use std::io::{BufRead, BufReader};
    let reader = BufReader::new(
        std::fs::File::open(file).map_err(|_| anyhow!("Pi 会话文件缺失，拒绝静默新建会话"))?,
    );
    let mut lines = reader.lines();
    let header: serde_json::Value = serde_json::from_str(
        &lines
            .next()
            .transpose()?
            .ok_or_else(|| anyhow!("Pi session header missing"))?,
    )?;
    ensure!(
        header["type"] == "session" && header["id"].as_str() == Some(expected),
        "Pi session changed unexpectedly"
    );
    ensure!(
        header["version"] == 3,
        "Incompatible Pi native session format; restore a compatible command or migrate explicitly"
    );
    for line in lines {
        let line = line?;
        if !line.trim().is_empty() {
            let value: serde_json::Value =
                serde_json::from_str(&line).map_err(|_| anyhow!("Pi session is corrupted"))?;
            ensure!(value.is_object(), "Pi session is corrupted");
        }
    }
    Ok(())
}
pub fn save_pi_session(store: &mut Store, session_id: &str) -> Result<()> {
    ensure!(!session_id.is_empty(), "Pi session ID missing");
    let mut all = bindings(store)?;
    let binding = all
        .iter_mut()
        .find(|b| b.state == BindingState::Active)
        .ok_or_else(|| anyhow!("No active binding"))?;
    match &mut binding.native_ref {
        NativeRef::Pi {
            file,
            session_id: previous,
        } => {
            ensure!(
                previous.as_deref().is_none_or(|old| old == session_id),
                "Pi session changed unexpectedly"
            );
            validate_pi_session(file, session_id)?;
            *previous = Some(session_id.into());
        }
        _ => anyhow::bail!("Expected Pi binding"),
    }
    let tx = store.0.transaction()?;
    set(&tx, BINDINGS, &serde_json::to_string(&all)?)?;
    set(&tx, "pi.session", session_id)?;
    tx.commit()?;
    Ok(())
}
pub fn mark_broken(store: &mut Store, reason: &str) -> Result<()> {
    let mut all = bindings(store)?;
    if let Some(binding) = all.iter_mut().find(|b| b.state != BindingState::Archived) {
        binding.state = BindingState::Broken;
        binding.failure = Some(reason.into());
    }
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}

/// Only an unstarted lane may inherit the current worker default.
/// Select an initial immutable binding without requiring a legacy Pi configuration.
pub fn select_initial(
    store: &mut Store,
    owner: &str,
    data: &Path,
    spec: ResolvedSpec,
) -> Result<()> {
    ensure!(current(store)?.is_none(), "Initial binding already exists");
    spec.validate()?;
    let native_ref = match spec.engine {
        Engine::Codex => NativeRef::Codex { thread_id: None },
        Engine::Claude => NativeRef::Claude {
            session_id: crate::id(),
            started: false,
        },
        Engine::Grok => NativeRef::Grok { session_id: None },
        Engine::Agy => NativeRef::Agy {
            conversation_id: None,
        },
        Engine::Pi => NativeRef::Pi {
            file: data.join("pi-session.jsonl"),
            session_id: None,
        },
    };
    let binding = Binding {
        pot_owner: owner.into(),
        epoch: 1,
        spec,
        native_ref,
        schema_version: SCHEMA_VERSION,
        state: BindingState::Active,
        model_switches: vec![],
        handoff_ref: None,
        failure: None,
    };
    store.set(BINDINGS, &serde_json::to_string(&vec![binding])?)?;
    store.set("agent.setup.pending", "false")?;
    Ok(())
}

pub fn assign_new(store: &mut Store, spec: ResolvedSpec) -> Result<()> {
    let mut all = bindings(store)?;
    ensure!(
        all.len() == 1
            && all[0].epoch == 1
            && matches!(
                all[0].native_ref,
                NativeRef::Pi {
                    session_id: None,
                    ..
                }
            ),
        "Cannot change an existing binding snapshot"
    );
    if spec.engine == Engine::Codex {
        all[0].native_ref = NativeRef::Codex { thread_id: None };
    } else if spec.engine == Engine::Claude {
        all[0].native_ref = NativeRef::Claude {
            session_id: crate::id(),
            started: false,
        };
    }
    if spec.engine == Engine::Agy {
        all[0].native_ref = NativeRef::Agy {
            conversation_id: None,
        };
    }
    all[0].spec = spec;
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}

pub fn save_codex_thread(store: &mut Store, thread: &str) -> Result<()> {
    ensure!(
        uuid::Uuid::parse_str(thread).is_ok(),
        "Invalid Codex thread identity"
    );
    let mut all = bindings(store)?;
    let binding = all
        .iter_mut()
        .find(|b| b.state == BindingState::Active)
        .ok_or_else(|| anyhow!("No active binding"))?;
    match &mut binding.native_ref {
        NativeRef::Codex { thread_id } => {
            ensure!(
                thread_id.as_deref().is_none_or(|old| old == thread),
                "Codex native thread changed unexpectedly"
            );
            *thread_id = Some(thread.into());
        }
        _ => anyhow::bail!("Expected a Codex binding"),
    }
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}

pub fn save_claude_session(store: &mut Store, session: &str) -> Result<()> {
    ensure!(
        uuid::Uuid::parse_str(session).is_ok(),
        "Invalid Claude session identity"
    );
    let mut all = bindings(store)?;
    let binding = all
        .iter_mut()
        .find(|b| b.state == BindingState::Active)
        .ok_or_else(|| anyhow!("No active binding"))?;
    match &mut binding.native_ref {
        NativeRef::Claude {
            session_id,
            started,
        } => {
            ensure!(session_id == session, "Claude changed native identity");
            *started = true;
        }
        _ => anyhow::bail!("Expected Claude binding"),
    }
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}

pub fn save_agy_conversation(store: &mut Store, conversation: &str) -> Result<()> {
    ensure!(
        uuid::Uuid::parse_str(conversation).is_ok(),
        "Invalid AGY conversation identity"
    );
    let mut all = bindings(store)?;
    let binding = all
        .iter_mut()
        .find(|b| b.state == BindingState::Active)
        .ok_or_else(|| anyhow!("Active binding missing"))?;
    let NativeRef::Agy { conversation_id } = &mut binding.native_ref else {
        anyhow::bail!("Expected AGY binding");
    };
    ensure!(
        conversation_id
            .as_deref()
            .is_none_or(|old| old == conversation),
        "AGY changed native identity"
    );
    *conversation_id = Some(conversation.into());
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}

pub fn save_grok_session(store: &mut Store, session: &str) -> Result<()> {
    ensure!(
        uuid::Uuid::parse_str(session).is_ok(),
        "Invalid Grok session identity"
    );
    let mut all = bindings(store)?;
    let binding = all
        .iter_mut()
        .find(|b| b.state == BindingState::Active)
        .ok_or_else(|| anyhow!("Active binding missing"))?;
    let NativeRef::Grok { session_id } = &mut binding.native_ref else {
        anyhow::bail!("Expected Grok binding");
    };
    ensure!(
        session_id.as_deref().is_none_or(|old| old == session),
        "Grok changed native identity"
    );
    *session_id = Some(session.into());
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}
