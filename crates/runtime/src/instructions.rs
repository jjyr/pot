//! Runtime-owned instruction discovery. Agents only receive the resulting text.
use super::*;
use std::{
    collections::VecDeque,
    fs, io,
    path::{Component, Path},
};

#[derive(Default)]
pub(crate) struct GlobalInstructions {
    last_good: String,
    warning: Option<String>,
}
impl GlobalInstructions {
    fn refresh(&mut self, path: &Path) -> (String, Option<String>) {
        match fs::read(path) {
            Ok(bytes) => {
                self.last_good = String::from_utf8_lossy(&bytes).trim().to_owned();
                self.warning = None;
                (self.last_good.clone(), None)
            }
            Err(e) if e.kind() == io::ErrorKind::NotFound => {
                self.last_good.clear();
                self.warning = None;
                (String::new(), None)
            }
            Err(e) => {
                let warning = format!(
                    "Failed to refresh global AGENTS.md {}: {e}; retaining last successful instructions",
                    path.display()
                );
                let fresh = (self.warning.as_ref() != Some(&warning)).then(|| warning.clone());
                self.warning = Some(warning);
                (self.last_good.clone(), fresh)
            }
        }
    }
}
#[derive(Default)]
pub(crate) struct InstructionCatalog {
    pub text: String,
    pub diagnostics: Vec<String>,
}

/// Discovery uses execution-visible paths, independently of the model's Host cwd.
/// Implementations own mount/path semantics; no process is started by discovery.
trait InstructionFileSystem {
    fn exists(&self, path: &Path) -> io::Result<bool>;
    fn read(&self, path: &Path) -> io::Result<Option<String>>;
}

#[derive(Debug)]
struct OutsideWorkspace;
impl std::fmt::Display for OutsideWorkspace {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("instruction path outside Sandbox workspace mount")
    }
}
impl std::error::Error for OutsideWorkspace {}

