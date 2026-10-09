use pot_protocol::{Message, MessageStatus, Role};
use std::path::{Component, Path, PathBuf};

// Only surface a successful write/edit, and resolve strictly inside the workspace.
// Reveal in Finder; do not execute a model-generated file on the host.
pub fn from_message(message: &Message, root: &Path) -> Option<PathBuf> {
    if message.role != Role::Tool || message.status != MessageStatus::Complete {
        return None;
    }
    let mut lines = message.text.lines();
    if !matches!(lines.next()?, "write" | "edit") {
        return None;
    }
    let args: serde_json::Value = serde_json::from_str(lines.next()?).ok()?;
    resolve(root, args["path"].as_str()?)
}
fn resolve(root: &Path, path: &str) -> Option<PathBuf> {
    let guest_prefix = if root
        .parent()
        .and_then(Path::file_name)
        .is_some_and(|n| n == "tasks")
    {
        format!("/data/workspace/tasks/{}/", root.file_name()?.to_str()?)
    } else {
        "/data/workspace/".into()
    };
    let path = Path::new(path.strip_prefix(&guest_prefix).unwrap_or(path));
    if path
        .components()
        .any(|c| !matches!(c, Component::Normal(_) | Component::CurDir))
    {
        return None;
    }
    let root = root.canonicalize().ok()?;
    let file = root.join(path).canonicalize().ok()?;
    (file.starts_with(&root) && file.is_file()).then_some(file)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn traversal_and_symlink_do_not_escape() {
        let root = std::env::temp_dir().join(format!("pot-artifact-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&root).unwrap();
        std::fs::write(root.join("report.txt"), "report").unwrap();
        assert!(resolve(&root, "report.txt").is_some());
        assert!(resolve(&root, "/data/workspace/report.txt").is_some());
        assert!(resolve(&root, "../report.txt").is_none());
        assert!(resolve(&root, "/etc/passwd").is_none());
        std::os::unix::fs::symlink("/etc/passwd", root.join("link")).unwrap();
        assert!(resolve(&root, "link").is_none());
        let task = root.join("tasks/task-a");
        std::fs::create_dir_all(&task).unwrap();
        std::fs::write(task.join("result.txt"), "done").unwrap();
        assert!(resolve(&task, "result.txt").is_some());
        assert!(resolve(&task, "/data/workspace/tasks/task-a/result.txt").is_some());
        assert!(resolve(&task, "/data/workspace/tasks/task-b/result.txt").is_none());
        std::fs::remove_dir_all(root).unwrap();
    }
}
