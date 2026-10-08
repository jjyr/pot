//! Explicit Host tools, executed only after the Agent permission gate.
use anyhow::{Result, ensure};
use pot_core::ExecutionContext;
use serde_json::{Value, json};
use std::{
    ffi::CString,
    fs::File,
    io::{Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd},
        unix::ffi::OsStrExt,
    },
    path::{Component, Path},
};
use tokio::io::AsyncReadExt;

pub fn schemas() -> Vec<Value> {
    [
        ("host_set_sandbox_mappings", "Replace all Host directory mappings for a stopped Linux Sandbox after owner approval. Show complete mappings; all tasks in that Sandbox can access them. Existing mappings not listed are removed. Changes persist until replaced.", json!({"sandbox_id":{"type":"string"},"mappings":{"type":"array","items":{"type":"object","properties":{"source":{"type":"string"},"destination":{"type":"string"},"read_only":{"type":"boolean"}},"required":["source","destination","read_only"],"additionalProperties":false}}}), vec!["sandbox_id","mappings"]),
        ("host_read_file", "Read a UTF-8 Host file after owner authorization.", json!({"path":{"type":"string"}}), vec!["path"]),
        ("host_write_file", "Create or replace a UTF-8 Host file after owner authorization. The owner sees the complete content. Host paths are absolute.", json!({"path":{"type":"string"},"content":{"type":"string"}}), vec!["path","content"]),
        ("host_list_directory", "List one Host directory after owner authorization. Persistent approval covers reads beneath this directory.", json!({"path":{"type":"string"}}), vec!["path"]),
        ("host_exec_command", "Run an explicitly approved Host shell command. Approval permits all effects of the command under the Server OS account; path grants do not authorize shell execution. No PTY or retained sessions.", json!({"cmd":{"type":"string"},"workdir":{"type":"string"},"timeout_ms":{"type":"integer","minimum":1,"maximum":600000}}), vec!["cmd","workdir"]),
    ].into_iter().map(|(name,description,properties,required)|json!({"name":name,"description":description,"inputSchema":{"type":"object","properties":properties,"required":required,"additionalProperties":false}})).collect()
}
pub fn validate(name: &str, args: &Value) -> Result<()> {
    if name == "host_set_sandbox_mappings" {
        let object = args
            .as_object()
            .ok_or_else(|| anyhow::anyhow!("Arguments must be an object"))?;
        ensure!(
            object
                .keys()
                .all(|k| ["sandbox_id", "mappings"].contains(&k.as_str()))
                && args["sandbox_id"].as_str().is_some_and(|s| !s.is_empty()),
            "Invalid mapping target"
        );
        let mappings = args["mappings"]
            .as_array()
            .ok_or_else(|| anyhow::anyhow!("Mappings must be an array"))?;
        ensure!(mappings.len() <= 16, "Mapping limit reached");
        for mapping in mappings {
            ensure!(
                mapping.as_object().is_some_and(|o| o.len() == 3)
                    && mapping["source"].is_string()
                    && mapping["destination"].is_string()
                    && mapping["read_only"].is_boolean(),
                "Invalid mapping"
            );
        }
        return Ok(());
    }
    let fields: &[&str] = match name {
        "host_read_file" | "host_list_directory" => &["path"],
        "host_write_file" => &["path", "content"],
        "host_exec_command" => &["cmd", "workdir", "timeout_ms"],
        _ => anyhow::bail!("Unknown Host tool"),
    };
    let args = args
        .as_object()
        .ok_or_else(|| anyhow::anyhow!("Arguments must be an object"))?;
    ensure!(
        args.keys().all(|k| fields.contains(&k.as_str())),
        "Unknown Host tool argument"
    );
    for field in fields.iter().filter(|f| **f != "timeout_ms") {
        ensure!(
            args.get(*field).is_some_and(Value::is_string),
            "Missing string argument: {field}"
        );
    }
    if let Some(timeout) = args.get("timeout_ms") {
        ensure!(
            timeout.as_u64().is_some_and(|n| (1..=600000).contains(&n)),
            "Invalid timeout"
        );
    }
    ensure!(
        serde_json::to_vec(args)?.len() <= 262144,
        "Host arguments exceed 256 KiB"
    );
    Ok(())
}
pub fn normalize(name: &str, args: &mut Value) -> Result<()> {
    validate(name, args)?;
    if name == "host_set_sandbox_mappings" {
        ensure!(
            args["sandbox_id"].as_str().is_some_and(|s| !s.is_empty()),
            "Explicit Sandbox required"
        );
        for mapping in args["mappings"].as_array_mut().unwrap() {
            mapping["source"] = json!(crate::permissions::resolve_path(
                mapping["source"].as_str().unwrap()
            )?);
            ensure!(
                Path::new(mapping["source"].as_str().unwrap()).is_dir(),
                "Mapping source must be a directory"
            );
            let destination = Path::new(mapping["destination"].as_str().unwrap());
            ensure!(
                destination.starts_with("/mnt/host")
                    && destination != Path::new("/mnt/host")
                    && destination
                        .components()
                        .all(|c| !matches!(c, Component::ParentDir | Component::CurDir)),
                "Mappings must target a child of /mnt/host without parent components"
            );
        }
        return Ok(());
    }
    let key = if name == "host_exec_command" {
        "workdir"
    } else {
        "path"
    };
    args[key] = json!(crate::permissions::resolve_path(
        args[key].as_str().unwrap()
    )?);
    if name == "host_exec_command" && args.get("timeout_ms").is_none() {
        args["timeout_ms"] = json!(60000);
    }
    Ok(())
}