/// The current Sandbox workspace is a Host-shared mount. Resolve symlinks using
/// guest paths, not Host canonicalize: /data/workspace links have guest meaning.
struct WorkspaceFileSystem {
    source: PathBuf,
    mount: PathBuf,
}
impl WorkspaceFileSystem {
    fn resolve(&self, path: &Path) -> io::Result<PathBuf> {
        let denied = || io::Error::new(io::ErrorKind::PermissionDenied, OutsideWorkspace);
        let relative = path.strip_prefix(&self.mount).map_err(|_| denied())?;
        let mut queue: VecDeque<_> = relative
            .components()
            .map(|c| c.as_os_str().to_owned())
            .collect();
        let mut resolved = PathBuf::new();
        let mut links = 0;
        while let Some(part) = queue.pop_front() {
            if part == "." {
                continue;
            }
            if part == ".." {
                if !resolved.pop() {
                    return Err(denied());
                }
                continue;
            }
            let host = self.source.join(&resolved).join(&part);
            let metadata = fs::symlink_metadata(&host)?;
            if !metadata.file_type().is_symlink() {
                resolved.push(part);
                continue;
            }
            links += 1;
            if links > 40 {
                return Err(io::Error::other("instruction symlink loop"));
            }
            let target = fs::read_link(host)?;
            let target = if target.is_absolute() {
                resolved.clear();
                target
                    .strip_prefix(&self.mount)
                    .map_err(|_| denied())?
                    .to_path_buf()
            } else {
                target
            };
            for component in target.components().rev() {
                match component {
                    Component::Normal(p) => queue.push_front(p.to_owned()),
                    Component::ParentDir => queue.push_front("..".into()),
                    Component::CurDir => {}
                    _ => return Err(denied()),
                }
            }
        }
        Ok(self.source.join(resolved))
    }
}
impl InstructionFileSystem for WorkspaceFileSystem {
    fn exists(&self, path: &Path) -> io::Result<bool> {
        match self.resolve(path) {
            Ok(_) => Ok(true),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(false),
            Err(e) => Err(e),
        }
    }
    fn read(&self, path: &Path) -> io::Result<Option<String>> {
        let host = match self.resolve(path) {
            Ok(host) => host,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
            Err(e) => return Err(e),
        };
        if !fs::metadata(&host)?.is_file() {
            return Ok(None);
        }
        match fs::read(host) {
            Ok(bytes) => Ok(Some(String::from_utf8_lossy(&bytes).into_owned())),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e),
        }
    }
}
fn project(
    fs: &dyn InstructionFileSystem,
    cwd: &Path,
    boundary: &Path,
) -> Result<InstructionCatalog> {
    anyhow::ensure!(
        cwd.starts_with(boundary),
        "Instruction cwd outside execution boundary"
    );
    let mut ancestors = vec![];
    let mut cursor = cwd;
    let mut found_root = false;
    loop {
        ancestors.push(cursor.to_path_buf());
        if fs.exists(&cursor.join(".git"))? {
            found_root = true;
            break;
        }
        if cursor == boundary {
            break;
        }
        let Some(parent) = cursor.parent() else {
            break;
        };
        cursor = parent;
    }
    let directories = if found_root {
        ancestors.reverse();
        ancestors
    } else {
        vec![cwd.to_path_buf()]
    };
    let mut catalog = InstructionCatalog::default();
    for directory in directories {
        let path = directory.join("AGENTS.md");
        match fs.read(&path) {
            Ok(Some(text)) if !text.trim().is_empty() => catalog.text.push_str(&format!(
                "\n\n# AGENTS.md instructions for {}\n{}",
                directory.display(),
                text
            )),
            Ok(_) => {}
            Err(e)
                if e.get_ref()
                    .is_some_and(|cause| cause.is::<OutsideWorkspace>()) =>
            {
                catalog.diagnostics.push(format!("{}: {e}", path.display()))
            }
            Err(e) => return Err(e.into()),
        }
    }
    Ok(catalog)
}
pub(crate) fn load(s: &Service) -> Result<InstructionCatalog> {
    if crate::remote_environment::is_remote(s) {
        let contexts = s.remote_context.lock().unwrap();
        let context = contexts
            .get(s.environment_ref.as_ref().unwrap())
            .ok_or_else(|| anyhow!("Remote project instructions have not been prepared"))?;
        let mut skills: Vec<pot_agent::Skill> = serde_json::from_value(
            context
                .context
                .get("skills")
                .cloned()
                .unwrap_or_else(|| json!([])),
        )?;
        if let Some(selection) = &s.skill_selection {
            anyhow::ensure!(
                selection
                    .iter()
                    .all(|name| skills.iter().any(|skill| &skill.name == name)),
                "Selected skill is not installed on the destination machine"
            );
            skills.retain(|skill| selection.contains(&skill.name));
        }
        let skill_prompt = pot_agent::format_skills_for_prompt(&skills)?;
        return Ok(InstructionCatalog {
            text: format!(
                "Follow applicable directory instructions on the destination machine. Before editing descendants, inspect their AGENTS.md files. Destination workspace: {}\n{}{}",
                context.context["workspace"].as_str().unwrap_or(""),
                context.context["instructions"].as_str().unwrap_or(""),
                skill_prompt
            ),
            diagnostics: ["diagnostics", "skill_diagnostics"]
                .iter()
                .flat_map(|key| context.context[*key].as_array().into_iter().flatten())
                .filter_map(Value::as_str)
                .map(str::to_owned)
                .collect(),
        });
    }
    let (source, mount, _, cwd) = crate::environment::workspace_view(s)?;
    let filesystem = WorkspaceFileSystem { source, mount };
    let (global, warning) = if s.is_worker {
        (String::new(), None)
    } else {
        s.agent_service
            .resources
            .global_instructions
            .lock()
            .unwrap()
            .refresh(&s.agent_service.resources.home.join("AGENTS.md"))
    };
    let mut catalog = project(&filesystem, &cwd, &filesystem.mount)?;
    if !s.is_worker {
        let rules = crate::assistant_instructions::read(s)?;
        let text = rules["text"]
            .as_str()
            .ok_or_else(|| anyhow!("Assistant instructions missing"))?;
        if !text.trim().is_empty() {
            catalog.text.insert_str(
                0,
                &format!(
                    "\n\nThis assistant's AGENTS.md instructions (revision {}):\n{text}",
                    rules["revision"].as_str().unwrap()
                ),
            );
        }
        catalog.text.insert_str(0, "\n\nYour AGENTS.md belongs to this assistant, independently of execution environment. Use read_agent_instructions to read its current content/revision and update_agent_instructions to propose a full replacement. Updates require owner approval. Include applicable rules in delegated task briefs; workers do not inherit this assistant's AGENTS.md.\n");
    }

    if let Some(warning) = warning {
        catalog.diagnostics.push(warning);
    }
    if !global.is_empty() {
        catalog
            .text
            .insert_str(0, &format!("\n\nGlobal AGENTS.md instructions:\n{global}"));
    }
    if !catalog.text.is_empty() {
        catalog.text.insert_str(0, "\n\nFollow applicable directory instructions; deeper directory guidance takes precedence on conflicts, and the user's explicit instructions take precedence over these files. Before editing below the current directory, inspect additional AGENTS.md files along the target directory path.\n");
    }
    Ok(catalog)
}

