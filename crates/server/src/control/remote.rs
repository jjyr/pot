//! Caller journal and runtime port; effects are never retried automatically.
use super::*;
use futures_util::future::BoxFuture;
use pot_control::remote_environment::{RemoteBinding, RemoteEnvironment};

#[derive(Clone, Serialize, Deserialize)]
pub(super) struct ClientRun {
    pub(super) binding: RemoteBinding,
    pub(super) lease_id: Option<String>,
    context: Option<Value>,
    pub(super) cancelled: bool,
    #[serde(default)]
    pub(super) cancel_requested: bool,
    #[serde(default)]
    attempt: u64,
    #[serde(default)]
    waiting_reason: Option<String>,
    operations: BTreeMap<String, ClientOperation>,
    #[serde(default)]
    pub(super) task_launch: Option<pot_runner::Launch>,
    #[serde(default)]
    pub(super) task_result: Option<pot_runner::Terminal>,
    #[serde(default)]
    task_interrupted: bool,
    #[serde(default)]
    task_sent_at_ms: Option<i64>,
}
#[derive(Clone, Serialize, Deserialize, PartialEq)]
struct ClientOperation {
    name: String,
    args: Value,
    result: Option<Value>,
}
pub(super) fn run_key(binding: &RemoteBinding) -> Result<String> {
    digest(binding)
}
impl Authority {
    pub(super) fn client_run(&self, binding: &RemoteBinding) -> Result<ClientRun> {
        self.book
            .lock()
            .unwrap()
            .client_runs
            .get(&run_key(binding)?)
            .cloned()
            .ok_or_else(|| anyhow::anyhow!("Remote run has not been opened"))
    }
    pub(super) async fn ensure_run(&self, binding: &RemoteBinding) -> Result<ClientRun> {
        self.validate(&binding.environment)?;
        let key = run_key(binding)?;
        // Do not replace an unsettled older run, including after caller restart.
        let previous = self
            .book
            .lock()
            .unwrap()
            .client_runs
            .values()
            .filter(|r| {
                r.binding.owner == binding.owner
                    && r.binding.environment == binding.environment
                    && r.binding != *binding
            })
            .cloned()
            .collect::<Vec<_>>();
        for old in previous {
            if old.task_launch.is_some() && old.task_result.is_none() {
                let receipt = self
                    .call(
                        &old.binding.environment.runtime_id,
                        Rpc::InspectTask {
                            task_id: old.binding.owner.clone(),
                            run_id: old.binding.run_id.clone(),
                        },
                    )
                    .await?;
                let result: pot_runner::Terminal =
                    serde_json::from_value(receipt["result"].clone()).map_err(|_| {
                        anyhow::anyhow!(
                            "Previous Task Agent remains unresolved; replacement is blocked"
                        )
                    })?;
                self.update(|book| {
                    book.client_runs
                        .get_mut(&run_key(&old.binding)?)
                        .unwrap()
                        .task_result = Some(result);
                    Ok(())
                })?;
            }
            for (call, operation) in &old.operations {
                if operation.result.is_none() {
                    let lease = old.lease_id.as_ref().ok_or_else(|| {
                        anyhow::anyhow!("Unsettled remote run has no lease receipt")
                    })?;
                    let receipt = self
                        .call(
                            &old.binding.environment.runtime_id,
                            Rpc::Inspect {
                                lease_id: lease.clone(),
                                call_id: call.clone(),
                            },
                        )
                        .await?;
                    let terminal = terminal_receipt(&receipt).ok_or_else(|| anyhow::anyhow!(
                        "Previous remote operation requires reconciliation; replacement dispatch is blocked"
                    ))?;
                    self.store_result(&old.binding, call, terminal)?;
                }
            }
            if !old.cancelled {
                RemoteEnvironment::cancel(self, old.binding).await?;
            }
        }
        self.update(|book| {
            book.client_runs
                .entry(key.clone())
                .or_insert_with(|| ClientRun {
                    binding: binding.clone(),
                    lease_id: None,
                    context: None,
                    cancelled: false,
                    cancel_requested: false,
                    attempt: 0,
                    waiting_reason: None,
                    operations: BTreeMap::new(),
                    task_launch: None,
                    task_result: None,
                    task_interrupted: false,
                    task_sent_at_ms: None,
                });
            Ok(())
        })?;
        let run = self.client_run(binding)?;
        ensure!(
            !run.cancelled && !run.cancel_requested,
            "Remote run was cancelled; use a new turn and reconcile original receipts"
        );
        if run.lease_id.is_none() {
            let lease = self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::Acquire {
                        owner: binding.owner.clone(),
                        run_id: if run.attempt == 0 {
                            binding.run_id.clone()
                        } else {
                            attempt_id(&binding.run_id, run.attempt)?
                        },
                        generation: binding.generation,
                        target: binding.environment.target.clone(),
                    },
                )
                .await?;
            let id = lease["id"]
                .as_str()
                .ok_or_else(|| anyhow::anyhow!("Missing remote lease"))?
                .to_owned();
            self.update(|book| {
                let run = book.client_runs.get_mut(&key).unwrap();
                ensure!(
                    !run.cancelled && !run.cancel_requested,
                    "Remote run cancelled during acceptance"
                );
                run.lease_id = Some(id);
                Ok(())
            })?;
        }
        self.client_run(binding)
    }
    fn store_result(&self, binding: &RemoteBinding, call: &str, result: Value) -> Result<()> {
        let key = run_key(binding)?;
        self.update(|book| {
            let operation = book
                .client_runs
                .get_mut(&key)
                .and_then(|r| r.operations.get_mut(call))
                .ok_or_else(|| anyhow::anyhow!("Missing caller operation intent"))?;
            if let Some(old) = &operation.result {
                ensure!(*old == result, "Conflicting terminal remote outcome");
            }
            operation.result = Some(result);
            Ok(())
        })
    }
    pub(super) fn waiting(&self, binding: &RemoteBinding, reason: Option<&str>) -> Result<()> {
        let key = run_key(binding)?;
        let reason = reason.map(str::to_owned);
        if self.client_run(binding)?.waiting_reason == reason {
            return Ok(());
        }
        self.update(|book| {
            book.client_runs.get_mut(&key).unwrap().waiting_reason = reason;
            Ok(())
        })
    }
    async fn inspect_until_available(
        &self,
        binding: &RemoteBinding,
        lease: &str,
        call: &str,
    ) -> Result<Value> {
        loop {
            let run = self.client_run(binding)?;
            ensure!(
                !run.cancel_requested && !run.cancelled,
                "Remote run is cancelled"
            );
            match self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::Inspect {
                        lease_id: lease.into(),
                        call_id: call.into(),
                    },
                )
                .await
            {
                Ok(receipt) => return Ok(receipt),
                Err(_) => self.waiting(binding, Some("等待远程机器连接并核对原操作；不会重放"))?,
            }
            tokio::time::sleep(std::time::Duration::from_secs(1)).await;
        }
    }
}
fn attempt_id(run: &str, attempt: u64) -> Result<String> {
    let hash = ring::digest::digest(&ring::digest::SHA256, &serde_json::to_vec(&(run, attempt))?);
    Ok(uuid::Uuid::from_bytes(hash.as_ref()[..16].try_into().unwrap()).to_string())
}
impl RemoteEnvironment for Authority {
    fn machine_epoch(&self, machine: &str) -> Result<String> {
        let book = self.book.lock().unwrap();
        let member = book
            .members
            .get(machine)
            .filter(|m| m.enabled)
            .ok_or_else(|| anyhow::anyhow!("Machine membership is inactive"))?;
        digest(&(&member.identity, &member.token))
    }

