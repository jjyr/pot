//! Immutable placement values; no transport or filesystem behavior.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ObjectRef {
    pub runtime_id: String,
    pub object_id: String,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Assignment {
    pub schema_version: u32,
    pub owner: ObjectRef,
    pub dispatch_origin_runtime: String,
    pub agent_runtime_id: String,
    pub environment: Option<ObjectRef>,
    pub workspace_scope: String,
}
impl Assignment {
    pub fn local(runtime_id: &str, task_id: &str, environment_id: &str) -> Self {
        Self {
            schema_version: 1,
            owner: ObjectRef {
                runtime_id: runtime_id.into(),
                object_id: task_id.into(),
            },
            dispatch_origin_runtime: runtime_id.into(),
            agent_runtime_id: runtime_id.into(),
            environment: Some(ObjectRef {
                runtime_id: runtime_id.into(),
                object_id: environment_id.into(),
            }),
            workspace_scope: format!("tasks/{task_id}"),
        }
    }
    /// Local dispatch must never silently accept a remote or rebound assignment.
    pub fn validate_local(
        &self,
        runtime_id: &str,
        task_id: &str,
        environment_id: &str,
    ) -> Result<()> {
        ensure!(
            self == &Self::local(runtime_id, task_id, environment_id),
            "Unsupported or conflicting Task assignment"
        );
        Ok(())
    }
}
