//! Assistant-owned instructions persisted in SQLite; never environment files.
use anyhow::Result;
use pot_storage::{Store, id};
use serde::{Deserialize, Serialize};
const KEY: &str = "assistant.instructions.v1";
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Rules {
    schema_version: u32,
    pub text: String,
    pub revision: String,
}
pub fn load(store: &Store) -> Result<Rules> {
    if let Some(value) = store.get(KEY)? {
        let rules: Rules = serde_json::from_str(&value)?;
        anyhow::ensure!(
            rules.schema_version == 1 && !rules.revision.is_empty(),
            "Unsupported assistant instructions record"
        );
        return Ok(rules);
    }
    let rules = Rules {
        schema_version: 1,
        text: String::new(),
        revision: id(),
    };
    store.set(KEY, &serde_json::to_string(&rules)?)?;
    Ok(rules)
}
pub fn save(store: &Store, text: &str, expected_revision: &str) -> Result<Rules> {
    anyhow::ensure!(
        text.len() <= 262144,
        "Assistant instructions exceed 256 KiB"
    );
    let transaction = store.0.unchecked_transaction()?;
    let current = load(store)?;
    anyhow::ensure!(
        current.revision == expected_revision,
        "AGENTS.md changed; reload before saving"
    );
    let rules = Rules {
        schema_version: 1,
        text: text.into(),
        revision: id(),
    };
    store.set(KEY, &serde_json::to_string(&rules)?)?;
    transaction.commit()?;
    Ok(rules)
}