    fn machines(&self) -> Vec<pot_protocol::MachineRoute> {
        let mut machines = vec![pot_protocol::MachineRoute {
            runtime_id: self.identity.runtime_id.clone(),
            tags: self.local_tags(),
        }];
        machines.extend(
            self.book
                .lock()
                .unwrap()
                .members
                .values()
                .filter(|m| m.enabled)
                .map(|m| pot_protocol::MachineRoute {
                    runtime_id: m.identity.runtime_id.clone(),
                    tags: m.tags.clone(),
                }),
        );
        machines
    }

    fn run_task(
        &self,
        launch: pot_runner::Launch,
        cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
    ) -> BoxFuture<'_, Result<pot_runner::Terminal>> {
        Box::pin(async move {
            let binding = RemoteBinding {
                owner: launch.task_id.clone(),
                run_id: launch.run_id.clone(),
                generation: launch.generation,
                environment: launch.environment.clone(),
            };
            if let Ok(run) = self.client_run(&binding)
                && let Some(result) = run.task_result
            {
                while !self.client_run(&binding)?.cancelled {
                    if RemoteEnvironment::cancel(self, binding.clone())
                        .await
                        .is_err()
                    {
                        tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                    }
                }
                if run.task_interrupted {
                    return Err(pot_runner::Interrupted.into());
                }
                return Ok(result);
            }
            let run = loop {
                match self.ensure_run(&binding).await {
                    Ok(run) => break run,
                    Err(error) if error.downcast_ref::<RemoteRejection>().is_some() => {
                        return Err(error);
                    }
                    Err(error) => {
                        if self.client_run(&binding).is_err() {
                            return Err(error);
                        }
                        self.waiting(&binding, Some("等待远程机器接入；尚未启动 Task Agent"))?;
                        ensure!(!cancelled(), "Task cancelled before launch acceptance");
                        tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                    }
                }
            };
            let key = run_key(&binding)?;
            if run.task_interrupted {
                return Err(pot_runner::Interrupted.into());
            }
            if let Some(result) = run.task_result {
                return Ok(result);
            }
            let fresh = run.task_launch.is_none();
            if fresh {
                loop {
                    match RemoteEnvironment::install_resources(
                        self,
                        binding.clone(),
                        launch.resources.clone(),
                    )
                    .await
                    {
                        Ok(_) => break,
                        Err(error) if error.downcast_ref::<RemoteRejection>().is_some() => {
                            return Err(error);
                        }
                        Err(_) => {
                            self.waiting(
                                &binding,
                                Some("等待远程机器核对资源；尚未启动 Task Agent"),
                            )?;
                            ensure!(!cancelled(), "Task cancelled before launch acceptance");
                            tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                        }
                    }
                }
            }
            if let Some(old) = &run.task_launch {
                ensure!(
                    serde_json::to_value(old)? == serde_json::to_value(&launch)?,
                    "Task launch intent conflict"
                );
            } else {
                self.update(|book| {
                    let run = book.client_runs.get_mut(&key).unwrap();
                    run.task_launch = Some(launch.clone());
                    run.task_sent_at_ms = Some(now());
                    Ok(())
                })?;
            }
            let remote = &binding.environment.runtime_id;
            let mut receipt = if fresh {
                self.call(
                    remote,
                    Rpc::LaunchTask {
                        lease_id: run.lease_id.unwrap(),
                        launch: Box::new(launch),
                    },
                )
                .await
                .map(Some)
                .or_else(|error| {
                    if error.downcast_ref::<RemoteRejection>().is_some() {
                        Err(error)
                    } else {
                        Ok(None)
                    }
                })?
            } else {
                None
            };
            loop {
                if let Some(result) = receipt
                    .as_ref()
                    .and_then(|v| v.get("result"))
                    .filter(|v| !v.is_null())
                {
                    let result: pot_runner::Terminal = serde_json::from_value(result.clone())?;
                    self.update(|book| {
                        let run = book.client_runs.get_mut(&key).unwrap();
                        run.task_result = Some(result.clone());
                        run.task_interrupted = receipt
                            .as_ref()
                            .is_some_and(|v| v["phase"] == "interrupted");
                        Ok(())
                    })?;
                    while !self.client_run(&binding)?.cancelled {
                        if RemoteEnvironment::cancel(self, binding.clone())
                            .await
                            .is_err()
                        {
                            self.waiting(&binding, Some("结果已保存；等待远程执行范围清理确认"))?;
                            tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                        }
                    }
                    self.waiting(&binding, None)?;
                    if self.client_run(&binding)?.task_interrupted {
                        return Err(pot_runner::Interrupted.into());
                    }
                    return Ok(result);
                }
                if receipt
                    .as_ref()
                    .is_some_and(|v| v["phase"] == "recovery_required")
                {
                    self.waiting(
                        &binding,
                        Some("执行机器已重启；原操作结果待核对，不会自动续跑"),
                    )?;
                }
                if cancelled() {
                    RemoteEnvironment::cancel(self, binding.clone()).await?;
                }
                if receipt.is_none() {
                    self.waiting(
                        &binding,
                        Some("等待远程 Task Agent 原 run 回执；不会重新启动"),
                    )?;
                }
                let _ = RemoteEnvironment::renew(self, binding.clone()).await;
                tokio::time::sleep(std::time::Duration::from_millis(250)).await;
                receipt = self
                    .call(
                        remote,
                        Rpc::InspectTask {
                            task_id: binding.owner.clone(),
                            run_id: binding.run_id.clone(),
                        },
                    )
                    .await
                    .ok();
                if receipt
                    .as_ref()
                    .is_some_and(|v| v["phase"] == "not_accepted")
                    && self
                        .client_run(&binding)?
                        .task_sent_at_ms
                        .is_some_and(|t| now() - t > 30_000)
                {
                    anyhow::bail!(
                        "Original Task Agent launch was not accepted; no replay performed"
                    );
                }
            }
        })
    }
    fn read_file(
        &self,
        environment: EnvironmentRef,
        owner: String,
        query: pot_protocol::FileQuery,
    ) -> BoxFuture<'_, Result<pot_protocol::FileChunk>> {
        Box::pin(async move {
            self.validate(&environment)?;
            uuid::Uuid::parse_str(&owner)?;
            anyhow::ensure!(
                pot_protocol::valid_file_path(&query.path),
                "Invalid relative file path"
            );
            let result = self
                .call(
                    &environment.runtime_id,
                    Rpc::ReadFile {
                        owner,
                        target: environment.target,
                        query,
                    },
                )
                .await?;
            Ok(serde_json::from_value(result)?)
        })
    }

    fn ready_owner(&self, owner: String) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move { self.reconcile_management(&owner).await })
    }
    fn validate_management(&self, environment: &EnvironmentRef) -> Result<()> {
        self.validate_management_target(environment)
    }
    fn manage(
        &self,
        binding: RemoteBinding,
        call: String,
        args: Value,
        cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(self.manage_remote(binding, call, args, cancelled))
    }
    fn waiting_reason(&self, owner: &str) -> Option<String> {
        if self
            .book
            .lock()
            .unwrap()
            .client_management
            .values()
            .any(|i| i.binding.owner == owner && i.result.is_none())
        {
            return Some("等待远程 Sandbox 管理回执；不会重放，可取消".into());
        }
        self.book
            .lock()
            .unwrap()
            .client_runs
            .values()
            .filter(|run| run.binding.owner == owner && !run.cancelled)
            .find_map(|run| run.waiting_reason.clone())
    }
    fn validate(&self, environment: &EnvironmentRef) -> Result<()> {
        environment.validate()?;
        let book = self.book.lock().unwrap();
        let remote = book
            .members
            .get(&environment.runtime_id)
            .filter(|p| p.enabled)
            .ok_or_else(|| anyhow::anyhow!("Remote machine is not connected"))?;
        ensure!(
            remote.inventory["items"]
                .as_array()
                .is_some_and(|items| items
                    .iter()
                    .any(|item| item["environment_ref"] == json!(environment))),
            "Remote Environment is not in the Control machine inventory"
        );
        Ok(())
    }
    fn inventory(&self) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            let machines = self.machines().await;
            let mut items = vec![];
            for machine in machines {
                for mut item in machine["environments"]
                    .as_array()
                    .cloned()
                    .unwrap_or_default()
                {
                    item["connection"] = machine["connection"].clone();
                    item["machine_tags"] = machine["tags"].clone();
                    items.push(item);
                }
            }
            Ok(json!({"items":items}))
        })
    }
    fn install_resources(
        &self,
        binding: RemoteBinding,
        resources: pot_runner::resources::GlobalResources,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            resources.validate()?;
            let run = self.ensure_run(&binding).await?;
            let response = self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::Resources {
                        lease_id: run
                            .lease_id
                            .ok_or_else(|| anyhow::anyhow!("Remote lease missing"))?,
                        resources,
                    },
                )
                .await;
            let context = match response {
                Ok(context) => context,
                Err(error) => {
                    if error.downcast_ref::<RemoteRejection>().is_some() {
                        self.waiting(&binding, None)?;
                    } else {
                        self.waiting(
                            &binding,
                            Some("等待远程机器接收 Control 资源；不会回退到本机资源"),
                        )?;
                    }
                    return Err(error);
                }
            };
            let key = run_key(&binding)?;
            self.update(|book| {
                book.client_runs
                    .get_mut(&key)
                    .ok_or_else(|| anyhow::anyhow!("Remote run missing"))?
                    .context = Some(context.clone());
                Ok(())
            })?;
            self.waiting(&binding, None)?;
            Ok(context)
        })
    }
    fn open(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            // A checkpoint may resume the same logical turn after confirmed cleanup.
            // Reopen only explicitly here, with a fresh destination dispatch identity.
            let key = run_key(&binding)?;
            self.update(|book| {
                if let Some(run) = book.client_runs.get_mut(&key)
                    && run.cancelled
                {
                    ensure!(
                        run.operations.values().all(|op| op.result.is_some()),
                        "Cancelled run still has unsettled operations; reconcile before resuming"
                    );
                    run.attempt = run
                        .attempt
                        .checked_add(1)
                        .ok_or_else(|| anyhow::anyhow!("Dispatch attempts exhausted"))?;
                    run.cancelled = false;
                    run.cancel_requested = false;
                    run.lease_id = None;
                    run.context = None;
                    run.waiting_reason = None;
                }
                Ok(())
            })?;
            let run = match self.ensure_run(&binding).await {
                Ok(run) => run,
                Err(error) => {
                    if error.downcast_ref::<RemoteRejection>().is_some() {
                        if self.client_run(&binding).is_ok() {
                            self.waiting(&binding, None)?;
                        }
                        return Err(error);
                    }
                    if self.client_run(&binding).is_ok() {
                        self.waiting(
                            &binding,
                            Some("等待远程连接或租约核对；已过期的租约不会自动替换，可取消任务"),
                        )?;
                    }
                    return Err(error);
                }
            };
            let context = self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::Context {
                        lease_id: run.lease_id.unwrap(),
                    },
                )
                .await;
            let context = match context {
                Ok(context) => context,
                Err(error) => {
                    if error.downcast_ref::<RemoteRejection>().is_some() {
                        self.waiting(&binding, None)?;
                        return Err(error);
                    }
                    self.waiting(&binding, Some("等待远程机器连接并核对原操作；不会重放"))?;
                    return Err(error);
                }
            };
            let key = run_key(&binding)?;
            self.update(|book| {
                book.client_runs.get_mut(&key).unwrap().context = Some(context.clone());
                book.client_runs.get_mut(&key).unwrap().waiting_reason = None;
                Ok(())
            })?;
            Ok(context)
        })
    }
    fn prepare(
        &self,
        binding: RemoteBinding,
        name: String,
        args: Value,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            let run = self.ensure_run(&binding).await?;
            self.call(
                &binding.environment.runtime_id,
                Rpc::Prepare {
                    lease_id: run.lease_id.unwrap(),
                    name,
                    args,
                },
            )
            .await
        })
    }
    fn renew(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            let run = self.client_run(&binding)?;
            ensure!(
                !run.cancelled && !run.cancel_requested,
                "Remote run is cancelled"
            );
            let lease = run
                .lease_id
                .ok_or_else(|| anyhow::anyhow!("Remote lease acceptance unconfirmed"))?;
            if self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::Renew {
                        lease_id: lease.clone(),
                    },
                )
                .await
                .is_err()
            {
                ensure!(
                    run.task_launch.is_some(),
                    "Expired non-agent lease requires explicit reconciliation"
                );
                let directory = self.data.join("task-service-receipts");
                if directory.exists() {
                    for path in task_services::receipt_files(&directory)? {
                        let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
                        if receipt.request["binding"] == json!(binding) {
                            ensure!(
                                receipt.result.is_some(),
                                "Control service outcome requires reconciliation before continuation"
                            );
                        }
                    }
                }
                let next = self
                    .call(
                        &binding.environment.runtime_id,
                        Rpc::RecoverTaskLease { lease_id: lease },
                    )
                    .await?;
                let id = next["id"]
                    .as_str()
                    .ok_or_else(|| anyhow::anyhow!("Missing recovered lease"))?
                    .to_owned();
                self.update(|book| {
                    book.client_runs
                        .get_mut(&run_key(&binding)?)
                        .unwrap()
                        .lease_id = Some(id);
                    Ok(())
                })?;
            }
            Ok(())
        })
    }
    fn ready(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            let run = self.client_run(&binding)?;
            for (call, operation) in run.operations {
                if operation.result.is_none() {
                    let receipt = match self
                        .call(
                            &binding.environment.runtime_id,
                            Rpc::Inspect {
                                lease_id: run.lease_id.clone().unwrap(),
                                call_id: call.clone(),
                            },
                        )
                        .await
                    {
                        Ok(receipt) => receipt,
                        Err(error) => {
                            self.waiting(&binding, Some("等待远程机器连接并核对原操作；不会重放"))?;
                            return Err(error);
                        }
                    };
                    if let Some(result) = terminal_receipt(&receipt) {
                        self.store_result(&binding, &call, result)?;
                        continue;
                    }
                    self.waiting(&binding, Some("远程操作结果待核对；不会重放，可取消任务"))?;
                    anyhow::bail!(
                        "An earlier remote operation needs reconciliation before new model requests or tools"
                    );
                }
            }
            if let Err(error) = RemoteEnvironment::renew(self, binding.clone()).await {
                self.waiting(
                    &binding,
                    Some("等待远程连接或租约核对；已过期的租约不会自动替换，可取消任务"),
                )?;
                return Err(error);
            }
            self.waiting(&binding, None)?;
            Ok(())
        })
    }
    fn execute(
        &self,
        binding: RemoteBinding,
        call: String,
        name: String,
        args: Value,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            ensure!(
                !call.is_empty() && call.len() <= 256,
                "Invalid remote operation ID"
            );
            let run = self.ensure_run(&binding).await?;
            let lease = run.lease_id.unwrap();
            let key = run_key(&binding)?;
            let previous = run.operations.get(&call).cloned();
            let operation = ClientOperation {
                name: name.clone(),
                args: args.clone(),
                result: None,
            };
            if let Some(old) = &previous {
                ensure!(
                    old.name == name && old.args == args,
                    "Remote caller operation identity conflict"
                );
                if let Some(result) = &old.result {
                    return outcome(result);
                }
            } else {
                self.update(|book| {
                    book.client_runs
                        .get_mut(&key)
                        .unwrap()
                        .operations
                        .insert(call.clone(), operation);
                    Ok(())
                })?;
            }
            let remote = &binding.environment.runtime_id;
            let mut receipt = if previous.is_none() {
                // An uncertain send is followed only by inspect, never by another execute.
                match self
                    .call(
                        remote,
                        Rpc::Execute {
                            lease_id: lease.clone(),
                            call_id: call.clone(),
                            name,
                            args,
                        },
                    )
                    .await
                {
                    Ok(receipt) => receipt,
                    Err(_) => {
                        self.inspect_until_available(&binding, &lease, &call)
                            .await?
                    }
                }
            } else {
                self.inspect_until_available(&binding, &lease, &call)
                    .await?
            };
            let mut renewed = std::time::Instant::now();
            loop {
                if let Some(result) = terminal_receipt(&receipt) {
                    self.store_result(&binding, &call, result.clone())?;
                    self.waiting(&binding, None)?;
                    return outcome(&result);
                }
                match receipt["state"].as_str() {
                    Some("accepted") => {}
                    _ => {
                        self.waiting(&binding, Some("远程操作结果待核对；不会重放，可取消任务"))?
                    }
                }
                if renewed.elapsed() >= std::time::Duration::from_secs(5) {
                    if RemoteEnvironment::renew(self, binding.clone())
                        .await
                        .is_err()
                    {
                        self.waiting(&binding, Some("远程租约无法续期，等待核对原操作；不会重放"))?;
                    }
                    renewed = std::time::Instant::now();
                }
                tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                receipt = self
                    .inspect_until_available(&binding, &lease, &call)
                    .await?;
            }
        })
    }
    fn cancel(&self, binding: RemoteBinding) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            let key = run_key(&binding)?;
            self.update(|book| {
                let run = book
                    .client_runs
                    .get_mut(&key)
                    .ok_or_else(|| anyhow::anyhow!("Remote run has not been opened"))?;
                run.cancel_requested = true;
                Ok(())
            })?;
            let run = self.client_run(&binding)?;
            if run.cancelled {
                return Ok(());
            }
            if let Some(lease) = run.lease_id {
                self.call(
                    &binding.environment.runtime_id,
                    Rpc::Cancel { lease_id: lease },
                )
                .await?;
            }
            let key = run_key(&binding)?;
            self.update(|book| {
                book.client_runs.get_mut(&key).unwrap().cancelled = true;
                Ok(())
            })
        })
    }
}
pub(super) fn outcome(result: &Value) -> Result<Value> {
    ensure!(
        result["ok"] == true,
        "Remote operation failed: {}",
        result["error"].as_str().unwrap_or("unconfirmed result")
    );
    Ok(result["value"].clone())
}

