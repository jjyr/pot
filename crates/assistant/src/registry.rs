//! Durable assistant identities. Runtime owns only their running handles.
use anyhow::{Result, anyhow, ensure};
use pot_storage::{Store, id};
use serde::{Deserialize, Serialize};
use std::{path::Path, sync::Mutex};
const KEY: &str = "assistants.v1";
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Definition {
    pub id: String,
    pub name: String,
    pub revision: u64,
    pub request_id: String,
}
#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Book {
    schema_version: u32,
    definitions: Vec<Definition>,
}
pub struct Registry {
    store: Mutex<Store>,
    root: String,
}
impl Registry {
    pub fn open(path: &Path, root: &str) -> Result<Self> {
        let store = Store::open(path)?;
        if store.get(KEY)?.is_none() {
            store.set(
                KEY,
                &serde_json::to_string(&Book {
                    schema_version: 1,
                    definitions: vec![],
                })?,
            )?;
        }
        load(&store)?;
        Ok(Self {
            store: Mutex::new(store),
            root: root.into(),
        })
    }
    pub fn definitions(&self) -> Result<Vec<Definition>> {
        Ok(load(&self.store.lock().unwrap())?.definitions)
    }
    pub fn create(&self, request: &str, name: &str) -> Result<Definition> {
        ensure!(
            !request.is_empty() && request.len() <= 128,
            "Invalid request identity"
        );
        validate_name(name)?;
        let mut store = self.store.lock().unwrap();
        let tx = store.0.transaction()?;
        let raw: String = tx.query_row("SELECT value FROM metadata WHERE key=?", [KEY], |r| {
            r.get(0)
        })?;
        let mut book: Book = serde_json::from_str(&raw)?;
        if let Some(old) = book.definitions.iter().find(|d| d.request_id == request) {
            ensure!(old.name == name, "Conflicting assistant creation request");
            return Ok(old.clone());
        }
        ensure!(book.definitions.len() < 64, "Assistant limit reached");
        let definition = Definition {
            id: if book.definitions.is_empty() {
                self.root.clone()
            } else {
                id()
            },
            name: name.into(),
            revision: 1,
            request_id: request.into(),
        };
        book.definitions.push(definition.clone());
        tx.execute(
            "UPDATE metadata SET value=? WHERE key=?",
            [serde_json::to_string(&book)?, KEY.into()],
        )?;
        tx.commit()?;
        Ok(definition)
    }
    pub fn rename(&self, owner: &str, name: &str, revision: u64) -> Result<Definition> {
        validate_name(name)?;
        let mut store = self.store.lock().unwrap();
        let tx = store.0.transaction()?;
        let raw: String = tx.query_row("SELECT value FROM metadata WHERE key=?", [KEY], |r| {
            r.get(0)
        })?;
        let mut book: Book = serde_json::from_str(&raw)?;
        let entry = book
            .definitions
            .iter_mut()
            .find(|d| d.id == owner)
            .ok_or_else(|| anyhow!("Assistant missing"))?;
        ensure!(
            entry.revision == revision,
            "Assistant changed; reload before saving"
        );
        entry.name = name.into();
        entry.revision = revision
            .checked_add(1)
            .ok_or_else(|| anyhow!("Revision overflow"))?;
        let result = entry.clone();
        tx.execute(
            "UPDATE metadata SET value=? WHERE key=?",
            [serde_json::to_string(&book)?, KEY.into()],
        )?;
        tx.commit()?;
        Ok(result)
    }
}
fn load(store: &Store) -> Result<Book> {
    let book: Book = serde_json::from_str(
        &store
            .get(KEY)?
            .ok_or_else(|| anyhow!("Assistant registry missing"))?,
    )?;
    ensure!(book.schema_version == 1, "Unsupported assistant schema");
    let mut seen = std::collections::BTreeSet::new();
    for definition in &book.definitions {
        validate_name(&definition.name)?;
        ensure!(
            definition.revision > 0 && seen.insert(&definition.id),
            "Invalid assistant identity"
        );
    }
    Ok(book)
}
pub fn validate_name(name: &str) -> Result<()> {
    ensure!(
        !name.trim().is_empty()
            && name.chars().count() <= 80
            && !name.chars().any(char::is_control),
        "Invalid assistant name"
    );
    Ok(())
}
