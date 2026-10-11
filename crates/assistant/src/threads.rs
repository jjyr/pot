use anyhow::{Result, ensure};
use pot_core::*;
use pot_storage::Store;
use rusqlite::params;

pub const PROFILES: &str = "agent.profiles.v1";
pub const THREAD: &str = "thread";
pub const CONTEXT: &str = "thread.context";
const EMPTY_CONTEXT: &str = r#"{"type":"context","version":1,"messages":[]}"#;

pub fn set(db: &rusqlite::Connection, key: &str, value: &str) -> Result<()> {
    db.execute(
        "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
        params![key, value],
    )?;
    Ok(())
}
pub fn profiles(store: &Store) -> Result<Vec<Profile>> {
    store
        .get(PROFILES)?
        .map(|v| Ok(serde_json::from_str(&v)?))
        .unwrap_or_else(|| Ok(vec![]))
}
pub fn current(store: &Store) -> Result<Option<Thread>> {
    let thread: Option<Thread> = store
        .get(THREAD)?
        .map(|v| serde_json::from_str(&v))
        .transpose()?;
    if let Some(t) = &thread {
        ensure!(t.generation > 0, "Invalid execution generation");
        t.config.validate()?;
    }
    Ok(thread)
}
pub fn initialize_store(store: &mut Store, owner: &str, config: &impl ProfileConfig) -> Result<()> {
    if let Some(thread) = current(store)? {
        ensure!(thread.owner == owner, "Thread owner mismatch");
        return Ok(());
    }
    let profile = Profile::api(config);
    let tx = store.0.transaction()?;
    set(
        &tx,
        PROFILES,
        &serde_json::to_string(&vec![profile.clone()])?,
    )?;
    set(
        &tx,
        THREAD,
        &serde_json::to_string(&Thread {
            owner: owner.into(),
            generation: 1,
            config: profile.resolve()?,
        })?,
    )?;
    set(&tx, CONTEXT, EMPTY_CONTEXT)?;
    tx.commit()?;
    Ok(())
}
pub fn select_initial(store: &mut Store, owner: &str, config: ResolvedSpec) -> Result<()> {
    ensure!(current(store)?.is_none(), "Thread already configured");
    config.validate()?;
    let tx = store.0.savepoint()?;
    set(
        &tx,
        THREAD,
        &serde_json::to_string(&Thread {
            owner: owner.into(),
            generation: 1,
            config,
        })?,
    )?;
    set(&tx, CONTEXT, EMPTY_CONTEXT)?;
    set(&tx, "agent.setup.pending", "false")?;
    tx.commit()?;
    Ok(())
}
pub fn assign_new(store: &mut Store, config: ResolvedSpec) -> Result<()> {
    let mut thread = current(store)?.ok_or_else(|| anyhow::anyhow!("Thread missing"))?;
    let context = load_context(store)?.ok_or_else(|| anyhow::anyhow!("Thread context missing"))?;
    ensure!(
        context["messages"].as_array().is_some_and(|m| m.is_empty())
            && context["last_request"].as_str().is_none_or(str::is_empty),
        "Cannot change a started task's initial configuration"
    );
    config.validate()?;
    thread.config = config;
    store.set(THREAD, &serde_json::to_string(&thread)?)
}
pub fn load_context(store: &Store) -> Result<Option<serde_json::Value>> {
    let value = store
        .get(CONTEXT)?
        .map(|v| serde_json::from_str::<serde_json::Value>(&v))
        .transpose()?;
    if let Some(context) = &value {
        validate_context(context)?;
    } else {
        ensure!(
            current(store)?.is_none(),
            "Thread context missing; cannot resume with empty history"
        );
    }
    Ok(value)
}
fn validate_context(value: &serde_json::Value) -> Result<()> {
    ensure!(
        value["type"] == "context" && value["version"] == 1 && value["messages"].is_array(),
        "Invalid thread context"
    );
    Ok(())
}
pub fn save_context(store: &Store, value: &serde_json::Value) -> Result<()> {
    validate_context(value)?;
    store.set(CONTEXT, &serde_json::to_string(value)?)
}
