//! Immutable placement values; no transport or filesystem behavior.
use crate::EnvironmentRef;
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
    pub environment: Option<EnvironmentRef>,
    pub workspace_scope: String,
}
impl Assignment {
    pub fn local(runtime_id: &str, task_id: &str, environment_id: &str) -> Self {
        Self {
            schema_version: 3,
            owner: ObjectRef {
                runtime_id: runtime_id.into(),
                object_id: task_id.into(),
            },
            dispatch_origin_runtime: runtime_id.into(),
            agent_runtime_id: runtime_id.into(),
            environment: Some(EnvironmentRef::sandbox(runtime_id, environment_id)),
            workspace_scope: format!("tasks/{task_id}"),
        }
    }
    pub fn on_environment(runtime_id: &str, task_id: &str, environment: EnvironmentRef) -> Self {
        let mut assignment = Self::local(runtime_id, task_id, "default");
        assignment.agent_runtime_id = environment.runtime_id.clone();
        assignment.environment = Some(environment);
        assignment
    }
    pub fn validate_binding(
        &self,
        runtime_id: &str,
        task_id: &str,
        environment: &EnvironmentRef,
    ) -> Result<()> {
        environment.validate()?;
        ensure!(
            self == &Self::on_environment(runtime_id, task_id, environment.clone()),
            "Unsupported or conflicting Task assignment"
        );
        Ok(())
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
            environment: Option<serde_json::Value>,
            workspace_scope: String,
        }
        let stored = Stored::deserialize(deserializer)?;
        let environment = match stored.schema_version {
            1 => stored
                .environment
                .map(|value| {
                    serde_json::from_value::<ObjectRef>(value)
                        .map(|v| EnvironmentRef::sandbox(&v.runtime_id, &v.object_id))
                })
                .transpose(),
            2 | 3 => stored.environment.map(serde_json::from_value).transpose(),
            _ => return Err(serde::de::Error::custom("Unsupported Assignment schema")),
        }
        .map_err(serde::de::Error::custom)?;
        // Current persisted assignments predate destination-local Task Agents.
        // Upgrade their placement only; retain owner, target and workspace identity.
        let agent_runtime_id =
            if stored.schema_version < 3 && stored.agent_runtime_id == stored.owner.runtime_id {
                environment
                    .as_ref()
                    .map_or(stored.agent_runtime_id.clone(), |e| e.runtime_id.clone())
            } else {
                stored.agent_runtime_id
            };
        Ok(Self {
            schema_version: 3,
            owner: stored.owner,
            dispatch_origin_runtime: stored.dispatch_origin_runtime,
            agent_runtime_id,
            environment,
            workspace_scope: stored.workspace_scope,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn version_one_assignment_upgrades_without_rebinding() {
        let value = json!({"schema_version":1,"owner":{"runtime_id":"a","object_id":"task"},"dispatch_origin_runtime":"a","agent_runtime_id":"a","environment":{"runtime_id":"b","object_id":"default"},"workspace_scope":"tasks/task"});
        let assignment: Assignment = serde_json::from_value(value).unwrap();
        assert_eq!(assignment.schema_version, 3);
        assert_eq!(assignment.agent_runtime_id, "b");
        assignment
            .validate_binding("a", "task", &EnvironmentRef::sandbox("b", "default"))
            .unwrap();
        assert!(assignment.validate_local("a", "task", "default").is_err());
        let stored = serde_json::to_value(&assignment).unwrap();
        assert_eq!(stored["environment"]["target"]["kind"], "sandbox");
        assert_eq!(
            serde_json::from_value::<Assignment>(stored).unwrap(),
            assignment
        );
    }
    #[test]
    fn current_remote_assignment_upgrade_changes_only_agent_placement() {
        let original =
            Assignment::on_environment("control", "task", EnvironmentRef::host("machine"));
        let mut value = serde_json::to_value(&original).unwrap();
        value["schema_version"] = json!(2);
        value["agent_runtime_id"] = json!("control");
        let upgraded: Assignment = serde_json::from_value(value.clone()).unwrap();
        assert_eq!(upgraded, original);
        value["agent_runtime_id"] = json!("third-machine");
        let invalid: Assignment = serde_json::from_value(value).unwrap();
        assert!(
            invalid
                .validate_binding("control", "task", &EnvironmentRef::host("machine"))
                .is_err()
        );
    }
    #[test]
    fn typed_binding_rejects_host_remote_and_owner_substitution() {
        let environment = EnvironmentRef::host("b");
        let assignment = Assignment::on_environment("a", "task", environment.clone());
        assert_eq!(assignment.agent_runtime_id, "b");
        assignment
            .validate_binding("a", "task", &environment)
            .unwrap();
        for target in [
            EnvironmentRef::host("a"),
            EnvironmentRef::sandbox("b", "host"),
        ] {
            assert!(assignment.validate_binding("a", "task", &target).is_err());
        }
        assert!(
            assignment
                .validate_binding("b", "task", &environment)
                .is_err()
        );
        let mut stored = serde_json::to_value(assignment).unwrap();
        stored["schema_version"] = json!(4);
        assert!(serde_json::from_value::<Assignment>(stored).is_err());
    }
}
