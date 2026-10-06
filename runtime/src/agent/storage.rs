pub use pot_storage::bindings::*;
#[cfg(test)]
mod tests {
    use super::*;
    use crate::*;
    use pot_core::NativeRef;
    #[test]
    fn context_identity_is_durable_and_missing_context_fails() {
        let dir = std::env::temp_dir().join(id());
        std::fs::create_dir_all(&dir).unwrap();
        let mut store = Store::open(&dir.join("pot-v2.sqlite")).unwrap();
        initialize_store(
            &mut store,
            "main",
            &dir,
            &execution::Config {
                model: "fixture".into(),
                ..Default::default()
            },
        )
        .unwrap();
        assert!(load_context(&store).unwrap().is_none());
        let value = json!({"type":"session","version":1,"id":"session","messages":[]});
        save_context(&store, &value).unwrap();
        save_agent_session(&mut store, "session").unwrap();
        drop(store);
        let store = Store::open(&dir.join("pot-v2.sqlite")).unwrap();
        assert_eq!(load_context(&store).unwrap(), Some(value));
        assert!(
            save_context(
                &store,
                &json!({"type":"session","version":1,"id":"changed","messages":[]})
            )
            .is_err()
        );
        let NativeRef::Pot { file, .. } = current(&store).unwrap().unwrap().native_ref else {
            panic!()
        };
        std::fs::remove_file(file).unwrap();
        assert!(load_context(&store).is_err());
        drop(store);
        std::fs::remove_dir_all(dir).unwrap();
    }
}
