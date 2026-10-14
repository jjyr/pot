//! Runtime selects resource roots; Agent discovery is filesystem-based and role agnostic.
use super::*;
use pot_core::{SkillResources, SkillRoot};

/// Discover project resources plus Control-supplied global bundles.
/// Destination user/builtin catalogs are never discovery sources.
pub(crate) fn destination_catalog(
    _data: &std::path::Path,
    source: &std::path::Path,
    mount: &std::path::Path,
    cwd: &std::path::Path,
    _host: bool,
) -> Result<pot_agent::SkillCatalog> {
    let relative = cwd.strip_prefix(mount)?;
    let mut project = source.join(relative);
    let mut location = cwd.to_path_buf();
    let mut roots = vec![];
    loop {
        roots.push(SkillRoot {
            path: project.join(".agents/skills"),
            location: location.join(".agents/skills"),
            builtin: false,
        });
        if project.join(".git").exists() || project == source {
            break;
        }
        project = project
            .parent()
            .ok_or_else(|| anyhow!("Invalid destination skill root"))?
            .to_path_buf();
        location = location
            .parent()
            .ok_or_else(|| anyhow!("Invalid destination skill location"))?
            .to_path_buf();
    }
    let mut resources = crate::control_resources::installed_resources(&source.join(relative), cwd)?
        .unwrap_or_default();
    roots.append(&mut resources.roots);
    resources.roots = roots;
    pot_agent::discover_skills(&resources)
}

pub(crate) fn resources(s: &Service) -> Result<SkillResources> {
    if crate::remote_execution::is_remote(s) {
        return Ok(SkillResources::default());
    }
    let (base, _, cwd, visible) = crate::execution_target::workspace_view(s)?;
    let host = s.sandbox_id.is_none();
    let mut roots = vec![];
    let mut project = cwd.as_path();
    let mut location = visible.as_path();
    loop {
        roots.push(SkillRoot {
            path: project.join(".agents/skills"),
            location: location.join(".agents/skills"),
            builtin: false,
        });
        if project.join(".git").exists() || project == base {
            break;
        }
        let (Some(parent), Some(visible_parent)) = (project.parent(), location.parent()) else {
            break;
        };
        project = parent;
        location = visible_parent;
    }
    if s.is_worker
        && let Some(mut installed) = crate::control_resources::installed_resources(&cwd, &visible)?
    {
        roots.append(&mut installed.roots);
        installed.roots = roots;
        return Ok(installed);
    }
    let global_location = visible.join(".pot/agent/skills");
    let builtin_root = SkillRoot {
        path: s.agent_service.resources.skill_builtin.clone(),
        location: if host {
            s.agent_service.resources.skill_builtin.clone()
        } else {
            global_location.clone()
        },
        builtin: true,
    };
    let mut reserved_names: Vec<String> = pot_agent::discover_skills(&SkillResources {
        roots: vec![builtin_root.clone()],
        ..Default::default()
    })?
    .skills
    .into_iter()
    .map(|skill| skill.name)
    .collect();
    let globals = vec![
        SkillRoot {
            path: s.agent_service.resources.skill_user.clone(),
            location: if host {
                s.agent_service.resources.skill_user.clone()
            } else {
                global_location
            },
            builtin: false,
        },
        builtin_root,
    ];
    if s.is_worker {
        let selected = selected_global_roots(
            globals,
            &reserved_names,
            s.skill_selection.clone().unwrap_or_default(),
        )?;
        reserved_names.retain(|name| {
            s.skill_selection
                .as_ref()
                .is_some_and(|names| names.contains(name))
        });
        roots.extend(selected);
    } else {
        roots.extend(globals);
    }
    Ok(SkillResources {
        roots,
        reserved_names,
        selection: None,
    })
}

