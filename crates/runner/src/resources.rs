//! Immutable Control-selected resource manifest. No node-local discovery.
use anyhow::Result;
use base64::{Engine, engine::general_purpose::STANDARD};
use pot_core::plugins::PluginConfig;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
const MAX_BYTES: usize = 512 * 1024;
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ResourceFile {
    pub data: String,
    pub executable: bool,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GlobalResources {
    pub files: BTreeMap<String, ResourceFile>,
    pub reserved_names: Vec<String>,
    pub plugins: BTreeMap<String, PluginConfig>,
    pub shell: pot_core::ShellSettings,
    pub vision: bool,
}
impl GlobalResources {
    pub fn revision(&self) -> Result<String> {
        let mut value = serde_json::to_value(self)?;
        value.sort_all_objects();
        let bytes = serde_json::to_vec(&value)?;
        anyhow::ensure!(
            bytes.len() <= MAX_BYTES,
            "Global resource snapshot exceeds 512 KiB"
        );
        Ok(Sha256::digest(bytes)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect())
    }
    pub fn validate(&self) -> Result<()> {
        self.revision()?;
        anyhow::ensure!(self.files.len() <= 2048, "Too many global resource files");
        let settings = pot_core::AgentSettings {
            plugins: self.plugins.clone(),
            shell: self.shell.clone(),
            ..Default::default()
        };
        settings.validate()?;
        anyhow::ensure!(
            self.plugins
                .values()
                .all(|p| p.source.is_none() && (!p.enabled || p.bundle.is_some())),
            "Control must pin plugins before delivery"
        );
        for (name, file) in &self.files {
            anyhow::ensure!(
                pot_protocol::valid_file_path(name)
                    && name.starts_with("skills/")
                    && name.split('/').count() >= 4,
                "Invalid global resource path"
            );
            STANDARD.decode(&file.data)?;
        }
        for name in &self.reserved_names {
            anyhow::ensure!(
                self.files
                    .contains_key(&format!("skills/builtin/{name}/SKILL.md")),
                "Missing reserved skill bundle"
            );
        }
        Ok(())
    }
}
