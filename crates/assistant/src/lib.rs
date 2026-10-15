//! Assistant identity, instructions and conversation state, independent of execution hosts.
pub mod driver;
pub mod instructions;
pub mod message;
pub mod orchestration;
pub mod registry;
pub mod threads;

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn identities_and_rules_are_durable_and_stale_edits_fail() {
        let root = std::env::temp_dir().join(pot_storage::id());
        std::fs::create_dir_all(&root).unwrap();
        let path = root.join("control.sqlite");
        let registry = registry::Registry::open(&path, "main").unwrap();
        assert!(registry.definitions().unwrap().is_empty());
        let assistant = registry.create("create-one", "Research").unwrap();
        assert_eq!(
            registry.create("create-one", "Research").unwrap().id,
            assistant.id
        );
        assert!(registry.create("create-one", "Other").is_err());
        let renamed = registry
            .rename(&assistant.id, "Writer", assistant.revision)
            .unwrap();
        assert!(
            registry
                .rename(&assistant.id, "Stale", assistant.revision)
                .is_err()
        );
        drop(registry);
        assert_eq!(
            registry::Registry::open(&path, "main")
                .unwrap()
                .definitions()
                .unwrap()[0]
                .name,
            renamed.name
        );
        let store = pot_storage::Store::open(&root.join("assistant.sqlite")).unwrap();
        let initial = instructions::load(&store).unwrap();
        let rules =
            instructions::save(&store, "Use the fixed task route", &initial.revision).unwrap();
        assert!(instructions::save(&store, "Stale overwrite", &initial.revision).is_err());
        drop(store);
        assert_eq!(
            instructions::load(&pot_storage::Store::open(&root.join("assistant.sqlite")).unwrap())
                .unwrap()
                .text,
            rules.text
        );
        std::fs::remove_dir_all(root).unwrap();
    }
}
