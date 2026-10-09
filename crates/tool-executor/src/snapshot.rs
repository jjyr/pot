//! Bounded, in-memory login initialization snapshots. Commands never update the baseline.
use anyhow::{Result, anyhow, ensure};
use std::{
    collections::{HashMap, VecDeque, hash_map::DefaultHasher},
    hash::{Hash, Hasher},
    path::{Path, PathBuf},
    time::Duration,
};
use tokio::{io::AsyncReadExt, process::Command};
const LIMIT: usize = 512 * 1024;
struct CaptureGroup(u32);
impl Drop for CaptureGroup {
    fn drop(&mut self) {
        unsafe {
            libc::kill(-(self.0 as i32), libc::SIGKILL);
        }
    }
}
const MARKER: &[u8] = b"\0POT_SNAPSHOT_ENV\0";
#[derive(Clone)]
pub(crate) struct Snapshot {
    state: String,
    env: HashMap<String, String>,
}
#[derive(Default)]
pub(crate) struct Snapshots {
    entries: VecDeque<(u64, Option<Snapshot>)>,
}

fn key(shell: &str, cwd: &Path, home: &Path) -> u64 {
    let mut hash = DefaultHasher::new();
    shell.hash(&mut hash);
    cwd.hash(&mut hash);
    home.hash(&mut hash);
    // Include startup contents, not only timestamps (editors can preserve mtimes).
    for path in [
        PathBuf::from(shell),
        PathBuf::from("/etc/profile"),
        PathBuf::from("/etc/bash.bashrc"),
        PathBuf::from("/etc/zshenv"),
        PathBuf::from("/etc/zprofile"),
        PathBuf::from("/etc/zsh/zshenv"),
        PathBuf::from("/etc/zsh/zprofile"),
    ]
    .into_iter()
    .chain(
        [
            ".profile",
            ".bash_profile",
            ".bash_login",
            ".bashrc",
            ".zshenv",
            ".zprofile",
            ".zshrc",
            ".zlogin",
        ]
        .map(|p| home.join(p)),
    ) {
        path.hash(&mut hash);
        if let Ok(metadata) = std::fs::metadata(&path) {
            metadata.len().hash(&mut hash);
            metadata.modified().ok().hash(&mut hash);
        }
        // Never read the shell executable as a startup file.
        if path != Path::new(shell) {
            use std::io::Read;
            if let Ok(file) = std::fs::File::open(&path) {
                let mut bytes = Vec::new();
                if file
                    .take((LIMIT + 1) as u64)
                    .read_to_end(&mut bytes)
                    .is_ok()
                {
                    bytes.hash(&mut hash);
                }
            }
        }
    }
    hash.finish()
}
impl Snapshots {
    pub async fn command(
        &mut self,
        shell: &str,
        cwd: &Path,
        home: &Path,
        cmd: &str,
        login: bool,
        enabled: bool,
    ) -> Command {
        let mut command = Command::new(shell);
        command.current_dir(cwd).env("HOME", home);
        if !login
            || !enabled
            || !matches!(
                Path::new(shell).file_name().and_then(|n| n.to_str()),
                Some("bash" | "zsh")
            )
        {
            command.args([if login { "-lc" } else { "-c" }, cmd]);
            return command;
        }
        let key = key(shell, cwd, home);
        let snapshot = if let Some(position) = self.entries.iter().position(|(k, _)| *k == key) {
            let entry = self.entries.remove(position).unwrap();
            let result = entry.1.clone();
            self.entries.push_back(entry);
            result
        } else {
            let result = capture(shell, cwd, home).await.ok();
            if self.entries.len() == 16 {
                self.entries.pop_front();
            }
            // A failed baseline is cached until inputs change/eviction, preventing retry storms.
            self.entries.push_back((key, result.clone()));
            result
        };
        if let Some(snapshot) = snapshot {
            command.env_clear().envs(snapshot.env);
            let flag = if shell.ends_with("zsh") { "-fc" } else { "-pc" };
            let startup = if shell.ends_with("zsh") {
                "setopt RCS"
            } else {
                "set +o privileged"
            };
            // Feed state via environment; shell expands it as data inside quoted eval.
            command
                .env("POT_SHELL_BASELINE", snapshot.state)
                .env("POT_SHELL_COMMAND", cmd)
                .env("POT_SHELL_EXECUTABLE", shell);
            command.args([
                flag,
                &format!(
                    "{startup}\nif ! builtin eval \"$POT_SHELL_BASELINE\"; then exec /usr/bin/env -u POT_SHELL_BASELINE -u POT_SHELL_COMMAND -u POT_SHELL_EXECUTABLE \"$POT_SHELL_EXECUTABLE\" -lc \"$POT_SHELL_COMMAND\"; fi\nbuiltin unset POT_SHELL_BASELINE POT_SHELL_EXECUTABLE\nbuiltin eval \"builtin unset POT_SHELL_COMMAND\n$POT_SHELL_COMMAND\""
                ),
            ]);
        } else {
            command.args(["-lc", cmd]);
        }
        command
    }
}
async fn capture(shell: &str, cwd: &Path, home: &Path) -> Result<Snapshot> {
    let state = if shell.ends_with("zsh") {
        "functions; alias -L; setopt | sed 's/^/setopt /'; printf 'umask '; umask"
    } else {
        "declare -f; alias -p; set +o; shopt -p || true; printf 'umask '; umask"
    };
    let script = format!(
        "printf '\\0POT_SNAPSHOT_STATE\\0'\n{state}\nprintf '\\0POT_SNAPSHOT_ENV\\0'\nenv -0"
    );
    let mut child = Command::new(shell)
        .args(["-lc", &script])
        .env("HOME", home)
        .current_dir(cwd)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::null())
        .process_group(0)
        .kill_on_drop(true)
        .spawn()?;
    let pid = child.id().ok_or_else(|| anyhow!("Snapshot PID missing"))?;
    let _group = CaptureGroup(pid);
    let mut reader = child.stdout.take().unwrap().take((LIMIT + 1) as u64);
    let result = tokio::time::timeout(Duration::from_secs(10), async {
        let mut bytes = Vec::new();
        reader.read_to_end(&mut bytes).await?;
        ensure!(bytes.len() <= LIMIT, "Shell snapshot too large");
        ensure!(
            child.wait().await?.success(),
            "Shell snapshot capture failed"
        );
        let offset = bytes
            .windows(MARKER.len())
            .position(|b| b == MARKER)
            .ok_or_else(|| anyhow!("Invalid shell snapshot"))?;
        let start_marker = b"\0POT_SNAPSHOT_STATE\0";
        let start = bytes[..offset]
            .windows(start_marker.len())
            .position(|b| b == start_marker)
            .ok_or_else(|| anyhow!("Invalid snapshot start"))?
            + start_marker.len();
        let state = String::from_utf8(bytes[start..offset].to_vec())?;
        // Bound each environment value below common execve per-string limits.
        ensure!(state.len() <= 60 * 1024, "Shell state too large");
        let mut env = HashMap::new();
        for record in bytes[offset + MARKER.len()..]
            .split(|b| *b == 0)
            .filter(|b| !b.is_empty())
        {
            let record = std::str::from_utf8(record)?;
            let (name, value) = record
                .split_once('=')
                .ok_or_else(|| anyhow!("Invalid environment record"))?;
            ensure!(value.len() <= 60 * 1024, "Environment value too large");
            if !matches!(
                name,
                "POT_SHELL_BASELINE" | "POT_SHELL_COMMAND" | "POT_SHELL_EXECUTABLE"
            ) {
                env.insert(name.to_owned(), value.to_owned());
            }
        }
        Ok(Snapshot { state, env })
    })
    .await;
    // Capture initialization must not leave background descendants behind.
    unsafe {
        libc::kill(-(pid as i32), libc::SIGKILL);
    }
    if child.try_wait()?.is_none() {
        child.kill().await?;
    }
    result.map_err(|_| anyhow!("Shell snapshot capture timed out"))?
}
