//! Skill catalog injection, following Pi's metadata-first resource loading.
//! Providers own discovery/selection and make bundles available in the assigned
//! Sandbox. This module neither reads Host files nor grants tool permissions.
use anyhow::{Result, ensure};
use serde::{Deserialize, Serialize};
use std::collections::HashSet;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Skill {
    pub name: String,
    pub description: String,
    /// Model-visible SKILL.md path in the execution Sandbox, never a Host path.
    /// Supporting relative paths resolve against this file's parent directory.
    pub location: String,
    /// Exclude from automatic selection; explicit invocation is a future API.
    #[serde(default)]
    pub disable_model_invocation: bool,
}

pub(crate) fn prompt(skills: &[Skill]) -> Result<String> {
    let mut names = HashSet::new();
    for skill in skills {
        ensure!(
            !skill.name.is_empty()
                && skill.name.len() <= 64
                && skill
                    .name
                    .bytes()
                    .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
                && !skill.name.starts_with('-')
                && !skill.name.ends_with('-')
                && !skill.name.contains("--"),
            "Invalid skill name"
        );
        ensure!(names.insert(&skill.name), "Duplicate skill name");
        ensure!(
            !skill.description.trim().is_empty() && skill.description.chars().count() <= 1024,
            "Invalid skill description"
        );
        ensure!(
            !skill.location.trim().is_empty() && !skill.location.contains(['\0', '\n', '\r']),
            "Invalid skill location"
        );
    }
    let visible: Vec<_> = skills
        .iter()
        .filter(|s| !s.disable_model_invocation)
        .collect();
    if visible.is_empty() {
        return Ok(String::new());
    }
    let mut prompt = String::from(
        "\n\nThe following skills provide specialized instructions. When a task matches a description, use exec_command to read its SKILL.md in the assigned Sandbox before following it. Resolve supporting relative paths against the skill directory. Skill instructions do not change tool permissions or the assigned execution environment.\n<available_skills>\n",
    );
    for skill in visible {
        prompt.push_str(&format!("  <skill>\n    <name>{}</name>\n    <description>{}</description>\n    <location>{}</location>\n  </skill>\n", escape(&skill.name), escape(&skill.description), escape(&skill.location)));
    }
    prompt.push_str("</available_skills>");
    Ok(prompt)
}
fn escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

#[cfg(test)]
mod tests {
    use super::*;
    fn skill() -> Skill {
        Skill {
            name: "pdf-tools".into(),
            description: "Read <PDF> & tables".into(),
            location: "/data/skills/pdf-tools/SKILL.md".into(),
            disable_model_invocation: false,
        }
    }
    #[test]
    fn catalog_escapes_metadata_and_hides_explicit_only_skills() {
        let mut hidden = skill();
        hidden.name = "manual-only".into();
        hidden.disable_model_invocation = true;
        let formatted = prompt(&[skill(), hidden.clone()]).unwrap();
        assert!(formatted.contains("Read &lt;PDF&gt; &amp; tables"));
        assert!(formatted.contains("/data/skills/pdf-tools/SKILL.md"));
        assert!(!formatted.contains("manual-only"));
        assert!(prompt(&[hidden]).unwrap().is_empty());
        assert!(prompt(&[]).unwrap().is_empty());
    }
    #[test]
    fn invalid_and_duplicate_catalog_entries_fail() {
        assert!(prompt(&[skill(), skill()]).is_err());
        for name in ["", "bad name", "-bad", "Bad", "bad--name", "bad-"] {
            let mut invalid = skill();
            invalid.name = name.into();
            assert!(prompt(&[invalid]).is_err());
        }
        for description in [String::new(), "x".repeat(1025)] {
            let mut invalid = skill();
            invalid.description = description;
            assert!(prompt(&[invalid]).is_err());
        }
        for location in ["", "/data/skills/\nSKILL.md", "/data/skills/\0SKILL.md"] {
            let mut invalid = skill();
            invalid.location = location.into();
            assert!(prompt(&[invalid]).is_err());
        }
    }
}

