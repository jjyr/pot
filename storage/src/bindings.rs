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
                (Engine::Pot, NativeRef::Pot { .. })
                    | (Engine::Codex, NativeRef::Codex { .. })
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
    config: &impl ProfileConfig,
) -> Result<()> {
    if let Some(binding) = current(store)? {
        ensure!(binding.pot_owner == owner, "Binding owner mismatch");
        return Ok(());
    }
    let profile = Profile::api(config);
    let spec = profile.resolve()?;
    let file = data.join("agent-context.json");
    let failure = None;
    let binding = Binding {
        pot_owner: owner.into(),
        epoch: 1,
        spec,
        native_ref: NativeRef::Pot {
            file,
            session_id: None,
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
    tx.commit()?;
    Ok(())
}

pub fn validate_agent_session(file: &Path, expected: &str) -> Result<()> {
    let value: serde_json::Value = serde_json::from_slice(&std::fs::read(file)?)?;
    ensure!(
        value["type"] == "session"
            && value["id"].as_str() == Some(expected)
            && value["version"] == 1
            && value["messages"].is_array(),
        "Invalid Pot execution context"
    );
    Ok(())
}
pub fn load_context(store: &Store) -> Result<Option<serde_json::Value>> {
    let binding = current(store)?.ok_or_else(|| anyhow!("Binding missing"))?;
    let NativeRef::Pot { file, session_id } = binding.native_ref else {
        anyhow::bail!("Expected Pot binding");
    };
    if !file.exists() {
        ensure!(session_id.is_none(), "Execution context missing");
        return Ok(None);
    }
    if let Some(expected) = session_id {
        validate_agent_session(&file, &expected)?;
    }
    Ok(Some(serde_json::from_slice(&std::fs::read(file)?)?))
}
pub fn save_context(store: &Store, value: &serde_json::Value) -> Result<()> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let binding = current(store)?.ok_or_else(|| anyhow!("Binding missing"))?;
    let NativeRef::Pot { file, session_id } = binding.native_ref else {
        anyhow::bail!("Expected Pot binding");
    };
    ensure!(
        value["type"] == "session"
            && value["version"] == 1
            && value["messages"].is_array()
            && value["id"].is_string(),
        "Invalid context"
    );
    ensure!(
        session_id
            .as_deref()
            .is_none_or(|id| value["id"].as_str() == Some(id)),
        "Context identity changed"
    );
    std::fs::create_dir_all(file.parent().unwrap())?;
    let temp = file.with_extension("tmp");
    let mut output = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    output.write_all(&serde_json::to_vec(value)?)?;
    output.sync_all()?;
    std::fs::rename(&temp, &file)?;
    std::fs::File::open(file.parent().unwrap())?.sync_all()?;
    Ok(())
}
pub fn save_agent_session(store: &mut Store, session_id: &str) -> Result<()> {
    ensure!(!session_id.is_empty(), "Pot session ID missing");
    let mut all = bindings(store)?;
    let binding = all
        .iter_mut()
        .find(|b| b.state == BindingState::Active)
        .ok_or_else(|| anyhow!("No active binding"))?;
    match &mut binding.native_ref {
        NativeRef::Pot {
            file,
            session_id: previous,
        } => {
            ensure!(
                previous.as_deref().is_none_or(|old| old == session_id),
                "Pot session changed unexpectedly"
            );
            validate_agent_session(file, session_id)?;
            *previous = Some(session_id.into());
        }
        _ => anyhow::bail!("Expected Pot binding"),
    }
    let tx = store.0.transaction()?;
    set(&tx, BINDINGS, &serde_json::to_string(&all)?)?;
    set(&tx, "execution.session", session_id)?;
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
/// Select an initial immutable binding without requiring a initial API configuration.
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
        Engine::Grok => NativeRef::Grok { session_id: None },
        Engine::Pot => NativeRef::Pot {
            file: data.join("agent-context.json"),
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
                NativeRef::Pot {
                    session_id: None,
                    ..
                }
            ),
        "Cannot change an existing binding snapshot"
    );
    if spec.engine == Engine::Codex {
        all[0].native_ref = NativeRef::Codex { thread_id: None };
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
