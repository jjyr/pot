//! Serializable policy for the Pot-owned loop, copied into each Thread configuration.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct AgentSettings {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model_catalog: Option<crate::ModelCatalog>,
    #[serde(skip_serializing_if = "crate::plugins::is_default")]
    pub plugins: std::collections::BTreeMap<String, crate::plugins::PluginConfig>,
    pub context_window: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_input_tokens: Option<u64>,
    /// None derives the threshold from the context window and output reservation.
    pub compact_at: Option<u64>,
    pub auto_compact: bool,
    /// Calibrate unchanged history using complete provider-reported usage.
    pub use_provider_usage: bool,
    pub context_reserve: u64,
    pub system_prompt: Option<String>,
    pub builtin_tools: Vec<String>,
    /// None exposes all role-authorized extension/MCP tools; Some narrows them.
    pub allowed_tools: Option<Vec<String>>,
    pub request_timeout_ms: u64,
    pub shell: ShellSettings,
}
impl Default for AgentSettings {
    fn default() -> Self {
        Self {
            model_catalog: None,
            plugins: crate::plugins::defaults(),
            context_window: 32768,
            max_input_tokens: None,
            compact_at: None,
            auto_compact: true,
            use_provider_usage: true,
            context_reserve: 4096,
            system_prompt: None,
            builtin_tools: vec![
                "exec_command".into(),
                "write_stdin".into(),
                "apply_patch".into(),
                "exec".into(),
                "wait".into(),
            ],
            allowed_tools: None,
            request_timeout_ms: 180000,
            shell: ShellSettings::default(),
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct ShellSettings {
    pub executable: String,
    pub allow_login_shell: bool,
    pub shell_snapshot: bool,
    pub session_idle_timeout_ms: u64,
    pub default_timeout_ms: u64,
    pub max_timeout_ms: u64,
    pub default_output_bytes: usize,
    pub max_output_bytes: usize,
    pub max_sessions: usize,
    pub default_yield_ms: u64,
    pub max_yield_ms: u64,
}
impl Default for ShellSettings {
    fn default() -> Self {
        Self {
            executable: "/bin/bash".into(),
            allow_login_shell: true,
            shell_snapshot: true,
            session_idle_timeout_ms: 1_800_000,
            default_timeout_ms: 120000,
            max_timeout_ms: 300000,
            default_output_bytes: 16384,
            max_output_bytes: 65536,
            max_sessions: 8,
            default_yield_ms: 1000,
            max_yield_ms: 10000,
        }
    }
}
impl AgentSettings {
    pub fn model_descriptor(&self, provider: &str, model: &str) -> Result<crate::ModelDescriptor> {
        self.model_catalog.as_ref().map_or_else(
            || Ok(crate::ModelDescriptor::default()),
            |catalog| catalog.resolve(provider, model),
        )
    }
    pub fn validate(&self) -> Result<()> {
        self.validate_plugins()?;
        if let Some(catalog) = &self.model_catalog {
            catalog.validate()?;
        }
        ensure!(self.context_window > 0, "Context window must be positive");
        ensure!(
            self.max_input_tokens.is_none_or(|n| n > 0),
            "Input limit must be positive"
        );
        ensure!(
            self.context_reserve < self.context_window,
            "Context reserve exceeds window"
        );
        ensure!(
            self.compact_at
                .is_none_or(|n| n > 0 && n < self.context_window),
            "Invalid compaction threshold"
        );
        ensure!(
            self.request_timeout_ms > 0,
            "Request timeout must be positive"
        );
        ensure!(
            self.builtin_tools.iter().all(|n| matches!(
                n.as_str(),
                "exec_command" | "write_stdin" | "apply_patch" | "exec" | "wait"
            )),
            "Unknown built-in tool"
        );
        ensure!(
            !self
                .builtin_tools
                .iter()
                .enumerate()
                .any(|(i, n)| self.builtin_tools[..i].contains(n)),
            "Duplicate built-in tool"
        );
        let shell = &self.shell;
        ensure!(
            !shell.executable.is_empty()
                && shell.default_timeout_ms > 0
                && shell.default_timeout_ms <= shell.max_timeout_ms,
            "Invalid shell timeout/executable"
        );
        ensure!(
            shell.default_output_bytes > 0
                && shell.default_output_bytes <= shell.max_output_bytes
                && shell.max_output_bytes <= isize::MAX as usize,
            "Invalid shell output limit"
        );
        ensure!(
            shell.max_sessions > 0
                && shell.session_idle_timeout_ms > 0
                && shell.default_yield_ms > 0
                && shell.default_yield_ms <= shell.max_yield_ms,
            "Invalid shell session/yield limit"
        );
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn settings_are_serializable_and_reject_impossible_limits() {
        let mut settings = AgentSettings {
            context_window: 1_000_000,
            compact_at: Some(900_000),
            builtin_tools: vec![],
            allowed_tools: Some(vec!["application_tool".into()]),
            ..Default::default()
        };
        settings.shell.max_timeout_ms = 3_600_000;
        settings.shell.max_output_bytes = 2_000_000;
        settings.validate().unwrap();
        assert_eq!(
            serde_json::from_value::<AgentSettings>(serde_json::to_value(&settings).unwrap())
                .unwrap(),
            settings
        );
        settings.compact_at = Some(settings.context_window);
        assert!(settings.validate().is_err());
        settings.compact_at = None;
        settings.builtin_tools = vec!["unknown".into()];
        assert!(settings.validate().is_err());
        assert!(
            serde_json::from_value::<AgentSettings>(serde_json::json!({"typo_limit": 1})).is_err()
        );
    }
}
