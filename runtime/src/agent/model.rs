//! Engine-independent, credential-free persisted values.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::path::PathBuf;

pub const SCHEMA_VERSION: u32 = 1;
pub const PI_VERSION: &str = "0.87.1";

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Engine {
    Pi,
    Codex,
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
    pub fn legacy(config: &crate::pi::Config) -> Self {
        Self {
            id: "pi-default".into(),
            revision: 1,
            name: "Pi · DeepSeek Flash".into(),
            engine: Engine::Pi,
            provider: "deepseek".into(),
            model: config.model.clone(),
            auth_ref: "pi.json:api_key".into(),
            params: Parameters {
                max_output_tokens: config.max_tokens,
                reasoning_effort: None,
            },
            tool_policy: ToolPolicy {
                worker_orchestration: false,
                summary_read_only: true,
                unattended: "guest_vm".into(),
            },
        }
    }
    pub fn validate(&self) -> Result<()> {
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
            self.params.reasoning_effort.is_none(),
            "Reasoning configuration is not accepted for this Pi route"
        );
        // Capability declarations must follow accepted transports, never UI guesses.
        ensure!(
            self.engine == Engine::Pi,
            "Codex has not passed guest acceptance"
        );
        ensure!(
            (self.provider == "deepseek" && self.auth_ref == "pi.json:api_key")
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
            engine_version: PI_VERSION.into(),
            provider: self.provider.clone(),
            model: self.model.clone(),
            auth_ref: self.auth_ref.clone(),
            params: self.params.clone(),
            tool_policy: self.tool_policy.clone(),
            gateway_protocol: "openai_chat".into(),
            context_window: 32768,
            capabilities: Capabilities {
                native_compaction: true,
                safe_checkpoint_resume: true,
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
        spec.spec_hash = spec.hash()?;
        Ok(spec)
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ResolvedSpec {
    pub engine: Engine,
    pub engine_version: String,
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
            matches!(protocol, "openai_chat" | "unknown"),
            "Unsupported gateway protocol"
        );
        self.gateway_protocol = protocol.into();
        self.capabilities.request_level_budget_enforcement = protocol == "openai_chat";
        self.capabilities.usage_reporting = protocol == "openai_chat";
        self.capabilities.safe_checkpoint_resume = protocol == "openai_chat";
        self.spec_hash = self.hash()?;
        Ok(())
    }
    fn hash(&self) -> Result<String> {
        let mut value = self.clone();
        value.spec_hash.clear();
        Ok(format!("{:x}", Sha256::digest(serde_json::to_vec(&value)?)))
    }
    pub fn validate(&self) -> Result<()> {
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
    Pi {
        file: PathBuf,
        session_id: Option<String>,
    },
    Codex {
        thread_id: String,
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
