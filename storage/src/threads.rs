use crate::store::Store;
use anyhow::{Result, ensure};
use pot_core::*;
use rusqlite::params;

pub const PROFILES: &str = "agent.profiles.v1";
pub const THREAD: &str = "thread";
pub const CONTEXT: &str = "thread.context";

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
    tx.commit()?;
    Ok(())
}
pub fn select_initial(store: &mut Store, owner: &str, config: ResolvedSpec) -> Result<()> {
    ensure!(current(store)?.is_none(), "Thread already configured");
    config.validate()?;
    store.set(
        THREAD,
        &serde_json::to_string(&Thread {
            owner: owner.into(),
            generation: 1,
            config,
        })?,
    )?;
    store.set("agent.setup.pending", "false")?;
    Ok(())
}
pub fn assign_new(store: &mut Store, config: ResolvedSpec) -> Result<()> {
    let mut thread = current(store)?.ok_or_else(|| anyhow::anyhow!("Thread missing"))?;
    ensure!(
        load_context(store)?.is_none(),
        "Cannot change a started task's initial configuration"
    );
    config.validate()?;
    thread.config = config;
    store.set(THREAD, &serde_json::to_string(&thread)?)
}
pub fn load_context(store: &Store) -> Result<Option<serde_json::Value>> {
    store
        .get(CONTEXT)?
        .map(|v| Ok(serde_json::from_str(&v)?))
        .transpose()
}
pub fn save_context(store: &Store, value: &serde_json::Value) -> Result<()> {
    ensure!(
        value["type"] == "context" && value["version"] == 1 && value["messages"].is_array(),
        "Invalid thread context"
    );
    store.set(CONTEXT, &serde_json::to_string(value)?)
}