/// Destination-owned project rules only: no assistant DB or global Agent home.
pub(crate) fn project_workspace(
    source: PathBuf,
    mount: PathBuf,
    cwd: PathBuf,
) -> Result<InstructionCatalog> {
    let filesystem = WorkspaceFileSystem { source, mount };
    project(&filesystem, &cwd, &filesystem.mount)
}

#[cfg(test)]
mod tests {
    use super::*;
    struct Fixture(PathBuf);
    impl Fixture {
        fn new() -> Self {
            let root =
                std::env::temp_dir().join(format!("pot-instructions-{}", uuid::Uuid::new_v4()));
            fs::create_dir_all(root.join("repo/sub/deep")).unwrap();
            Self(root)
        }
        fn write(&self, path: &str, text: &str) {
            let path = self.0.join(path);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, text).unwrap();
        }
        fn filesystem(&self) -> WorkspaceFileSystem {
            WorkspaceFileSystem {
                source: self.0.join("repo"),
                mount: "/data/workspace".into(),
            }
        }
        fn load(&self) -> InstructionCatalog {
            project(
                &self.filesystem(),
                Path::new("/data/workspace/sub/deep"),
                Path::new("/data/workspace"),
            )
            .unwrap()
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }
    #[test]
    fn repository_hierarchy_ignores_overrides_and_descendants() {
        for file_marker in [false, true] {
            let f = Fixture::new();
            if file_marker {
                f.write("repo/.git", "gitdir: unused");
            } else {
                fs::create_dir(f.0.join("repo/.git")).unwrap();
            }
            f.write("AGENTS.md", "OUTSIDE");
            f.write("repo/AGENTS.md", "ROOT");
            f.write("repo/sub/AGENTS.md", "MIDDLE");
            f.write("repo/sub/AGENTS.override.md", "OVERRIDE");
            f.write("repo/sub/deep/AGENTS.md", "DEEP");
            f.write("repo/sub/deep/child/AGENTS.md", "DESCENDANT");
            let text = f.load().text;
            assert!(text.find("ROOT").unwrap() < text.find("MIDDLE").unwrap());
            assert!(text.find("MIDDLE").unwrap() < text.find("DEEP").unwrap());
            for excluded in ["OUTSIDE", "OVERRIDE", "DESCENDANT", f.0.to_str().unwrap()] {
                assert!(!text.contains(excluded));
            }
            assert!(text.contains("instructions for /data/workspace/sub"));
        }
    }
    #[test]
    fn no_repository_reads_only_cwd_and_boundary_blocks_outer_repositories() {
        let f = Fixture::new();
        f.write(".git", "outer");
        f.write("repo/AGENTS.md", "PARENT");
        f.write("repo/sub/deep/AGENTS.md", "CWD_ONLY");
        let text = f.load().text;
        assert!(text.contains("CWD_ONLY") && !text.contains("PARENT"));
    }
    #[test]
    fn nested_repository_stops_outer_inheritance() {
        let f = Fixture::new();
        f.write("repo/.git", "outer");
        f.write("repo/AGENTS.md", "OUTER");
        f.write("repo/sub/.git", "inner");
        f.write("repo/sub/AGENTS.md", "INNER");
        let text = f.load().text;
        assert!(text.contains("INNER") && !text.contains("OUTER"));
    }
    #[test]
    fn empty_override_does_not_mask_agents_and_files_refresh() {
        let f = Fixture::new();
        f.write("repo/sub/deep/AGENTS.override.md", "");
        f.write("repo/sub/deep/AGENTS.md", "OLD");
        assert!(f.load().text.contains("OLD"));
        f.write("repo/sub/deep/AGENTS.md", "NEW");
        assert!(f.load().text.contains("NEW") && !f.load().text.contains("OLD"));
        fs::remove_file(f.0.join("repo/sub/deep/AGENTS.md")).unwrap();
        assert!(f.load().text.is_empty());
        fs::create_dir(f.0.join("repo/sub/deep/AGENTS.md")).unwrap();
        assert!(f.load().text.is_empty());
    }
    #[test]
    fn unreadable_project_instructions_fail_instead_of_silently_disappearing() {
        struct Unreadable;
        impl InstructionFileSystem for Unreadable {
            fn exists(&self, _path: &Path) -> io::Result<bool> {
                Ok(false)
            }
            fn read(&self, _path: &Path) -> io::Result<Option<String>> {
                Err(io::Error::new(
                    io::ErrorKind::PermissionDenied,
                    "unreadable project document",
                ))
            }
        }
        let error = project(
            &Unreadable,
            Path::new("/data/workspace"),
            Path::new("/data/workspace"),
        )
        .err()
        .unwrap();
        assert!(error.to_string().contains("unreadable project document"));
    }

