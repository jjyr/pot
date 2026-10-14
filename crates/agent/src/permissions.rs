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
#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DefaultDecision {
    #[default]
    Ask,
    Allow,
    Deny,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    #[serde(default)]
    pub machines: std::collections::BTreeMap<String, MachinePolicy>,
    #[serde(default)]
    pub machine_epochs: std::collections::BTreeMap<String, String>,
}
#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ExecutionMode {
    #[default]
    Sandbox,
    Host,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct MachinePolicy {
    pub execution_mode: ExecutionMode,
    pub host_access: DefaultDecision,
}
impl Default for MachinePolicy {
    fn default() -> Self {
        Self {
            execution_mode: ExecutionMode::Sandbox,
            host_access: DefaultDecision::Ask,
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SandboxLimits {
    pub cpus: u64,
    pub memory_gib: u64,
    pub disk_gib: u64,
}
impl Default for SandboxLimits {
    fn default() -> Self {
        Self {
            cpus: 4,
            memory_gib: 8,
            disk_gib: 32,
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub version: u32,
    pub policy: Policy,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            version: 4,
            policy: Policy::default(),
        }
    }
}
impl Config {
    pub fn validate(&self) -> Result<()> {
        ensure!(
            self.version == 4,
            "Unsupported permission format; configure machine execution policies afresh"
        );
        for (machine, policy) in &self.policy.machines {
            ensure!(!machine.trim().is_empty(), "Machine identity required");
            ensure!(
                policy.execution_mode != ExecutionMode::Host
                    || policy.host_access != DefaultDecision::Deny,
                "Host cannot be the default execution mode while Host operations are forbidden"
            );
        }
        Ok(())
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub owner: String,
    pub execution_target: String,
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
    let instructions = super::instruction_tools::instructions_tool(tool);
    let execution =
        is_execution_tool(tool) || thread.config.params.agent.plugin_for_tool(tool)?.is_some();
    if instructions {
        ensure!(
            context.role == ExecutionRole::Main,
            "Only the owning assistant can edit instructions"
        );
    }
    ensure!(
        context.role != ExecutionRole::Summary || (!execution && !instructions),
        "Summary is read-only"
    );
    if tool == "read_agent_instructions" || (!execution && !instructions) {
        return Ok(());
    }
    // Embedders without machine execution use their own contained executor.
    if !instructions && !context.feedback.owner_authorization_available()? {
        return Ok(());
    }
    let execution_target = match args.get("execution_target") {
        Some(Value::String(s)) => s.clone(),
        Some(v) => format!("remote:{}", serde_json::to_string(v)?),
        None => String::new(),
    };
    let request = Request {
        owner: context.owner.clone(),
        execution_target,
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
    fn policies_are_machine_scoped_and_legacy_grants_are_rejected() {
        assert!(Config::default().validate().is_ok());
        assert!(
            serde_json::from_value::<Config>(
                serde_json::json!({"version":2,"policy":{},"grants":[]})
            )
            .is_err()
        );
        let mut config = Config::default();
        config.policy.machines.insert(
            "devpc".into(),
            MachinePolicy {
                execution_mode: ExecutionMode::Host,
                host_access: DefaultDecision::Deny,
            },
        );
        assert!(config.validate().is_err());
    }
}
