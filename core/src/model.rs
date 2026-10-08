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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub launch_spec: Option<LaunchSpec>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub protocol_contract: Option<ProtocolContract>,
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
            launch_spec: None,
            protocol_contract: None,
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
            self.engine != Engine::Pot || descriptor.supported_in_api,
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
        if matches!(self.engine, Engine::Codex) {
            return self.resolve_native();
        }
        self.launch_spec.clone().unwrap_or_default().validate()?;
        self.protocol_contract
            .clone()
            .unwrap_or_default()
            .validate()?;
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
            self.launch_spec.clone().unwrap_or_default().transport == "pot_agent"
                && self.protocol_contract.clone().unwrap_or_default().events == "pot_agent_v1",
            "Pot requires the built-in Agent contract"
        );
        ensure!(
            self.launch_spec
                .as_ref()
                .is_none_or(|v| v == &LaunchSpec::default()),
            "Pot Agent runs in-process; executable overrides are unsupported"
        );
        // Capability declarations follow the implemented execution backend.
        ensure!(
            self.engine == Engine::Pot,
            "Backend has not passed guest acceptance"
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
            engine_version: String::new(),
            launch_spec: Some(self.launch_spec.clone().unwrap_or_default()),
            protocol_contract: Some(self.protocol_contract.clone().unwrap_or_default()),
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
    fn resolve_native(&self) -> Result<ResolvedSpec> {
        let expected_auth = "cli:codex";
        let default_launch = LaunchSpec::codex();
        let default_protocol = ProtocolContract::codex();
        ensure!(
            self.auth_ref == expected_auth
                && !self.provider.is_empty()
                && self.provider.len() <= 128
                && self
                    .provider
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_'),
            "Native engines require their CLI login context"
        );
        let launch = self
            .launch_spec
            .clone()
            .unwrap_or_else(|| default_launch.clone());
        let protocol = self
            .protocol_contract
            .clone()
            .unwrap_or_else(|| default_protocol.clone());
        launch.validate()?;
        protocol.validate()?;
        ensure!(
            launch.transport == default_launch.transport
                && protocol.events == default_protocol.events,
            "Native engine transport contract mismatch"
        );
        let mut spec = ResolvedSpec {
            engine: self.engine.clone(),
            engine_version: String::new(),
            launch_spec: Some(launch),
            protocol_contract: Some(protocol),
            provider: self.provider.clone(),
            model: self.model.clone(),
            auth_ref: self.auth_ref.clone(),
            params: self.params.clone(),
            tool_policy: self.tool_policy.clone(),
            gateway_protocol: "native_reported".into(),
            context_window: 0,
            capabilities: Capabilities {
                native_compaction: true,
                usage_reporting: true,
                tool_policy_enforcement: true,
                mcp_tools: true,
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
    /// Legacy diagnostic only; excluded from new checksums and never gates execution.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub engine_version: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub launch_spec: Option<LaunchSpec>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub protocol_contract: Option<ProtocolContract>,
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
        let mut value = serde_json::to_value(value)?;
        value.as_object_mut().unwrap().remove("engine_version");
        Ok(Sha256::digest(serde_json::to_vec(&value)?)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect())
    }
    pub fn validate(&self) -> Result<()> {
        self.params.agent.validate()?;
        self.launch_spec.clone().unwrap_or_default().validate()?;
        self.protocol_contract
            .clone()
            .unwrap_or_default()
            .validate()?;
        let launch = self.launch_spec.clone().unwrap_or_default();
        let protocol = self.protocol_contract.clone().unwrap_or_default();
        ensure!(
            match self.engine {
                Engine::Pot => launch.transport == "pot_agent" && protocol.events == "pot_agent_v1",
                Engine::Codex =>
                    launch.transport == "codex_app_server"
                        && protocol.events == "codex_app_server_v2",
            },
            "Engine and transport contract mismatch"
        );
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
    Codex {
        thread_id: Option<String>,
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

/// One typed expansion carries all runtime-owned identity and role policy.
/// Literal arguments are direct argv entries, never shell text.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", content = "value", rename_all = "snake_case")]
pub enum LaunchArgument {
    Literal(String),
    RuntimeArguments,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct LaunchSpec {
    pub transport: String,
    pub executable: String,
    pub argv: Vec<LaunchArgument>,
    pub cwd_policy: String,
}
impl Default for LaunchSpec {
    fn default() -> Self {
        Self {
            transport: "pot_agent".into(),
            executable: "pot-agent".into(),
            argv: vec![LaunchArgument::RuntimeArguments],
            cwd_policy: "workspace".into(),
        }
    }
}
impl LaunchSpec {
    pub fn codex() -> Self {
        Self {
            transport: "codex_app_server".into(),
            executable: "codex".into(),
            ..Default::default()
        }
    }
    pub fn validate(&self) -> Result<()> {
        ensure!(
            matches!(self.transport.as_str(), "pot_agent" | "codex_app_server"),
            "Transport has not passed driver acceptance"
        );
        ensure!(
            self.cwd_policy == "workspace",
            "Only workspace cwd is supported"
        );
        ensure!(
            !self.executable.is_empty()
                && self.executable.len() <= 4096
                && !self.executable.contains('\0'),
            "Invalid executable path"
        );
        ensure!(
            self.argv.len() <= 64
                && self
                    .argv
                    .iter()
                    .filter(|a| matches!(a, LaunchArgument::RuntimeArguments))
                    .count()
                    == 1,
            "Invocation must contain exactly one runtime_arguments placeholder"
        );
        let reserved = [
            "--mode",
            "--provider",
            "--model",
            "--thinking",
            "--tools",
            "--no-tools",
            "--extension",
            "-e",
            "--no-extensions",
            "--no-skills",
            "--skill",
            "--no-prompt-templates",
            "--prompt-template",
            "--no-context-files",
            "--append-system-prompt",
            "--system-prompt",
            "--no-approve",
            "--session",
            "--session-dir",
            "--no-session",
            "--continue",
            "-c",
            "--resume",
            "-r",
            "--api-key",
            "--print",
            "-p",
            "--",
            "--help",
            "-h",
            "--version",
            "-v",
        ];
        let native_reserved = [
            "--conversation",
            "--project",
            "--new-project",
            "--prompt",
            "--prompt-interactive",
            "-i",
            "--print-timeout",
            "--log-file",
            "--remote-control",
            "--config",
            "--profile",
            "--enable",
            "--disable",
            "--sandbox",
            "-s",
            "--ask-for-approval",
            "-a",
            "--dangerously-bypass-approvals-and-sandbox",
            "--dangerously-skip-permissions",
            "--allow-dangerously-skip-permissions",
            "--permission-mode",
            "--permission-prompt-tool",
            "--allowedTools",
            "--allowed-tools",
            "--disallowedTools",
            "--disallowed-tools",
            "--mcp-config",
            "--strict-mcp-config",
            "--mcp-debug",
            "--settings",
            "--setting-sources",
            "--agent",
            "--agents",
            "--plugin-dir",
            "--input-format",
            "--output-format",
            "--include-partial-messages",
            "--json-schema",
            "--session-id",
            "--fork-session",
            "--no-session-persistence",
            "--resume-session-at",
            "--fallback-model",
            "--effort",
            "--max-turns",
            "--max-budget-usd",
            "--system-prompt-snapshot",
            "--append-system-prompt-file",
            "--system-prompt-file",
            "--add-dir",
            "--chrome",
            "--no-chrome",
            "--dangerously-load-development-channels",
            "--channels",
            "--teammate-mode",
            "--team-name",
            "--agent-id",
            "--permission-prompt-target",
            "--remote",
            "--teleport",
            "--worktree",
            "-w",
            "--listen",
            "--auth-file",
            "--login-server",
            "--api-key",
            "--oss",
            "--local-provider",
            "--disable-slash-commands",
            "--no-prompt-suggestions",
            "--file",
        ];
        for arg in &self.argv {
            if let LaunchArgument::Literal(value) = arg {
                ensure!(
                    value.len() <= 4096 && !value.contains('\0'),
                    "Invalid literal argument"
                );
                let flag = value.split('=').next().unwrap_or_default();
                let native = self.transport != "pot_agent";
                ensure!(
                    !reserved
                        .iter()
                        .chain(native_reserved.iter().filter(|_| native))
                        .any(|blocked| {
                            flag == *blocked
                                || (blocked.len() == 2
                                    && *blocked != "--"
                                    && value.starts_with(blocked))
                        }),
                    "Invocation conflicts with a Runtime-owned protocol, session, input or policy argument"
                );
            }
        }
        Ok(())
    }
    pub fn expand(&self, owned: &[String]) -> Result<Vec<String>> {
        self.validate()?;
        Ok(self
            .argv
            .iter()
            .flat_map(|arg| match arg {
                LaunchArgument::Literal(value) => vec![value.clone()],
                LaunchArgument::RuntimeArguments => owned.to_vec(),
            })
            .collect())
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ProtocolContract {
    pub events: String,
    pub native_session: String,
}
impl Default for ProtocolContract {
    fn default() -> Self {
        Self {
            events: "pot_agent_v1".into(),
            native_session: "pot_context_v1".into(),
        }
    }
}
impl ProtocolContract {
    pub fn codex() -> Self {
        Self {
            events: "codex_app_server_v2".into(),
            native_session: "codex_thread".into(),
        }
    }
    pub fn validate(&self) -> Result<()> {
        ensure!(
            (self.events == "pot_agent_v1" && self.native_session == "pot_context_v1")
                || (self.events == "codex_app_server_v2" && self.native_session == "codex_thread"),
            "Unsupported protocol/session contract; restore the command or migrate explicitly"
        );
        Ok(())
    }
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
    fn version_is_diagnostic_and_command_is_identity() {
        let mut spec = spec();
        let original = spec.spec_hash.clone();
        spec.engine_version = "a compatible future release".into();
        spec.validate().unwrap();
        assert_eq!(spec.hash().unwrap(), original);
        spec.launch_spec = Some(LaunchSpec {
            executable: "/opt/my wrapper".into(),
            ..Default::default()
        });
        assert!(spec.validate().is_err());
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