/// Walk from / using directory descriptors, refusing symlinks at every component.
/// Canonical names shown at approval therefore cannot be redirected before I/O.
fn open(path: &Path, flags: i32, mode: u32) -> Result<File> {
    ensure!(path.is_absolute(), "Absolute Host path required");
    let mut directory = File::open("/")?;
    let parts: Vec<_> = path
        .components()
        .filter_map(|c| match c {
            Component::Normal(v) => Some(v),
            _ => None,
        })
        .collect();
    ensure!(
        !parts.is_empty() || flags == (libc::O_RDONLY | libc::O_DIRECTORY),
        "Root file access invalid"
    );
    for (index, part) in parts.iter().enumerate() {
        let name = CString::new(part.as_bytes())?;
        let final_part = index + 1 == parts.len();
        let access = if final_part {
            flags
        } else {
            libc::O_RDONLY | libc::O_DIRECTORY
        };
        let fd = unsafe {
            libc::openat(
                directory.as_raw_fd(),
                name.as_ptr(),
                access | libc::O_NOFOLLOW | libc::O_CLOEXEC | libc::O_NONBLOCK,
                mode,
            )
        };
        ensure!(
            fd >= 0,
            "Host path open failed: {}",
            std::io::Error::last_os_error()
        );
        directory = unsafe { File::from_raw_fd(fd) };
    }
    Ok(directory)
}
fn files(name: &str, args: &Value) -> Result<Value> {
    let path = Path::new(args["path"].as_str().unwrap());
    match name {
        "host_read_file" => {
            let file = open(path, libc::O_RDONLY, 0)?;
            ensure!(
                file.metadata()?.is_file(),
                "Host path must be a regular file"
            );
            let mut data = Vec::new();
            file.take(262145).read_to_end(&mut data)?;
            ensure!(data.len() <= 262144, "Host file exceeds 256 KiB");
            Ok(json!({"path":path,"content":String::from_utf8(data)?}))
        }
        "host_write_file" => {
            // Open without truncation first: special files must not be modified.
            let mut file = open(path, libc::O_WRONLY | libc::O_CREAT, 0o600)?;
            ensure!(
                file.metadata()?.is_file()
                    && std::os::unix::fs::MetadataExt::nlink(&file.metadata()?) == 1,
                "Host writes require a regular file without hard links"
            );
            file.set_len(0)?;
            file.write_all(args["content"].as_str().unwrap().as_bytes())?;
            file.sync_all()?;
            Ok(json!({"path":path,"written":true}))
        }
        "host_list_directory" => {
            let directory = open(path, libc::O_RDONLY | libc::O_DIRECTORY, 0)?;
            let fd = unsafe { libc::dup(directory.as_raw_fd()) };
            ensure!(fd >= 0, "Directory descriptor unavailable");
            let handle = unsafe { libc::fdopendir(fd) };
            if handle.is_null() {
                unsafe {
                    libc::close(fd);
                }
                anyhow::bail!("Directory listing failed");
            }
            let mut entries = Vec::new();
            loop {
                let entry = unsafe { libc::readdir(handle) };
                if entry.is_null() {
                    break;
                }
                let name = unsafe { std::ffi::CStr::from_ptr((*entry).d_name.as_ptr()) }
                    .to_string_lossy()
                    .into_owned();
                if name != "." && name != ".." {
                    entries.push(name);
                }
                if entries.len() > 2000 {
                    break;
                }
            }
            unsafe {
                libc::closedir(handle);
            }
            let truncated = entries.len() > 2000;
            entries.truncate(2000);
            entries.sort();
            Ok(json!({"path":path,"entries":entries,"truncated":truncated}))
        }
        _ => anyhow::bail!("Unknown Host file tool"),
    }
}
struct ProcessGroup(i32);
impl Drop for ProcessGroup {
    fn drop(&mut self) {
        unsafe {
            libc::kill(-self.0, libc::SIGKILL);
        }
    }
}
async fn capture(mut reader: impl tokio::io::AsyncRead + Unpin) -> Result<(String, bool)> {
    let mut saved = Vec::new();
    let mut truncated = false;
    let mut buffer = [0; 4096];
    loop {
        let count = reader.read(&mut buffer).await?;
        if count == 0 {
            break;
        }
        let keep = count.min(65536usize.saturating_sub(saved.len()));
        truncated |= keep < count;
        saved.extend_from_slice(&buffer[..keep]);
    }
    Ok((String::from_utf8_lossy(&saved).into_owned(), truncated))
}
pub async fn execute(context: &ExecutionContext, name: &str, args: &Value) -> Result<Value> {
    ensure!(!context.feedback.cancelled(), "Host operation cancelled");
    if name == "host_set_sandbox_mappings" {
        ensure!(
            context.role == pot_core::ExecutionRole::Main,
            "Only Main can modify Sandbox mappings"
        );
        return context
            .feedback
            .execute_authorized_host(name, args.clone())
            .await;
    }
    if name != "host_exec_command" {
        return files(name, args);
    }
    let workdir = args["workdir"].as_str().unwrap();
    ensure!(
        crate::permissions::resolve_path(workdir)? == Path::new(workdir),
        "Host working directory changed after review"
    );
    let mut command = tokio::process::Command::new("/bin/sh");
    command
        .arg("-c")
        .arg(args["cmd"].as_str().unwrap())
        .current_dir(workdir)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true)
        .process_group(0);
    let mut child = command.spawn()?;
    let group = ProcessGroup(child.id().unwrap() as i32);
    let mut out = tokio::spawn(capture(child.stdout.take().unwrap()));
    let mut err = tokio::spawn(capture(child.stderr.take().unwrap()));
    let status: Result<_> = tokio::select! {
        value = tokio::time::timeout(std::time::Duration::from_millis(args["timeout_ms"].as_u64().unwrap()), child.wait()) => match value { Ok(v) => v.map_err(Into::into), Err(_) => Err(anyhow::anyhow!("Host command timed out")) },
        _ = context.feedback.cancellation() => Err(anyhow::anyhow!("Host command cancelled")),
    };
    drop(group);
    let status = match status {
        Ok(status) => status,
        Err(error) => {
            let _ = child.wait().await;
            out.abort();
            err.abort();
            return Err(error);
        }
    };
    let output = tokio::time::timeout(std::time::Duration::from_secs(2), async {
        Ok::<_, anyhow::Error>(((&mut out).await??, (&mut err).await??))
    })
    .await;
    match output {
        Ok(Ok((stdout, stderr))) => Ok(
            json!({"exit_code":status.code(),"stdout":stdout.0,"stdout_truncated":stdout.1,"stderr":stderr.0,"stderr_truncated":stderr.1,"output_limit_bytes":65536}),
        ),
        value => {
            out.abort();
            err.abort();
            anyhow::bail!("Host output collection failed: {value:?}");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn resolved_paths_cannot_be_redirected_and_writes_refuse_hardlinks() {
        let temp = std::env::temp_dir().join(format!("pot-host-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&temp).unwrap();
        let root = temp.canonicalize().unwrap();
        let inner = root.join("inner");
        std::fs::create_dir(&inner).unwrap();
        let file = inner.join("file");
        std::fs::write(&file, "keep").unwrap();
        std::fs::hard_link(&file, inner.join("hard")).unwrap();
        assert!(files("host_write_file", &json!({"path":file,"content":"changed"})).is_err());
        assert_eq!(std::fs::read_to_string(&file).unwrap(), "keep");
        std::fs::rename(&inner, root.join("old")).unwrap();
        std::os::unix::fs::symlink(root.join("old"), &inner).unwrap();
        assert!(files("host_read_file", &json!({"path":file})).is_err());
        std::fs::remove_dir_all(temp).unwrap();
    }
}
