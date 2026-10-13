//! Execution location is independent of Agent placement and transport.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};

/// The destination resolves this target. Host is never a Sandbox identifier.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ExecutionTargetKind {
    Host {},
    Sandbox { sandbox_id: String },
}

/// Remote is a relationship to the caller, not an Execution target kind.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(deny_unknown_fields)]
pub struct ExecutionTarget {
    pub runtime_id: String,
    pub target: ExecutionTargetKind,
}
impl ExecutionTarget {
    pub fn host(runtime_id: &str) -> Self {
        Self {
            runtime_id: runtime_id.into(),
            target: ExecutionTargetKind::Host {},
        }
    }
    pub fn sandbox(runtime_id: &str, sandbox_id: &str) -> Self {
        Self {
            runtime_id: runtime_id.into(),
            target: ExecutionTargetKind::Sandbox {
                sandbox_id: sandbox_id.into(),
            },
        }
    }
    pub fn validate(&self) -> Result<()> {
        identifier(&self.runtime_id)?;
        if let ExecutionTargetKind::Sandbox { sandbox_id } = &self.target {
            identifier(sandbox_id)?;
        }
        Ok(())
    }
    pub fn is_local_to(&self, runtime_id: &str) -> bool {
        self.runtime_id == runtime_id
    }
}
fn identifier(value: &str) -> Result<()> {
    ensure!(
        !value.is_empty()
            && value.len() <= 128
            && value
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_' | b'.')),
        "Invalid Execution target identity"
    );
    ensure!(
        value != "." && value != "..",
        "Invalid Execution target identity"
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn host_and_sandbox_references_never_alias() {
        let host = ExecutionTarget::host("runtime-a");
        let sandbox = ExecutionTarget::sandbox("runtime-a", "host");
        assert_ne!(host, sandbox);
        assert_ne!(host, ExecutionTarget::host("runtime-b"));
        assert_eq!(
            serde_json::to_value(&host).unwrap(),
            json!({"runtime_id":"runtime-a","target":{"kind":"host"}})
        );
        assert!(host.is_local_to("runtime-a"));
        assert!(!host.is_local_to("runtime-b"));
    }
    #[test]
    fn malformed_targets_and_path_identifiers_are_rejected() {
        for value in [
            json!({"runtime_id":"a","target":{"kind":"host","sandbox_id":"b"}}),
            json!({"runtime_id":"a","target":{"kind":"sandbox"}}),
            json!({"runtime_id":"a","target":{"kind":"remote","sandbox_id":"b"}}),
        ] {
            assert!(serde_json::from_value::<ExecutionTarget>(value).is_err());
        }
        for id in ["", "..", "a/b", "a\\b", "a\n"] {
            assert!(
                ExecutionTarget::sandbox("runtime-a", id)
                    .validate()
                    .is_err()
            );
        }
    }
}
