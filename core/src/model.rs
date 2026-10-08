//! Engine-independent, credential-free persisted values.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::path::PathBuf;

pub const SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Engine {
    Pot,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct Capabilities {
    pub native_compaction: bool,
    pub safe_checkpoint_resume: bool,
    pub live_steering: bool,
    pub model_switch: bool,
    pub usage_reporting: bool,
    pub request_level_budget_enforcement: bool,
    pub tool_policy_enforcement: bool,
    pub mcp_tools: bool,
    pub subagents_disableable: bool,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Parameters {
    pub max_output_tokens: u64,
    pub reasoning_effort: Option<String>,
    #[serde(default)]
    pub agent: crate::AgentSettings,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ToolPolicy {
    pub worker_orchestration: bool,
    pub summary_read_only: bool,
    pub unattended: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Profile {
    pub id: String,
    pub revision: u64,
    pub name: String,
    pub engine: Engine,
    pub provider: String,
    pub model: String,
    pub auth_ref: String,
    pub params: Parameters,
    pub tool_policy: ToolPolicy,
}
impl Profile {
    pub fn api(config: &impl ProfileConfig) -> Self {
        Self {
            id: "pot-default".into(),
            revision: 1,
            name: "Pot · DeepSeek Flash".into(),
            engine: Engine::Pot,
            provider: "deepseek".into(),
            model: config.model().to_owned(),
            auth_ref: "providers.json:deepseek".into(),
            params: Parameters {
                max_output_tokens: config.max_tokens(),
                reasoning_effort: None,
                agent: config.agent_settings(),
            },
            tool_policy: ToolPolicy {
                worker_orchestration: false,
                summary_read_only: true,
                unattended: "guest_vm".into(),
            },
        }
    }
    pub fn validate(&self) -> Result<()> {
        self.params.agent.validate()?;
        let descriptor = self
            .params
            .agent
            .model_descriptor(&self.provider, &self.model)?;
        ensure!(
            descriptor.supported_in_api,
            "Configured model does not support API execution"
        );
        ensure!(
            !self.id.is_empty() && self.id.len() <= 128 && self.revision > 0,
            "Invalid profile identity"
        );
        ensure!(
            !self.name.is_empty() && self.name.len() <= 256,
            "Invalid profile name"
        );
        ensure!(
            !self.model.is_empty() && self.model.len() <= 256,
            "Invalid model"
        );
        ensure!(
            (1..=9_007_199_254_740_991).contains(&self.params.max_output_tokens),
            "Invalid output limit"
        );
        ensure!(
            !self.tool_policy.worker_orchestration && self.tool_policy.summary_read_only,
            "Unsupported tool policy"
        );
        ensure!(
            self.tool_policy.unattended == "guest_vm",
            "Unsupported unattended policy"
        );
        Ok(())
    }
    pub fn resolve(&self) -> Result<ResolvedSpec> {
        self.validate()?;
        ensure!(
            self.params
                .reasoning_effort
                .as_deref()
                .is_none_or(|v| matches!(
                    v,
                    "none" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max"
                )),
            "Invalid reasoning effort"
        );
        ensure!(
            (self.provider == "deepseek" && self.auth_ref == "providers.json:deepseek")
                || (self
                    .provider
                    .bytes()
                    .all(|c| c.is_ascii_alphanumeric() || c == b'-')
                    && !self.provider.is_empty()
                    && self.auth_ref == format!("providers.json:{}", self.provider)),
            "Unsupported credential route"
        );
        let mut spec = ResolvedSpec {
            engine: self.engine.clone(),
            provider: self.provider.clone(),
            model: self.model.clone(),
            auth_ref: self.auth_ref.clone(),
            params: self.params.clone(),
            tool_policy: self.tool_policy.clone(),
            gateway_protocol: "openai_chat".into(),
            context_window: self.params.agent.context_window,
            capabilities: Capabilities {
                native_compaction: true,
                safe_checkpoint_resume: true,
                live_steering: true,
                usage_reporting: true,
                request_level_budget_enforcement: true,
                tool_policy_enforcement: true,
                subagents_disableable: true,
                ..Default::default()
            },
            source_profile_id: self.id.clone(),
            source_profile_revision: self.revision,
            spec_hash: String::new(),
        };
        spec.params.agent.pin_plugins()?;
        spec.spec_hash = spec.hash()?;
        Ok(spec)
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ResolvedSpec {
    pub engine: Engine,
    pub provider: String,
    pub model: String,
    pub auth_ref: String,
    pub params: Parameters,
    pub tool_policy: ToolPolicy,
    pub gateway_protocol: String,
    pub context_window: u64,
    pub capabilities: Capabilities,
    pub source_profile_id: String,
    pub source_profile_revision: u64,
    pub spec_hash: String,
}
impl ResolvedSpec {
    pub fn set_gateway_protocol(&mut self, protocol: &str) -> Result<()> {
        ensure!(
            matches!(
                protocol,
                "openai_chat" | "openai_responses" | "anthropic_messages" | "gemini" | "unknown"
            ),
            "Unsupported gateway protocol"
        );
        self.gateway_protocol = protocol.into();
        self.capabilities.request_level_budget_enforcement = protocol != "unknown";
        self.capabilities.usage_reporting = protocol != "unknown";
        self.capabilities.safe_checkpoint_resume = protocol != "unknown";
        self.spec_hash = self.hash()?;
        Ok(())
    }
    fn hash(&self) -> Result<String> {
        let mut value = self.clone();
        value.spec_hash.clear();
        let value = serde_json::to_value(value)?;
        Ok(Sha256::digest(serde_json::to_vec(&value)?)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect())
    }
    pub fn validate(&self) -> Result<()> {
        self.params.agent.validate()?;
        ensure!(
            self.hash()? == self.spec_hash,
            "Resolved spec checksum mismatch"
        );
        Ok(())
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "engine", rename_all = "snake_case")]
pub enum NativeRef {
    Pot {
        file: PathBuf,
        session_id: Option<String>,
    },
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum BindingState {
    Active,
    Archived,
    Broken,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ModelSwitch {
    pub from_turn: String,
    pub model: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Binding {
    pub pot_owner: String,
    pub epoch: u64,
    pub spec: ResolvedSpec,
    pub native_ref: NativeRef,
    pub schema_version: u32,
    pub state: BindingState,
    pub model_switches: Vec<ModelSwitch>,
    pub handoff_ref: Option<PathBuf>,
    pub failure: Option<String>,
}

/// Non-secret settings for constructing an API profile.
pub trait ProfileConfig {
    fn model(&self) -> &str;
    fn max_tokens(&self) -> u64;
    fn agent_settings(&self) -> crate::AgentSettings {
        Default::default()
    }
}

#[cfg(test)]
mod checksum_tests {
    use super::*;
    struct Config;
    impl ProfileConfig for Config {
        fn model(&self) -> &str {
            "model"
        }
        fn max_tokens(&self) -> u64 {
            100
        }
    }
    fn spec() -> ResolvedSpec {
        Profile::api(&Config).resolve().unwrap()
    }

    #[test]
    fn checksum_detects_context_and_model_changes() {
        let mut spec = spec();
        spec.model = "tampered".into();
        assert!(spec.validate().is_err());
    }
    #[test]
    fn shell_settings_round_trip_and_policy_changes_are_detected() {
        let mut original = spec();
        original.params.agent.shell.allow_login_shell = false;
        original.params.agent.shell.shell_snapshot = false;
        original.spec_hash = original.hash().unwrap();
        let serialized = serde_json::to_value(&original).unwrap();
        let shell = &serialized["params"]["agent"]["shell"];
        assert_eq!(shell["allow_login_shell"], false);
        assert_eq!(shell["shell_snapshot"], false);
        assert_eq!(shell["session_idle_timeout_ms"], 1_800_000);
        let mut restored: ResolvedSpec = serde_json::from_value(serialized).unwrap();
        restored.validate().unwrap();
        assert!(!restored.params.agent.shell.allow_login_shell);
        assert!(!restored.params.agent.shell.shell_snapshot);
        restored.params.agent.shell.allow_login_shell = true;
        assert!(restored.validate().is_err());
    }
    #[test]
    fn omitted_shell_settings_use_the_current_defaults() {
        let settings: crate::ShellSettings = serde_json::from_value(serde_json::json!({})).unwrap();
        assert_eq!(settings, crate::ShellSettings::default());
        assert_eq!(settings.executable, "/bin/bash");
        assert!(settings.allow_login_shell);
        assert!(settings.shell_snapshot);
    }
}