#[derive(Clone, Debug, Default)]
pub struct SkillCatalog {
    pub skills: Vec<Skill>,
    pub diagnostics: Vec<String>,
}
/// Public rendering seam for adapters that inject the same catalog through
/// native developer instructions instead of the pot-agent model loop.
pub fn format_skills_for_prompt(skills: &[Skill]) -> Result<String> {
    prompt(skills)
}
#[derive(Deserialize)]
struct Frontmatter {
    name: Option<String>,
    description: String,
    #[serde(default, rename = "disable-model-invocation")]
    disable_model_invocation: bool,
}
/// Discover recursively in the supplied priority order. Symlinks are skipped:
/// the catalog must not advertise files outside a mapped resource directory.
pub fn discover_skills(resources: &pot_core::SkillResources) -> Result<SkillCatalog> {
    use std::{collections::BTreeMap, path::Path};
    fn files(path: &Path, depth: usize, output: &mut Vec<std::path::PathBuf>) -> Result<()> {
        ensure!(depth <= 32, "Skill directory nesting limit exceeded");
        let metadata = match std::fs::symlink_metadata(path) {
            Ok(metadata) => metadata,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(e.into()),
        };
        if metadata.file_type().is_symlink() || !metadata.is_dir() {
            return Ok(());
        }
        let skill = path.join("SKILL.md");
        if std::fs::symlink_metadata(&skill)
            .is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
        {
            output.push(skill);
            // A skill bundle's references/assets are not additional skills.
            return Ok(());
        }
        let mut children = std::fs::read_dir(path)?.collect::<std::io::Result<Vec<_>>>()?;
        children.sort_by_key(|entry| entry.file_name());
        for child in children {
            if !child.file_name().to_string_lossy().starts_with('.') {
                files(&child.path(), depth + 1, output)?;
            }
        }
        Ok(())
    }
    let mut catalog = SkillCatalog::default();
    let mut selected = BTreeMap::<String, Skill>::new();
    for root in &resources.roots {
        let mut paths = vec![];
        files(&root.path, 0, &mut paths)?;
        ensure!(paths.len() <= 1024, "Too many skills in a directory");
        for path in paths {
            let parsed = (|| -> Result<Skill> {
                ensure!(
                    path.metadata()?.len() <= 1024 * 1024,
                    "Skill file too large"
                );
                let text = std::fs::read_to_string(&path)?;
                let text = text.trim_start_matches('\u{feff}').replace("\r\n", "\n");
                let rest = text
                    .strip_prefix("---\n")
                    .ok_or_else(|| anyhow::anyhow!("Missing skill frontmatter"))?;
                let (header, _) = rest
                    .split_once("\n---\n")
                    .or_else(|| rest.strip_suffix("\n---").map(|header| (header, "")))
                    .ok_or_else(|| anyhow::anyhow!("Unclosed skill frontmatter"))?;
                let header: Frontmatter = serde_yaml::from_str(header)?;
                let skill = Skill {
                    name: header.name.unwrap_or_else(|| {
                        path.parent()
                            .unwrap()
                            .file_name()
                            .unwrap()
                            .to_string_lossy()
                            .into()
                    }),
                    description: header.description,
                    location: root
                        .location
                        .join(path.strip_prefix(&root.path)?)
                        .to_string_lossy()
                        .into(),
                    disable_model_invocation: header.disable_model_invocation,
                };
                prompt(std::slice::from_ref(&skill))?;
                Ok(skill)
            })();
            let skill = match parsed {
                Ok(skill) => skill,
                Err(e) => {
                    catalog.diagnostics.push(format!("{}: {e}", path.display()));
                    continue;
                }
            };
            if resources.reserved_names.contains(&skill.name) && !root.builtin {
                catalog.diagnostics.push(format!(
                    "{}: reserved skill name {} ignored",
                    path.display(),
                    skill.name
                ));
            } else if let Some(winner) = selected.get(&skill.name) {
                catalog.diagnostics.push(format!(
                    "{}: skill {} shadowed by {}",
                    path.display(),
                    skill.name,
                    winner.location
                ));
            } else {
                selected.insert(skill.name.clone(), skill);
            }
        }
    }
    if let Some(selection) = &resources.selection {
        for name in selection {
            ensure!(
                selected.contains_key(name),
                "Selected skill unavailable: {name}"
            );
        }
        selected.retain(|name, _| selection.contains(name));
    }
    catalog.skills = selected.into_values().collect();
    Ok(catalog)
}

