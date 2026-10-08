pub use pot_storage::threads::*;
#[cfg(test)]
mod tests {
    use super::*;
    use crate::*;
    #[test]
    fn context_is_durable_and_corrupt_context_is_rejected() {
        let dir = std::env::temp_dir().join(id());
        std::fs::create_dir_all(&dir).unwrap();
        let mut store = Store::open(&dir.join("pot-v2.sqlite")).unwrap();
        initialize_store(
            &mut store,
            "main",
            &execution::Config {
                model: "fixture".into(),
                ..Default::default()
            },
        )
        .unwrap();
        assert!(load_context(&store).unwrap().is_none());
        let value = json!({"type":"context","version":1,"messages":[]});
        save_context(&store, &value).unwrap();
        drop(store);
        let store = Store::open(&dir.join("pot-v2.sqlite")).unwrap();
        assert_eq!(load_context(&store).unwrap(), Some(value));
        assert!(save_context(&store, &json!({"messages":[]})).is_err());
        store.set(CONTEXT, "corrupt").unwrap();
        assert!(load_context(&store).is_err());
        drop(store);
        std::fs::remove_dir_all(dir).unwrap();
    }
}
