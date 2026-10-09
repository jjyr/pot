use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum ImageFormat {
    Oci,
    MacosVm,
}
/// Source description or resolved immutable template; never contains credentials.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TemplateDescriptor {
    pub schema_version: u32,
    pub id: String,
    pub version: String,
    pub os: String,
    pub architecture: String,
    pub format: ImageFormat,
    pub providers: Vec<String>,
    pub bootstrap: String,
    pub recipe: String,
    #[serde(default)]
    pub digest: Option<String>,
}
impl TemplateDescriptor {
    pub fn parse(source: &str) -> Result<Self> {
        let value: Self = toml::from_str(source)?;
        ensure!(
            value.schema_version == 1
                && !value.id.is_empty()
                && !value.version.is_empty()
                && !value.providers.is_empty(),
            "Invalid template description"
        );
        ensure!(
            !value.recipe.is_empty()
                && std::path::Path::new(&value.recipe)
                    .components()
                    .all(|c| matches!(c, std::path::Component::Normal(_))),
            "Invalid template recipe"
        );
        Ok(value)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn source_description_is_not_an_oci_manifest_and_cannot_escape_recipe_root() {
        let source = include_str!("../linux/default/template.toml");
        let descriptor = TemplateDescriptor::parse(source).unwrap();
        assert_eq!(descriptor.format, ImageFormat::Oci);
        assert!(descriptor.digest.is_none());
        assert!(TemplateDescriptor::parse(&source.replace("build.sh", "../secret")).is_err());
        assert!(
            TemplateDescriptor::parse(&source.replace("schema_version = 1", "schema_version = 99"))
                .is_err()
        );
    }
}
