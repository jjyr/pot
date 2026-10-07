mod template;
pub use template::{ImageFormat, TemplateDescriptor};
// Embedded, immutable image catalog. No network action is performed by inspection.
use anyhow::{Result, ensure};
pub use pot_resource::{Progress, Resource, Verified};
use serde::{Deserialize, Serialize};
use std::{collections::BTreeMap, path::PathBuf};
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Bundle {
    pub kernel: String,
    pub boot: String,
    pub image: String,
    pub image_reference: String,
    pub boot_reference: String,
    pub bootstrap_version: String,
    pub os: String,
    pub architecture: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Catalog {
    pub schema_version: u32,
    pub default_linux_bundle: String,
    pub resources: BTreeMap<String, Resource>,
    pub bundles: BTreeMap<String, Bundle>,
}
impl Catalog {
    pub fn embedded() -> Result<Self> {
        Self::parse(include_str!("../catalog.toml"))
    }
    pub fn parse(text: &str) -> Result<Self> {
        let c: Self = toml::from_str(text)?;
        ensure!(
            c.schema_version == 1 && c.bundles.contains_key(&c.default_linux_bundle),
            "Invalid image catalog"
        );
        for r in c.resources.values() {
            r.validate()?;
        }
        for b in c.bundles.values() {
            ensure!(
                b.os == "linux"
                    && b.architecture == "arm64"
                    && matches!(
                        b.bootstrap_version.as_str(),
                        "pot-executor-v1" | "pot-executor-v2"
                    ),
                "Unsupported bundle"
            );
            ensure!(
                matches!(
                    c.resources.get(&b.kernel).map(|r| r.format),
                    Some(pot_resource::Format::Raw)
                ) && [&b.boot, &b.image].iter().all(|id| matches!(
                    c.resources.get(*id).map(|r| r.format),
                    Some(pot_resource::Format::OciTarGzip)
                )),
                "Incompatible bundle resource formats"
            );
            for id in [&b.kernel, &b.boot, &b.image] {
                c.resources
                    .get(id)
                    .ok_or_else(|| anyhow::anyhow!("Unknown bundle resource"))?
                    .validate()?;
            }
        }
        Ok(c)
    }
    pub fn bundle(&self, id: &str) -> Result<&Bundle> {
        self.bundles
            .get(id)
            .ok_or_else(|| anyhow::anyhow!("Unknown image bundle"))
    }
    pub fn resource_ids<'a>(&'a self, id: &str) -> Result<[&'a str; 3]> {
        let b = self.bundle(id)?;
        Ok([&b.kernel, &b.boot, &b.image])
    }
}
pub struct PreparedBundle {
    pub id: String,
    pub kernel: Verified,
    pub boot: Verified,
    pub image: Verified,
    pub image_reference: String,
    pub boot_reference: String,
}
pub struct Images {
    pub catalog: Catalog,
    pub store: pot_resource::Store,
}
impl Images {
    pub fn open(root: PathBuf) -> Result<Self> {
        Ok(Self {
            catalog: Catalog::embedded()?,
            store: pot_resource::Store::new(root)?,
        })
    }
    pub fn missing_bytes(&self, id: &str) -> Result<u64> {
        let mut size = 0;
        for key in self.catalog.resource_ids(id)? {
            let r = &self.catalog.resources[key];
            if self.store.cached(r)?.is_none() {
                size += r.size_bytes;
            }
        }
        Ok(size)
    }
    pub fn prepared(&self, id: &str) -> Result<Option<PreparedBundle>> {
        let b = self.catalog.bundle(id)?;
        let get = |key: &str| self.store.cached(&self.catalog.resources[key]);
        match (get(&b.kernel)?, get(&b.boot)?, get(&b.image)?) {
            (Some(kernel), Some(boot), Some(image)) => Ok(Some(PreparedBundle {
                id: id.into(),
                kernel,
                boot,
                image,
                image_reference: b.image_reference.clone(),
                boot_reference: b.boot_reference.clone(),
            })),
            _ => Ok(None),
        }
    }
    pub async fn prepare(
        &self,
        id: &str,
        cancelled: &(dyn Fn() -> bool + Send + Sync),
        emit: &(dyn Fn(&str, Progress) + Send + Sync),
    ) -> Result<PreparedBundle> {
        let bundle = self.catalog.bundle(id)?;
        let mut prepared = Vec::new();
        for key in self.catalog.resource_ids(id)? {
            prepared.push(
                self.store
                    .prepare(
                        &self.catalog.resources[key],
                        "https://github.com/jjyr/pot",
                        cancelled,
                        &|p| emit(key, p),
                    )
                    .await?,
            );
        }
        let mut resources = prepared.into_iter();
        Ok(PreparedBundle {
            id: id.into(),
            kernel: resources.next().unwrap(),
            boot: resources.next().unwrap(),
            image: resources.next().unwrap(),
            image_reference: bundle.image_reference.clone(),
            boot_reference: bundle.boot_reference.clone(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn embedded_catalog_is_a_complete_compatible_trust_root() {
        let c = Catalog::embedded().unwrap();
        let b = c.bundle(&c.default_linux_bundle).unwrap();
        assert_eq!(c.resource_ids(&c.default_linux_bundle).unwrap().len(), 3);
        assert!(b.boot_reference.contains("@sha256:"));
        let source = include_str!("../catalog.toml");
        assert!(
            Catalog::parse(&source.replace("schema_version = 1", "schema_version = 2")).is_err()
        );
        assert!(
            Catalog::parse(&source.replace("format = \"raw\"", "format = \"oci-tar-gzip\""))
                .is_err()
        );
        assert!(
            Catalog::parse(
                &source.replace("architecture = \"arm64\"", "architecture = \"x86_64\"")
            )
            .is_err()
        );
        assert!(
            Catalog::parse(&source.replace("linux-arm64-kernel-fb2cfb79eb1ae194", "../kernel"))
                .is_err()
        );
    }
}