// Only an authenticated destination receipt proves that a send had no effects.
pub(super) fn terminal_receipt(receipt: &Value) -> Option<Value> {
    match receipt["state"].as_str() {
        Some("completed") => {
            let result = &receipt["result"];
            match result["ok"].as_bool() {
                Some(true) if result.get("value").is_some() => Some(result.clone()),
                Some(false) if result["error"].is_string() => Some(result.clone()),
                _ => None,
            }
        }
        Some("not_accepted") => Some(
            json!({"ok":false,"error":"Destination confirmed operation was not accepted; no replay performed"}),
        ),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn checkpoint_attempts_are_stable_distinct_protocol_uuids() {
        let run = uuid::Uuid::new_v4().to_string();
        let first = attempt_id(&run, 1).unwrap();
        assert!(uuid::Uuid::parse_str(&first).is_ok());
        assert_eq!(first, attempt_id(&run, 1).unwrap());
        assert_ne!(first, attempt_id(&run, 2).unwrap());
        assert_ne!(first, run);
    }
    #[tokio::test]
    async fn lost_connection_waits_then_inspects_original_effect_without_replay() {
        use super::super::tests::{native, resources};
        let caller = resources().await;
        let target = resources().await;
        assert!(
            native(
                &target,
                json!({"request_id":"host","resource":{"kind":"host"}})
            )
            .await
            .status()
            .is_success()
        );
        super::super::tests::join_nodes(&caller, &target).await;
        let listener: Option<Listen> = serde_json::from_slice(
            &std::fs::read(caller.control.data.join("control-listener.json")).unwrap(),
        )
        .unwrap();
        caller
            .control
            .call(&target.control.identity.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        let binding = RemoteBinding {
            owner: "offline-owner".into(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            environment: EnvironmentRef::host(&target.control.identity.runtime_id),
        };
        let context = caller.control.open(binding.clone()).await.unwrap();
        let workspace = PathBuf::from(context["workspace"].as_str().unwrap());
        let transport = caller.control.clone();
        let executing = binding.clone();
        let task = tokio::spawn(async move {
            transport.execute(executing, "once".into(), "exec_command".into(),
                json!({"cmd":"printf X >> once.txt; sleep 3; cat once.txt","yield_time_ms":5000})).await
        });
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while !workspace.join("once.txt").exists() {
                tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        caller.control.stop_listener(false).await.unwrap();
        tokio::time::timeout(std::time::Duration::from_secs(12), async {
            while caller.control.waiting_reason(&binding.owner).is_none() {
                tokio::time::sleep(std::time::Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        assert!(!task.is_finished());
        assert_eq!(
            std::fs::read_to_string(workspace.join("once.txt")).unwrap(),
            "X"
        );
        // Listener restart preserves the target boot and original operation receipt.
        caller
            .control
            .start_listener(caller.clone(), listener.unwrap())
            .await
            .unwrap();
        let result = tokio::time::timeout(std::time::Duration::from_secs(10), task)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        assert!(result.to_string().contains('X'));
        assert!(caller.control.waiting_reason(&binding.owner).is_none());
        assert_eq!(
            std::fs::read_to_string(workspace.join("once.txt")).unwrap(),
            "X"
        );
        caller.control.ready(binding.clone()).await.unwrap();
        // Cancel fences the old lease; re-open uses a distinct attempt after reconciliation.
        RemoteEnvironment::cancel(&*caller.control, binding.clone())
            .await
            .unwrap();
        let reopened = caller.control.open(binding.clone()).await.unwrap();
        assert_eq!(reopened["workspace"], context["workspace"]);
        assert_eq!(caller.control.client_run(&binding).unwrap().attempt, 1);
        let cached = caller
            .control
            .execute(
                binding.clone(),
                "once".into(),
                "exec_command".into(),
                json!({"cmd":"printf X >> once.txt; sleep 3; cat once.txt","yield_time_ms":5000}),
            )
            .await
            .unwrap();
        assert_eq!(cached, result);
        assert_eq!(
            std::fs::read_to_string(workspace.join("once.txt")).unwrap(),
            "X"
        );
        RemoteEnvironment::cancel(&*caller.control, binding)
            .await
            .unwrap();
        for node in [caller, target] {
            node.shutdown().await;
            std::fs::remove_dir_all(node.control.data.parent().unwrap()).unwrap();
        }
    }
    #[tokio::test]
    async fn cancellation_intent_survives_failed_transport_and_restart() {
        let path = std::env::temp_dir().join(format!("pot-client-cancel-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&path).unwrap();
        let id = uuid::Uuid::new_v4().to_string();
        let authority = Authority::open(&path, &id).unwrap();
        let binding = RemoteBinding {
            owner: "task-owner".into(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            environment: EnvironmentRef::host(&uuid::Uuid::new_v4().to_string()),
        };
        let key = run_key(&binding).unwrap();
        authority
            .update(|book| {
                book.client_runs.insert(
                    key.clone(),
                    ClientRun {
                        binding: binding.clone(),
                        lease_id: Some("original-lease".into()),
                        context: None,
                        cancelled: false,
                        cancel_requested: false,
                        attempt: 0,
                        waiting_reason: None,
                        operations: BTreeMap::new(),
                        task_launch: None,
                        task_result: None,
                        task_interrupted: false,
                        task_sent_at_ms: None,
                    },
                );
                Ok(())
            })
            .unwrap();
        assert!(
            RemoteEnvironment::cancel(&*authority, binding.clone())
                .await
                .is_err()
        );
        let run = authority.client_run(&binding).unwrap();
        assert!(run.cancel_requested);
        assert!(!run.cancelled);
        drop(authority);
        let restored = Authority::open(&path, &id).unwrap();
        assert!(restored.client_run(&binding).unwrap().cancel_requested);
        assert!(
            RemoteEnvironment::renew(&*restored, binding)
                .await
                .unwrap_err()
                .to_string()
                .contains("cancelled")
        );
        drop(restored);
        std::fs::remove_dir_all(path).unwrap();
    }
    #[test]
    fn unknown_receipts_never_become_terminal_results() {
        for state in ["accepted", "uncertain", "missing", "unknown"] {
            assert!(terminal_receipt(&json!({"state":state})).is_none());
        }
        for result in [
            Value::Null,
            json!({"ok":true}),
            json!({"ok":false}),
            json!({"ok":"true","value":1}),
        ] {
            assert!(terminal_receipt(&json!({"state":"completed","result":result})).is_none());
        }
        let rejected = terminal_receipt(&json!({"state":"not_accepted"})).unwrap();
        assert!(outcome(&rejected).is_err());
        let result = json!({"ok":true,"value":{"result":"original"}});
        assert_eq!(
            terminal_receipt(&json!({"state":"completed","result":result})),
            Some(result)
        );
    }
}
