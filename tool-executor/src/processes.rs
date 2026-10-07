//! Agent-session processes. Handles are in memory and cannot survive executor restart.
use anyhow::{Result, anyhow, ensure};
use pot_core::{ExecutionContext, ShellSettings, Update};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    sync::{Notify, watch},
    task::{JoinHandle, JoinSet},
    time::Instant,
};

type Reader = Box<dyn tokio::io::AsyncRead + Unpin + Send>;
type Writer = Box<dyn tokio::io::AsyncWrite + Unpin + Send>;
type Spawned = (
    tokio::process::Child,
    Option<Writer>,
    Option<crate::terminal::Terminal>,
    Reader,
    Option<Reader>,
);

struct Group {
    pid: u32,
    cleaned: bool,
}
impl Group {
    fn kill(&mut self) {
        if !self.cleaned {
            let result = unsafe { libc::kill(-(self.pid as i32), libc::SIGKILL) };
            // A successful signal is not confirmation that the group is gone:
            // a child being forked can survive the first signal on macOS.
            self.cleaned =
                result == -1 && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH);
        }
    }
}
impl Drop for Group {
    fn drop(&mut self) {
        self.kill();
    }
}
#[derive(Default)]
struct Output {
    stdout: Vec<u8>,
    stderr: Vec<u8>,
    truncated: bool,
    done: bool,
    exit: Option<i32>,
    reason: Option<String>,
}
struct Process {
    input: Option<Writer>,
    terminal: Option<crate::terminal::Terminal>,
    pid: u32,
    touch: watch::Sender<Instant>,
    output: Arc<Mutex<Output>>,
    changed: Arc<Notify>,
    stop: watch::Sender<bool>,
    task: JoinHandle<Result<()>>,
}
impl Drop for Process {
    fn drop(&mut self) {
        let _ = self.stop.send(true);
        // Abort still drops the process-group guard and the child.
        self.task.abort();
    }
}
#[derive(Default)]
pub struct Processes {
    sessions: HashMap<String, Process>,
    snapshots: crate::snapshot::Snapshots,
}