/// Select only global bundles before merging the independently discovered project roots.
fn selected_global_roots(
    roots: Vec<SkillRoot>,
    reserved_names: &[String],
    selection: Vec<String>,
) -> Result<Vec<SkillRoot>> {
    let catalog = pot_agent::discover_skills(&SkillResources {
        roots: roots
            .iter()
            .map(|root| SkillRoot {
                location: root.path.clone(),
                ..root.clone()
            })
            .collect(),
        reserved_names: reserved_names.to_vec(),
        selection: Some(selection),
    })?;
    catalog
        .skills
        .into_iter()
        .map(|skill| {
            let location = PathBuf::from(skill.location);
            let root = roots
                .iter()
                .find(|root| location.starts_with(&root.path))
                .ok_or_else(|| anyhow!("Selected global skill has no resource root"))?;
            let relative = location.strip_prefix(&root.path)?;
            Ok(SkillRoot {
                path: root.path.join(relative).parent().unwrap().to_path_buf(),
                location: root.location.join(relative).parent().unwrap().to_path_buf(),
                builtin: root.builtin,
            })
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    fn bundle(root: &std::path::Path, name: &str, description: &str) {
        std::fs::create_dir_all(root.join(name)).unwrap();
        std::fs::write(
            root.join(name).join("SKILL.md"),
            format!("---\nname: {name}\ndescription: {description}\n---\n"),
        )
        .unwrap();
    }
    #[test]
    fn builtin_management_skills_use_actual_host_locations_and_ignore_user_overrides() {
        let root = tasks::tests::fixture();
        for name in ["skill-creator", "skill-installer"] {
            bundle(
                &root.data.join("agent-workspace/.agents/skills"),
                name,
                "forged project override",
            );
            bundle(
                &root.agent_service.resources.skill_user,
                name,
                "forged user override",
            );
        }
        let catalog = pot_agent::discover_skills(&resources(&root).unwrap()).unwrap();
        for name in ["skill-creator", "skill-installer"] {
            let skill = catalog
                .skills
                .iter()
                .find(|skill| skill.name == name)
                .unwrap();
            assert_eq!(
                skill.location,
                root.agent_service
                    .resources
                    .skill_builtin
                    .join(name)
                    .join("SKILL.md")
                    .to_string_lossy()
            );
            assert!(
                std::fs::read_to_string(&skill.location)
                    .unwrap()
                    .contains(&format!("name: {name}"))
            );
            assert!(!skill.description.contains("forged"));
        }
        assert_eq!(catalog.diagnostics.len(), 4);
        let mut sandbox_main = root.clone();
        sandbox_main.sandbox_id = Some(root.execution_service.sandboxes.ensure_shared().unwrap());
        let (_, _, _, visible) = crate::execution_target::workspace_view(&sandbox_main).unwrap();
        let sandbox_catalog =
            pot_agent::discover_skills(&resources(&sandbox_main).unwrap()).unwrap();
        for name in ["skill-creator", "skill-installer"] {
            let skill = sandbox_catalog
                .skills
                .iter()
                .find(|skill| skill.name == name)
                .unwrap();
            assert_eq!(
                skill.location,
                visible
                    .join(".pot/agent/skills")
                    .join(name)
                    .join("SKILL.md")
                    .to_string_lossy()
            );
        }
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn task_project_discovery_stops_at_git_directory_or_worktree_file() {
        for worktree_file in [false, true] {
            let root = tasks::tests::fixture();
            let task = tasks::create(
                State(root.clone()),
                tasks::tests::headers(),
                Json(CreateTask {
                    request_id: "project-boundary".into(),
                    text: "fixture".into(),
                    ..Default::default()
                }),
            )
            .await
            .unwrap()
            .0;
            let worker = tasks::owner(&root, &task.id).unwrap();
            let base = root
                .execution_service
                .sandboxes
                .workspace(worker.sandbox_id.as_ref().unwrap());
            bundle(&base.join(".agents/skills"), "outside", "sandbox ancestor");
            bundle(
                &worker.workspace.join(".agents/skills"),
                "inside",
                "task repository",
            );
            let git = worker.workspace.join(".git");
            if worktree_file {
                std::fs::write(&git, "gitdir: /unused/worktree").unwrap();
            } else {
                std::fs::create_dir(&git).unwrap();
            }
            let catalog = pot_agent::discover_skills(&resources(&worker).unwrap()).unwrap();
            assert!(catalog.skills.iter().any(|s| s.name == "inside"));
            assert!(!catalog.skills.iter().any(|s| s.name == "outside"));
            if worktree_file {
                std::fs::remove_file(git).unwrap();
            } else {
                std::fs::remove_dir(git).unwrap();
            }
            let catalog = pot_agent::discover_skills(&resources(&worker).unwrap()).unwrap();
            let inherited = catalog.skills.iter().find(|s| s.name == "outside").unwrap();
            assert_eq!(
                inherited.location,
                "/data/workspace/.agents/skills/outside/SKILL.md"
            );
            std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
        }
    }
    #[tokio::test]
    async fn task_skill_resources_remain_bound_when_main_changes_default() {
        let root = tasks::tests::fixture();
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: "fixed-skills".into(),
                text: "fixture".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let worker = tasks::owner(&root, &task.id).unwrap();
        bundle(
            &root
                .execution_service
                .sandboxes
                .workspace(&task.sandbox_id)
                .join(".agents/skills"),
            "execution-target",
            "original sandbox",
        );
        sandbox::management(&root, &json!({"action":"create","sandbox_id":"second"}))
            .await
            .unwrap();
        bundle(
            &root
                .execution_service
                .sandboxes
                .workspace("second")
                .join(".agents/skills"),
            "execution-target",
            "new default",
        );
        sandbox::management(&root, &json!({"action":"select","sandbox_id":"second"}))
            .await
            .unwrap();
        bundle(
            &root.data.join("agent-workspace/.agents/skills"),
            "execution-target",
            "actual Host project",
        );
        for (service, expected) in [
            (&root, "actual Host project"),
            (&worker, "original sandbox"),
        ] {
            let catalog = pot_agent::discover_skills(&resources(service).unwrap()).unwrap();
            assert_eq!(
                catalog
                    .skills
                    .iter()
                    .find(|s| s.name == "execution-target")
                    .unwrap()
                    .description,
                expected
            );
        }
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn completions_offer_globals_without_project_selectors() {
        let root = tasks::tests::fixture();
        bundle(
            &root.data.join("agent-workspace/.agents/skills"),
            "project-only",
            "project",
        );
        bundle(
            &root.agent_service.resources.skill_user,
            "global-only",
            "global",
        );
        let response = crate::control::completions(State(root.clone()), tasks::tests::headers())
            .await
            .unwrap()
            .0;
        let text = response.to_string();
        assert!(text.contains("/skill:global-only"));
        assert!(text.contains("/skill:skill-creator"));
        assert!(!text.contains("/skill:project-only"));
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn task_selects_globals_independently_of_project_skills_and_restores_selection() {
        let root = tasks::tests::fixture();
        bundle(
            &root.agent_service.resources.skill_user,
            "user-skill",
            "same for every agent",
        );
        bundle(
            &root.agent_service.resources.skill_user,
            "collision",
            "user",
        );
        let main = resources(&root).unwrap();
        let catalog = pot_agent::discover_skills(&main).unwrap();
        for name in ["skill-creator", "skill-installer"] {
            assert!(catalog.skills.iter().any(|s| s.name == name));
        }
        assert!(catalog.skills.iter().any(|s| s.name == "user-skill"));
        assert_eq!(
            catalog
                .skills
                .iter()
                .find(|s| s.name == "collision")
                .unwrap()
                .description,
            "user"
        );
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: "skill-task".into(),
                text: "fixture".into(),
                skills: Some(vec!["collision".into()]),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let worker = tasks::owner(&root, &task.id).unwrap();
        bundle(
            &worker.workspace.join(".agents/skills"),
            "collision",
            "project wins",
        );
        bundle(
            &worker.workspace.join(".agents/skills"),
            "project-only",
            "always discovered",
        );
        let catalog = pot_agent::discover_skills(&resources(&worker).unwrap()).unwrap();
        assert_eq!(catalog.skills.len(), 2);
        assert_eq!(catalog.skills[0].description, "project wins");
        assert_eq!(
            catalog.skills[0].location,
            format!(
                "/data/workspace/tasks/{}/.agents/skills/collision/SKILL.md",
                task.id
            )
        );
        let mut unfiltered = worker.clone();
        unfiltered.skill_selection = None;
        let automatic = pot_agent::discover_skills(&resources(&unfiltered).unwrap()).unwrap();
        assert_eq!(automatic.skills.len(), 2);
        assert_eq!(automatic.skills[0].name, "collision");
        unfiltered.skill_selection = Some(vec![]);
        assert_eq!(
            pot_agent::discover_skills(&resources(&unfiltered).unwrap())
                .unwrap()
                .skills,
            automatic.skills
        );
        unfiltered.skill_selection = Some(vec!["skill-creator".into()]);
        let builtin = pot_agent::discover_skills(&resources(&unfiltered).unwrap()).unwrap();
        assert!(
            builtin
                .skills
                .iter()
                .any(|skill| skill.name == "skill-creator")
        );
        assert!(
            builtin
                .skills
                .iter()
                .any(|skill| skill.name == "project-only")
        );
        unfiltered.skill_selection = Some(vec!["project-only".into()]);
        assert!(resources(&unfiltered).is_err());
        unfiltered.skill_selection = Some(vec!["missing-global".into()]);
        assert!(resources(&unfiltered).is_err());
        tasks::restore(&root).unwrap();
        assert_eq!(
            tasks::owner(&root, &task.id).unwrap().skill_selection,
            Some(vec!["collision".into()])
        );
        assert!(
            !root.data.parent().unwrap().join("helper.sock").exists(),
            "discovery started VM"
        );
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
}
