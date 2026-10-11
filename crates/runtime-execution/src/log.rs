use super::Execution;
use anyhow::Result;
use std::{
    fs::{File, OpenOptions},
    io::Write,
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
};
// Bounded, private metadata logs. Never serialize inputs, model/tool payloads or errors.
pub(super) struct Log {
    file: Option<File>,
    path: PathBuf,
    bytes: u64,
}
impl Log {
    pub fn open(data: &Path) -> Result<Self> {
        std::fs::create_dir_all(data)?;
        let path = data.join("runtime-executions.jsonl");
        Ok(Self {
            file: None,
            path,
            bytes: 0,
        })
    }
    fn file(path: &Path) -> Result<File> {
        let file = OpenOptions::new()
            .create(true)
            .append(true)
            .mode(0o600)
            .open(path)?;
        file.set_permissions(std::fs::Permissions::from_mode(0o600))?;
        Ok(file)
    }
    pub fn record(
        &mut self,
        execution: &Execution,
        event: &str,
        sequence: Option<u64>,
        operation_id: Option<&str>,
    ) -> Result<()> {
        if self.file.is_none() {
            let file = Self::file(&self.path)?;
            self.bytes = file.metadata()?.len();
            self.file = Some(file);
        }
        if self.bytes >= 8 * 1024 * 1024 {
            std::fs::rename(&self.path, self.path.with_extension("jsonl.1"))?;
            self.file = Some(Self::file(&self.path)?);
            self.bytes = 0;
        }
        let mut bytes = serde_json::to_vec(&serde_json::json!({
            "time_ms": std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH)?.as_millis(),
            "execution": execution, "event": event, "sequence": sequence, "operation_id": operation_id,
        }))?;
        bytes.push(b'\n');
        self.file.as_mut().unwrap().write_all(&bytes)?;
        self.bytes += bytes.len() as u64;
        Ok(())
    }
}
