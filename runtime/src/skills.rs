//! Runtime selects resource roots; Agent discovery is filesystem-based and role agnostic.
use super::*;
use pot_core::{SkillResources, SkillRoot};

pub(crate) fn resources(s: &Service) -> Result<SkillResources> {
    let role = if s.is_worker { "task" } else { "main" };
    let target = if s.is_worker {
        s.sandbox_id
            .clone()
            .ok_or_else(|| anyhow!("Worker Sandbox missing"))?
    } else {
        s.sandboxes.assignment("")?
    };
    let base = s.sandboxes.workspace(&target);
    let (cwd, visible) = if s.is_worker {
        (
            base.join("tasks").join(&s.usage_scope),
            PathBuf::from("/data/workspace/tasks").join(&s.usage_scope),
        )
    } else {
        (base.clone(), PathBuf::from("/data/workspace"))
    };
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
    for (path, visible, builtin) in [
        (&s.sandboxes.skill_user, "/mnt/pot-skills/user", false),
        (&s.sandboxes.skill_builtin, "/mnt/pot-skills/builtin", true),
    ] {
        for directory in [role, "shared"] {
            roots.push(SkillRoot {
                path: path.join(directory),
                location: PathBuf::from(visible).join(directory),
                builtin,
            });
        }
    }
    Ok(SkillResources {
        roots,
        reserved_names: vec!["skill-creator".into(), "skill-installer".into()],
        selection: s.skill_selection.clone(),
    })
}
pub(crate) fn instructions(s: &Service, resources: &SkillResources) -> Result<String> {
    let catalog = pot_agent::discover_skills(resources)?;
    for message in &catalog.diagnostics {
        tracing_diagnostic(s, message)?;
    }
    pot_agent::format_skills_for_prompt(&catalog.skills)
}
fn tracing_diagnostic(_s: &Service, message: &str) -> Result<()> {
    eprintln!("Skill discovery: {message}");
    Ok(())
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
    fn builtin_management_skills_use_guest_locations_and_ignore_user_overrides() {
        let root = tasks::tests::fixture();
        for name in ["skill-creator", "skill-installer"] {
            bundle(
                &root.sandboxes.workspace("default").join(".agents/skills"),
                name,
                "forged project override",
            );
            bundle(
                &root.sandboxes.skill_user.join("main"),
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
                format!("/mnt/pot-skills/builtin/main/{name}/SKILL.md")
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
            &root.sandboxes.workspace("default").join(".agents/skills"),
            "environment",
            "original sandbox",
        );
        sandbox::management(&root, &json!({"action":"create","sandbox_id":"second"}))
            .await
            .unwrap();
        bundle(
            &root.sandboxes.workspace("second").join(".agents/skills"),
            "environment",
            "new default",
        );
        sandbox::management(&root, &json!({"action":"select","sandbox_id":"second"}))
            .await
            .unwrap();
        for (service, expected) in [(&root, "new default"), (&worker, "original sandbox")] {
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
    async fn runtime_selects_role_roots_project_priority_and_restores_task_filter() {
        let root = tasks::tests::fixture();
        for role in ["shared", "main", "task"] {
            bundle(&root.sandboxes.skill_user.join(role), role, role);
            bundle(&root.sandboxes.skill_user.join(role), "collision", role);
        }
        let main = resources(&root).unwrap();
        let catalog = pot_agent::discover_skills(&main).unwrap();
        for name in ["skill-creator", "skill-installer"] {
            assert!(catalog.skills.iter().any(|s| s.name == name));
        }
        assert!(catalog.skills.iter().any(|s| s.name == "main"));
        assert!(!catalog.skills.iter().any(|s| s.name == "task"));
        assert_eq!(
            catalog
                .skills
                .iter()
                .find(|s| s.name == "collision")
                .unwrap()
                .description,
            "main"
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
        assert!(automatic.skills.iter().any(|s| s.name == "task"));
        assert!(
            !automatic.skills.iter().any(|s| s.name == "main"
                || s.name == "skill-creator"
                || s.name == "skill-installer")
        );
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