async fn drain<R: tokio::io::AsyncRead + Unpin>(
    mut reader: R,
    output: Arc<Mutex<Output>>,
    changed: Arc<Notify>,
    stderr: bool,
    cap: usize,
) -> Result<()> {
    let mut bytes = [0; 8192];
    let mut pending = Vec::new();
    loop {
        let n = reader.read(&mut bytes).await?;
        pending.extend_from_slice(&bytes[..n]);
        let complete = if n == 0 {
            pending.len()
        } else {
            complete_prefix_len(&pending)
        };
        {
            let mut out = output.lock().unwrap();
            let buffer = if stderr {
                &mut out.stderr
            } else {
                &mut out.stdout
            };
            let keep = complete.min(cap.saturating_sub(buffer.len()));
            buffer.extend_from_slice(&pending[..keep]);
            out.truncated |= keep < complete;
        }
        pending.drain(..complete);
        changed.notify_one();
        if n == 0 {
            return Ok(());
        }
    }
}
fn number(args: &Value, key: &str, default: u64, max: u64) -> Result<u64> {
    let n = args
        .get(key)
        .map(|v| v.as_u64().ok_or_else(|| anyhow!("Invalid {key}")))
        .transpose()?
        .unwrap_or(default);
    ensure!(n > 0 && n <= max, "{key} exceeds configured limit");
    Ok(n)
}
impl Processes {
    pub async fn execute(
        &mut self,
        s: &ExecutionContext,
        policy: &ShellSettings,
        name: &str,
        args: &Value,
        call: &str,
        emit: &mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> Result<Value> {
        pot_core::environment_tools::validate(name, args)?;
        if name == "exec_command" && self.sessions.len() >= policy.max_sessions {
            self.reap().await?;
        }
        let cap = number(
            args,
            "max_output_chars",
            policy.default_output_bytes as u64,
            policy.max_output_bytes as u64,
        )? as usize;
        let yielding = name == "write_stdin" || args.get("yield_time_ms").is_some();
        let wait = if yielding {
            number(
                args,
                "yield_time_ms",
                policy.default_yield_ms,
                policy.max_yield_ms,
            )?
        } else {
            policy.max_timeout_ms
        };
        let id = if name == "exec_command" {
            ensure!(
                self.sessions.len() < policy.max_sessions,
                "Process session limit reached"
            );
            let cmd = args["cmd"]
                .as_str()
                .filter(|c| !c.is_empty() && c.len() <= 1024 * 1024)
                .ok_or_else(|| anyhow!("Invalid cmd"))?;
            let cwd = args["workdir"]
                .as_str()
                .map(|p| s.workspace.join(p))
                .unwrap_or_else(|| s.workspace.clone());
            let timeout = if !yielding || args.get("timeout_ms").is_some() {
                Some(number(
                    args,
                    "timeout_ms",
                    policy.default_timeout_ms,
                    policy.max_timeout_ms,
                )?)
            } else {
                None
            };
            let shell = args["shell"].as_str().unwrap_or(&policy.executable);
            ensure!(
                std::path::Path::new(shell).is_absolute()
                    && matches!(
                        std::path::Path::new(shell)
                            .file_name()
                            .and_then(|p| p.to_str()),
                        Some("sh" | "bash" | "zsh")
                    ),
                "Unsupported shell; use an absolute sh/bash/zsh path"
            );
            let login = args["login"].as_bool().unwrap_or(policy.allow_login_shell);
            ensure!(
                !login || policy.allow_login_shell,
                "Login shell disabled by execution policy"
            );
            let tty = args["tty"].as_bool().unwrap_or(false);
            let mut command = self
                .snapshots
                .command(shell, &cwd, &s.workspace, cmd, login, policy.shell_snapshot)
                .await;
            command.kill_on_drop(true);
            let (mut child, input, terminal, stdout, stderr): Spawned = if tty {
                let (read, write, control) = crate::terminal::attach(&mut command)?;
                let child = command.spawn()?;
                (
                    child,
                    Some(Box::new(write)),
                    Some(control),
                    Box::new(read),
                    None,
                )
            } else {
                let mut child = command
                    .stdin(std::process::Stdio::piped())
                    .stdout(std::process::Stdio::piped())
                    .stderr(std::process::Stdio::piped())
                    .process_group(0)
                    .spawn()?;
                let input = child.stdin.take().map(|p| Box::new(p) as Writer);
                let stdout = Box::new(child.stdout.take().unwrap()) as Reader;
                let stderr = Some(Box::new(child.stderr.take().unwrap()) as Reader);
                (child, input, None, stdout, stderr)
            };
            let pid = child.id().ok_or_else(|| anyhow!("Missing process PID"))?;
            let mut group = Group {
                pid,
                cleaned: false,
            };
            let output = Arc::new(Mutex::new(Output::default()));
            let changed = Arc::new(Notify::new());
            let (stop, mut stopped) = watch::channel(false);
            let out = output.clone();
            let notify = changed.clone();
            let (touch, mut touched) = watch::channel(Instant::now());
            let idle_timeout = policy.session_idle_timeout_ms;
            let buffer_cap = policy.max_output_bytes;
            let task = tokio::spawn(async move {
                // Own the readers so every error/abort path cancels them too.
                let mut readers = JoinSet::new();
                readers.spawn(drain(
                    stdout,
                    out.clone(),
                    notify.clone(),
                    false,
                    buffer_cap,
                ));
                if let Some(stderr) = stderr {
                    readers.spawn(drain(stderr, out.clone(), notify.clone(), true, buffer_cap));
                }
                let idle = async {
                    loop {
                        let deadline = *touched.borrow() + Duration::from_millis(idle_timeout);
                        tokio::select! {
                            _ = tokio::time::sleep_until(deadline) => break,
                            changed = touched.changed() => if changed.is_err() { break; },
                        }
                    }
                };
                let timeout = async {
                    if let Some(timeout) = timeout {
                        tokio::time::sleep(Duration::from_millis(timeout)).await;
                    } else {
                        std::future::pending::<()>().await;
                    }
                };
                let (status, reason) = tokio::select! {
                    exit = child.wait() => (Some(exit?), None),
                    _ = timeout => (None, Some("timeout")),
                    _ = idle => (None, Some("idle_timeout")),
                    _ = stopped.wait_for(|v| *v) => (None, Some("cancelled")),
                };
                group.kill();
                if status.is_none() {
                    child.start_kill()?;
                    tokio::time::timeout(Duration::from_secs(5), child.wait())
                        .await
                        .map_err(|_| anyhow!("Tool stop unconfirmed"))??;
                }
                tokio::time::timeout(Duration::from_secs(5), async {
                    while !readers.is_empty() {
                        tokio::select! {
                            result = readers.join_next() => {
                                if let Some(result) = result {
                                    result??;
                                }
                            }
                            _ = tokio::time::sleep(Duration::from_millis(10)) => group.kill(),
                        }
                    }
                    Ok::<_, anyhow::Error>(())
                })
                .await
                .map_err(|_| anyhow!("Tool output cleanup stop unconfirmed"))??;
                {
                    let mut out = out.lock().unwrap();
                    out.exit = status.and_then(|s| s.code());
                    out.reason = reason.map(str::to_owned);
                    out.done = true;
                }
                notify.notify_one();
                Ok(())
            });
            let id = uuid::Uuid::new_v4().to_string();
            self.sessions.insert(
                id.clone(),
                Process {
                    input,
                    terminal,
                    pid,
                    touch,
                    output,
                    changed,
                    stop,
                    task,
                },
            );
            id
        } else {
            args["session_id"]
                .as_str()
                .ok_or_else(|| anyhow!("session_id must be a string"))?
                .to_owned()
        };
        let process = self
            .sessions
            .get_mut(&id)
            .ok_or_else(|| anyhow!("Process session expired or missing in this agent session"))?;
        process.touch.send_replace(Instant::now());
        if !process.task.is_finished() {
            if args["terminate"] == true {
                let _ = process.stop.send(true);
            }
            if args["interrupt"] == true || args["chars"].as_str() == Some("\u{3}") {
                if let Some(terminal) = &process.terminal {
                    terminal.interrupt()?;
                } else {
                    let result = unsafe { libc::kill(-(process.pid as i32), libc::SIGINT) };
                    ensure!(
                        result == 0
                            || std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH),
                        "Process interrupt failed"
                    );
                }
            }
            if args.get("rows").is_some() || args.get("cols").is_some() {
                let terminal = process
                    .terminal
                    .as_ref()
                    .ok_or_else(|| anyhow!("Resize requires a PTY"))?;
                terminal.resize(
                    args["rows"].as_u64().map(|n| n as u16),
                    args["cols"].as_u64().map(|n| n as u16),
                )?;
            }
            if let Some(chars) = args.get("chars") {
                let chars = chars
                    .as_str()
                    .ok_or_else(|| anyhow!("chars must be a string"))?;
                ensure!(chars.len() <= 1024 * 1024, "Input too large");
                if !chars.is_empty() && chars != "\u{3}" {
                    let input = process
                        .input
                        .as_mut()
                        .ok_or_else(|| anyhow!("Process stdin is closed"))?;
                    tokio::select! {
                        result = tokio::time::timeout(Duration::from_millis(policy.max_yield_ms), input.write_all(chars.as_bytes())) => { result.map_err(|_| anyhow!("Process stdin write timed out"))??; },
                        _ = s.feedback.cancellation() => { let _ = process.stop.send(true); },
                    }
                }
            }
            if args.get("close_stdin").is_some_and(|v| v == true) || !yielding {
                if process.terminal.is_some() {
                    if let Some(input) = process.input.as_mut() {
                        input.write_all(b"\x04").await?;
                    }
                } else {
                    process.input.take();
                }
            }
        }
        let deadline = Instant::now() + Duration::from_millis(wait);
        let mut stdout = vec![];
        let mut stderr = vec![];
        let mut truncated = false;
        loop {
            let done = {
                let mut out = process.output.lock().unwrap();
                append(
                    &mut out.stdout,
                    &mut stdout,
                    cap,
                    &mut truncated,
                    call,
                    "stdout",
                    emit,
                )?;
                append(
                    &mut out.stderr,
                    &mut stderr,
                    cap,
                    &mut truncated,
                    call,
                    "stderr",
                    emit,
                )?;
                truncated |= out.truncated;
                out.truncated = false;
                out.done
            };
            if done || process.task.is_finished() || s.feedback.cancelled() {
                break;
            }
            tokio::select! {
                _ = process.changed.notified() => {},
                _ = tokio::time::sleep_until(deadline) => break,
                _ = s.feedback.cancellation() => { let _ = process.stop.send(true); break; },
            }
        }
        let (done, exit, reason) = {
            let mut out = process.output.lock().unwrap();
            append(
                &mut out.stdout,
                &mut stdout,
                cap,
                &mut truncated,
                call,
                "stdout",
                emit,
            )?;
            append(
                &mut out.stderr,
                &mut stderr,
                cap,
                &mut truncated,
                call,
                "stderr",
                emit,
            )?;
            truncated |= out.truncated;
            out.truncated = false;
            (out.done, out.exit, out.reason.clone())
        };
        let finished = done || process.task.is_finished();
        if finished {
            let mut process = self.sessions.remove(&id).unwrap();
            (&mut process.task).await??;
        }
        Ok(
            json!({"stdout":String::from_utf8_lossy(&stdout),"stderr":String::from_utf8_lossy(&stderr),"truncated":truncated,"exit_code":exit,"reason":reason,"session_id":if finished {None}else{Some(id)}}),
        )
    }
    pub async fn reap(&mut self) -> Result<()> {
        let ended: Vec<_> = self
            .sessions
            .iter()
            .filter(|(_, p)| p.task.is_finished())
            .map(|(id, _)| id.clone())
            .collect();
        for id in ended {
            let mut process = self.sessions.remove(&id).unwrap();
            (&mut process.task).await??;
        }
        Ok(())
    }
    pub fn inspect(&self) -> Value {
        json!({"sessions":self.sessions.keys().collect::<Vec<_>>(),"active":self.sessions.values().any(|p|!p.task.is_finished()),"ended":false})
    }
    pub async fn shutdown(&mut self) -> Result<()> {
        for process in self.sessions.values() {
            let _ = process.stop.send(true);
        }
        for (_, mut process) in self.sessions.drain() {
            tokio::time::timeout(Duration::from_secs(6), &mut process.task)
                .await
                .map_err(|_| anyhow!("Tool stop unconfirmed"))???;
        }
        Ok(())
    }
}
fn append(
    source: &mut Vec<u8>,
    dest: &mut Vec<u8>,
    cap: usize,
    truncated: &mut bool,
    call: &str,
    stream: &str,
    emit: &mut (dyn FnMut(Update) -> Result<()> + Send),
) -> Result<()> {
    let keep = source.len().min(cap.saturating_sub(dest.len()));
    if keep > 0 {
        // Display decoding must not split multibyte text across reader chunks.
        let before = display_prefix(dest);
        dest.extend_from_slice(&source[..keep]);
        let after = display_prefix(dest);
        let delta = after.strip_prefix(&before).unwrap_or(&after);
        if !delta.is_empty() {
            emit(Update::ToolOutput {
                id: call.to_owned(),
                stream: stream.to_owned(),
                text: delta.to_owned(),
            })?;
        }
    }
    *truncated |= keep < source.len();
    source.clear();
    Ok(())
}
/// Keep invalid bytes visible, but wait for trailing incomplete UTF-8 sequences.
/// A literal U+FFFD is valid output and must not be mistaken for a decoding placeholder.
fn display_prefix(bytes: &[u8]) -> String {
    String::from_utf8_lossy(&bytes[..complete_prefix_len(bytes)]).into_owned()
}
fn complete_prefix_len(bytes: &[u8]) -> usize {
    let mut offset = 0;
    let mut end = bytes.len();
    while offset < bytes.len() {
        match std::str::from_utf8(&bytes[offset..]) {
            Ok(_) => break,
            Err(error) => match error.error_len() {
                Some(length) => offset += error.valid_up_to() + length,
                None => {
                    end = offset + error.valid_up_to();
                    break;
                }
            },
        }
    }
    end
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn streamed_utf8_split_at_every_byte_and_literal_replacement_character_are_not_duplicated() {
        for text in [
            "中文 🌱 done",
            "a\u{fffd}b",
            "a\u{fffd}中",
            "a\u{fffd}",
            "\u{fffd}",
        ] {
            let mut dest = vec![];
            let mut streamed = String::new();
            let mut truncated = false;
            for byte in text.as_bytes() {
                append(
                    &mut vec![*byte],
                    &mut dest,
                    1024,
                    &mut truncated,
                    "call",
                    "stdout",
                    &mut |u| {
                        if let Update::ToolOutput { text, .. } = u {
                            streamed.push_str(&text);
                        }
                        Ok(())
                    },
                )
                .unwrap();
            }
            assert_eq!(streamed, text);
            assert_eq!(dest, text.as_bytes());
            assert!(!truncated);
        }
    }
    #[tokio::test]
    async fn full_output_buffer_is_drained_to_eof_without_blocking_the_writer() {
        let (mut writer, reader) = tokio::io::duplex(64);
        let output = Arc::new(Mutex::new(Output::default()));
        let changed = Arc::new(Notify::new());
        let write = tokio::spawn(async move {
            writer.write_all(&vec![b'x'; 20_000]).await.unwrap();
        });
        tokio::time::timeout(
            Duration::from_secs(2),
            drain(reader, output.clone(), changed, false, 8),
        )
        .await
        .unwrap()
        .unwrap();
        write.await.unwrap();
        let output = output.lock().unwrap();
        assert_eq!(output.stdout, b"xxxxxxxx");
        assert!(output.truncated);
    }
    #[tokio::test]
    async fn incomplete_utf8_is_retained_across_pipe_reads_instead_of_returned_to_a_poll() {
        let (mut writer, reader) = tokio::io::duplex(64);
        let output = Arc::new(Mutex::new(Output::default()));
        let changed = Arc::new(Notify::new());
        let task = tokio::spawn(drain(reader, output.clone(), changed.clone(), false, 64));
        writer.write_all(&"中".as_bytes()[..1]).await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), changed.notified())
            .await
            .unwrap();
        assert!(output.lock().unwrap().stdout.is_empty());
        writer.write_all(&"中".as_bytes()[1..]).await.unwrap();
        drop(writer);
        task.await.unwrap().unwrap();
        assert_eq!(output.lock().unwrap().stdout, "中".as_bytes());
    }
}
