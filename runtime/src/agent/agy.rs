//! Original AGY CLI transport. Credentials and conversation history remain CLI-owned.
//! Task/MCP roles stay disabled until AGY offers a verified isolation contract.
use super::*;
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};

pub async fn prepare(spec: &ResolvedSpec) -> Result<()> {
    spec.validate()?;
    let launch = spec
        .launch_spec
        .as_ref()
        .ok_or_else(|| anyhow!("Missing AGY launch spec"))?;
    let mut child = tokio::process::Command::new(&launch.executable)
        .args(launch.expand(&["--help".into()])?)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .process_group(0)
        .kill_on_drop(true)
        .spawn()?;
    let pid = child.id().unwrap() as i32;
    let mut stdout = child.stdout.take().unwrap().take(65537);
    let mut stderr = child.stderr.take().unwrap().take(65537);
    let check = tokio::time::timeout(Duration::from_secs(10), async {
        let mut out = vec![];
        let mut err = vec![];
        tokio::try_join!(stdout.read_to_end(&mut out), stderr.read_to_end(&mut err))?;
        anyhow::ensure!(
            out.len() <= 65536 && err.len() <= 65536,
            "AGY capability output exceeds limit"
        );
        let status = child.wait().await?;
        // Some AGY releases print help to stderr. Never expose diagnostics.
        out.extend(err);
        let help = String::from_utf8_lossy(&out);
        anyhow::ensure!(
            status.success()
                && [
                    "--print",
                    "--output-format",
                    "stream-json",
                    "--conversation",
                    "--model",
                    "--disable-slash-commands"
                ]
                .iter()
                .all(|v| help.contains(v)),
            "AGY CLI lacks the required structured transport; update it in Terminal"
        );
        Ok::<_, anyhow::Error>(())
    })
    .await;
    // Dispose wrapper descendants even when the help process has exited.
    unsafe {
        libc::kill(-pid, libc::SIGKILL);
    }
    let _ = child.wait().await;
    check.map_err(|_| anyhow!("AGY capability check timed out"))?
}

pub struct AgyHost;
struct AgySession {
    service: Service,
    binding: Binding,
}
impl driver::DriverHost for AgyHost {
    fn probe(&self, spec: &ResolvedSpec) -> Result<()> {
        spec.validate()?;
        anyhow::ensure!(spec.engine == Engine::Agy, "Expected AGY spec");
        Ok(())
    }
    fn session(
        &self,
        service: Service,
        binding: Binding,
    ) -> Result<Box<dyn driver::SessionHandle>> {
        self.probe(&binding.spec)?;
        anyhow::ensure!(
            binding.state == BindingState::Active,
            "AGY session needs explicit rebind"
        );
        Ok(Box::new(AgySession { service, binding }))
    }
}
impl driver::SessionHandle for AgySession {
    fn start_turn<'a>(
        &'a mut self,
        request: &'a SendMessage,
        resuming: bool,
        sink: driver::Sink<'a>,
    ) -> futures::future::BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            anyhow::ensure!(
                !resuming,
                "AGY interrupted turns cannot be automatically replayed"
            );
            let mut seq = 0;
            let mut emit = |update| {
                seq += 1;
                sink(driver::DriverEvent {
                    epoch: self.binding.epoch,
                    turn_id: request.request_id.clone(),
                    seq,
                    update,
                })
            };
            let result = execute(&self.service, &self.binding, request, &mut emit).await;
            let outcome = result
                .as_ref()
                .cloned()
                .unwrap_or_else(|e| driver::Outcome::Failed(e.to_string()));
            emit(driver::Update::TurnFinished(outcome))?;
            result.map(|_| ())
        })
    }
}

