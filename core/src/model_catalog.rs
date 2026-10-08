//! Versioned, provider-exact model metadata. Descriptors never grant execution authority.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ModelCatalog {
    pub schema_version: u32,
    pub default: Value,
    #[serde(default)]
    pub models: BTreeMap<String, BTreeMap<String, Value>>,
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct ModelDescriptor {
    pub input_modalities: Vec<String>,
    pub supports_image_detail_original: bool,
    pub supported_in_api: bool,
    pub visibility: String,
    pub priority: u32,
    pub model_messages: ModelMessages,
}
impl Default for ModelDescriptor {
    fn default() -> Self {
        Self {
            input_modalities: vec!["text".into()],
            supports_image_detail_original: false,
            supported_in_api: true,
            visibility: "list".into(),
            priority: 100,
            model_messages: Default::default(),
        }
    }
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct ModelMessages {
    pub instructions_template: Option<String>,
    pub tools: BTreeMap<String, ToolOverride>,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct ToolOverride {
    pub description: Option<String>,
}
pub fn merge(base: &mut Value, override_value: &Value) {
    if override_value.is_null() {
        return;
    }
    if let (Some(base), Some(overrides)) = (base.as_object_mut(), override_value.as_object()) {
        for (key, value) in overrides {
            if !value.is_null() {
                merge(base.entry(key).or_insert(Value::Null), value);
            }
        }
    } else {
        *base = override_value.clone();
    }
}
impl ModelDescriptor {
    pub fn vision(&self) -> bool {
        self.input_modalities.iter().any(|m| m == "image")
    }
    pub fn validate(&self) -> Result<()> {
        ensure!(
            self.input_modalities.iter().any(|m| m == "text"),
            "Model requires text input"
        );
        let mut seen = std::collections::HashSet::new();
        ensure!(
            self.input_modalities
                .iter()
                .all(|m| matches!(m.as_str(), "text" | "image" | "audio") && seen.insert(m)),
            "Invalid or duplicate input modality"
        );
        ensure!(
            !self.supports_image_detail_original || self.vision(),
            "Original image detail requires image input"
        );
        ensure!(
            matches!(self.visibility.as_str(), "list" | "hide" | "none"),
            "Invalid model visibility"
        );
        Ok(())
    }
    pub fn apply_tools(&self, tools: &mut [Value]) -> Result<()> {
        for (name, settings) in &self.model_messages.tools {
            let tool = tools
                .iter_mut()
                .find(|t| t["name"] == name.as_str())
                .ok_or_else(|| anyhow::anyhow!("Unregistered tool override: {name}"))?;
            if let Some(description) = &settings.description {
                tool["description"] = Value::String(description.clone());
            }
        }
        Ok(())
    }
}
impl ModelCatalog {
    pub fn resolve(&self, provider: &str, model: &str) -> Result<ModelDescriptor> {
        ensure!(
            self.schema_version == 1,
            "Unsupported model catalog schema_version"
        );
        ensure!(
            self.default.is_object(),
            "Model catalog default must be an object"
        );
        let mut effective = serde_json::to_value(ModelDescriptor::default())?;
        merge(&mut effective, &self.default);
        if let Some(overrides) = self
            .models
            .get(provider)
            .and_then(|models| models.get(model))
        {
            ensure!(
                overrides.is_object(),
                "Model override must be an object: {provider}/{model}"
            );
            merge(&mut effective, overrides);
        }
        let descriptor: ModelDescriptor = serde_json::from_value(effective)?;
        descriptor.validate()?;
        Ok(descriptor)
    }
    pub fn validate(&self) -> Result<()> {
        self.resolve("", "")?;
        for (provider, models) in &self.models {
            for model in models.keys() {
                self.resolve(provider, model)?;
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn exact_identity_and_recursive_merge_preserve_explicit_values() {
        let catalog: ModelCatalog = serde_json::from_value(json!({"schema_version":1,"default":{"model_messages":{"instructions_template":"base","tools":{"browser":{"description":"base tool"}}}},"models":{"a":{"vision":{"input_modalities":["text","image"],"priority":0,"supports_image_detail_original":true,"model_messages":{"instructions_template":"","tools":{}}},"null":{"input_modalities":null}}}})).unwrap();
        catalog.validate().unwrap();
        let vision = catalog.resolve("a", "vision").unwrap();
        assert!(vision.vision());
        assert_eq!(vision.priority, 0);
        assert_eq!(
            vision.model_messages.instructions_template.as_deref(),
            Some("")
        );
        assert_eq!(
            vision.model_messages.tools["browser"]
                .description
                .as_deref(),
            Some("base tool")
        );
        assert!(!catalog.resolve("b", "vision").unwrap().vision());
        assert!(!catalog.resolve("a", "null").unwrap().vision());
        let mut base = json!({"a":true,"b":[1],"c":{"d":"yes"}});
        merge(&mut base, &json!({"a":false,"b":[],"c":{"d":null}}));
        assert_eq!(base, json!({"a":false,"b":[],"c":{"d":"yes"}}));
    }
    #[test]
    fn broken_override_does_not_fall_back() {
        for overrides in [
            json!({"input_modalities":[]}),
            json!({"supports_image_detail_original":true}),
            json!({"typo":true}),
        ] {
            let catalog: ModelCatalog = serde_json::from_value(
                json!({"schema_version":1,"default":{},"models":{"a":{"m":overrides}}}),
            )
            .unwrap();
            assert!(catalog.resolve("a", "m").is_err());
        }
    }
}