#[cfg(test)]
mod discovery_tests {
    use super::*;
    use pot_core::{SkillResources, SkillRoot};
    struct Fixture(std::path::PathBuf);
    impl Fixture {
        fn new() -> Self {
            let path = std::env::temp_dir().join(format!("pot-skills-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&path).unwrap();
            Self(path)
        }
        fn write(&self, directory: &str, text: &str) {
            let path = self.0.join(directory);
            std::fs::create_dir_all(&path).unwrap();
            std::fs::write(path.join("SKILL.md"), text).unwrap();
        }
        fn resources(&self) -> SkillResources {
            SkillResources {
                roots: vec![SkillRoot {
                    path: self.0.clone(),
                    location: "/visible".into(),
                    builtin: false,
                }],
                ..Default::default()
            }
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }
    #[test]
    fn loader_and_authoring_validator_share_format_fixtures() {
        let f = Fixture::new();
        let cases: Vec<serde_json::Value> =
            serde_json::from_str(include_str!("../../scripts/tests/skill-format-cases.json"))
                .unwrap();
        for case in cases {
            f.write("example", case["text"].as_str().unwrap());
            let catalog = discover_skills(&f.resources()).unwrap();
            assert_eq!(
                !catalog.skills.is_empty(),
                case["valid"].as_bool().unwrap(),
                "{}: {:?}",
                case["id"],
                catalog.diagnostics
            );
        }
        f.write(
            "example",
            "---\nname: example\ndescription: useful\n---suffix\n",
        );
        assert!(discover_skills(&f.resources()).unwrap().skills.is_empty());
    }
    #[test]
    fn recursive_discovery_stops_at_bundles_and_skips_hidden_and_symlink_resources() {
        let f = Fixture::new();
        let text = "---\ndescription: valid bundle\n---\n";
        f.write("group/visible", text);
        f.write("group/visible/references/nested", text);
        f.write(".hidden", text);
        f.write("linked-file", text);
        let linked = f.0.join("linked-file/SKILL.md");
        std::fs::remove_file(&linked).unwrap();
        std::os::unix::fs::symlink(f.0.join("group/visible/SKILL.md"), linked).unwrap();
        let catalog = discover_skills(&f.resources()).unwrap();
        assert!(catalog.diagnostics.is_empty());
        assert_eq!(catalog.skills.len(), 1);
        assert_eq!(catalog.skills[0].name, "visible");
        assert_eq!(
            catalog.skills[0].location,
            "/visible/group/visible/SKILL.md"
        );
        let mut resources = f.resources();
        let alias = f.0.join("root-alias");
        std::os::unix::fs::symlink(f.0.join("group"), &alias).unwrap();
        resources.roots[0].path = alias;
        assert!(discover_skills(&resources).unwrap().skills.is_empty());
    }
    #[test]
    fn bom_crlf_yaml_and_explicit_only_metadata_are_loaded_without_bodies() {
        let f = Fixture::new();
        f.write("fallback", "\u{feff}---\r\ndescription: 'Quoted: <text> & data'\r\ndisable-model-invocation: true\r\n---\r\nPRIVATE BODY");
        let catalog = discover_skills(&f.resources()).unwrap();
        assert!(catalog.diagnostics.is_empty());
        assert_eq!(catalog.skills.len(), 1);
        assert_eq!(catalog.skills[0].name, "fallback");
        assert_eq!(catalog.skills[0].description, "Quoted: <text> & data");
        assert!(catalog.skills[0].disable_model_invocation);
        assert!(
            format_skills_for_prompt(&catalog.skills)
                .unwrap()
                .is_empty()
        );
    }
    #[test]
    fn malformed_bundles_do_not_hide_valid_neighbors() {
        let f = Fixture::new();
        for (name, text) in [
            ("missing", "plain markdown"),
            ("unclosed", "---\ndescription: no closing delimiter"),
            ("yaml", "---\ndescription: [broken\n---\n"),
            ("empty", "---\ndescription: ''\n---\n"),
            ("invalid", "---\nname: Invalid\ndescription: test\n---\n"),
        ] {
            f.write(name, text);
        }
        f.write("valid", "---\ndescription: valid\n---\n");
        let catalog = discover_skills(&f.resources()).unwrap();
        assert_eq!(catalog.skills.len(), 1);
        assert_eq!(catalog.skills[0].name, "valid");
        assert_eq!(catalog.diagnostics.len(), 5);
    }
    #[test]
    fn same_root_collisions_are_deterministic_and_selections_distinguish_empty_from_missing() {
        let f = Fixture::new();
        // Create in reverse order to ensure directory enumeration is sorted.
        for name in ["z", "a"] {
            f.write(
                name,
                &format!("---\nname: collision\ndescription: {name}\n---\n"),
            );
        }
        f.write("first", "---\ndescription: first\n---\n");
        let mut resources = f.resources();
        let catalog = discover_skills(&resources).unwrap();
        assert_eq!(
            catalog
                .skills
                .iter()
                .map(|s| s.name.as_str())
                .collect::<Vec<_>>(),
            ["collision", "first"]
        );
        assert_eq!(catalog.skills[0].description, "a");
        assert_eq!(catalog.diagnostics.len(), 1);
        resources.selection = Some(vec![]);
        assert!(discover_skills(&resources).unwrap().skills.is_empty());
        resources.selection = Some(vec!["missing".into()]);
        assert!(
            discover_skills(&resources)
                .unwrap_err()
                .to_string()
                .contains("Selected skill unavailable: missing")
        );
        resources.roots[0].path = f.0.join("nonexistent");
        resources.selection = None;
        assert!(discover_skills(&resources).unwrap().skills.is_empty());
    }
    #[test]
    fn directory_priority_reserved_names_yaml_diagnostics_and_refresh() {
        let base =
            std::env::temp_dir().join(format!("pot-skill-discovery-{}", uuid::Uuid::new_v4()));
        let roots: Vec<_> = ["project", "user", "builtin"]
            .iter()
            .enumerate()
            .map(|(i, name)| SkillRoot {
                path: base.join(name),
                location: std::path::PathBuf::from(format!("/visible/{name}")),
                builtin: i >= 2,
            })
            .collect();
        for root in &roots {
            std::fs::create_dir_all(root.path.join("example")).unwrap();
            std::fs::write(root.path.join("example/SKILL.md"),format!("---\nname: example\ndescription: >-\n  {} example\n  with folded YAML\n---\nSECRET BODY",root.path.display())).unwrap();
        }
        let resources = SkillResources {
            roots: roots.clone(),
            reserved_names: vec!["skill-creator".into()],
            selection: None,
        };
        for root in [&roots[0], &roots[2]] {
            std::fs::create_dir_all(root.path.join("manager")).unwrap();
            std::fs::write(
                root.path.join("manager/SKILL.md"),
                "---\nname: skill-creator\ndescription: Manage skills\n---\n",
            )
            .unwrap();
        }
        std::fs::create_dir_all(roots[0].path.join("invalid")).unwrap();
        std::fs::write(roots[0].path.join("invalid/SKILL.md"), "bad frontmatter").unwrap();
        std::os::unix::fs::symlink(&roots[2].path, roots[0].path.join("outside")).unwrap();
        let catalog = discover_skills(&resources).unwrap();
        assert_eq!(catalog.skills.len(), 2);
        assert_eq!(
            catalog.skills[0].location,
            "/visible/project/example/SKILL.md"
        );
        assert_eq!(
            catalog.skills[1].location,
            "/visible/builtin/manager/SKILL.md"
        );
        assert!(catalog.skills[0].description.contains("with folded YAML"));
        assert_eq!(catalog.diagnostics.len(), 4); // 2 shadows, reserved override, malformed file
        assert!(
            !format_skills_for_prompt(&catalog.skills)
                .unwrap()
                .contains("SECRET BODY")
        );
        std::fs::remove_dir_all(roots[0].path.join("example")).unwrap();
        assert_eq!(
            discover_skills(&resources).unwrap().skills[0].location,
            "/visible/user/example/SKILL.md"
        );
        let mut filtered = resources.clone();
        filtered.selection = Some(vec!["skill-creator".into()]);
        assert_eq!(discover_skills(&filtered).unwrap().skills.len(), 1);
        filtered.selection = Some(vec!["missing".into()]);
        assert!(discover_skills(&filtered).is_err());
        std::fs::remove_dir_all(base).unwrap();
    }
}
