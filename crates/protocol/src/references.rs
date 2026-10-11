//! Explicit composer references are ordinary durable message text.
use serde::{Deserialize, Serialize};

pub fn valid_label(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-' || b == b'_')
}
pub fn default_labels(control: bool, os: &str) -> Vec<String> {
    let mut labels = vec![os.to_owned()];
    if control {
        labels.extend(["task-runner".into(), "desktop".into()]);
    }
    labels.sort();
    labels
}
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct References {
    pub machine: Option<String>,
    pub tags: Vec<String>,
    pub skills: Vec<String>,
}
impl References {
    pub fn parse(text: &str) -> Result<Self, &'static str> {
        let mut result = Self::default();
        for token in text.split_whitespace() {
            if let Some(id) = token.strip_prefix("/machine:") {
                if id.is_empty()
                    || id.len() > 128
                    || !id
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
                {
                    return Err("Invalid machine reference");
                }
                if result.machine.as_ref().is_some_and(|old| old != id) {
                    return Err("Select only one machine");
                }
                result.machine = Some(id.into());
            } else if let Some(tag) = token.strip_prefix("/tag:") {
                if !valid_label(tag) {
                    return Err("Invalid machine label");
                }
                result.tags.push(tag.into());
            } else if let Some(skill) = token.strip_prefix("/skill:") {
                if !valid_label(skill) {
                    return Err("Invalid Skill reference");
                }
                result.skills.push(skill.into());
            }
        }
        result.tags.sort();
        result.tags.dedup();
        result.skills.sort();
        result.skills.dedup();
        if result.tags.len() > 64 || result.skills.len() > 128 {
            return Err("Too many references");
        }
        Ok(result)
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Completion {
    pub token: String,
    pub label: String,
    pub detail: String,
}
/// Only a standalone slash word immediately before the caret opens completion.
pub fn query(text: &str, caret: usize) -> Option<(usize, &str)> {
    let prefix = text.get(..caret)?;
    let start = prefix
        .rfind(char::is_whitespace)
        .map_or(0, |i| i + prefix[i..].chars().next().unwrap().len_utf8());
    let word = &prefix[start..];
    let query = word.strip_prefix('/')?;
    if query.contains('/') {
        return None;
    }
    Some((start, query))
}
pub fn matches(item: &Completion, query: &str) -> bool {
    let query = query.to_lowercase();
    item.token.to_lowercase().contains(&query)
        || item.label.to_lowercase().contains(&query)
        || item.detail.to_lowercase().contains(&query)
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct MachineRoute {
    pub runtime_id: String,
    pub tags: Vec<String>,
}
pub fn select_machine<'a>(
    machines: &'a [MachineRoute],
    control: &str,
    refs: &References,
) -> Result<&'a str, &'static str> {
    let default_tags = vec!["task-runner".to_owned()];
    let tags = if refs.machine.is_none() && refs.tags.is_empty() {
        &default_tags
    } else {
        &refs.tags
    };
    machines
        .iter()
        .filter(|m| {
            refs.machine.as_ref().is_none_or(|id| id == &m.runtime_id)
                && tags.iter().all(|t| m.tags.contains(t))
        })
        .min_by_key(|m| (m.runtime_id != control, &m.runtime_id))
        .map(|m| m.runtime_id.as_str())
        .ok_or("No machine matches the selected labels or machine")
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn routing_is_and_matching_control_first_and_never_capability_fallback() {
        let machines = vec![
            MachineRoute {
                runtime_id: "runner".into(),
                tags: vec!["linux".into()],
            },
            MachineRoute {
                runtime_id: "control".into(),
                tags: default_labels(true, "macos"),
            },
        ];
        assert_eq!(
            select_machine(&machines, "control", &References::default()),
            Ok("control")
        );
        assert_eq!(
            select_machine(
                &machines,
                "control",
                &References::parse("/tag:linux").unwrap()
            ),
            Ok("runner")
        );
        assert!(
            select_machine(
                &machines,
                "control",
                &References::parse("/tag:linux /tag:desktop").unwrap()
            )
            .is_err()
        );
        assert!(
            select_machine(
                &machines,
                "control",
                &References::parse("/machine:runner /tag:desktop").unwrap()
            )
            .is_err()
        );
        assert_eq!(
            select_machine(
                &machines,
                "control",
                &References::parse("/machine:runner").unwrap()
            ),
            Ok("runner")
        );
        let machines = vec![MachineRoute {
            runtime_id: "control".into(),
            tags: vec![],
        }];
        assert!(select_machine(&machines, "control", &References::default()).is_err());
    }
    #[test]
    fn explicit_tokens_are_unambiguous_and_composable() {
        let r = References::parse(
            "工作 /machine:runtime-1 /tag:linux /tag:desktop /tag:linux /skill:pdf",
        )
        .unwrap();
        assert_eq!(r.machine.as_deref(), Some("runtime-1"));
        assert_eq!(r.tags, ["desktop", "linux"]);
        assert_eq!(r.skills, ["pdf"]);
        assert!(References::parse("/machine:a /machine:b").is_err());
        assert_eq!(
            References::parse("https://host/tag:linux `/tag:linux` /usr/bin").unwrap(),
            References::default()
        );
        assert_eq!(query("中文 /desk", "中文 /desk".len()), Some((7, "desk")));
        assert!(query("https://example", 15).is_none());
        assert!(query("/tmp/file", 9).is_none());
        assert_eq!(default_labels(false, "linux"), ["linux"]);
        assert_eq!(
            default_labels(true, "macos"),
            ["desktop", "macos", "task-runner"]
        );
    }
}
