//! Selection is view state, never execution routing or authorization.
use serde_json::{Value, json};

pub fn selection(directory: &Value, preferred: &Value) -> Value {
    let machines = directory["machines"]
        .as_array()
        .map(Vec::as_slice)
        .unwrap_or(&[]);
    if let Some(machine) = machines
        .iter()
        .find(|m| m["runtime_id"] == preferred["runtime_id"])
    {
        if preferred.get("target").is_none()
            || machine["environments"]
                .as_array()
                .is_some_and(|items| items.iter().any(|e| e["environment_ref"] == *preferred))
        {
            return preferred.clone();
        }
        return json!({"runtime_id":machine["runtime_id"]});
    }
    machines
        .first()
        .map(|m| json!({"runtime_id":m["runtime_id"]}))
        .unwrap_or(Value::Null)
}

pub fn task_location(
    task: &pot_protocol::TaskInfo,
    catalog: crate::localization::Catalog,
    local_runtime: &str,
) -> String {
    match &task.environment_ref {
        Some(reference) => {
            let target = match &reference.target {
                pot_core::EnvironmentTarget::Host {} => "Host".to_owned(),
                pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                    format!("Sandbox: {sandbox_id}")
                }
            };
            format!(
                "{} / {target}",
                if reference.runtime_id == local_runtime {
                    catalog.text("machines.local")
                } else {
                    &reference.runtime_id
                }
            )
        }
        None => format!(
            "{} / Sandbox: {}",
            catalog.text("machines.local"),
            task.sandbox_id
        ),
    }
}

/// UI affordances follow actual transport availability, never just Provider support.
pub fn desktop_available(machine: &Value, environment: &Value, runtime: Option<&str>) -> bool {
    runtime.is_some_and(|id| machine["runtime_id"] == id)
        && environment["environment_ref"]["target"]["kind"] == "sandbox"
        && environment["desktop_capabilities"]["viewing"] == true
}

pub fn schedule_location(spec: &pot_protocol::ScheduleSpec) -> String {
    spec.environment_ref
        .as_ref()
        .map(|reference| {
            let target = match &reference.target {
                pot_core::EnvironmentTarget::Host {} => "Host".to_owned(),
                pot_core::EnvironmentTarget::Sandbox { sandbox_id } => {
                    format!("Sandbox: {sandbox_id}")
                }
            };
            format!("{} / {target}", reference.runtime_id)
        })
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn desktop_entry_requires_the_available_sandbox_transport() {
        let machine = json!({"runtime_id":"a"});
        let sandbox = json!({"environment_ref":{"runtime_id":"a","target":{"kind":"sandbox","sandbox_id":"build"}},"desktop_capabilities":{"viewing":true}});
        assert!(desktop_available(&machine, &sandbox, Some("a")));
        assert!(!desktop_available(&machine, &sandbox, None));
        assert!(!desktop_available(&machine, &sandbox, Some("other")));
        let host = json!({"environment_ref":{"target":{"kind":"host"}},"desktop_capabilities":{"viewing":true}});
        assert!(!desktop_available(&machine, &host, Some("a")));
    }
    #[test]
    fn selection_preserves_offline_targets_and_never_changes_default() {
        let host = json!({"runtime_id":"b","target":{"kind":"host"}});
        let directory = json!({"machines":[{"runtime_id":"a","default":"build","environments":[]},
            {"runtime_id":"b","connection":"offline","environments":[{"environment_ref":host}]}]});
        assert_eq!(selection(&directory, &host), host);
        assert_eq!(
            selection(
                &directory,
                &json!({"runtime_id":"b","target":{"kind":"sandbox","sandbox_id":"deleted"}})
            ),
            json!({"runtime_id":"b"})
        );
        assert_eq!(
            selection(&directory, &Value::Null),
            json!({"runtime_id":"a"})
        );
        assert_eq!(directory["machines"][0]["default"], "build");
        assert_eq!(selection(&json!({"machines":[]}), &host), Value::Null);
    }
}
