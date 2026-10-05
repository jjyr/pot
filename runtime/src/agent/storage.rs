#[cfg(test)]
use super::*;
pub use pot_storage::bindings::*;
#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;
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
