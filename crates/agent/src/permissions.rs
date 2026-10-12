//! Owner approval requests. Machine capability decisions belong to Control.
use anyhow::{Result, ensure};
use pot_core::{ExecutionContext, ExecutionRole, Thread, TurnRequest};
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "snake_case")]
pub enum Capability {
    Host,
    Sandbox,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(deny_unknown_fields)]
pub struct Scope {
    pub machine_id: String,
    pub capability: Capability,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Grant {
    pub id: String,
    pub owner: String,
    pub scope: Scope,
}
#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DefaultDecision {
    #[default]
    Ask,
    Allow,
    Deny,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Rule {
    pub assistant_id: String,
    pub scope: Scope,
    pub decision: DefaultDecision,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    #[serde(default)]
    pub machine_epochs: std::collections::BTreeMap<String, String>,
    #[serde(default)]
    pub rules: Vec<Rule>,
    #[serde(default)]
    pub sandbox_limits: std::collections::BTreeMap<String, SandboxLimits>,
    #[serde(default)]
    pub sandboxes: Vec<SandboxAccess>,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SandboxLimits {
    pub max_sandboxes: u64,
    pub cpus: u64,
    pub memory_gib: u64,
    pub disk_gib: u64,
}
impl Default for SandboxLimits {
    fn default() -> Self {
        Self {
            max_sandboxes: 16,
            cpus: 4,
            memory_gib: 8,
            disk_gib: 32,
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SandboxAccess {
    pub machine_id: String,
    pub sandbox_id: String,
    pub owner: String,
    #[serde(default)]
    pub shared_with: Vec<String>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub version: u32,
    pub policy: Policy,
    pub grants: Vec<Grant>,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            version: 2,
            policy: Policy::default(),
            grants: vec![],
        }
    }
}
impl Config {
    pub fn from_grants(grants: &[Grant]) -> Self {
        Self {
            grants: grants.to_vec(),
            ..Self::default()
        }
    }
    pub fn into_grants(self) -> Result<Vec<Grant>> {
        ensure!(
            self.version == 2,
            "Unsupported permission format; configure machine capabilities afresh"
        );
        ensure!(
            self.grants.len() <= 1000 && self.policy.rules.len() <= 1000,
            "Permission limit reached"
        );
        let mut ids = std::collections::BTreeSet::new();
        let mut scopes = std::collections::BTreeSet::new();
        for g in &self.grants {
            validate_identity(&g.owner, &g.scope)?;
            ensure!(
                !g.id.is_empty() && ids.insert(&g.id) && scopes.insert((&g.owner, &g.scope)),
                "Duplicate or invalid grant"
            );
        }
        let mut rules = std::collections::BTreeSet::new();
        for r in &self.policy.rules {
            validate_identity(&r.assistant_id, &r.scope)?;
            ensure!(
                rules.insert((&r.assistant_id, &r.scope)),
                "Duplicate machine policy"
            );
        }
        let mut resources = std::collections::BTreeSet::new();
        for r in &self.policy.sandboxes {
            ensure!(
                !r.machine_id.is_empty()
                    && !r.sandbox_id.is_empty()
                    && !r.owner.is_empty()
                    && resources.insert((&r.machine_id, &r.sandbox_id)),
                "Invalid or duplicate sandbox ownership"
            );
        }
        for limit in self.policy.sandbox_limits.values() {
            ensure!(
                limit.max_sandboxes > 0
                    && limit.cpus > 0
                    && limit.memory_gib > 0
                    && limit.disk_gib > 0,
                "Sandbox limits must be positive"
            );
        }
        Ok(self.grants)
    }
}
fn validate_identity(owner: &str, scope: &Scope) -> Result<()> {
    ensure!(
        !owner.trim().is_empty() && !owner.contains('*') && !scope.machine_id.trim().is_empty(),
        "Explicit assistant and machine identities required"
    );
    Ok(())
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub owner: String,
    pub environment: String,
    pub generation: u64,
    pub turn: String,
    pub call_id: String,
    pub tool: String,
    pub args: Value,
}
pub fn is_execution_tool(name: &str) -> bool {
    ["exec_command", "write_stdin", "apply_patch"].contains(&name)
}
pub async fn authorize(
    context: &ExecutionContext,
    thread: &Thread,
    turn: &TurnRequest,
    call_id: &str,
    tool: &str,
    args: &Value,
) -> Result<()> {
    let instructions = super::environment_tools::instructions_tool(tool);
    let execution =
        is_execution_tool(tool) || thread.config.params.agent.plugin_for_tool(tool)?.is_some();
    let management = tool == "manage_sandbox";
    if instructions || management {
        ensure!(
            context.role == ExecutionRole::Main,
            "Only the owning assistant can manage resources and instructions"
        );
    }
    ensure!(
        context.role != ExecutionRole::Summary || (!execution && !management && !instructions),
        "Summary is read-only"
    );
    if tool == "read_agent_instructions" || (!execution && !management && !instructions) {
        return Ok(());
    }
    // Embedders without machine execution use their own contained executor.
    if !instructions && !context.feedback.owner_authorization_available()? {
        return Ok(());
    }
    let environment = match args.get("environment") {
        Some(Value::String(s)) => s.clone(),
        Some(v) => format!("remote:{}", serde_json::to_string(v)?),
        None => String::new(),
    };
    let request = Request {
        owner: context.owner.clone(),
        environment,
        generation: thread.generation,
        turn: turn.request_id.clone(),
        call_id: call_id.into(),
        tool: tool.into(),
        args: args.clone(),
    };
    let expected = serde_json::to_value(&request)?;
    let decision = tokio::select! {
        r = context.feedback.request_permission(expected.clone()) => r?,
        _ = context.feedback.cancellation() => anyhow::bail!("Permission request cancelled"),
    };
    ensure!(
        !context.feedback.cancelled()
            && decision["request"] == expected
            && decision["approved"] == true,
        "Owner denied permission"
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn old_operation_grants_are_rejected() {
        assert!(
            serde_json::from_value::<Config>(serde_json::json!({"version":1,"grants":[]})).is_err()
        );
        let g = Grant {
            id: "g".into(),
            owner: "assistant".into(),
            scope: Scope {
                machine_id: "machine".into(),
                capability: Capability::Sandbox,
            },
        };
        assert_eq!(
            Config::from_grants(std::slice::from_ref(&g))
                .into_grants()
                .unwrap(),
            vec![g]
        );
    }
}
