//! Extra shared file tools and an owner-reviewed environment-management operation.
use anyhow::{Result, ensure};
use serde_json::{Value, json};
use std::path::{Component, Path};
pub fn schemas() -> Vec<Value> {
    let mut tools = Vec::new();
    let extra = [
        ("set_sandbox_mappings", "Replace all Host directory mappings for a stopped Linux Sandbox after owner approval. Show complete mappings; all tasks in that Sandbox can access them. Existing mappings not listed are removed. Changes persist until replaced.", json!({"sandbox_id":{"type":"string"},"mappings":{"type":"array","items":{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"},"read_only":{"type":"boolean"}},"required":["source","destination","read_only"],"additionalProperties":false}}}), vec!["sandbox_id","mappings"]),
    ].into_iter().map(|(name,description,properties,required)|json!({"name":name,"description":description,"inputSchema":{"type":"object","properties":properties,"required":required,"additionalProperties":false}}));
    tools.extend(extra);
    tools
}
pub fn normalize_mapping(args: &mut Value) -> Result<()> {
    let sandbox = args["sandbox_id"]
        .as_str()
        .filter(|s| !s.is_empty())
        .ok_or_else(|| anyhow::anyhow!("Explicit Sandbox required"))?
        .to_owned();
    ensure!(
        args.as_object().is_some_and(|o| o.keys().all(|k| [
            "sandbox_id",
            "mappings",
            "environment"
        ]
        .contains(&k.as_str()))),
        "Invalid mapping arguments"
    );
    let mappings = args["mappings"]
        .as_array_mut()
        .ok_or_else(|| anyhow::anyhow!("Mappings required"))?;
    ensure!(mappings.len() <= 16, "Mapping limit reached");
    for mapping in mappings {
        ensure!(
            mapping.as_object().is_some_and(|o| o.len() == 3) && mapping["read_only"].is_boolean(),
            "Invalid mapping"
        );
        mapping["source"] = json!(crate::permissions::resolve_path(
            mapping["source"]
                .as_str()
                .ok_or_else(|| anyhow::anyhow!("Mapping source required"))?
        )?);
        ensure!(
            Path::new(mapping["source"].as_str().unwrap()).is_dir(),
            "Mapping source must be a directory"
        );
        let target = Path::new(
            mapping["destination"]
                .as_str()
                .ok_or_else(|| anyhow::anyhow!("Mapping destination required"))?,
        );
        ensure!(
            target.starts_with("/mnt/host")
                && target != Path::new("/mnt/host")
                && !target
                    .components()
                    .any(|c| matches!(c, Component::ParentDir | Component::CurDir)),
            "Invalid mapping destination"
        );
    }
    args["sandbox_id"] = json!(sandbox);
    Ok(())
}
