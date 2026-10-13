//! Immutable placement values; no transport or filesystem behavior.
use crate::ExecutionTarget;
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ObjectRef {
    pub runtime_id: String,
    pub object_id: String,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Assignment {
    pub schema_version: u32,
    pub owner: ObjectRef,
    pub dispatch_origin_runtime: String,
    pub agent_runtime_id: String,
    pub execution_target: Option<ExecutionTarget>,
    pub workspace_scope: String,
}
impl Assignment {
    pub fn local(runtime_id: &str, task_id: &str, sandbox_id: &str) -> Self {
        Self {
            schema_version: 3,
            owner: ObjectRef {
                runtime_id: runtime_id.into(),
                object_id: task_id.into(),
            },
            dispatch_origin_runtime: runtime_id.into(),
            agent_runtime_id: runtime_id.into(),
            execution_target: Some(ExecutionTarget::sandbox(runtime_id, sandbox_id)),
            workspace_scope: format!("tasks/{task_id}"),
        }
    }
    pub fn on_execution_target(
        runtime_id: &str,
        task_id: &str,
        execution_target: ExecutionTarget,
    ) -> Self {
        let mut assignment = Self::local(runtime_id, task_id, "default");
        assignment.agent_runtime_id = execution_target.runtime_id.clone();
        assignment.execution_target = Some(execution_target);
        assignment
    }
    pub fn validate_binding(
        &self,
        runtime_id: &str,
        task_id: &str,
        execution_target: &ExecutionTarget,
    ) -> Result<()> {
        execution_target.validate()?;
        ensure!(
            self == &Self::on_execution_target(runtime_id, task_id, execution_target.clone()),
            "Unsupported or conflicting Task assignment"
        );
        Ok(())
    }
    /// Local dispatch must never silently accept a remote or rebound assignment.
    pub fn validate_local(&self, runtime_id: &str, task_id: &str, sandbox_id: &str) -> Result<()> {
        ensure!(
            self == &Self::local(runtime_id, task_id, sandbox_id),
            "Unsupported or conflicting Task assignment"
        );
        Ok(())
    }
}

impl<'de> Deserialize<'de> for Assignment {
    fn deserialize<D: serde::Deserializer<'de>>(
        deserializer: D,
    ) -> std::result::Result<Self, D::Error> {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Stored {
            schema_version: u32,
            owner: ObjectRef,
            dispatch_origin_runtime: String,
            agent_runtime_id: String,
            execution_target: Option<ExecutionTarget>,
            workspace_scope: String,
        }
        let stored = Stored::deserialize(deserializer)?;
        if stored.schema_version != 3 {
            return Err(serde::de::Error::custom("Unsupported Assignment schema"));
        }
        Ok(Self {
            schema_version: 3,
            owner: stored.owner,
            dispatch_origin_runtime: stored.dispatch_origin_runtime,
            agent_runtime_id: stored.agent_runtime_id,
            execution_target: stored.execution_target,
            workspace_scope: stored.workspace_scope,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn assignment_accepts_only_current_schema() {
        let assignment =
            Assignment::on_execution_target("control", "task", ExecutionTarget::host("machine"));
        let value = serde_json::to_value(&assignment).unwrap();
        assert_eq!(
            serde_json::from_value::<Assignment>(value.clone()).unwrap(),
            assignment
        );
        for version in [1, 2, 4] {
            let mut unsupported = value.clone();
            unsupported["schema_version"] = json!(version);
            assert!(serde_json::from_value::<Assignment>(unsupported).is_err());
        }
        let mut old = value;
        old["environment"] = old
            .as_object_mut()
            .unwrap()
            .remove("execution_target")
            .unwrap();
        assert!(serde_json::from_value::<Assignment>(old).is_err());
    }
    #[test]
    fn typed_binding_rejects_host_remote_and_owner_substitution() {
        let execution_target = ExecutionTarget::host("b");
        let assignment = Assignment::on_execution_target("a", "task", execution_target.clone());
        assert_eq!(assignment.agent_runtime_id, "b");
        assignment
            .validate_binding("a", "task", &execution_target)
            .unwrap();
        for target in [
            ExecutionTarget::host("a"),
            ExecutionTarget::sandbox("b", "host"),
        ] {
            assert!(assignment.validate_binding("a", "task", &target).is_err());
        }
        assert!(
            assignment
                .validate_binding("b", "task", &execution_target)
                .is_err()
        );
        let mut stored = serde_json::to_value(assignment).unwrap();
        stored["schema_version"] = json!(4);
        assert!(serde_json::from_value::<Assignment>(stored).is_err());
    }
}
