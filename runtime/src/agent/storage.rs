use super::model::*;
use crate::{pi, store::Store};
use anyhow::{Result, anyhow, ensure};
use rusqlite::params;
use std::path::Path;

pub(super) const PROFILES: &str = "agent.profiles.v1";
pub(super) const BINDINGS: &str = "agent.bindings.v1";

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
pub(super) fn set(tx: &rusqlite::Transaction<'_>, key: &str, value: &str) -> Result<()> {
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
    config: &pi::Config,
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

#[cfg(test)]
mod tests {
    use super::*;
    fn config() -> pi::Config {
        pi::Config {
            api_key: "never-persist-this-key".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 100,
            max_tokens: 8192,
            max_requests_per_turn: 10,
            max_parallel_tasks: 5,
        }
    }
    #[test]
    fn import_is_atomic_idempotent_and_snapshots_survive_config_edits() {
        let dir = std::env::temp_dir().join(format!("pot-agent-{}", crate::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let file = dir.join("pi-session.jsonl");
        std::fs::write(
            &file,
            "{\"type\":\"session\",\"version\":3,\"id\":\"native-1\"}\n",
        )
        .unwrap();
        let path = dir.join("pot.sqlite");
        let mut store = Store::open(&path).unwrap();
        store.set("pi.session", "native-1").unwrap();
        store.set("pi.paused", "original checkpoint").unwrap();
        store.set("tasks.v1", "original tasks v1").unwrap();
        store.set("tasks.v2", "original tasks v2").unwrap();
        initialize_store(&mut store, "main", &dir, &config()).unwrap();
        let original = current(&store).unwrap().unwrap();
        assert_eq!(original.epoch, 1);
        assert_eq!(original.state, BindingState::Active);
        assert!(
            !store
                .get(BINDINGS)
                .unwrap()
                .unwrap()
                .contains("never-persist-this-key")
        );
        drop(store);
        let mut store = Store::open(&path).unwrap();
        let mut updated = config();
        updated.model = "new-model".into();
        updated.max_tokens = 100;
        initialize_store(&mut store, "main", &dir, &updated).unwrap();
        assert_eq!(current(&store).unwrap().unwrap(), original);
        assert_eq!(
            store.get("pi.paused").unwrap().unwrap(),
            "original checkpoint"
        );
        assert_eq!(store.get("tasks.v1").unwrap().unwrap(), "original tasks v1");
        assert_eq!(store.get("tasks.v2").unwrap().unwrap(), "original tasks v2");
        assert_eq!(profiles(&store).unwrap()[0].revision, 1);
        assert!(initialize_store(&mut store, "other", &dir, &config()).is_err());
        drop(store);
        std::fs::remove_dir_all(dir).unwrap();
    }
    #[test]
    fn missing_or_corrupt_legacy_session_is_broken_without_replacement() {
        let dir = std::env::temp_dir().join(format!("pot-agent-{}", crate::id()));
        std::fs::create_dir_all(&dir).unwrap();
        for contents in [
            None,
            Some("not json"),
            Some("{\"type\":\"session\",\"id\":\"other\"}\n"),
            Some("{\"type\":\"session\",\"id\":\"expected\"}\n{broken"),
        ] {
            if let Some(contents) = contents {
                std::fs::write(dir.join("pi-session.jsonl"), contents).unwrap();
            }
            let mut store = Store::open(Path::new(":memory:")).unwrap();
            store.set("pi.session", "expected").unwrap();
            initialize_store(&mut store, "task-1", &dir, &config()).unwrap();
            assert_eq!(
                current(&store).unwrap().unwrap().state,
                BindingState::Broken
            );
            assert_eq!(store.get("pi.session").unwrap().unwrap(), "expected");
        }
        std::fs::remove_dir_all(dir).unwrap();
    }
    #[test]
    fn corrupt_snapshot_is_rejected_not_reimported() {
        let mut store = Store::open(Path::new(":memory:")).unwrap();
        initialize_store(&mut store, "main", Path::new("/unused"), &config()).unwrap();
        let mut all = bindings(&store).unwrap();
        all[0].spec.model = "tampered".into();
        store
            .set(BINDINGS, &serde_json::to_string(&all).unwrap())
            .unwrap();
        assert!(initialize_store(&mut store, "main", Path::new("/unused"), &config()).is_err());
    }
}

/// Only an unstarted lane may inherit the current worker default.
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
    all[0].spec = spec;
    store.set(BINDINGS, &serde_json::to_string(&all)?)
}