    #[test]
    fn no_size_limit_and_invalid_utf8_is_lossy() {
        let f = Fixture::new();
        let long = format!("{}TAIL", "x".repeat(70000));
        f.write("repo/sub/deep/AGENTS.md", &long);
        assert!(f.load().text.ends_with("TAIL"));
        fs::write(f.0.join("repo/sub/deep/AGENTS.md"), [b'a', 0xff, b'z']).unwrap();
        assert!(f.load().text.ends_with("a\u{fffd}z"));
    }
    #[test]
    fn symlinks_resolve_in_guest_space_and_cannot_read_host_files() {
        let f = Fixture::new();
        f.write("repo/linked.md", "CONTAINED");
        let doc = f.0.join("repo/sub/deep/AGENTS.md");
        for target in [
            PathBuf::from("../../linked.md"),
            PathBuf::from("/data/workspace/linked.md"),
        ] {
            std::os::unix::fs::symlink(target, &doc).unwrap();
            assert!(f.load().text.contains("CONTAINED"));
            fs::remove_file(&doc).unwrap();
        }
        f.write("outside.md", "HOST_SECRET");
        std::os::unix::fs::symlink(f.0.join("outside.md"), &doc).unwrap();
        let catalog = f.load();
        assert!(!catalog.text.contains("HOST_SECRET"));
        assert_eq!(catalog.diagnostics.len(), 1);
        fs::remove_file(&doc).unwrap();
        std::os::unix::fs::symlink("../../../outside.md", &doc).unwrap();
        assert_eq!(f.load().diagnostics.len(), 1);
    }
    #[test]
    fn global_errors_retain_last_good_deduplicate_warn_and_recovery_resets() {
        let f = Fixture::new();
        let path = f.0.join("AGENTS.md");
        f.write("AGENTS.override.md", "IGNORED");
        let long = format!("{}TAIL", "g".repeat(70000));
        f.write("AGENTS.md", &long);
        let mut state = GlobalInstructions::default();
        assert_eq!(state.refresh(&path), (long.clone(), None));
        fs::remove_file(&path).unwrap();
        fs::create_dir(&path).unwrap(); // Deterministic read error, even when tests run as root.
        let (text, warning) = state.refresh(&path);
        assert_eq!(text, long);
        assert!(warning.is_some());
        assert!(state.refresh(&path).1.is_none());
        fs::remove_dir(&path).unwrap();
        f.write("AGENTS.md", "RECOVERED");
        assert_eq!(state.refresh(&path), ("RECOVERED".into(), None));
        fs::remove_file(&path).unwrap();
        fs::create_dir(&path).unwrap();
        assert!(state.refresh(&path).1.is_some());
        fs::remove_dir(&path).unwrap();
        assert_eq!(state.refresh(&path), (String::new(), None));
        f.write("AGENTS.md", " \n");
        assert_eq!(state.refresh(&path), (String::new(), None));
    }
    #[tokio::test]
    async fn default_changes_refresh_main_but_tasks_keep_instruction_environment() {
        let root = tasks::tests::fixture();
        for (target, text) in [("default", "FIRST"), ("second", "SECOND")] {
            if target == "second" {
                sandbox::management(&root, &json!({"action":"create","sandbox_id":target}))
                    .await
                    .unwrap();
            }
            let base = root.environment_service.sandboxes.workspace(target);
            fs::create_dir_all(&base).unwrap();
            fs::write(base.join(".git"), "gitdir").unwrap();
            fs::write(base.join("AGENTS.md"), text).unwrap();
        }
        fs::create_dir_all(&root.agent_service.resources.home).unwrap();
        fs::write(
            root.agent_service.resources.home.join("AGENTS.md"),
            "GLOBAL",
        )
        .unwrap();
        fs::write(
            root.agent_service.resources.home.join("AGENTS.override.md"),
            "WRONG_OVERRIDE",
        )
        .unwrap();
        fs::write(root.workspace.join("AGENTS.md"), "WRONG_HOST_CWD").unwrap();
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: "instructions".into(),
                text: "fixture".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let worker = tasks::owner(&root, &task.id).unwrap();
        fs::write(worker.workspace.join("AGENTS.md"), "TASK").unwrap();
        sandbox::management(&root, &json!({"action":"select","sandbox_id":"second"}))
            .await
            .unwrap();
        let main = load(&root).unwrap().text;
        assert!(main.contains("SECOND") && main.contains("GLOBAL"));
        assert!(
            !main.contains("FIRST")
                && !main.contains("WRONG_HOST_CWD")
                && !main.contains("WRONG_OVERRIDE")
        );
        let child = load(&worker).unwrap().text;
        assert!(child.contains("FIRST") && child.contains("TASK"));
        assert!(!child.contains("GLOBAL"));
        assert!(!child.contains("SECOND"));
        assert!(child.contains(&format!("/data/workspace/tasks/{}", task.id)));
        assert!(!root.data.parent().unwrap().join("helper.sock").exists());
        fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
    #[tokio::test]
    async fn macos_worker_instructions_skills_and_artifacts_use_provider_paths() {
        let mut root = tasks::tests::fixture();
        root.environment_service.sandboxes = Arc::new(
            sandbox::Manager::with_providers(
                &root.data,
                vec![
                    Arc::new(
                        pot_sandbox_macos_apple_container::MacOSAppleContainerProvider::new()
                            .unwrap(),
                    ),
                    Arc::new(pot_sandbox_macos_apple_vm::MacOSAppleVMProvider::new(
                        root.data.join("absent-helper"),
                    )),
                ],
            )
            .unwrap(),
        );
        sandbox::management(&root,&json!({"action":"create","sandbox_id":"mac-dev","provider":pot_sandbox_macos_apple_vm::PROVIDER_ID})).await.unwrap();
        sandbox::management(&root, &json!({"action":"select","sandbox_id":"mac-dev"}))
            .await
            .unwrap();
        let base = root.environment_service.sandboxes.workspace("mac-dev");
        fs::create_dir_all(&base).unwrap();
        fs::write(base.join("AGENTS.md"), "MAC_PROJECT").unwrap();
        fs::write(base.join(".git"), "gitdir").unwrap();
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: "mac-paths".into(),
                text: "Mac build".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let worker = tasks::owner(&root, &task.id).unwrap();
        assert_eq!(worker.workspace, base.join("tasks").join(&task.id));
        fs::write(worker.workspace.join("AGENTS.md"), "MAC_TASK").unwrap();
        let text = load(&worker).unwrap().text;
        assert!(text.contains("MAC_PROJECT") && text.contains("MAC_TASK"));
        assert!(text.contains(&format!(
            "/Volumes/PotWorkspace/workspace/tasks/{}",
            task.id
        )));
        let resources = crate::skills::resources(&worker).unwrap();
        assert!(
            resources
                .roots
                .iter()
                .all(|r| r.location.starts_with("/Volumes/PotWorkspace/workspace"))
        );
        assert!(!root.data.parent().unwrap().join("helper.sock").exists());
        fs::remove_dir_all(root.data.parent().unwrap()).unwrap();
    }
}