fn identity(event: &Value) -> Result<&str> {
    let id = event["conversation_id"]
        .as_str()
        .ok_or_else(|| anyhow!("Missing AGY conversation identity"))?;
    anyhow::ensure!(
        uuid::Uuid::parse_str(id).is_ok(),
        "Invalid AGY conversation identity"
    );
    Ok(id)
}
fn outcome(status: &str) -> Result<driver::Outcome> {
    use driver::Outcome;
    Ok(match status {
        "SUCCESS" => Outcome::Success,
        "CANCELED" | "INTERRUPTED" => Outcome::Cancelled,
        "ERROR" | "INVALID" => Outcome::Failed(
            "AGY request failed; check login, model and permissions in Terminal".into(),
        ),
        _ => bail!("AGY ended without a terminal result"),
    })
}
async fn execute(
    s: &Service,
    binding: &Binding,
    request: &SendMessage,
    emit: &mut (dyn FnMut(driver::Update) -> Result<()> + Send),
) -> Result<driver::Outcome> {
    use driver::{Outcome, Update};
    anyhow::ensure!(
        !s.is_worker && !request.request_id.starts_with(orchestration::RESULT_PREFIX),
        "AGY task and summary roles require a verified tool isolation contract; select Codex or Claude for these roles"
    );
    if cancelled(s) {
        return Ok(Outcome::Cancelled);
    }
    if let Err(error) = prepare(&binding.spec).await {
        s.inner.lock().unwrap().store.set("agent.needs_rebind", "AGY command unavailable or incompatible; restore/edit the command and retry, or explicitly migrate")?;
        return Err(error);
    }
    let NativeRef::Agy { conversation_id } = &binding.native_ref else {
        bail!("Expected AGY native conversation");
    };
    if let Some(id) = conversation_id {
        anyhow::ensure!(
            uuid::Uuid::parse_str(id).is_ok(),
            "Invalid stored AGY conversation identity; explicit rebind required"
        );
    }
    let spec = &binding.spec;
    let launch = spec.launch_spec.as_ref().unwrap();
    let mut args = vec![
        "--print".into(),
        "--input-format".into(),
        "text".into(),
        "--output-format".into(),
        "stream-json".into(),
        "--disable-slash-commands".into(),
        "--model".into(),
        spec.model.clone(),
    ];
    if let Some(id) = conversation_id {
        args.extend(["--conversation".into(), id.clone()]);
    }
    if let Some(effort) = &spec.params.reasoning_effort {
        args.extend(["--effort".into(), effort.clone()]);
    }
    let product_turn = s
        .inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .ok_or_else(|| anyhow!("No active turn"))?
        .user_message_id
        .clone();
    let ledger = format!(
        "agy:{}:{}:{}",
        s.usage_scope, binding.epoch, request.request_id
    );
    s.usage.lock().unwrap().begin_native(
        &ledger,
        &s.usage_scope,
        &product_turn,
        spec,
        binding.epoch,
    )?;
    record_turn(s, &request.request_id)?;
    let spawned = tokio::process::Command::new(&launch.executable)
        .args(launch.expand(&args)?)
        .current_dir(&s.workspace)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .process_group(0)
        .kill_on_drop(true)
        .spawn();
    let mut child = match spawned {
        Ok(child) => child,
        Err(e) => {
            s.usage.lock().unwrap().native_incomplete(&ledger)?;
            return Err(e.into());
        }
    };
    let pid = child.id().unwrap() as i32;
    let mut stdin = child.stdin.take().unwrap();
    let mut reader = BufReader::new(child.stdout.take().unwrap());
    let mut native = conversation_id.clone();
    let mut initialized = false;
    let mut terminal = None;
    let mut response = None;
    let mut reported = false;
    let execution: Result<()> = async {
        stdin.write_all(request.text.as_bytes()).await?;
        stdin.shutdown().await?;
        drop(stdin);
        let deadline = tokio::time::sleep(Duration::from_secs(1800));
        tokio::pin!(deadline);
        let mut stopping = false;
        loop {
            if cancelled(s) && !stopping {
                unsafe {
                    libc::kill(-pid, libc::SIGINT);
                }
                stopping = true;
                deadline
                    .as_mut()
                    .reset(tokio::time::Instant::now() + Duration::from_secs(15));
            }
            let mut frame = vec![];
            let mut limited = (&mut reader).take(4 * 1024 * 1024 + 1);
            let n = tokio::select! {
                _ = &mut deadline => bail!("AGY timed out; stop unconfirmed"),
                _ = s.cancel_notify.notified(), if !stopping => continue,
                result = limited.read_until(b'\n', &mut frame) => result?,
            };
            if n == 0 {
                break;
            }
            anyhow::ensure!(n <= 4 * 1024 * 1024, "AGY frame exceeds limit");
            // Parse errors may contain source snippets; do not propagate them.
            let event: Value = serde_json::from_slice(&frame)
                .map_err(|_| anyhow!("Invalid AGY structured output"))?;
            anyhow::ensure!(
                terminal.is_none(),
                "AGY emitted output after terminal result"
            );
            match event["event"].as_str() {
                Some("init") => {
                    anyhow::ensure!(!initialized, "Duplicate AGY initialization");
                    let id = identity(&event)?;
                    anyhow::ensure!(
                        native.as_deref().is_none_or(|old| old == id),
                        "AGY changed native conversation; explicit rebind required"
                    );
                    save_agy_conversation(&mut s.inner.lock().unwrap().store, id)?;
                    native = Some(id.into());
                    initialized = true;
                }
                Some("result") => {
                    let result = &event["result"];
                    let state = outcome(result["status"].as_str().unwrap_or(""))?;
                    if initialized {
                        anyhow::ensure!(
                            Some(identity(result)?) == native.as_deref(),
                            "AGY result conversation mismatch"
                        );
                    } else {
                        anyhow::ensure!(
                            matches!(state, Outcome::Failed(_)),
                            "AGY result before initialization"
                        );
                    }
                    if initialized && result["num_turns"].as_u64().unwrap_or(0) > 0 {
                        // Only the final envelope is authoritative; step usage is never added.
                        s.usage
                            .lock()
                            .unwrap()
                            .settle_agy(&ledger, &result["usage"])?;
                        reported = true;
                    }
                    if state == Outcome::Success && !cancelled(s) {
                        let text = result["response"]
                            .as_str()
                            .ok_or_else(|| anyhow!("Missing AGY response"))?;
                        response = Some(text.to_owned());
                    }
                    terminal = Some(state);
                }
                Some("step_update") => {
                    anyhow::ensure!(initialized, "AGY step before initialization");
                }
                _ => bail!("Unsupported AGY structured event"),
            }
        }
        Ok(())
    }
    .await;
    if execution.is_err() {
        unsafe {
            libc::kill(-pid, libc::SIGKILL);
        }
    }
    let exit = tokio::time::timeout(Duration::from_secs(15), child.wait()).await;
    // Always stop process-group descendants, including on successful completion.
    unsafe {
        libc::kill(-pid, libc::SIGKILL);
    }
    if exit.is_err() {
        let _ = child.kill().await;
        let _ = child.wait().await;
    }
    if execution.is_err()
        || !exit
            .as_ref()
            .is_ok_and(|r| r.as_ref().is_ok_and(|v| v.success()))
        || !reported
        || terminal.is_none()
        || cancelled(s)
    {
        s.usage.lock().unwrap().native_incomplete(&ledger)?;
    }
    execution?;
    let exit = exit.map_err(|_| anyhow!("AGY shutdown timed out; stop unconfirmed"))??;
    if cancelled(s) {
        return Ok(Outcome::Cancelled);
    }
    let state = terminal
        .ok_or_else(|| anyhow!("AGY exited without a terminal result; check login in Terminal"))?;
    anyhow::ensure!(
        state != Outcome::Success || exit.success(),
        "AGY process failed after result"
    );
    if state == Outcome::Success {
        emit(Update::MessageStarted)?;
        emit(Update::MessageFinished {
            text: response.ok_or_else(|| anyhow!("Missing AGY response"))?,
            outcome: state.clone(),
        })?;
    }
    Ok(state)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn terminal_status_is_explicit_and_errors_are_redacted() {
        assert_eq!(outcome("SUCCESS").unwrap(), driver::Outcome::Success);
        assert_eq!(outcome("INTERRUPTED").unwrap(), driver::Outcome::Cancelled);
        for status in ["WAITING", "RUNNING", "", "future"] {
            assert!(outcome(status).is_err());
        }
        assert!(matches!(
            outcome("ERROR").unwrap(),
            driver::Outcome::Failed(_)
        ));
    }
    #[test]
    fn identity_requires_native_uuid() {
        assert!(identity(&json!({"conversation_id":"../other"})).is_err());
        assert!(identity(&json!({})).is_err());
        assert!(identity(&json!({"conversation_id":id()})).is_ok());
    }
}
