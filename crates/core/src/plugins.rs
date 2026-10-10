//! Versioned Sandbox plugin packages. Resolved configuration snapshots contain immutable bundles.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Component, Path, PathBuf},
};

/// Declarative owner-facing approval text. Never executes plugin code before approval.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ApprovalDisplay {
    pub summary: BTreeMap<String, String>,
    #[serde(default)]
    pub labels: BTreeMap<String, BTreeMap<String, String>>,
}
impl ApprovalDisplay {
    fn validate(&self, properties: &Value) -> Result<()> {
        fn text(values: &BTreeMap<String, String>) -> bool {
            values.get("en").is_some_and(|s| !s.trim().is_empty())
                && values.len() <= 16
                && values.iter().all(|(locale, s)| {
                    locale.len() <= 32 && !s.trim().is_empty() && s.len() <= 2048
                })
        }
        ensure!(text(&self.summary), "Invalid plugin approval summary");
        ensure!(
            self.labels.len() <= 32
                && self
                    .labels
                    .iter()
                    .all(|(key, label)| properties.get(key).is_some() && text(label)),
            "Invalid plugin approval labels"
        );
        Ok(())
    }
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PluginManifest {
    pub schema_version: u32,
    pub id: String,
    pub version: String,
    pub runtime: String,
    pub execution: String,
    pub entrypoint: String,
    pub files: Vec<String>,
    #[serde(default)]
    pub requirements: Vec<String>,
    pub tools: Vec<Value>,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PluginBundle {
    pub manifest: PluginManifest,
    pub files: BTreeMap<String, String>,
    pub digest: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PluginIdentity {
    pub id: String,
    pub version: String,
    pub digest: String,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct PluginConfig {
    pub enabled: bool,
    /// Imported only when owner settings are saved; removed from pinned configuration snapshots.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source: Option<PathBuf>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub bundle: Option<PluginBundle>,
}
impl Default for PluginConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            source: None,
            bundle: None,
        }
    }
}
pub fn defaults() -> BTreeMap<String, PluginConfig> {
    BTreeMap::from([("computer-use".into(), PluginConfig::default())])
}
pub fn is_default(plugins: &BTreeMap<String, PluginConfig>) -> bool {
    plugins == &defaults()
}
fn safe_path(path: &str) -> bool {
    !path.is_empty()
        && !path.contains(['\\', '\0'])
        && Path::new(path)
            .components()
            .all(|c| matches!(c, Component::Normal(_)))
}
/// Candidate names for owner-defined plugin grants; dispatch still requires a bound tool.
pub fn is_plugin_tool_name(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 64
        && name.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'_')
        && !matches!(
            name,
            "exec_command"
                | "write_stdin"
                | "apply_patch"
                | "plugin_call"
                | "browser"
                | "exec"
                | "wait"
                | "set_sandbox_mappings"
                | "read_agent_instructions"
                | "update_agent_instructions"
        )
        && !name.starts_with("pot_")
        && !serde_json::from_str::<Vec<Value>>(crate::TOOL_SCHEMA)
            .expect("core schemas")
            .iter()
            .any(|tool| tool["name"] == name)
}
impl PluginBundle {
    pub fn identity(&self) -> PluginIdentity {
        PluginIdentity {
            id: self.manifest.id.clone(),
            version: self.manifest.version.clone(),
            digest: self.digest.clone(),
        }
    }
    pub fn hash(&self) -> Result<String> {
        Ok(
            Sha256::digest(serde_json::to_vec(&json!([&self.manifest, &self.files]))?)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect(),
        )
    }
    pub fn validate(&self) -> Result<()> {
        let m = &self.manifest;
        ensure!(
            m.schema_version == 1
                && m.runtime == "node"
                && matches!(m.execution.as_str(), "sandbox" | "environment"),
            "Unsupported plugin contract"
        );
        ensure!(
            !m.id.is_empty()
                && m.id.len() <= 64
                && m.id.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-'),
            "Invalid plugin ID"
        );
        ensure!(
            !m.version.is_empty() && m.version.len() <= 64,
            "Invalid plugin version"
        );
        ensure!(
            m.files.len() <= 32
                && m.files.iter().all(|p| safe_path(p))
                && m.files.contains(&m.entrypoint),
            "Invalid plugin files/entrypoint"
        );
        ensure!(
            m.files.iter().collect::<BTreeSet<_>>().len() == m.files.len()
                && m.files.len() == self.files.len()
                && m.files.iter().all(|p| self.files.contains_key(p)),
            "Plugin file set mismatch"
        );
        ensure!(
            self.files.values().map(String::len).sum::<usize>() <= 512 * 1024,
            "Plugin package too large"
        );
        ensure!(self.hash()? == self.digest, "Plugin digest mismatch");
        ensure!(
            !m.tools.is_empty() && m.tools.len() <= 16,
            "Invalid plugin tool count"
        );
        let mut names = BTreeSet::new();
        for tool in &m.tools {
            let name = tool["name"].as_str().unwrap_or("");
            ensure!(
                !name.is_empty()
                    && name.len() <= 64
                    && name.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'_')
                    && names.insert(name),
                "Invalid/duplicate plugin tool name"
            );
            ensure!(
                is_plugin_tool_name(name),
                "Plugin tool conflicts with core tool"
            );
            ensure!(
                tool["description"].is_string()
                    && tool["inputSchema"]["type"] == "object"
                    && tool["inputSchema"]["additionalProperties"] == false
                    && tool["inputSchema"]["properties"].is_object(),
                "Invalid plugin schema"
            );
            let input_schema = tool["inputSchema"].as_object().unwrap();
            if let Some(display) = tool.get("approval_display") {
                serde_json::from_value::<ApprovalDisplay>(display.clone())?
                    .validate(&input_schema["properties"])?;
            }
            ensure!(
                input_schema["properties"].get("sandbox_id").is_none()
                    && input_schema["properties"].get("environment").is_none(),
                "Plugin schema conflicts with execution routing"
            );
            ensure!(
                input_schema.keys().all(|key| matches!(
                    key.as_str(),
                    "type" | "properties" | "required" | "additionalProperties"
                )),
                "Unsupported plugin input schema constraint"
            );
            if let Some(required) = input_schema.get("required") {
                ensure!(
                    required
                        .as_array()
                        .is_some_and(|keys| keys.iter().all(|key| key
                            .as_str()
                            .is_some_and(|name| input_schema["properties"].get(name).is_some()))),
                    "Invalid plugin required fields"
                );
            }
            for schema in tool["inputSchema"]["properties"]
                .as_object()
                .unwrap()
                .values()
            {
                ensure!(
                    matches!(
                        schema["type"].as_str(),
                        Some("string" | "boolean" | "integer" | "number")
                    ),
                    "Unsupported plugin argument schema"
                );
                ensure!(
                    schema
                        .as_object()
                        .is_some_and(|s| s.keys().all(|k| matches!(
                            k.as_str(),
                            "type" | "description" | "maxLength" | "minimum" | "maximum" | "enum"
                        ))),
                    "Unsupported plugin schema constraint"
                );
                if let Some(max) = schema.get("maxLength") {
                    ensure!(
                        schema["type"] == "string" && max.as_u64().is_some(),
                        "Invalid plugin string bound"
                    );
                }
                for bound in ["minimum", "maximum"] {
                    if let Some(value) = schema.get(bound) {
                        ensure!(
                            matches!(schema["type"].as_str(), Some("integer" | "number"))
                                && value.is_number(),
                            "Invalid plugin numeric bound"
                        );
                    }
                }
                if let Some(choices) = schema.get("enum") {
                    ensure!(
                        choices.as_array().is_some_and(|values| !values.is_empty()),
                        "Invalid plugin enum"
                    );
                }
            }
        }
        Ok(())
    }
    pub fn load(root: &Path) -> Result<Self> {
        let root = root.canonicalize()?;
        let manifest_path = root.join("plugin.json").canonicalize()?;
        ensure!(
            manifest_path.starts_with(&root),
            "Plugin manifest escapes package"
        );
        ensure!(
            std::fs::metadata(&manifest_path)?.len() <= 64 * 1024,
            "Plugin manifest too large"
        );
        let manifest: PluginManifest = serde_json::from_slice(&std::fs::read(manifest_path)?)?;
        ensure!(manifest.files.len() <= 32, "Too many plugin files");
        let mut files = BTreeMap::new();
        let mut remaining = 512 * 1024;
        for name in &manifest.files {
            ensure!(safe_path(name), "Plugin path escapes package");
            let path = root.join(name).canonicalize()?;
            ensure!(path.starts_with(&root), "Plugin symlink escapes package");
            let len = usize::try_from(std::fs::metadata(&path)?.len())?;
            ensure!(len <= remaining, "Plugin package too large");
            remaining -= len;
            files.insert(name.clone(), std::fs::read_to_string(path)?);
        }
        let mut bundle = Self {
            manifest,
            files,
            digest: String::new(),
        };
        bundle.digest = bundle.hash()?;
        bundle.validate()?;
        Ok(bundle)
    }
    pub fn builtin(id: &str) -> Result<Self> {
        ensure!(
            id == "computer-use",
            "Unknown bundled plugin; provide a source package"
        );
        let manifest =
            serde_json::from_str(include_str!("../../../plugins/computer-use/plugin.json"))?;
        let files = BTreeMap::from([
            (
                "index.mjs".into(),
                include_str!("../../../plugins/computer-use/index.mjs").into(),
            ),
            (
                "browser.mjs".into(),
                include_str!("../../../plugins/computer-use/browser.mjs").into(),
            ),
            (
                "instructions.md".into(),
                include_str!("../../../plugins/computer-use/instructions.md").into(),
            ),
        ]);
        let mut bundle = Self {
            manifest,
            files,
            digest: String::new(),
        };
        bundle.digest = bundle.hash()?;
        bundle.validate()?;
        Ok(bundle)
    }
    pub fn validate_input(&self, name: &str, input: &Value) -> Result<()> {
        let tool = self
            .manifest
            .tools
            .iter()
            .find(|t| t["name"] == name)
            .ok_or_else(|| anyhow::anyhow!("Unknown plugin tool"))?;
        let input = input
            .as_object()
            .ok_or_else(|| anyhow::anyhow!("Plugin input must be an object"))?;
        let schema = &tool["inputSchema"];
        if let Some(required) = schema["required"].as_array() {
            ensure!(
                required
                    .iter()
                    .all(|key| key.as_str().is_some_and(|k| input.contains_key(k))),
                "Missing plugin argument"
            );
        }
        let properties = schema["properties"].as_object().unwrap();
        for (key, value) in input {
            let s = properties
                .get(key)
                .ok_or_else(|| anyhow::anyhow!("Unknown plugin argument: {key}"))?;
            ensure!(
                match s["type"].as_str() {
                    Some("string") => value.is_string(),
                    Some("boolean") => value.is_boolean(),
                    Some("integer") => value.as_i64().is_some(),
                    Some("number") => value.is_number(),
                    _ => false,
                },
                "Invalid plugin argument type: {key}"
            );
            if let Some(max) = s["maxLength"].as_u64() {
                ensure!(
                    value.as_str().is_some_and(|s| s.len() as u64 <= max),
                    "Plugin string too long"
                );
            }
            if let Some(min) = s["minimum"].as_f64() {
                ensure!(
                    value.as_f64().is_some_and(|n| n >= min),
                    "Plugin argument below minimum"
                );
            }
            if let Some(max) = s["maximum"].as_f64() {
                ensure!(
                    value.as_f64().is_some_and(|n| n <= max),
                    "Plugin argument above maximum"
                );
            }
            if let Some(choices) = s["enum"].as_array() {
                ensure!(choices.contains(value), "Invalid plugin enum value");
            }
        }
        Ok(())
    }
    pub fn approval_display(&self, name: &str) -> Result<Option<ApprovalDisplay>> {
        self.manifest
            .tools
            .iter()
            .find(|t| t["name"] == name)
            .and_then(|t| t.get("approval_display"))
            .map(|display| serde_json::from_value(display.clone()).map_err(Into::into))
            .transpose()
    }
}
impl crate::AgentSettings {
    /// Called by owner settings import and spec resolution, never by the Guest.
    pub fn pin_plugins(&mut self) -> Result<()> {
        for (id, config) in &mut self.plugins {
            if !config.enabled && config.source.is_none() && config.bundle.is_none() {
                continue;
            }
            let bundle = if let Some(source) = config.source.take() {
                PluginBundle::load(&source)?
            } else if let Some(bundle) = &config.bundle {
                bundle.clone()
            } else {
                PluginBundle::builtin(id)?
            };
            ensure!(bundle.manifest.id == *id, "Plugin ID differs from package");
            config.bundle = Some(bundle);
        }
        // Translate the previous browser setting on migration to the plugin tool.
        self.builtin_tools.retain(|n| n != "browser");
        if let Some(allow) = &mut self.allowed_tools {
            for name in allow {
                if name == "browser" {
                    *name = "cua_repl".into();
                }
            }
        }
        self.validate_plugins()
    }
    pub fn validate_plugins(&self) -> Result<()> {
        ensure!(self.plugins.len() <= 16, "Too many plugins");
        let mut names = BTreeSet::new();
        for (id, config) in &self.plugins {
            if !config.enabled {
                continue;
            }
            ensure!(
                config.source.is_none(),
                "Import plugin sources through owner settings before resolution"
            );
            let bundle = config
                .bundle
                .clone()
                .map(Ok)
                .unwrap_or_else(|| PluginBundle::builtin(id))?;
            ensure!(bundle.manifest.id == *id, "Plugin identity mismatch");
            bundle.validate()?;
            ensure!(
                bundle
                    .manifest
                    .tools
                    .iter()
                    .all(|tool| names.insert(tool["name"].as_str().unwrap().to_owned())),
                "Tool collision between plugins"
            );
        }
        Ok(())
    }
    pub fn plugin_for_tool(&self, name: &str) -> Result<Option<PluginBundle>> {
        for (id, config) in &self.plugins {
            if !config.enabled {
                continue;
            }
            let bundle = config
                .bundle
                .clone()
                .map(Ok)
                .unwrap_or_else(|| PluginBundle::builtin(id))?;
            if bundle
                .manifest
                .tools
                .iter()
                .any(|tool| tool["name"] == name)
            {
                return Ok(Some(bundle));
            }
        }
        Ok(None)
    }
    pub fn plugin_tools(&self) -> Result<Vec<Value>> {
        let mut tools = vec![];
        for (id, config) in &self.plugins {
            if config.enabled {
                tools.extend(
                    config
                        .bundle
                        .clone()
                        .map(Ok)
                        .unwrap_or_else(|| PluginBundle::builtin(id))?
                        .manifest
                        .tools,
                );
            }
        }
        for tool in &mut tools {
            tool.as_object_mut().unwrap().remove("approval_display");
        }
        Ok(tools)
    }
    pub fn plugin_instructions(&self, vision: bool) -> Result<String> {
        let mut out = String::new();
        for (id, config) in &self.plugins {
            if !config.enabled {
                continue;
            }
            let bundle = config
                .bundle
                .clone()
                .map(Ok)
                .unwrap_or_else(|| PluginBundle::builtin(id))?;
            if !bundle.manifest.tools.iter().any(|tool| {
                self.allowed_tools
                    .as_ref()
                    .is_none_or(|allow| allow.iter().any(|n| tool["name"] == *n))
            }) {
                continue;
            }
            if let Some(instructions) = bundle.files.get("instructions.md") {
                out.push('\n');
                out.push_str(instructions);
            }
        }
        if out.is_empty() {
            return Ok(out);
        }
        out.push_str(if vision { "\nPlugin observation mode: mixed text and images. Coordinates use the returned viewport." } else { "\nPlugin observation mode: text only. No screenshots or coordinate actions. Report content that requires vision." });
        Ok(out)
    }
}
pub fn validate_call(args: &Value) -> Result<PluginBundle> {
    let bundle: PluginBundle = serde_json::from_value(args["plugin"].clone())?;
    bundle.validate()?;
    let object = args
        .as_object()
        .ok_or_else(|| anyhow::anyhow!("Invalid plugin call"))?;
    ensure!(
        object
            .keys()
            .all(|k| ["plugin", "tool", "input", "mode", "session_id"].contains(&k.as_str())),
        "Unknown plugin envelope field"
    );
    ensure!(
        matches!(args["mode"].as_str(), Some("text" | "mixed")),
        "Invalid plugin mode"
    );
    if let Some(id) = args.get("session_id") {
        ensure!(
            id.as_str().is_some_and(|s| !s.is_empty() && s.len() <= 64),
            "Invalid plugin session ID"
        );
    }
    let name = args["tool"].as_str().unwrap_or("");
    bundle.validate_input(name, &args["input"])?;
    ensure!(
        args.get("session_id") == args["input"].get("session_id"),
        "Plugin session identity mismatch"
    );
    ensure!(
        args["input"]["reset"] != true || args.get("session_id").is_some(),
        "Reset requires the existing session_id; no new session was created"
    );
    Ok(bundle)
}
pub fn call(bundle: PluginBundle, tool: &str, mut input: Value, vision: bool) -> Result<Value> {
    input
        .as_object_mut()
        .ok_or_else(|| anyhow::anyhow!("Invalid plugin input"))?
        .remove("sandbox_id");
    bundle.validate_input(tool, &input)?;
    let mut envelope =
        json!({"plugin":bundle,"tool":tool,"input":input,"mode":if vision {"mixed"} else {"text"}});
    if let Some(id) = input.get("session_id") {
        envelope["session_id"] = id.clone();
    }
    Ok(envelope)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn plugin_disable_collision_and_tampered_packages_fail_closed() {
        let mut settings = crate::AgentSettings::default();
        settings.pin_plugins().unwrap();
        assert_eq!(settings.plugin_tools().unwrap()[0]["name"], "cua_repl");
        let mut corrupt = settings.clone();
        corrupt
            .plugins
            .get_mut("computer-use")
            .unwrap()
            .bundle
            .as_mut()
            .unwrap()
            .files
            .insert("index.mjs".into(), "changed".into());
        assert!(corrupt.validate().is_err());
        let mut bundle = PluginBundle::builtin("computer-use").unwrap();
        bundle.manifest.id = "other".into();
        bundle.digest = bundle.hash().unwrap();
        settings.plugins.insert(
            "other".into(),
            PluginConfig {
                bundle: Some(bundle),
                ..Default::default()
            },
        );
        assert!(settings.validate().is_err());
        settings.plugins.clear();
        assert!(settings.plugin_tools().unwrap().is_empty());
        assert!(settings.plugin_instructions(false).unwrap().is_empty());
        settings.plugins = defaults();
        settings.plugins.get_mut("computer-use").unwrap().enabled = false;
        settings.pin_plugins().unwrap();
        assert!(settings.plugin_for_tool("cua_repl").unwrap().is_none());
    }
    #[test]
    fn owner_import_pins_sources_and_preserves_replacement_identity() {
        let root = std::env::temp_dir().join(format!("pot-plugin-import-{}", std::process::id()));
        std::fs::create_dir_all(&root).unwrap();
        let mut bundle = PluginBundle::builtin("computer-use").unwrap();
        bundle.manifest.version = "custom-1".into();
        std::fs::write(
            root.join("plugin.json"),
            serde_json::to_vec(&bundle.manifest).unwrap(),
        )
        .unwrap();
        for (name, source) in &bundle.files {
            std::fs::write(root.join(name), source).unwrap();
        }
        let mut settings = crate::AgentSettings::default();
        settings.plugins.get_mut("computer-use").unwrap().source = Some(root.clone());
        settings.pin_plugins().unwrap();
        let pinned = settings.plugin_for_tool("cua_repl").unwrap().unwrap();
        assert_eq!(pinned.manifest.version, "custom-1");
        settings.plugins.get_mut("computer-use").unwrap().enabled = false;
        settings.pin_plugins().unwrap();
        assert!(settings.plugin_tools().unwrap().is_empty());
        settings.plugins.get_mut("computer-use").unwrap().enabled = true;
        assert_eq!(
            settings
                .plugin_for_tool("cua_repl")
                .unwrap()
                .unwrap()
                .digest,
            pinned.digest
        );
        std::fs::write(root.join("index.mjs"), "changed source").unwrap();
        assert_eq!(
            settings
                .plugin_for_tool("cua_repl")
                .unwrap()
                .unwrap()
                .digest,
            pinned.digest
        );
        assert!(settings.plugins["computer-use"].source.is_none());
        bundle.manifest.files[0] = "../escape.mjs".into();
        std::fs::write(
            root.join("plugin.json"),
            serde_json::to_vec(&bundle.manifest).unwrap(),
        )
        .unwrap();
        assert!(PluginBundle::load(&root).is_err());
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn model_cannot_override_the_dispatch_envelope() {
        let bundle = PluginBundle::builtin("computer-use").unwrap();
        assert!(
            call(
                bundle.clone(),
                "cua_repl",
                json!({"code":"","mode":"mixed"}),
                false
            )
            .is_err()
        );
        let mut args = call(
            bundle,
            "cua_repl",
            json!({"session_id":"owned","code":""}),
            false,
        )
        .unwrap();
        args["session_id"] = json!("foreign");
        assert!(validate_call(&args).is_err());
        let reset = call(
            PluginBundle::builtin("computer-use").unwrap(),
            "cua_repl",
            json!({"reset":true}),
            false,
        )
        .unwrap();
        assert!(
            validate_call(&reset)
                .unwrap_err()
                .to_string()
                .contains("existing session_id")
        );
    }
}

#[cfg(test)]
mod permission_compatibility_tests {
    use super::*;
    #[test]
    fn plugins_cannot_shadow_new_product_tools_or_routing_arguments() {
        let original = PluginBundle::builtin("computer-use").unwrap();
        for name in [
            "exec",
            "wait",
            "create_task",
            "manage_sandbox",
            "set_sandbox_mappings",
        ] {
            let mut bundle = original.clone();
            bundle.manifest.tools[0]["name"] = json!(name);
            bundle.digest = bundle.hash().unwrap();
            assert!(bundle.validate().is_err(), "{name}");
        }
        for field in ["environment", "sandbox_id"] {
            let mut bundle = original.clone();
            bundle.manifest.tools[0]["inputSchema"]["properties"][field] = json!({"type":"string"});
            bundle.digest = bundle.hash().unwrap();
            assert!(bundle.validate().is_err(), "{field}");
        }
        assert!(is_plugin_tool_name("counter"));
    }
    #[test]
    fn approval_metadata_is_optional_validated_and_not_model_visible() {
        let original = PluginBundle::builtin("computer-use").unwrap();
        assert!(original.approval_display("cua_repl").unwrap().is_some());
        assert!(
            crate::AgentSettings::default().plugin_tools().unwrap()[0]
                .get("approval_display")
                .is_none()
        );
        let mut external = original.clone();
        external.manifest.tools[0]
            .as_object_mut()
            .unwrap()
            .remove("approval_display");
        external.digest = external.hash().unwrap();
        external.validate().unwrap();
        assert!(external.approval_display("cua_repl").unwrap().is_none());
        for invalid in [
            json!({"summary":{"zh-CN":"没有英文回退"}}),
            json!({"summary":{"en":"Allow?"},"labels":{"unknown":{"en":"Unknown"}}}),
            json!({"summary":{"en":"Allow?"},"run":"index.mjs"}),
        ] {
            let mut bundle = original.clone();
            bundle.manifest.tools[0]["approval_display"] = invalid;
            bundle.digest = bundle.hash().unwrap();
            assert!(bundle.validate().is_err());
        }
    }
}
