//! Run-bound execution-node ports; no local provider or approval fallback.
use super::*;

pub(in crate::control) struct Port {
    pub authority: Arc<Authority>,
    pub host: pot_control::host::Host,
    pub binding: RemoteBinding,
    pub control: String,
    pub lease: String,
    pub cancelled: std::sync::atomic::AtomicBool,
}
impl Port {
    pub fn current_lease(&self) -> Result<Lease> {
        let boot = self.host.describe()["boot_id"].as_str().unwrap().to_owned();
        let mut lease = self
            .authority
            .lease(&self.control, &self.lease, &boot, false)?;
        let mut seen = std::collections::BTreeSet::new();
        while let Some(next) = &lease.replacement {
            ensure!(seen.insert(lease.id.clone()), "Lease replacement cycle");
            lease = self.authority.lease(&self.control, next, &boot, false)?;
        }
        Ok(lease)
    }
    pub(in crate::control) async fn send(
        &self,
        id: &str,
        operation: Option<Operation>,
        query: bool,
    ) -> Result<Value> {
        let lease = self.current_lease()?;
        let binding = self
            .authority
            .book
            .lock()
            .unwrap()
            .control
            .clone()
            .filter(|b| b.identity.runtime_id == self.control)
            .ok_or_else(|| anyhow::anyhow!("Control binding lost"))?;
        let request = self.authority.sign(Request {
            caller: self.authority.identity.runtime_id.clone(),
            recipient: self.control.clone(),
            issued_at_ms: now(),
            accept_before_ms: lease.expires_at_ms,
            nonce: secret()?,
            binding: self.binding.clone(),
            lease_id: lease.id,
            id: id.into(),
            operation,
            query,
        })?;
        let expected = protocol_digest(&request)?;
        let (client, url) = client(&binding.url, binding.ca_pem.as_deref())?;
        let response = client
            .post(url.join("control/v1/task-service")?)
            .bearer_auth(binding.token)
            .json(&request)
            .send()
            .await?;
        let status = response.status();
        let mut stream = response.bytes_stream();
        let mut bytes = Vec::new();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk?;
            ensure!(
                bytes.len() + chunk.len() <= 24 * 1024 * 1024,
                "Task service response exceeds limit"
            );
            bytes.extend_from_slice(&chunk);
        }
        if !status.is_success() {
            let message = serde_json::from_slice::<Value>(&bytes)
                .ok()
                .and_then(|value| value["error"].as_str().map(str::to_owned))
                .unwrap_or_else(|| format!("HTTP {status}"));
            anyhow::bail!("Control service rejected request: {message}");
        }
        let reply: Signed<Value> = serde_json::from_slice(&bytes)?;
        binding.identity.verify(&reply.payload, &reply.signature)?;
        ensure!(
            reply.payload["digest"] == expected,
            "Service reply identity mismatch"
        );
        Ok(reply.payload["value"].clone())
    }
    pub async fn effect(&self, id: &str, operation: Operation) -> Result<Value> {
        // Destination intent survives reply loss and process restart. Never resend an
        // effect whose acceptance is uncertain, even if Control says not accepted.
        let path = self
            .authority
            .data
            .join("task-service-intents")
            .join(digest(&(&self.control, &self.binding, id))?);
        let value = intent_value(&self.binding, &operation)?;
        let fresh = {
            let _guard = self.authority.operations.lock().await;
            if path.exists() {
                ensure!(
                    serde_json::from_slice::<Value>(&std::fs::read(&path)?)?["input"] == value,
                    "Task service intent conflict"
                );
                false
            } else {
                std::fs::create_dir_all(path.parent().unwrap())?;
                private_json(
                    &path,
                    &json!({"input":value,"result":null,"sent_at_ms":now(),"id":id}),
                )?;
                true
            }
        };
        let sent_at_ms = serde_json::from_slice::<Value>(&std::fs::read(&path)?)?["sent_at_ms"]
            .as_i64()
            .ok_or_else(|| anyhow::anyhow!("Service acceptance deadline missing"))?;
        let mut receipt = if fresh {
            self.send(id, Some(operation), false).await.ok()
        } else {
            None
        };
        loop {
            if let Some(result) = receipt.as_ref().and_then(remote::terminal_receipt) {
                private_json(
                    &path,
                    &json!({"input":value,"result":result,"sent_at_ms":sent_at_ms,"id":id}),
                )?;
                return remote::outcome(&result);
            }
            if receipt
                .as_ref()
                .is_some_and(|v| v["state"] == "not_accepted")
                && now() - sent_at_ms > 30_000
            {
                anyhow::bail!(
                    "Control did not accept original service operation; no replay performed"
                );
            }
            ensure!(!self.is_cancelled(), "Task Agent cancelled");
            tokio::time::sleep(std::time::Duration::from_millis(200)).await;
            receipt = self.send(id, None, false).await.ok();
        }
    }
    pub fn is_cancelled(&self) -> bool {
        self.cancelled.load(std::sync::atomic::Ordering::SeqCst)
            || self
                .authority
                .stopping
                .load(std::sync::atomic::Ordering::SeqCst)
            || self.current_lease().map_or(true, |l| {
                l.cancelled
                    || self
                        .authority
                        .book
                        .lock()
                        .unwrap()
                        .control
                        .as_ref()
                        .is_none_or(|c| {
                            c.identity.runtime_id != self.control
                                || digest(&c.token).ok().as_ref() != Some(&l.session)
                        })
            })
    }
}
impl pot_core::ExecutionFeedback for Port {
    fn owner_authorization_available(&self) -> Result<bool> {
        Ok(true)
    }
    fn resolve_execution_operation(
        &self,
        _name: &str,
        mut args: Value,
    ) -> futures_util::future::BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            if let Some(value) = args.get("execution_target") {
                let expected = json!(self.binding.execution_target);
                let remote = json!(format!(
                    "remote:{}",
                    serde_json::to_string(&self.binding.execution_target)?
                ));
                ensure!(
                    value == &expected || value == &remote,
                    "Worker cannot switch its machine target"
                );
            }
            ensure!(args.get("sandbox_id").is_none(), "Worker target is fixed");
            args["execution_target"] = json!(format!(
                "remote:{}",
                serde_json::to_string(&self.binding.execution_target)?
            ));
            Ok(args)
        })
    }
    fn prepare_execution_operation(
        &self,
        name: &str,
        args: Value,
    ) -> futures_util::future::BoxFuture<'_, Result<Value>> {
        let name = name.to_owned();
        Box::pin(async move {
            self.ready().await?;
            let lease = self.current_lease()?;
            let mut args = args;
            let object = args
                .as_object_mut()
                .ok_or_else(|| anyhow::anyhow!("Tool arguments must be an object"))?;
            object.remove("execution_target");
            object.remove("sandbox_id");
            let mut args = self
                .authority
                .dispatch_with_hub(
                    &self.host,
                    &self.control,
                    Rpc::Prepare {
                        lease_id: lease.id,
                        name,
                        args,
                    },
                    None,
                )
                .await?;
            args["execution_target"] = json!(format!(
                "remote:{}",
                serde_json::to_string(&self.binding.execution_target)?
            ));
            Ok(args)
        })
    }
    fn request_permission(
        &self,
        request: Value,
    ) -> futures_util::future::BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            let original = request.clone();
            let mut request = request;
            request["execution_target"] = json!(format!(
                "remote:{}",
                serde_json::to_string(&self.binding.execution_target)?
            ));
            let mut result = self
                .effect(
                    &format!("permission:{}", digest(&request)?),
                    Operation::Permission { request },
                )
                .await?;
            result["request"] = original;
            Ok(result)
        })
    }
    fn fetch_steering(
        &self,
        finalizing: bool,
    ) -> futures_util::future::BoxFuture<'_, Result<Vec<(String, String)>>> {
        Box::pin(async move {
            self.ready().await?;
            Ok(serde_json::from_value(
                self.effect(
                    &format!("steering:{}", secret()?),
                    Operation::Steering { finalizing },
                )
                .await?,
            )?)
        })
    }
    fn commit_steering<'a>(
        &'a self,
        ids: &'a [String],
    ) -> futures_util::future::BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            self.effect(
                &format!("ack:{}", digest(&ids)?),
                Operation::Acknowledge { ids: ids.to_vec() },
            )
            .await?;
            Ok(())
        })
    }
    fn ready(&self) -> futures_util::future::BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            loop {
                ensure!(!self.is_cancelled(), "Task Agent cancelled");
                let lease = self.current_lease()?;
                let boot = self.host.describe()["boot_id"].as_str().unwrap().to_owned();
                if self
                    .authority
                    .lease(&self.control, &lease.id, &boot, true)
                    .is_ok()
                    && let Ok(value) = self.send("ready", Some(Operation::Ready), true).await
                {
                    if value["cancelled"] == true {
                        self.cancelled
                            .store(true, std::sync::atomic::Ordering::SeqCst);
                        anyhow::bail!("Task Agent cancelled");
                    }
                    if value["ready"] == true {
                        return Ok(());
                    }
                }
                tokio::time::sleep(std::time::Duration::from_millis(250)).await;
            }
        })
    }
    fn settle(&self) -> futures_util::future::BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            loop {
                let mut settled = true;
                let leases = self
                    .authority
                    .book
                    .lock()
                    .unwrap()
                    .leases
                    .values()
                    .filter(|l| {
                        l.control_id == self.control
                            && l.owner == self.binding.owner
                            && l.run_id == self.binding.run_id
                    })
                    .cloned()
                    .collect::<Vec<_>>();
                let directory = self.authority.data.join("execution-operations");
                if directory.exists() {
                    for path in receipt_files(&directory)? {
                        let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
                        if leases.iter().any(|l| receipt.request["lease_id"] == l.id)
                            && receipt.result.is_none()
                        {
                            settled = false;
                        }
                    }
                }
                let directory = self.authority.data.join("task-service-intents");
                if directory.exists() {
                    for path in receipt_files(&directory)? {
                        let mut intent: Value = serde_json::from_slice(&std::fs::read(&path)?)?;
                        if intent["input"]["binding"] != json!(self.binding)
                            || !intent["result"].is_null()
                        {
                            continue;
                        }
                        let id = intent["id"]
                            .as_str()
                            .ok_or_else(|| anyhow::anyhow!("Original service identity missing"))?;
                        match self.send(id, None, false).await {
                            Ok(receipt)
                                if receipt["state"] != "not_accepted"
                                    || intent["sent_at_ms"]
                                        .as_i64()
                                        .is_some_and(|t| now() - t > 30_000) =>
                            {
                                if let Some(result) = remote::terminal_receipt(&receipt) {
                                    intent["result"] = result;
                                    private_json(&path, &intent)?;
                                } else {
                                    settled = false;
                                }
                            }
                            _ => settled = false,
                        }
                    }
                }
                if settled {
                    return Ok(());
                }
                // Cancellation does not turn unknown effects into terminal outcomes.
                tokio::time::sleep(std::time::Duration::from_millis(200)).await;
            }
        })
    }
    fn cancelled(&self) -> bool {
        self.is_cancelled()
    }
    fn cancellation(&self) -> futures_util::future::BoxFuture<'_, ()> {
        Box::pin(async move {
            while !self.is_cancelled() {
                tokio::time::sleep(std::time::Duration::from_millis(100)).await;
            }
        })
    }
    fn record_turn(&self, request: &str) -> Result<()> {
        ensure!(request == self.binding.run_id, "Task turn mismatch");
        Ok(())
    }
    fn input_streaming(&self, request: &str) -> Result<()> {
        self.record_turn(request)
    }
}
impl pot_core::ExecutionServices for Port {
    fn model<'a>(
        &'a self,
        request: pot_core::ModelRequest,
    ) -> futures_util::future::BoxFuture<
        'a,
        Result<futures_util::stream::BoxStream<'a, Result<Vec<u8>>>>,
    > {
        Box::pin(async move {
            use pot_core::ExecutionFeedback;
            self.ready().await?;
            let mut attempt = 0u64;
            let body = loop {
                self.ready().await?;
                let value = self
                    .effect(
                        &format!("model:{}:{attempt}", request.id),
                        Operation::Model {
                            request: request.clone(),
                        },
                    )
                    .await?;
                let body = B64.decode(
                    value["body"]
                        .as_str()
                        .ok_or_else(|| anyhow::anyhow!("Missing model response"))?,
                )?;
                if value["status"] == 429
                    && serde_json::from_slice::<Value>(&body)
                        .ok()
                        .is_some_and(|v| v["code"] == "pot_token_limit")
                {
                    attempt = attempt
                        .checked_add(1)
                        .ok_or_else(|| anyhow::anyhow!("Model allowance attempts exhausted"))?;
                    continue;
                }
                ensure!(
                    value["status"]
                        .as_u64()
                        .is_some_and(|s| (200..300).contains(&s)),
                    "Control model refused request: {}",
                    String::from_utf8_lossy(&body)
                );
                break body;
            };
            Ok(
                Box::pin(futures_util::stream::once(async move { Ok(body) }))
                    as futures_util::stream::BoxStream<'a, Result<Vec<u8>>>,
            )
        })
    }
    fn rpc<'a>(
        &'a self,
        _: Option<&'a str>,
        _: &'a str,
        _: &'a str,
        _: Value,
    ) -> futures_util::future::BoxFuture<'a, Result<(Value, Option<String>)>> {
        Box::pin(async { anyhow::bail!("Task Agent product RPC unavailable") })
    }
    fn compaction(&self, event: Value) -> futures_util::future::BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            self.effect(
                &format!("compaction:{}", digest(&event)?),
                Operation::Compaction { event },
            )
            .await?;
            Ok(())
        })
    }
}
impl pot_core::ToolExecutor for Port {
    fn execute<'a>(
        &'a self,
        context: &'a pot_core::ExecutionContext,
        _: &'a pot_core::ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        _: &'a mut (dyn FnMut(pot_core::Update) -> Result<()> + Send),
    ) -> futures_util::future::BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            use pot_core::ExecutionFeedback;
            ensure!(
                context.owner == self.binding.owner
                    && context.role == pot_core::ExecutionRole::Worker,
                "Task execution owner mismatch"
            );
            self.ready().await?;
            let path = self.authority.data.join("task-tool-intents").join(digest(&(
                &self.control,
                &self.binding,
                call,
            ))?);
            let input = json!({"name":name,"args":args});
            let (lease, fresh) = {
                let _guard = self.authority.operations.lock().await;
                if path.exists() {
                    let old: Value = serde_json::from_slice(&std::fs::read(&path)?)?;
                    ensure!(old["input"] == input, "Task tool intent conflict");
                    (
                        old["lease"]
                            .as_str()
                            .ok_or_else(|| anyhow::anyhow!("Task tool lease missing"))?
                            .to_owned(),
                        false,
                    )
                } else {
                    let lease = self.current_lease()?.id;
                    std::fs::create_dir_all(path.parent().unwrap())?;
                    private_json(&path, &json!({"lease":lease,"input":input}))?;
                    (lease, true)
                }
            };
            let mut receipt = if fresh {
                self.authority
                    .dispatch_with_hub(
                        &self.host,
                        &self.control,
                        Rpc::Execute {
                            lease_id: lease.clone(),
                            call_id: call.into(),
                            name: name.into(),
                            args: {
                                let mut args = args.clone();
                                args.as_object_mut().unwrap().remove("execution_target");
                                args.as_object_mut().unwrap().remove("sandbox_id");
                                args
                            },
                        },
                        None,
                    )
                    .await?
            } else {
                self.authority
                    .dispatch_with_hub(
                        &self.host,
                        &self.control,
                        Rpc::Inspect {
                            lease_id: lease.clone(),
                            call_id: call.into(),
                        },
                        None,
                    )
                    .await?
            };
            loop {
                if let Some(result) = remote::terminal_receipt(&receipt) {
                    let value = remote::outcome(&result)?;
                    return Ok(value["result"].clone());
                }
                ensure!(
                    !self.is_cancelled(),
                    "Task cancelled during tool receipt reconciliation"
                );
                tokio::time::sleep(std::time::Duration::from_millis(100)).await;
                receipt = self
                    .authority
                    .dispatch_with_hub(
                        &self.host,
                        &self.control,
                        Rpc::Inspect {
                            lease_id: lease.clone(),
                            call_id: call.into(),
                        },
                        None,
                    )
                    .await?;
            }
        })
    }
    fn finish<'a>(
        &'a self,
        _: &'a pot_core::ExecutionContext,
    ) -> futures_util::future::BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            let lease = self.current_lease()?;
            self.host
                .cancel_controlled(&scope(&lease)?, &lease.target)
                .await
        })
    }
}

