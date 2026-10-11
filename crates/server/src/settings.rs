use anyhow::Result;
use std::{collections::BTreeMap, path::Path};
/// Loads owner listener settings without provider secrets.
///
/// # Safety
/// Call before any other threads start: this function changes process environment.
pub unsafe fn apply_settings(root: &Path) -> Result<()> {
    let path = root.join("server.json");
    let values: BTreeMap<String, String> = if path.exists() {
        serde_json::from_slice(&std::fs::read(path)?)?
    } else {
        BTreeMap::new()
    };
    for (key, value) in values {
        anyhow::ensure!(
            matches!(
                key.as_str(),
                "POT_TAILSCALE_HOSTNAME"
                    | "POT_ACCESS_ORIGIN"
                    | "POT_ACCESS_BIND"
                    | "POT_ACCESS_CERT"
                    | "POT_ACCESS_KEY"
                    | "POT_ACCESS_ASSETS"
                    | "POT_ACCESS_VAPID_KEY"
                    | "POT_ACCESS_VAPID_CONTACT"
            ),
            "Unsupported server setting"
        );
        if std::env::var_os(&key).is_none() {
            unsafe {
                std::env::set_var(key, value);
            }
        }
    }
    Ok(())
}
