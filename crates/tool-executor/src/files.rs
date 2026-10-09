//! Canonical working-directory resolution for local execution.
use anyhow::{Result, ensure};
use std::path::{Component, Path, PathBuf};

pub fn resolve(path: &Path, workspace: &Path) -> Result<PathBuf> {
    let path = if path.is_absolute() {
        path.to_owned()
    } else {
        workspace.join(path)
    };
    ensure!(
        !path.components().any(|c| matches!(c, Component::ParentDir)),
        "Parent path components are not allowed"
    );
    if path.exists() {
        Ok(path.canonicalize()?)
    } else {
        Ok(path
            .parent()
            .ok_or_else(|| anyhow::anyhow!("Missing parent"))?
            .canonicalize()?
            .join(
                path.file_name()
                    .ok_or_else(|| anyhow::anyhow!("Missing filename"))?,
            ))
    }
}
