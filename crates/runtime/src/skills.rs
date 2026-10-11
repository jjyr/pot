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
    if crate::remote_environment::is_remote(s) {
        return Ok(SkillResources::default());
    }
    let (base, _, cwd, visible) = crate::environment::workspace_view(s)?;
    let host = !s.is_worker || s.sandbox_id.is_none();
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
    let reserved_names = pot_agent::discover_skills(&SkillResources {
        roots: vec![builtin_root.clone()],
        ..Default::default()
    })?
    .skills
    .into_iter()
    .map(|skill| skill.name)
    .collect();
    roots.push(SkillRoot {
        path: s.agent_service.resources.skill_user.clone(),
        location: if host {
            s.agent_service.resources.skill_user.clone()
        } else {
            global_location
        },
        builtin: false,
    });
    roots.push(builtin_root);
    Ok(SkillResources {
        roots,
        reserved_names,
        selection: s.skill_selection.clone(),
    })
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
                .environment_service
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
                .environment_service
                .sandboxes
                .workspace("default")
                .join(".agents/skills"),
            "environment",
            "original sandbox",
        );
        sandbox::management(&root, &json!({"action":"create","sandbox_id":"second"}))
            .await
            .unwrap();
        bundle(
            &root
                .environment_service
                .sandboxes
                .workspace("second")
                .join(".agents/skills"),
            "environment",
            "new default",
        );
        sandbox::management(&root, &json!({"action":"select","sandbox_id":"second"}))
            .await
            .unwrap();
        bundle(
            &root.data.join("agent-workspace/.agents/skills"),
            "environment",
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
                    .find(|s| s.name == "environment")
                    .unwrap()
                    .description,
                expected
            );
        }
        std::fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn runtime_shares_flat_skills_project_priority_and_restores_task_filter() {
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
        let catalog = pot_agent::discover_skills(&resources(&worker).unwrap()).unwrap();
        assert_eq!(catalog.skills.len(), 1);
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
        for name in ["user-skill", "skill-creator", "skill-installer"] {
            let skill = automatic
                .skills
                .iter()
                .find(|skill| skill.name == name)
                .unwrap();
            assert_eq!(
                skill.location,
                format!(
                    "/data/workspace/tasks/{}/.pot/agent/skills/{name}/SKILL.md",
                    task.id
                )
            );
        }
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
