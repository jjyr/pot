//! Control-owned configuration. Assistants and task owners share this handle.
use std::{
    path::{Path, PathBuf},
    sync::{Arc, RwLock},
};
pub(crate) struct ControlConfig {
    root: PathBuf,
    keys: RwLock<Arc<dyn pot_secrets::KeySource>>,
}
impl ControlConfig {
    pub(crate) fn new(root: PathBuf) -> Self {
        #[cfg(not(test))]
        let keys = Arc::new(pot_secrets::SystemKey::new(
            root.to_string_lossy().into_owned(),
        ));
        #[cfg(test)]
        let keys = Arc::new(pot_secrets::MemoryKey::new(vec![19; 32]).unwrap());
        Self {
            root,
            keys: RwLock::new(keys),
        }
    }
    pub(crate) fn set_keys(&self, keys: Arc<dyn pot_secrets::KeySource>) {
        *self.keys.write().unwrap() = keys;
    }
    pub(crate) fn root(&self) -> &Path {
        &self.root
    }
    pub(crate) fn registry(&self) -> anyhow::Result<pot_gateway::registry::Registry> {
        pot_gateway::registry::Registry::open(
            &self.root.join("control.sqlite"),
            self.keys.read().unwrap().clone(),
        )
    }
}
