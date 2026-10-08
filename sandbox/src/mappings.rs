//! Explicit owner-configured Host directory mappings for Linux guests.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    collections::BTreeSet,
    path::{Component, Path, PathBuf},
};
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HostMapping {
    pub source: PathBuf,
    pub destination: PathBuf,
    pub read_only: bool,
}
pub fn host_mappings(config: &Value) -> Result<Vec<HostMapping>> {
    let mappings: Vec<HostMapping> = serde_json::from_value(
        config
            .get("mappings")
            .cloned()
            .unwrap_or_else(|| serde_json::json!([])),
    )?;
    ensure!(mappings.len() <= 16, "Mapping limit reached");
    let mut targets = BTreeSet::new();
    for mapping in &mappings {
        ensure!(
            mapping.source.is_absolute()
                && mapping.source.is_dir()
                && mapping.source.canonicalize()? == mapping.source,
            "Mapping source must remain a canonical Host directory"
        );
        ensure!(
            !mapping
                .source
                .to_string_lossy()
                .contains([':', ',', '\n', '\0']),
            "Unsupported mapping source"
        );
        let target = &mapping.destination;
        ensure!(
            target.starts_with("/mnt/host")
                && target != Path::new("/mnt/host")
                && target
                    .components()
                    .all(|c| !matches!(c, Component::ParentDir | Component::CurDir))
                && !target.to_string_lossy().contains([':', ',', '\n', '\0']),
            "Mapping target must be beneath /mnt/host"
        );
        ensure!(
            targets
                .iter()
                .all(|old: &PathBuf| !target.starts_with(old) && !old.starts_with(target)),
            "Overlapping mapping targets"
        );
        targets.insert(target.clone());
    }
    Ok(mappings)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn mappings_cannot_replace_system_paths_or_overlap_and_keep_access_mode() {
        let source = std::env::temp_dir().canonicalize().unwrap();
        let one = json!({"source":source,"destination":"/mnt/host/project","read_only":true});
        assert!(host_mappings(&json!({"mappings":[one.clone()]})).unwrap()[0].read_only);
        for destination in ["/data", "/run", "/mnt/host/../secret", "/mnt/host"] {
            let mut invalid = one.clone();
            invalid["destination"] = json!(destination);
            assert!(host_mappings(&json!({"mappings":[invalid]})).is_err());
        }
        let mut child = one.clone();
        child["destination"] = json!("/mnt/host/project/nested");
        assert!(host_mappings(&json!({"mappings":[one,child]})).is_err());
    }
}
