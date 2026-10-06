use anyhow::{Result, anyhow, bail, ensure};
use pot_core::ExecutionContext;
use serde_json::{Value, json};
use std::path::{Path, PathBuf};

pub use pot_core::environment_tools::{schemas, validate};
pub fn patch(s: &ExecutionContext, args: &Value) -> Result<Value> {
    let patch = args["patch"]
        .as_str()
        .ok_or_else(|| anyhow!("patch must be a string"))?;
    ensure!(patch.len() <= 4 * 1024 * 1024, "Patch too large");
    Ok(json!({"output":apply_patch(&s.workspace, patch)?}))
}
fn path(root: &Path, name: &str) -> Result<PathBuf> {
    ensure!(!name.is_empty(), "Empty patch path");
    let root = root.canonicalize()?;
    let target: PathBuf = root
        .join(name)
        .components()
        .filter(|c| *c != std::path::Component::CurDir)
        .collect();
    ensure!(
        target.starts_with(&root)
            && !target
                .components()
                .any(|c| c == std::path::Component::ParentDir),
        "Patch path must stay in workspace"
    );
    let mut existing = target.as_path();
    while !existing.exists() {
        existing = existing.parent().ok_or_else(|| anyhow!("Invalid path"))?;
    }
    ensure!(
        existing.canonicalize()?.starts_with(root),
        "Patch symlink escapes workspace"
    );
    ensure!(
        !target
            .symlink_metadata()
            .is_ok_and(|m| m.file_type().is_symlink()),
        "Cannot patch a symlink"
    );
    Ok(target)
}
struct Edit {
    path: PathBuf,
    destination: Option<PathBuf>,
    content: Option<String>,
}
pub fn apply_patch(root: &Path, patch: &str) -> Result<String> {
    let lines: Vec<&str> = patch.lines().collect();
    ensure!(
        lines.first() == Some(&"*** Begin Patch") && lines.last() == Some(&"*** End Patch"),
        "Patch requires Begin/End markers"
    );
    let mut edits = vec![];
    let mut i = 1;
    while i + 1 < lines.len() {
        let header = lines[i];
        i += 1;
        if let Some(name) = header.strip_prefix("*** Add File: ") {
            let p = path(root, name)?;
            ensure!(!p.exists(), "Add target already exists");
            let mut text = String::new();
            while i < lines.len() && !lines[i].starts_with("*** ") {
                let line = lines[i]
                    .strip_prefix('+')
                    .ok_or_else(|| anyhow!("Added lines require +"))?;
                text.push_str(line);
                text.push('\n');
                i += 1;
            }
            edits.push(Edit {
                path: p,
                destination: None,
                content: Some(text),
            });
        } else if let Some(name) = header.strip_prefix("*** Delete File: ") {
            let p = path(root, name)?;
            ensure!(p.is_file(), "Delete target missing");
            edits.push(Edit {
                path: p,
                destination: None,
                content: None,
            });
        } else if let Some(name) = header.strip_prefix("*** Update File: ") {
            let p = path(root, name)?;
            ensure!(
                p.metadata()?.len() <= 4 * 1024 * 1024,
                "File too large for patch"
            );
            let original = std::fs::read_to_string(&p)?;
            let mut content: Vec<String> = original.lines().map(str::to_owned).collect();
            let mut cursor = 0;
            let mut hunks = 0;
            let destination = if i < lines.len() {
                if let Some(name) = lines[i].strip_prefix("*** Move to: ") {
                    i += 1;
                    let p = path(root, name)?;
                    ensure!(!p.exists(), "Move target exists");
                    Some(p)
                } else {
                    None
                }
            } else {
                None
            };
            while i < lines.len() && !lines[i].starts_with("*** ") {
                ensure!(
                    lines[i] == "@@" || lines[i].starts_with("@@ "),
                    "Expected @@ hunk"
                );
                if let Some(anchor) = lines[i].strip_prefix("@@ ") {
                    cursor = content
                        .iter()
                        .enumerate()
                        .skip(cursor)
                        .find(|(_, l)| l.as_str() == anchor)
                        .map(|(n, _)| n + 1)
                        .ok_or_else(|| anyhow!("Hunk anchor missing"))?;
                }
                i += 1;
                let mut old = vec![];
                let mut new = vec![];
                while i < lines.len()
                    && !lines[i].starts_with("@@")
                    && !lines[i].starts_with("*** ")
                {
                    let line = lines[i];
                    i += 1;
                    let (kind, text) = if line.is_empty() {
                        (' ', "")
                    } else {
                        ensure!(
                            matches!(line.as_bytes()[0], b' ' | b'+' | b'-'),
                            "Invalid patch line"
                        );
                        (line.as_bytes()[0] as char, &line[1..])
                    };
                    match kind {
                        ' ' => {
                            old.push(text.to_owned());
                            new.push(text.to_owned());
                        }
                        '-' => old.push(text.to_owned()),
                        '+' => new.push(text.to_owned()),
                        _ => bail!("Invalid patch line"),
                    }
                }
                let eof = lines.get(i) == Some(&"*** End of File");
                if eof {
                    i += 1;
                }
                ensure!(!old.is_empty() || !new.is_empty(), "Empty hunk");
                let at = if old.is_empty() {
                    if eof { content.len() } else { cursor }
                } else {
                    (cursor..=content.len().saturating_sub(old.len()))
                        .find(|n| {
                            content.get(*n..*n + old.len()) == Some(old.as_slice())
                                && (!eof || *n + old.len() == content.len())
                        })
                        .ok_or_else(|| anyhow!("Patch context does not match {}", p.display()))?
                };
                content.splice(at..at + old.len(), new.clone());
                cursor = at + new.len();
                hunks += 1;
            }
            ensure!(hunks > 0, "Update requires a hunk");
            let text = if content.is_empty() {
                String::new()
            } else {
                format!("{}\n", content.join("\n"))
            };
            edits.push(Edit {
                path: p,
                destination,
                content: Some(text),
            });
        } else {
            bail!("Invalid patch header: {header}");
        }
    }
    ensure!(!edits.is_empty(), "Empty patch");
    let mut seen = std::collections::HashSet::new();
    for e in &edits {
        ensure!(seen.insert(e.path.clone()), "Duplicate patch path");
        if let Some(p) = &e.destination {
            ensure!(seen.insert(p.clone()), "Duplicate move path");
        }
    }
    let mut report = vec![];
    // Every hunk and target is validated before the first filesystem mutation.
    for e in edits {
        if let Some(text) = e.content {
            let dst = e.destination.as_ref().unwrap_or(&e.path);
            std::fs::create_dir_all(dst.parent().unwrap())?;
            use std::io::Write;
            use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
            let temp = dst.with_file_name(format!(".pot-patch-{}", uuid::Uuid::new_v4()));
            let mode = e
                .path
                .metadata()
                .map(|m| m.permissions().mode())
                .unwrap_or(0o644);
            let mut output = std::fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .mode(mode)
                .open(&temp)?;
            output.write_all(text.as_bytes())?;
            output.sync_all()?;
            std::fs::rename(temp, dst)?;
            std::fs::File::open(dst.parent().unwrap())?.sync_all()?;
            if e.destination.is_some() {
                std::fs::remove_file(&e.path)?;
            }
            report.push(format!("Updated {}", dst.display()));
        } else {
            std::fs::remove_file(&e.path)?;
            report.push(format!("Deleted {}", e.path.display()));
        }
    }
    Ok(report.join("\n"))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn workspace() -> PathBuf {
        let p = std::env::temp_dir().join(uuid::Uuid::new_v4().to_string());
        std::fs::create_dir_all(&p).unwrap();
        p
    }
    #[test]
    fn codex_patch_add_update_move_delete_and_preflight() {
        let root = workspace();
        apply_patch(
            &root,
            "*** Begin Patch\n*** Add File: a.txt\n+one\n+two\n*** End Patch",
        )
        .unwrap();
        apply_patch(&root,"*** Begin Patch\n*** Update File: a.txt\n*** Move to: dir/b.txt\n@@\n one\n-two\n+三\n*** End of File\n*** End Patch").unwrap();
        assert!(!root.join("a.txt").exists());
        assert_eq!(
            std::fs::read_to_string(root.join("dir/b.txt")).unwrap(),
            "one\n三\n"
        );
        assert!(apply_patch(&root,"*** Begin Patch\n*** Add File: should-not-exist.txt\n+new\n*** Update File: dir/b.txt\n@@\n-missing\n+replacement\n*** End Patch").is_err());
        assert!(!root.join("should-not-exist.txt").exists());
        apply_patch(
            &root,
            "*** Begin Patch\n*** Delete File: dir/b.txt\n*** End Patch",
        )
        .unwrap();
        assert!(!root.join("dir/b.txt").exists());
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn patch_rejects_escape_symlinks_and_invalid_unicode_prefix() {
        let root = workspace();
        let outside = workspace();
        std::os::unix::fs::symlink(&outside, root.join("link")).unwrap();
        for target in ["../escape", "link/escape"] {
            assert!(
                apply_patch(
                    &root,
                    &format!("*** Begin Patch\n*** Add File: {target}\n+bad\n*** End Patch")
                )
                .is_err()
            );
        }
        std::fs::write(root.join("a"), "text\n").unwrap();
        assert!(
            apply_patch(
                &root,
                "*** Begin Patch\n*** Update File: a\n@@\n中文\n*** End Patch"
            )
            .is_err()
        );
        std::fs::remove_dir_all(root).unwrap();
        std::fs::remove_dir_all(outside).unwrap();
    }
}
