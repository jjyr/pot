use anyhow::{Result, bail};
use pot_protocol::RequestBudget;
use std::path::PathBuf;
#[derive(Clone)]
pub struct BudgetSettings {
    pub namespace: &'static str,
    pub data: PathBuf,
    pub limit: u64,
    pub max_output_tokens: u64,
}
impl BudgetSettings {
    pub fn status(&self) -> Result<RequestBudget> {
        let dir = self.data.join(self.namespace);
        let used = if dir.exists() {
            std::fs::read_dir(dir)?
                .collect::<std::io::Result<Vec<_>>>()?
                .iter()
                .filter(|e| {
                    e.file_name()
                        .to_str()
                        .is_some_and(|v| v.parse::<u64>().is_ok())
                })
                .count() as u64
        } else {
            0
        };
        Ok(RequestBudget {
            used,
            limit: self.limit,
            max_output_tokens: self.max_output_tokens,
        })
    }
}
// Files survive VM recreation independently of SQLite journal mode. Reserve
// before networking, fsync, never remove a reservation, and never auto-retry.
pub fn reserve_in(dir: &std::path::Path, limit: u64) -> Result<u64> {
    use std::io::Write;
    std::fs::create_dir_all(dir)?;
    for n in 1..=limit {
        match std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(dir.join(n.to_string()))
        {
            Ok(mut f) => {
                f.write_all(b"reserved\n")?;
                f.sync_all()?;
                std::fs::File::open(dir)?.sync_all()?;
                return Ok(n);
            }
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(e.into()),
        }
    }
    bail!("Model request budget exhausted")
}
