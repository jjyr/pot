//! Bounded upgrades run before the corresponding service opens current state.
use crate::*;
use std::path::Path;

pub fn rename_path(data: &Path, old: &str, new: &str) -> Result<()> {
    let source = data.join(old);
    if source.exists() {
        let destination = data.join(new);
        anyhow::ensure!(
            !destination.exists(),
            "Conflicting upgrade paths: {old} and {new}"
        );
        std::fs::rename(source, destination)?;
        std::fs::File::open(data)?.sync_all()?;
    }
    Ok(())
}

pub(crate) fn runtime_paths(data: &Path) -> Result<()> {
    rename_path(data, "peer-workspaces", "execution-workspaces")?;
    let preset = data.join("runtime-composition.json");
    if preset.exists() {
        std::fs::remove_file(preset)?;
    }
    Ok(())
}

pub(crate) fn open_store(path: &Path, receipts: &Path, owner: &str) -> Result<Store> {
    let store = Store::open(path)?;
    owner_store(&store, receipts, owner)?;
    Ok(store)
}

pub(crate) fn owner_store(store: &Store, receipts: &Path, owner: &str) -> Result<()> {
    if store.get("runtime.upgrade.v1")?.is_some() {
        return Ok(());
    }
    // Publish receipts before deleting old records; interruption can safely resume.
    let entries = {
        let mut statement = store
            .0
            .prepare("SELECT key,value FROM metadata WHERE key LIKE 'sandbox.operation:%'")?;
        statement
            .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
            .collect::<std::result::Result<Vec<_>, _>>()?
    };
    for (key, text) in entries {
        let (turn, call) = key
            .strip_prefix("sandbox.operation:")
            .unwrap()
            .split_once(':')
            .ok_or_else(|| anyhow!("Invalid stored Sandbox operation identity"))?;
        use sha2::{Digest, Sha256};
        let digest: String = Sha256::digest(serde_json::to_vec(&(owner, turn, call))?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        let directory = receipts.join("sandbox-receipts");
        std::fs::create_dir_all(&directory)?;
        let path = directory.join(format!("{digest}.json"));
        let value: Value = serde_json::from_str(&text)?;
        if !path.exists() {
            execution::private_json(&path, &value)?;
        }
    }
    store.0.execute_batch("SAVEPOINT runtime_upgrade")?;
    let result = (|| -> Result<()> {
        if let Some(worker) = store.get("agent.default.worker")? {
            let mut policy: agent::routing::Policy = store
                .get("agent.routing.v1")?
                .map(|s| serde_json::from_str(&s))
                .transpose()?
                .unwrap_or_default();
            if !policy.follow_chat && policy.worker_profile.is_none() {
                policy.worker_profile = Some(worker);
                store.set("agent.routing.v1", &serde_json::to_string(&policy)?)?;
            }
        }
        store.0.execute("DELETE FROM metadata WHERE key='agent.default.worker' OR key LIKE 'sandbox.operation:%'", [])?;
        store.set("runtime.upgrade.v1", "complete")?;
        Ok(())
    })();
    if let Err(e) = result {
        store
            .0
            .execute_batch("ROLLBACK TO runtime_upgrade; RELEASE runtime_upgrade")?;
        return Err(e);
    }
    store.0.execute_batch("RELEASE runtime_upgrade")?;
    Ok(())
}

pub(crate) fn task_book(store: &Store, runtime: &str, assistant: &str) -> Result<()> {
    let current = store.get("tasks.v2")?;
    let old = store.get("tasks.v1")?;
    let Some(mut book) = current
        .as_ref()
        .map(|s| serde_json::from_str::<Value>(s))
        .transpose()?
        .or_else(|| {
            old.as_ref()
                .map(|_| json!({"tasks":[],"schedules":crate::schedules::Book::default()}))
        })
    else {
        return Ok(());
    };
    let mut changed = current.is_none();
    if changed {
        book["tasks"] = serde_json::from_str(old.as_ref().unwrap())?;
    }
    let records = book["tasks"]
        .as_array_mut()
        .ok_or_else(|| anyhow!("Invalid task upgrade records"))?;
    for record in records {
        if record["info"]["assistant_id"]
            .as_str()
            .is_none_or(|id| id.is_empty())
        {
            record["info"]["assistant_id"] = json!(assistant);
            changed = true;
        }
        if record["info"]["environment_ref"].is_null() {
            let environment = if !record["assignment"]["environment"].is_null() {
                record["assignment"]["environment"].clone()
            } else {
                json!(pot_core::EnvironmentRef::sandbox(
                    runtime,
                    record["info"]["sandbox_id"].as_str().unwrap_or("")
                ))
            };
            record["info"]["environment_ref"] = environment;
            changed = true;
        }
        if record["assignment"].is_null() {
            let environment = serde_json::from_value(record["info"]["environment_ref"].clone())?;
            record["assignment"] = json!(pot_core::Assignment::on_environment(
                runtime,
                record["info"]["id"]
                    .as_str()
                    .ok_or_else(|| anyhow!("Missing task upgrade identity"))?,
                environment
            ));
            changed = true;
        }
    }
    if let Some(plans) = book["schedules"]["plans"].as_object_mut() {
        for plan in plans.values_mut() {
            if plan["info"]["spec"]["environment_ref"].is_null() {
                let sandbox = plan["info"]["spec"]["sandbox_id"].as_str().unwrap_or("");
                anyhow::ensure!(
                    !sandbox.is_empty(),
                    "Persisted schedule Sandbox assignment missing"
                );
                plan["info"]["spec"]["environment_ref"] =
                    json!(pot_core::EnvironmentRef::sandbox(runtime, sandbox));
                plan["info"]["spec"]["sandbox_id"] = json!("");
                changed = true;
            }
        }
    }
    if changed {
        for record in book["tasks"].as_array().unwrap() {
            let info: TaskInfo = serde_json::from_value(record["info"].clone())?;
            anyhow::ensure!(
                info.assistant_id == assistant,
                "Task assistant ownership mismatch"
            );
            let environment = info
                .environment_ref
                .ok_or_else(|| anyhow!("Task Environment missing"))?;
            environment.validate()?;
            let assignment: pot_core::Assignment =
                serde_json::from_value(record["assignment"].clone())?;
            assignment.validate_binding(runtime, &info.id, &environment)?;
        }
        store.set("tasks.v2", &book.to_string())?;
    }
    // Current state is durable before the old source is removed.
    if old.is_some() {
        store
            .0
            .execute("DELETE FROM metadata WHERE key='tasks.v1'", [])?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn schedule_upgrade_fixes_original_sandbox_and_is_idempotent() {
        let data = std::env::temp_dir().join(format!("pot-schedule-upgrade-{}", id()));
        std::fs::create_dir_all(&data).unwrap();
        let store = Store::open(&data.join("pot-v2.sqlite")).unwrap();
        store.set("tasks.v2", &json!({"tasks":[], "schedules":{"plans":{"schedule":{"info":{"spec":{"sandbox_id":"original"}}}}}}).to_string()).unwrap();
        task_book(&store, "own-runtime", "main").unwrap();
        let saved = store.get("tasks.v2").unwrap().unwrap();
        let book: Value = serde_json::from_str(&saved).unwrap();
        assert_eq!(
            book["schedules"]["plans"]["schedule"]["info"]["spec"]["environment_ref"],
            json!(pot_core::EnvironmentRef::sandbox("own-runtime", "original"))
        );
        assert_eq!(
            book["schedules"]["plans"]["schedule"]["info"]["spec"]["sandbox_id"],
            ""
        );
        task_book(&store, "other-runtime", "main").unwrap();
        assert_eq!(store.get("tasks.v2").unwrap().unwrap(), saved);
        std::fs::remove_dir_all(data).unwrap();
    }
    #[test]
    fn upgrade_preserves_worker_policy_and_unknown_receipts_once() {
        let data = std::env::temp_dir().join(format!("pot-upgrade-{}", id()));
        std::fs::create_dir_all(&data).unwrap();
        let store = Store::open(&data.join("pot-v2.sqlite")).unwrap();
        store.set("agent.default.worker", "chosen").unwrap();
        let old = json!({"name":"exec_command","args":{"cmd":"effect"},"status":"accepted"});
        store
            .set("sandbox.operation:turn:call", &old.to_string())
            .unwrap();
        owner_store(&store, &data, "main").unwrap();
        assert!(store.get("agent.default.worker").unwrap().is_none());
        assert_eq!(
            serde_json::from_str::<Value>(&store.get("agent.routing.v1").unwrap().unwrap())
                .unwrap()["worker_profile"],
            "chosen"
        );
        let files = std::fs::read_dir(data.join("sandbox-receipts"))
            .unwrap()
            .collect::<Vec<_>>();
        assert_eq!(files.len(), 1);
        assert_eq!(
            serde_json::from_slice::<Value>(
                &std::fs::read(files[0].as_ref().unwrap().path()).unwrap()
            )
            .unwrap(),
            old
        );
        owner_store(&store, &data, "main").unwrap();
        assert_eq!(
            std::fs::read_dir(data.join("sandbox-receipts"))
                .unwrap()
                .count(),
            1
        );
        std::fs::remove_dir_all(data).unwrap();
    }
}
