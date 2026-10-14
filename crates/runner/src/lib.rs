//! Destination-owned Task Agent runs. No assistant store or local global catalog.
use anyhow::{Result, anyhow, ensure};
use pot_core::{ExecutionTarget, Outcome, ResolvedSpec};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::Mutex,
};

const VERSION: u32 = 2;
pub mod control;
pub mod resources;
mod runner;
pub use runner::{Destination, ResourceProvider, RunServices, execute};

/// Credential-free, immutable input selected by Control, distinct from a chat session.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Launch {
    pub task_id: String,
    pub run_id: String,
    pub generation: u64,
    pub execution_target: ExecutionTarget,
    pub config: ResolvedSpec,
    pub plugins: Vec<String>,
    /// Explicit global skills; absent or empty selects none. Project discovery is independent.
    pub skills: Option<Vec<String>>,
    pub text: String,
}
impl Launch {
    pub fn workspace_scope(&self, control: &str) -> Result<String> {
        use base64::Engine;
        uuid::Uuid::parse_str(control)?;
        Ok(
            base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(Sha256::digest(
                serde_json::to_vec(&(control, &self.task_id, &self.execution_target.target))?,
            )),
        )
    }
    pub fn validate(&self, runtime_id: &str) -> Result<()> {
        uuid::Uuid::parse_str(&self.task_id)?;
        uuid::Uuid::parse_str(&self.run_id)?;
        self.execution_target.validate()?;
        ensure!(
            self.execution_target.is_local_to(runtime_id),
            "Task Agent and Execution target must belong to the destination Runtime"
        );
        ensure!(
            self.generation > 0 && !self.text.trim().is_empty() && self.text.len() <= 256 * 1024,
            "Invalid Task Agent input"
        );
        self.config.validate()?;
        ensure!(
            self.config.params.agent.plugins.is_empty(),
            "Task launches carry plugin names, not bundles"
        );
        for names in [&self.plugins, self.skills.as_ref().unwrap_or(&Vec::new())] {
            ensure!(
                names.len() <= 128
                    && names.iter().all(|name| !name.is_empty()
                        && name.len() <= 64
                        && name.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-')),
                "Invalid resource selection"
            );
            ensure!(
                names
                    .iter()
                    .collect::<std::collections::BTreeSet<_>>()
                    .len()
                    == names.len(),
                "Duplicate resource name"
            );
        }
        // Transport bounds cover only the brief, resource names and model settings.
        ensure!(
            serde_json::to_vec(self)?.len() <= 768 * 1024,
            "Task Agent launch exceeds transport limit"
        );
        pot_agent::validate(&self.config)
    }
    fn digest(&self) -> Result<String> {
        Ok(Sha256::digest(serde_json::to_vec(self)?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect())
    }
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Phase {
    Accepted,
    Running,
    Complete,
    Failed,
    Cancelled,
    RecoveryRequired,
    Interrupted,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Terminal {
    pub outcome: Outcome,
    pub text: String,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Run {
    schema_version: u32,
    pub control_id: String,
    pub launch: Launch,
    launch_digest: String,
    pub boot_id: String,
    pub phase: Phase,
    pub result: Option<Terminal>,
    pub context: Option<Value>,
    #[serde(default)]
    sequence: u64,
}

/// One journal per process authority; the Host already holds the data-root lock.
/// Every transition is committed before acknowledging it to the caller.
pub struct Journal {
    data: PathBuf,
    runtime_id: String,
    boot_id: String,
    records: Mutex<BTreeMap<String, Run>>,
}
impl Journal {
    pub fn open(data: &Path, runtime_id: &str, boot_id: &str) -> Result<Self> {
        uuid::Uuid::parse_str(runtime_id)?;
        uuid::Uuid::parse_str(boot_id)?;
        let data = data.join("task-agent-runs");
        std::fs::create_dir_all(&data)?;
        let mut records = BTreeMap::new();
        for entry in std::fs::read_dir(&data)? {
            let entry = entry?;
            if entry.path().extension().is_none_or(|e| e != "json") {
                continue;
            }
            let mut run: Run = serde_json::from_slice(&std::fs::read(entry.path())?)?;
            ensure!(
                run.schema_version == VERSION,
                "Unsupported Task Agent record"
            );
            run.launch.validate(runtime_id)?;
            ensure!(
                run.launch_digest == run.launch.digest()?,
                "Task Agent launch checksum mismatch"
            );
            uuid::Uuid::parse_str(&run.control_id)?;
            uuid::Uuid::parse_str(&run.boot_id)?;
            let key = Self::key(&run.control_id, &run.launch.task_id, &run.launch.run_id)?;
            ensure!(
                entry.path().file_stem().and_then(|s| s.to_str()) == Some(&key),
                "Task Agent record identity mismatch"
            );
            ensure!(
                matches!(
                    run.phase,
                    Phase::Complete | Phase::Failed | Phase::Cancelled | Phase::Interrupted
                ) == run.result.is_some(),
                "Task Agent terminal record is inconsistent"
            );
            if let Some(result) = &run.result {
                ensure!(
                    run.phase == result.phase()?
                        || (run.phase == Phase::Interrupted
                            && result.outcome == Outcome::Cancelled),
                    "Task Agent outcome differs from terminal phase"
                );
            }
            if run.result.is_none() && run.boot_id != boot_id {
                run.phase = Phase::RecoveryRequired;
                Self::write(&data, &key, &run)?;
            }
            ensure!(
                records.insert(key, run).is_none(),
                "Duplicate Task Agent record"
            );
        }
        Ok(Self {
            data,
            runtime_id: runtime_id.into(),
            boot_id: boot_id.into(),
            records: Mutex::new(records),
        })
    }
    fn key(control: &str, task: &str, run: &str) -> Result<String> {
        for id in [control, task, run] {
            uuid::Uuid::parse_str(id)?;
        }
        Ok(Sha256::digest(serde_json::to_vec(&(control, task, run))?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect())
    }
    fn write(data: &Path, key: &str, run: &Run) -> Result<()> {
        pot_sandbox::private_json(
            &data.join(format!("{key}.json")),
            &serde_json::to_value(run)?,
        )
    }
    pub fn accept(&self, control: &str, launch: Launch) -> Result<Run> {
        launch.validate(&self.runtime_id)?;
        let key = Self::key(control, &launch.task_id, &launch.run_id)?;
        let digest = launch.digest()?;
        let mut records = self.records.lock().unwrap();
        if let Some(old) = records.get(&key) {
            ensure!(
                old.launch_digest == digest,
                "Task Agent launch conflicts with accepted input"
            );
            return Ok(old.clone());
        }
        ensure!(
            !records.values().any(|r| r.control_id == control
                && r.launch.task_id == launch.task_id
                && r.result.is_none()),
            "Previous Task Agent run requires reconciliation"
        );
        let previous = records
            .values()
            .filter(|r| r.control_id == control && r.launch.task_id == launch.task_id)
            .max_by_key(|r| r.sequence);
        let sequence = previous.map_or(Ok(1), |r| {
            r.sequence
                .checked_add(1)
                .ok_or_else(|| anyhow!("Task run sequence exhausted"))
        })?;
        let context = previous
            .filter(|r| {
                r.result.is_some()
                    && r.launch.generation == launch.generation
                    && r.launch.execution_target == launch.execution_target
                    && serde_json::to_value(&r.launch.config).ok()
                        == serde_json::to_value(&launch.config).ok()
                    && r.launch.plugins == launch.plugins
                    && r.launch.skills == launch.skills
            })
            .and_then(|r| r.context.clone());
        let run = Run {
            schema_version: VERSION,
            control_id: control.into(),
            launch,
            launch_digest: digest,
            boot_id: self.boot_id.clone(),
            phase: Phase::Accepted,
            result: None,
            context,
            sequence,
        };
        Self::write(&self.data, &key, &run)?;
        records.insert(key, run.clone());
        Ok(run)
    }
    pub fn inspect(&self, control: &str, task: &str, run: &str) -> Result<Option<Run>> {
        Ok(self
            .records
            .lock()
            .unwrap()
            .get(&Self::key(control, task, run)?)
            .cloned())
    }
    fn change<T>(
        &self,
        control: &str,
        task: &str,
        run: &str,
        change: impl FnOnce(&mut Run) -> Result<T>,
    ) -> Result<T> {
        let key = Self::key(control, task, run)?;
        let mut records = self.records.lock().unwrap();
        let mut next = records
            .get(&key)
            .ok_or_else(|| anyhow!("Task Agent run was not accepted"))?
            .clone();
        let output = change(&mut next)?;
        Self::write(&self.data, &key, &next)?;
        records.insert(key, next);
        Ok(output)
    }
    /// Only the winner may start an Agent; exact retries never obtain a second claim.
    pub fn claim(&self, control: &str, task: &str, run: &str) -> Result<bool> {
        self.change(control, task, run, |record| {
            if record.phase != Phase::Accepted {
                return Ok(false);
            }
            ensure!(
                record.boot_id == self.boot_id,
                "Task Agent belongs to a previous boot"
            );
            record.phase = Phase::Running;
            Ok(true)
        })
    }
    pub fn save_context(&self, control: &str, task: &str, run: &str, context: Value) -> Result<()> {
        self.change(control, task, run, |record| {
            ensure!(
                record.phase == Phase::Running && record.boot_id == self.boot_id,
                "Task Agent is not running on this boot"
            );
            record.context = Some(context);
            Ok(())
        })
    }
    /// Called only after the transport has reconciled every issued operation and
    /// drained the previous boot's scope. This never claims or restarts an Agent.
    pub fn reconcile_recovered_context(
        &self,
        control: &str,
        task: &str,
        run: &str,
        receipts: &BTreeMap<String, Value>,
    ) -> Result<()> {
        self.change(control, task, run, |record| {
            ensure!(
                record.phase == Phase::RecoveryRequired && record.boot_id != self.boot_id,
                "Run does not require previous-boot recovery"
            );
            if let Some(context) = &record.context {
                record.context = Some(pot_agent::reconcile_context(
                    context,
                    receipts,
                    record
                        .launch
                        .config
                        .params
                        .agent
                        .model_descriptor(
                            &record.launch.config.provider,
                            &record.launch.config.model,
                        )?
                        .vision(),
                )?);
            }
            Ok(())
        })
    }
    pub fn interrupt_recovered(&self, control: &str, task: &str, run: &str) -> Result<()> {
        self.change(control,task,run,|record| {
            ensure!(record.phase==Phase::RecoveryRequired && record.boot_id!=self.boot_id,"Run does not require previous-boot recovery");
            record.phase=Phase::Interrupted;
            record.result=Some(Terminal{outcome:Outcome::Cancelled,text:"Execution Runtime restarted; use guide_task to continue explicitly after receipt reconciliation.".into()});
            Ok(())
        })
    }
    pub fn finish(&self, control: &str, task: &str, run: &str, result: Terminal) -> Result<()> {
        self.change(control, task, run, |record| {
            if let Some(old) = &record.result {
                ensure!(old == &result, "Conflicting Task Agent terminal result");
                return Ok(());
            }
            ensure!(
                record.phase == Phase::Running && record.boot_id == self.boot_id,
                "Task Agent is not running on this boot"
            );
            record.phase = result.phase()?;
            record.result = Some(result);
            Ok(())
        })
    }
}

impl Terminal {
    fn phase(&self) -> Result<Phase> {
        match self.outcome {
            Outcome::Success => Ok(Phase::Complete),
            Outcome::Failed(_) => Ok(Phase::Failed),
            Outcome::Cancelled => Ok(Phase::Cancelled),
            Outcome::Unknown => Err(anyhow!(
                "Unknown Task Agent outcome requires reconciliation"
            )),
        }
    }
}

#[derive(Debug)]
pub struct Interrupted;
impl std::fmt::Display for Interrupted {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Execution Runtime restarted; original run stopped after receipt reconciliation. Use guide_task to continue explicitly.")
    }
}
impl std::error::Error for Interrupted {}

#[cfg(test)]
mod target_storage_tests {
    use super::*;
    struct Config;
    impl pot_core::ProfileConfig for Config {
        fn model(&self) -> &str {
            "fixture"
        }
        fn max_tokens(&self) -> u64 {
            4096
        }
    }
    #[test]
    fn journal_rejects_old_fields_and_mismatched_checksums() {
        let id = || uuid::Uuid::new_v4().to_string();
        let runtime = id();
        let control = id();
        let data = std::env::temp_dir().join(format!("pot-target-storage-{}", id()));
        let mut config = pot_core::Profile::api(&Config).resolve().unwrap();
        config.set_plugins(Default::default()).unwrap();
        let launch = Launch {
            task_id: id(),
            run_id: id(),
            generation: 1,
            execution_target: ExecutionTarget::host(&runtime),
            plugins: vec![],
            config,
            skills: None,
            text: "produce a result".into(),
        };
        let serialized = serde_json::to_value(&launch).unwrap();
        assert!(serialized.get("resources").is_none());
        assert!(
            serialized["config"]["params"]["agent"]["plugins"]
                .as_object()
                .unwrap()
                .is_empty()
        );
        let mut embedded = launch.clone();
        embedded
            .config
            .set_plugins(pot_core::plugins::defaults())
            .unwrap();
        assert!(embedded.validate(&runtime).is_err());
        let journal = Journal::open(&data, &runtime, &id()).unwrap();
        journal.accept(&control, launch.clone()).unwrap();
        let old = serde_json::to_string(&launch).unwrap().replacen(
            "\"execution_target\":",
            "\"environment\":",
            1,
        );
        let digest: String = Sha256::digest(old.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        assert_ne!(launch.digest().unwrap(), digest);
        let path = data.join("task-agent-runs").join(format!(
            "{}.json",
            Journal::key(&control, &launch.task_id, &launch.run_id).unwrap()
        ));
        let mut record: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        record["launch"] = serde_json::from_str(&old).unwrap();
        record["launch_digest"] = Value::String(digest.clone());
        std::fs::write(&path, serde_json::to_vec(&record).unwrap()).unwrap();
        drop(journal);
        assert!(Journal::open(&data, &runtime, &id()).is_err());
        record["launch"] = serde_json::to_value(&launch).unwrap();
        std::fs::write(&path, serde_json::to_vec(&record).unwrap()).unwrap();
        assert!(Journal::open(&data, &runtime, &id()).is_err());
        std::fs::remove_dir_all(data).unwrap();
    }
}
