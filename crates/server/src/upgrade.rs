//! Control data upgrades; old enrollment/grant protocols are never imported.
use anyhow::{Result, ensure};
use std::path::Path;
pub(crate) fn control_data(data: &Path) -> Result<()> {
    for (old, new) in [
        ("peer-identity.json", "control-identity.json"),
        ("peer-operations", "execution-operations"),
        ("peer-management", "environment-management"),
    ] {
        pot_runtime::upgrade::rename_path(data, old, new)?;
    }
    let path = data.join("runtime-control.json");
    if path.exists() {
        let mut book: serde_json::Value = serde_json::from_slice(&std::fs::read(&path)?)?;
        if book["schema_version"] == 1 {
            if let Some(leases) = book["leases"].as_object_mut() {
                for lease in leases.values_mut() {
                    let lease = lease
                        .as_object_mut()
                        .ok_or_else(|| anyhow::anyhow!("Invalid execution lease"))?;
                    if let Some(control) = lease.remove("peer_id") {
                        ensure!(
                            !lease.contains_key("control_id"),
                            "Conflicting execution lease identity"
                        );
                        lease.insert("control_id".into(), control);
                    }
                }
            }
            book["schema_version"] = 2.into();
            super::control::private_json(&path, &book)?;
        }
    }
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn rename_preserves_signing_identity_receipts_and_lease_owner() {
        let data =
            std::env::temp_dir().join(format!("pot-control-upgrade-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(data.join("peer-operations")).unwrap();
        std::fs::write(data.join("peer-identity.json"), "private signing key").unwrap();
        std::fs::write(data.join("peer-operations/receipt"), "accepted effect").unwrap();
        super::super::control::private_json(
            &data.join("runtime-control.json"),
            &json!({"schema_version":1,"leases":{"run":{"peer_id":"control","owner":"task"}}}),
        )
        .unwrap();
        control_data(&data).unwrap();
        control_data(&data).unwrap();
        assert_eq!(
            std::fs::read_to_string(data.join("control-identity.json")).unwrap(),
            "private signing key"
        );
        assert_eq!(
            std::fs::read_to_string(data.join("execution-operations/receipt")).unwrap(),
            "accepted effect"
        );
        let book: serde_json::Value =
            serde_json::from_slice(&std::fs::read(data.join("runtime-control.json")).unwrap())
                .unwrap();
        assert_eq!(book["schema_version"], 2);
        assert_eq!(
            book["leases"]["run"],
            json!({"control_id":"control","owner":"task"})
        );
        assert!(!data.join("peer-identity.json").exists());
        std::fs::remove_dir_all(data).unwrap();
    }
}
