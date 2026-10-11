//! Control-owned Provider metadata and encrypted credential references in SQLite.
use anyhow::{Result, anyhow, ensure};
use pot_model_protocol::codecs::Protocol;
use rusqlite::{Connection, OptionalExtension, params};
use serde::{Deserialize, Serialize};
use std::{
    path::Path,
    sync::{Arc, Mutex},
};

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
pub struct Provider {
    pub id: String,
    pub endpoint: String,
    pub allowed_hosts: Vec<String>,
    pub protocol: Protocol,
    pub secret_ref: String,
}
pub struct Registry {
    db: Mutex<Connection>,
    secrets: pot_secrets::Store,
}
impl Registry {
    pub fn open(path: &Path, keys: Arc<dyn pot_secrets::KeySource>) -> Result<Self> {
        let secrets = pot_secrets::Store::open(path, keys)?;
        let db = Connection::open(path)?;
        db.execute_batch("PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS providers (id TEXT PRIMARY KEY, metadata TEXT NOT NULL);")?;
        Ok(Self {
            db: Mutex::new(db),
            secrets,
        })
    }
    pub fn get(&self, id: &str) -> Result<Provider> {
        let value: Option<String> = self
            .db
            .lock()
            .unwrap()
            .query_row("SELECT metadata FROM providers WHERE id=?", [id], |r| {
                r.get(0)
            })
            .optional()?;
        serde_json::from_str(
            &value.ok_or_else(|| anyhow!("Provider unavailable; configure it on Control"))?,
        )
        .map_err(Into::into)
    }
    pub fn save(&self, provider: &Provider, key: &[u8]) -> Result<()> {
        ensure!(!key.is_empty(), "Provider credential missing");
        self.secrets.put(&provider.secret_ref, key)?;
        let value = serde_json::to_string(provider)?;
        let mut db = self.db.lock().unwrap();
        let tx = db.transaction()?;
        let existing: Option<String> = tx
            .query_row(
                "SELECT metadata FROM providers WHERE id=?",
                [&provider.id],
                |r| r.get(0),
            )
            .optional()?;
        if let Some(existing) = existing {
            ensure!(
                existing == value,
                "Provider reference already contains another configuration"
            );
        } else {
            tx.execute(
                "INSERT INTO providers VALUES (?,?)",
                params![provider.id, value],
            )?;
        }
        tx.commit()?;
        Ok(())
    }
    pub fn credential(&self, provider: &Provider) -> Result<String> {
        let value = self.secrets.get(&provider.secret_ref)?;
        Ok(std::str::from_utf8(&value)?.to_owned())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn metadata_never_contains_credentials_and_pinned_routes_cannot_change() {
        let path = std::env::temp_dir().join(format!(
            "pot-provider-{}.sqlite",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let keys = Arc::new(pot_secrets::MemoryKey::new(vec![42; 32]).unwrap());
        let registry = Registry::open(&path, keys.clone()).unwrap();
        let provider = Provider {
            id: "api-one".into(),
            endpoint: "https://example.com/model".into(),
            allowed_hosts: vec!["example.com".into()],
            protocol: Protocol::OpenaiChat,
            secret_ref: "provider/api-one".into(),
        };
        registry.save(&provider, b"provider-private-token").unwrap();
        let public = registry.get("api-one").unwrap();
        assert!(
            !serde_json::to_string(&public)
                .unwrap()
                .contains("provider-private-token")
        );
        assert!(registry.save(&provider, b"changed").is_err());
        drop(registry);
        assert!(
            !std::fs::read(&path)
                .unwrap()
                .windows(b"provider-private-token".len())
                .any(|w| w == b"provider-private-token")
        );
        let registry = Registry::open(&path, keys).unwrap();
        assert_eq!(
            registry
                .credential(&registry.get("api-one").unwrap())
                .unwrap(),
            "provider-private-token"
        );
        drop(registry);
        std::fs::remove_file(path).unwrap();
    }
}