// Resource reads are run-bound queries, not durable effect receipts containing bundle history.
impl pot_runner::ResourceProvider for Port {
    fn synchronize<'a>(
        &'a self,
        launch: &'a pot_runner::Launch,
    ) -> futures_util::future::BoxFuture<'a, Result<pot_runner::resources::GlobalResources>> {
        Box::pin(async move {
            let value = self
                .send(
                    "resources",
                    Some(Operation::Resources {
                        skills: launch.skills.clone().unwrap_or_default(),
                        plugins: launch.plugins.clone(),
                    }),
                    true,
                )
                .await?;
            let resources: pot_runner::resources::GlobalResources = serde_json::from_value(value)?;
            resources.validate()?;
            let revision = resources.revision()?;
            let lease = self.current_lease()?;
            let boot = self.host.describe()["boot_id"].as_str().unwrap().to_owned();
            self.authority
                .lease(&self.control, &lease.id, &boot, true)?;
            self.authority.update(|book| {
                let accepted = book
                    .leases
                    .get_mut(&lease.id)
                    .ok_or_else(|| anyhow::anyhow!("Lease missing"))?;
                ensure!(
                    accepted
                        .resources_revision
                        .as_ref()
                        .is_none_or(|old| old == &revision),
                    "Resources changed during an accepted run"
                );
                accepted.resources_revision = Some(revision);
                Ok(())
            })?;
            Ok(resources)
        })
    }
}
