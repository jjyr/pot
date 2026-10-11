//! Control-selected global bundles. Execution nodes only materialize supplied versions.
use crate::*;
use base64::{Engine, engine::general_purpose::STANDARD};
use pot_core::{SkillResources, SkillRoot, plugins::PluginConfig};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{collections::BTreeMap, os::unix::fs::PermissionsExt, path::Path};

const MAX_BYTES: usize = 512 * 1024;
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ResourceFile {
    pub data: String,
    pub executable: bool,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GlobalResources {
    pub files: BTreeMap<String, ResourceFile>,
    pub reserved_names: Vec<String>,
    pub plugins: BTreeMap<String, PluginConfig>,
}
impl GlobalResources {
    pub fn revision(&self) -> Result<String> {
        let bytes = serde_json::to_vec(self)?;
        anyhow::ensure!(
            bytes.len() <= MAX_BYTES,
            "Global resource snapshot exceeds 512 KiB"
        );
        Ok(Sha256::digest(bytes)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect())
    }
    pub fn validate(&self) -> Result<()> {
        self.revision()?;
        anyhow::ensure!(self.files.len() <= 2048, "Too many global resource files");
        let settings = pot_core::AgentSettings {
            plugins: self.plugins.clone(),
            ..Default::default()
        };
        settings.validate_plugins()?;
        anyhow::ensure!(
            self.plugins
                .values()
                .all(|p| p.source.is_none() && (!p.enabled || p.bundle.is_some())),
            "Control must pin plugins before delivery"
        );
        for (name, file) in &self.files {
            anyhow::ensure!(
                pot_protocol::valid_file_path(name)
                    && name.starts_with("skills/")
                    && name.split('/').count() >= 4,
                "Invalid global resource path"
            );
            STANDARD.decode(&file.data)?;
        }
        for name in &self.reserved_names {
            anyhow::ensure!(
                self.files
                    .contains_key(&format!("skills/builtin/{name}/SKILL.md")),
                "Missing reserved skill bundle"
            );
        }
        Ok(())
    }
}
fn collect(
    root: &Path,
    relative: &Path,
    output: &mut BTreeMap<String, ResourceFile>,
    depth: usize,
) -> Result<()> {
    anyhow::ensure!(depth < 32, "Global resource nesting exceeds limit");
    let dir = root.join(relative);
    anyhow::ensure!(
        !std::fs::symlink_metadata(&dir)?.file_type().is_symlink(),
        "Global bundle contains symlink"
    );
    for entry in std::fs::read_dir(dir)? {
        let entry = entry?;
        let path = relative.join(entry.file_name());
        let metadata = std::fs::symlink_metadata(entry.path())?;
        anyhow::ensure!(
            !metadata.file_type().is_symlink(),
            "Global bundle contains symlink"
        );
        if metadata.is_dir() {
            collect(root, &path, output, depth + 1)?;
        } else {
            anyhow::ensure!(
                metadata.is_file() && metadata.len() <= MAX_BYTES as u64,
                "Invalid or oversized global resource file"
            );
            let name = path
                .to_str()
                .ok_or_else(|| anyhow!("Global bundle path is not UTF-8"))?;
            output.insert(
                format!("skills/{name}"),
                ResourceFile {
                    data: STANDARD.encode(std::fs::read(entry.path())?),
                    executable: metadata.permissions().mode() & 0o111 != 0,
                },
            );
            anyhow::ensure!(
                output.len() <= 2048 && serde_json::to_vec(output)?.len() <= MAX_BYTES,
                "Global resource snapshot exceeds limit"
            );
        }
    }
    Ok(())
}
pub(crate) fn snapshot(s: &Service) -> Result<GlobalResources> {
    let path = s.data.join("control-global-resources.json");
    if s.is_worker && path.exists() {
        let snapshot: GlobalResources = serde_json::from_slice(&std::fs::read(path)?)?;
        snapshot.validate()?;
        return Ok(snapshot);
    }
    let resources = &s.agent_service.resources;
    let builtin = SkillRoot {
        path: resources.skill_builtin.clone(),
        location: resources.skill_builtin.clone(),
        builtin: true,
    };
    let reserved_names = pot_agent::discover_skills(&SkillResources {
        roots: vec![builtin.clone()],
        ..Default::default()
    })?
    .skills
    .into_iter()
    .map(|s| s.name)
    .collect();
    let catalog = pot_agent::discover_skills(&SkillResources {
        roots: vec![
            SkillRoot {
                path: resources.skill_user.clone(),
                location: resources.skill_user.clone(),
                builtin: false,
            },
            builtin,
        ],
        reserved_names,
        selection: None,
    })?;
    let mut result = GlobalResources {
        plugins: agent::thread(s)?.config.params.agent.plugins,
        ..Default::default()
    };
    for skill in catalog.skills {
        let file = PathBuf::from(skill.location);
        let dir = file
            .parent()
            .ok_or_else(|| anyhow!("Invalid skill location"))?;
        let mut files = BTreeMap::new();
        collect(dir, Path::new(""), &mut files, 0)?;
        let kind = if dir.starts_with(&resources.skill_builtin) {
            "builtin"
        } else {
            "user"
        };
        for (path, content) in files {
            result.files.insert(
                format!(
                    "skills/{kind}/{}/{}",
                    skill.name,
                    path.strip_prefix("skills/").unwrap()
                ),
                content,
            );
        }
        if dir.starts_with(&resources.skill_builtin) {
            result.reserved_names.push(skill.name);
        }
    }
    result.reserved_names.sort();
    result.validate()?;
    if s.is_worker {
        execution::private_json(&path, &json!(result))?;
    }
    Ok(result)
}
/// Called only after bound Control identity and execution lease checks.
pub fn install(workspace: &Path, snapshot: &GlobalResources) -> Result<String> {
    snapshot.validate()?;
    let revision = snapshot.revision()?;
    let parent = workspace.join(".pot/control-resources");
    for path in [workspace.join(".pot"), parent.clone()] {
        if path.exists() {
            anyhow::ensure!(
                !std::fs::symlink_metadata(&path)?.file_type().is_symlink(),
                "Invalid resource cache directory"
            );
        }
    }
    std::fs::create_dir_all(&parent)?;
    anyhow::ensure!(
        !std::fs::symlink_metadata(workspace.join(".pot"))?
            .file_type()
            .is_symlink()
            && !std::fs::symlink_metadata(&parent)?.file_type().is_symlink(),
        "Invalid resource cache directory"
    );
    let root = parent.join(&revision);
    if root.exists() {
        verify(&root, snapshot)?;
    } else {
        let temp = parent.join(format!(".install-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&temp)?;
        let result = (|| -> Result<()> {
            for (path, content) in &snapshot.files {
                let file = temp.join(path);
                std::fs::create_dir_all(file.parent().unwrap())?;
                std::fs::write(&file, STANDARD.decode(&content.data)?)?;
                std::fs::set_permissions(
                    &file,
                    std::fs::Permissions::from_mode(if content.executable { 0o555 } else { 0o444 }),
                )?;
            }
            execution::private_json(&temp.join("manifest.json"), &json!(snapshot))?;
            std::fs::rename(&temp, &root)?;
            Ok(())
        })();
        if result.is_err() {
            let _ = std::fs::remove_dir_all(temp);
        }
        result?;
    }
    execution::private_json(&parent.join("active.json"), &json!({"revision":revision}))?;
    Ok(revision)
}
fn verify(root: &Path, snapshot: &GlobalResources) -> Result<()> {
    anyhow::ensure!(
        !std::fs::symlink_metadata(root)?.file_type().is_symlink(),
        "Invalid global resource cache"
    );
    for (path, content) in &snapshot.files {
        let chunk = crate::files::read_chunk(
            root,
            &pot_protocol::FileQuery {
                path: path.clone(),
                ..Default::default()
            },
        )?;
        let expected = STANDARD.decode(&content.data)?;
        // Resource files may exceed file transport's per-chunk bound.
        anyhow::ensure!(
            chunk.size == expected.len() as u64,
            "Global resource cache changed"
        );
        let mut offset = 0;
        while offset < chunk.size {
            let query = pot_protocol::FileQuery {
                path: path.clone(),
                offset,
                revision: chunk.revision.clone(),
            };
            let data = STANDARD.decode(crate::files::read_chunk(root, &query)?.data)?;
            anyhow::ensure!(
                expected[offset as usize..offset as usize + data.len()] == data,
                "Global resource cache changed"
            );
            offset += data.len() as u64;
        }
    }
    Ok(())
}
fn read_json<T: serde::de::DeserializeOwned>(root: &Path, path: &str) -> Result<T> {
    let mut query = pot_protocol::FileQuery {
        path: path.into(),
        ..Default::default()
    };
    let mut bytes = Vec::new();
    loop {
        let chunk = crate::files::read_chunk(root, &query)?;
        anyhow::ensure!(
            chunk.size <= MAX_BYTES as u64,
            "Resource manifest exceeds limit"
        );
        let data = STANDARD.decode(chunk.data)?;
        bytes.extend(data);
        query.offset = bytes.len() as u64;
        query.revision = chunk.revision;
        if query.offset == chunk.size {
            break;
        }
    }
    Ok(serde_json::from_slice(&bytes)?)
}
pub(crate) fn installed_snapshot(source: &Path) -> Result<Option<(String, GlobalResources)>> {
    let active = ".pot/control-resources/active.json";
    if !source.join(active).try_exists()? {
        return Ok(None);
    }
    let value: Value = read_json(source, active)?;
    let revision = value["revision"]
        .as_str()
        .ok_or_else(|| anyhow!("Resource revision missing"))?
        .to_owned();
    anyhow::ensure!(
        revision.len() == 64 && revision.bytes().all(|b| b.is_ascii_hexdigit()),
        "Invalid resource revision"
    );
    let relative = format!(".pot/control-resources/{revision}");
    let snapshot: GlobalResources = read_json(source, &format!("{relative}/manifest.json"))?;
    snapshot.validate()?;
    anyhow::ensure!(
        snapshot.revision()? == revision,
        "Global resource manifest changed"
    );
    verify(&source.join(relative), &snapshot)?;
    Ok(Some((revision, snapshot)))
}
pub(crate) fn installed_resources(source: &Path, visible: &Path) -> Result<Option<SkillResources>> {
    let Some((revision, snapshot)) = installed_snapshot(source)? else {
        return Ok(None);
    };
    let relative = PathBuf::from(".pot/control-resources").join(revision);
    let root = source.join(&relative);
    Ok(Some(SkillResources {
        roots: vec![
            SkillRoot {
                path: root.join("skills/user"),
                location: visible.join(&relative).join("skills/user"),
                builtin: false,
            },
            SkillRoot {
                path: root.join("skills/builtin"),
                location: visible.join(relative).join("skills/builtin"),
                builtin: true,
            },
        ],
        reserved_names: snapshot.reserved_names,
        selection: None,
    }))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn skill(snapshot: &mut GlobalResources, kind: &str, name: &str, description: &str) {
        snapshot.files.insert(
            format!("skills/{kind}/{name}/SKILL.md"),
            ResourceFile {
                data: STANDARD.encode(format!(
                    "---\nname: {name}\ndescription: {description}\n---\n"
                )),
                executable: false,
            },
        );
    }
    #[test]
    fn supplied_globals_replace_destination_discovery_preserve_project_rules_and_detect_tampering()
    {
        let root =
            std::env::temp_dir().join(format!("pot-control-globals-{}", uuid::Uuid::new_v4()));
        let workspace = root.join("project");
        std::fs::create_dir_all(workspace.join(".agents/skills/shared")).unwrap();
        std::fs::create_dir_all(root.join("skills-user/node-only")).unwrap();
        std::fs::write(
            root.join("skills-user/node-only/SKILL.md"),
            "---\nname: node-only\ndescription: forbidden node global\n---\n",
        )
        .unwrap();
        std::fs::write(workspace.join("AGENTS.md"), "PROJECT_RULE").unwrap();
        std::fs::write(
            workspace.join(".agents/skills/shared/SKILL.md"),
            "---\nname: shared\ndescription: project wins\n---\n",
        )
        .unwrap();
        let mut snapshot = GlobalResources {
            plugins: BTreeMap::new(),
            ..Default::default()
        };
        skill(&mut snapshot, "user", "shared", "control user");
        skill(&mut snapshot, "user", "control-only", "Control global");
        skill(&mut snapshot, "builtin", "reserved", "Control builtin");
        snapshot.reserved_names.push("reserved".into());
        let revision = install(&workspace, &snapshot).unwrap();
        assert_eq!(revision, install(&workspace, &snapshot).unwrap());
        let catalog = crate::skills::destination_catalog(
            &root.join("data"),
            &workspace,
            &workspace,
            &workspace,
            true,
        )
        .unwrap();
        assert!(catalog.skills.iter().any(|s| s.name == "control-only"));
        assert!(catalog.skills.iter().any(|s| s.name == "reserved"));
        assert!(!catalog.skills.iter().any(|s| s.name == "node-only"));
        assert_eq!(
            catalog
                .skills
                .iter()
                .find(|s| s.name == "shared")
                .unwrap()
                .description,
            "project wins"
        );
        assert!(
            crate::instructions::project_workspace(
                workspace.clone(),
                workspace.clone(),
                workspace.clone()
            )
            .unwrap()
            .text
            .contains("PROJECT_RULE")
        );
        let file = workspace
            .join(".pot/control-resources")
            .join(revision)
            .join("skills/user/control-only/SKILL.md");
        std::fs::set_permissions(&file, std::fs::Permissions::from_mode(0o600)).unwrap();
        std::fs::write(file, "tampered").unwrap();
        assert!(install(&workspace, &snapshot).is_err());
        assert!(installed_resources(&workspace, &workspace).is_err());
        let mut invalid = snapshot.clone();
        invalid.files.insert(
            "skills/user/../../escape".into(),
            ResourceFile {
                data: STANDARD.encode("x"),
                executable: false,
            },
        );
        assert!(install(&workspace, &invalid).is_err());
        let redirected = root.join("redirected");
        std::fs::create_dir(&redirected).unwrap();
        std::os::unix::fs::symlink(root.join("outside"), redirected.join(".pot")).unwrap();
        assert!(install(&redirected, &snapshot).is_err());
        assert!(!root.join("outside").exists());
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn control_task_snapshot_remains_fixed_after_global_changes() {
        let root = tasks::tests::fixture();
        let user = root.agent_service.resources.skill_user.join("fixed-global");
        std::fs::create_dir_all(&user).unwrap();
        std::fs::write(
            user.join("SKILL.md"),
            "---\nname: fixed-global\ndescription: original\n---\n",
        )
        .unwrap();
        let mut worker = root.clone();
        worker.scope.is_worker = true;
        worker.scope.data = root.data.join("resource-fixture");
        std::fs::create_dir_all(&worker.data).unwrap();
        let first = snapshot(&worker).unwrap();
        std::fs::write(
            user.join("SKILL.md"),
            "---\nname: fixed-global\ndescription: updated\n---\n",
        )
        .unwrap();
        assert_eq!(
            first.revision().unwrap(),
            snapshot(&worker).unwrap().revision().unwrap()
        );
        assert_ne!(
            first.revision().unwrap(),
            snapshot(&root).unwrap().revision().unwrap()
        );
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
}
