//! Engine-independent, credential-free persisted values.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

pub const SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Parameters {
    pub max_output_tokens: u64,
    /// Provider-defined reasoning effort. None uses the provider default.
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
    pub provider: String,
    pub model: String,
    pub auth_ref: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub model_settings: Option<ModelSettings>,
    pub params: Parameters,
    pub tool_policy: ToolPolicy,
}
/// Editable model defaults. None inherits Control's local catalog; resolved specs
/// contain concrete values and never consult that catalog during execution.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ModelSettings {
    /// Some selects an exact provider; None searches by model ID, preferring its
    /// official provider before the first provider in catalog ID order.
    pub catalog_provider: Option<String>,
    pub context_window: Option<u64>,
    pub max_output_tokens: Option<u64>,
}
impl Profile {
    pub fn api(config: &impl ProfileConfig) -> Self {
        Self {
            id: "pot-default".into(),
            revision: 1,
            name: "Pot · DeepSeek Flash".into(),
            provider: "deepseek".into(),
            model: config.model().to_owned(),
            auth_ref: "provider:deepseek".into(),
            model_settings: None,
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
            (self.provider == "deepseek" && self.auth_ref == "provider:deepseek")
                || (self
                    .provider
                    .bytes()
                    .all(|c| c.is_ascii_alphanumeric() || c == b'-')
                    && !self.provider.is_empty()
                    && self.auth_ref == format!("provider:{}", self.provider)),
            "Unsupported credential route"
        );
        let mut spec = ResolvedSpec {
            provider: self.provider.clone(),
            model: self.model.clone(),
            auth_ref: self.auth_ref.clone(),
            params: self.params.clone(),
            tool_policy: self.tool_policy.clone(),
            gateway_protocol: "openai_chat".into(),
            context_window: self.params.agent.context_window,
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
    pub provider: String,
    pub model: String,
    pub auth_ref: String,
    pub params: Parameters,
    pub tool_policy: ToolPolicy,
    pub gateway_protocol: String,
    pub context_window: u64,
    pub source_profile_id: String,
    pub source_profile_revision: u64,
    pub spec_hash: String,
}
impl ResolvedSpec {
    /// Replace execution-time plugins while preserving the selected model configuration.
    pub fn set_plugins(
        &mut self,
        plugins: std::collections::BTreeMap<String, crate::plugins::PluginConfig>,
    ) -> Result<()> {
        self.params.agent.plugins = plugins;
        self.params.agent.validate()?;
        self.spec_hash = self.hash()?;
        Ok(())
    }
    pub fn supports_checkpoint(&self) -> bool {
        self.gateway_protocol != "unknown"
    }
    pub fn set_gateway_protocol(&mut self, protocol: &str) -> Result<()> {
        ensure!(
            matches!(
                protocol,
                "openai_chat" | "openai_responses" | "anthropic_messages" | "gemini" | "unknown"
            ),
            "Unsupported gateway protocol"
        );
        self.gateway_protocol = protocol.into();
        self.spec_hash = self.hash()?;
        Ok(())
    }
    fn hash(&self) -> Result<String> {
        self.payload_hash(true)
    }
    fn payload_hash(&self, canonical: bool) -> Result<String> {
        let mut value = self.clone();
        value.spec_hash.clear();
        let mut value = serde_json::to_value(value)?;
        if canonical {
            value.sort_all_objects();
        }
        Ok(Sha256::digest(serde_json::to_vec(&value)?)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect())
    }
    pub fn validate(&self) -> Result<()> {
        self.params.agent.validate()?;
        ensure!(
            self.hash()? == self.spec_hash || self.payload_hash(false)? == self.spec_hash,
            "Resolved spec checksum mismatch"
        );
        Ok(())
    }
}
/// A product-owned conversation. Configuration changes never replace its context.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Thread {
    pub owner: String,
    pub generation: u64,
    pub config: ResolvedSpec,
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
    fn provider_defined_reasoning_effort_is_preserved_in_execution_snapshots() {
        let mut profile = Profile::api(&Config);
        profile.params.reasoning_effort = Some("provider-specific-mode".into());
        let resolved = profile.resolve().unwrap();
        resolved.validate().unwrap();
        assert_eq!(
            resolved.params.reasoning_effort.as_deref(),
            Some("provider-specific-mode")
        );
        profile.params.reasoning_effort = None;
        assert_ne!(resolved.spec_hash, profile.resolve().unwrap().spec_hash);
    }

    #[test]
    fn checksum_is_independent_of_json_order_and_verifies_existing_data() {
        let mut original = spec();
        let canonical = original.hash().unwrap();
        let mut value = serde_json::to_value(&original).unwrap();
        value.sort_all_objects();
        let restored: ResolvedSpec = serde_json::from_value(value).unwrap();
        assert_eq!(restored.hash().unwrap(), canonical);
        original.spec_hash = original.payload_hash(false).unwrap();
        original.validate().unwrap();
        original.model = "changed".into();
        assert!(original.validate().is_err());
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
