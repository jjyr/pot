//! Independent Sandbox management authority and durable, non-replayed effects.
use super::*;
use pot_runtime::remote_environment::RemoteBinding;

#[derive(Clone, Serialize, Deserialize)]
pub(super) struct Intent {
    pub binding: RemoteBinding,
    pub call: String,
    pub args: Value,
    pub accept_before_ms: i64,
    pub result: Option<Value>,
}
fn management_terminal(receipt: &Value, accept_before_ms: i64) -> Option<Value> {
    // An absence observation cannot fence a still-in-flight acceptance request.
    if receipt["state"] == "not_accepted" && now() <= accept_before_ms {
        return None;
    }
    remote::terminal_receipt(receipt)
}
pub(super) struct Request {
    pub operation: String,
    pub accept_before_ms: i64,
    pub target: Target,
    pub args: Value,
}
impl Authority {
    pub(super) async fn reconcile_management(&self, owner: &str) -> Result<()> {
        let intents = self
            .book
            .lock()
            .unwrap()
            .client_management
            .iter()
            .filter(|(_, i)| i.binding.owner == owner && i.result.is_none())
            .map(|(key, i)| (key.clone(), i.clone()))
            .collect::<Vec<_>>();
        for (operation, intent) in intents {
            let receipt = self
                .call(
                    &intent.binding.environment.runtime_id,
                    Rpc::InspectManagement {
                        operation_id: operation.clone(),
                    },
                )
                .await?;
            let result =
                management_terminal(&receipt, intent.accept_before_ms).ok_or_else(|| {
                    anyhow::anyhow!("Management outcome awaits reconciliation; no replay")
                })?;
            self.update(|book| {
                book.client_management.get_mut(&operation).unwrap().result = Some(result);
                Ok(())
            })?;
        }
        Ok(())
    }
    pub(super) fn validate_management_target(&self, environment: &EnvironmentRef) -> Result<()> {
        environment.validate()?;
        ensure!(
            matches!(environment.target, Target::Sandbox { .. }),
            "Host cannot be managed as a Sandbox"
        );
        let book = self.book.lock().unwrap();
        let control = book
            .members
            .get(&environment.runtime_id)
            .filter(|p| p.enabled)
            .ok_or_else(|| anyhow::anyhow!("Remote machine is not connected"))?;
        ensure!(
            control.inventory["items"]
                .as_array()
                .is_some_and(|items| items
                    .iter()
                    .any(|item| item["environment_ref"] == json!(environment))),
            "Remote Sandbox is not in the Control machine inventory"
        );
        Ok(())
    }
    fn management_path(&self, control: &str, operation: &str) -> Result<PathBuf> {
        ensure!(
            !operation.is_empty() && operation.len() <= 256,
            "Invalid management operation identity"
        );
        Ok(self
            .data
            .join("environment-management")
            .join(digest(&(control, operation))?))
    }
    pub(super) fn inspect_management(
        &self,
        control: &str,
        operation: &str,
        boot: &str,
    ) -> Result<Value> {
        let path = self.management_path(control, operation)?;
        if !path.exists() {
            return Ok(json!({"state":"not_accepted"}));
        }
        let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
        ensure!(
            receipt.schema_version == VERSION,
            "Unsupported management receipt"
        );
        Ok(self.operation_result(&receipt, &path, boot))
    }
    /// Called under the acceptance lock, before acquiring any execution lease.
    pub(super) fn management_busy(&self, target: &Target) -> Result<()> {
        let dir = self.data.join("environment-management");
        if dir.exists() {
            for entry in std::fs::read_dir(dir)? {
                let receipt: Receipt = serde_json::from_slice(&std::fs::read({
                    let path = entry?.path();
                    if path.extension().is_some_and(|x| x == "tmp") {
                        continue;
                    }
                    path
                })?)?;
                ensure!(
                    receipt.request["target"] != json!(target) || receipt.result.is_some(),
                    "Sandbox management is active or uncertain; inspect original receipt"
                );
            }
        }
        Ok(())
    }
    fn execution_busy(&self, target: &Target, boot: &str) -> Result<()> {
        let book = self.book.lock().unwrap();
        for lease in book.leases.values().filter(|l| &l.target == target) {
            ensure!(
                !self.lease_valid(&book, lease, boot),
                "Sandbox has an active remote execution lease"
            );
            let dir = self.data.join("execution-operations");
            if dir.exists() {
                for entry in std::fs::read_dir(&dir)? {
                    let receipt: Receipt = serde_json::from_slice(&std::fs::read({
                        let path = entry?.path();
                        if path.extension().is_some_and(|x| x == "tmp") {
                            continue;
                        }
                        path
                    })?)?;
                    ensure!(
                        receipt.request["lease_id"] != lease.id || receipt.result.is_some(),
                        "Sandbox has an unsettled remote execution receipt"
                    );
                }
            }
        }
        Ok(())
    }
    pub(super) async fn accept_management(
        self: &Arc<Self>,
        host: &pot_runtime::host::Host,
        control: &str,
        request: Request,
        hub: Option<Arc<pot_runtime::assistants::Hub>>,
    ) -> Result<Value> {
        let Request {
            operation,
            accept_before_ms,
            target,
            mut args,
        } = request;
        let _guard = self.operations.lock().await;
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let Target::Sandbox { sandbox_id } = &target else {
            anyhow::bail!("Host cannot be managed as a Sandbox")
        };
        EnvironmentRef::sandbox(&self.identity.runtime_id, sandbox_id).validate()?;
        ensure!(
            args["sandbox_id"] == *sandbox_id,
            "Management target mismatch"
        );
        ensure!(
            matches!(
                args["action"].as_str(),
                Some("start" | "stop" | "recover" | "update" | "delete")
            ),
            "Unsupported remote Sandbox management action"
        );
        // The remote destination is pinned by the signed recipient; aliases are never resolved locally.
        if let Some(runtime) = args.get("runtime_id") {
            ensure!(
                runtime == &self.identity.runtime_id,
                "Management Runtime mismatch"
            );
        }
        args.as_object_mut()
            .ok_or_else(|| anyhow::anyhow!("Invalid management arguments"))?
            .remove("runtime_id");
        let path = self.management_path(control, &operation)?;
        let request = json!({"target":target,"args":args});
        if path.exists() {
            let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
            ensure!(
                receipt.schema_version == VERSION && receipt.request == request,
                "Management identity conflicts with accepted request"
            );
            return Ok(self.operation_result(&receipt, &path, &boot));
        }
        ensure!(
            accept_before_ms > now() && accept_before_ms <= now() + LEASE_MS,
            "Management acceptance window expired or invalid"
        );
        self.management_busy(&target)?;
        let session = self.control_session(control)?;
        require_target(host, &target).await?;
        // Recovery reconciles executor state, but cannot erase control uncertainty or terminate a live control lease.
        self.execution_busy(&target, &boot)?;
        let check = self.clone();
        let control = control.to_owned();
        let deadline = accept_before_ms;
        let cancelled: Arc<dyn Fn() -> bool + Send + Sync> = Arc::new(move || {
            check.stopping.load(std::sync::atomic::Ordering::SeqCst)
                || now() >= deadline
                || check.control_session(&control).ok().as_ref() != Some(&session)
        });
        ensure!(!cancelled(), "Management authority expired");
        std::fs::create_dir_all(path.parent().unwrap())?;
        let receipt = Receipt {
            schema_version: VERSION,
            request,
            boot_id: boot.clone(),
            result: None,
        };
        private_json(&path, &receipt)?;
        self.active.lock().unwrap().insert(path.clone());
        let active = ActiveOperation {
            authority: self.clone(),
            path: path.clone(),
        };
        let result = self.operation_result(&receipt, &path, &boot);
        let authority = self.clone();
        let host = host.clone();
        self.track(tokio::spawn(async move {
            let _active = active;
            let outcome = host.manage_controlled_sandbox(args, cancelled, hub).await;
            let mut receipt = receipt;
            receipt.result = Some(match outcome {
                Ok(value) => json!({"ok":true,"value":value}),
                Err(error) => json!({"ok":false,"error":error.to_string()}),
            });
            if let Err(error) = private_json(&path, &receipt) {
                eprintln!(
                    "Management receipt persistence failed; outcome remains uncertain: {error}"
                );
            }
            drop(authority);
        }));
        Ok(result)
    }
    pub(super) async fn manage_remote(
        &self,
        binding: RemoteBinding,
        call: String,
        args: Value,
        cancelled: Arc<dyn Fn() -> bool + Send + Sync>,
    ) -> Result<Value> {
        let _guard = self.management_calls.lock().await;
        ensure!(!cancelled(), "Management cancelled");
        let operation = digest(&(&binding, &call))?;
        let previous = self
            .book
            .lock()
            .unwrap()
            .client_management
            .get(&operation)
            .cloned();
        let accept_before_ms = previous
            .as_ref()
            .map(|i| i.accept_before_ms)
            .unwrap_or_else(|| now() + LEASE_MS);
        if let Some(old) = &previous {
            ensure!(
                old.binding == binding && old.call == call && old.args == args,
                "Caller management identity conflict"
            );
            if let Some(result) = &old.result {
                return remote::outcome(result);
            }
        } else {
            self.reconcile_management(&binding.owner).await?;
            self.validate_management_target(&binding.environment)?;
            ensure!(
                !call.is_empty() && call.len() <= 256,
                "Invalid management call identity"
            );
            // Do not issue another management effect for this owner/target with an unresolved intent.
            ensure!(
                !self
                    .book
                    .lock()
                    .unwrap()
                    .client_management
                    .values()
                    .any(|i| i.binding.owner == binding.owner
                        && i.binding.environment == binding.environment
                        && i.result.is_none()),
                "Previous management operation needs reconciliation"
            );
            self.update(|book| {
                book.client_management.insert(
                    operation.clone(),
                    Intent {
                        binding: binding.clone(),
                        call,
                        args: args.clone(),
                        accept_before_ms,
                        result: None,
                    },
                );
                Ok(())
            })?;
        }
        let mut receipt = if previous.is_none() {
            match self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::ManageSandbox {
                        operation_id: operation.clone(),
                        accept_before_ms,
                        target: binding.environment.target.clone(),
                        args,
                    },
                )
                .await
            {
                Ok(receipt) => Some(receipt),
                Err(error) if error.downcast_ref::<RemoteRejection>().is_some() => Some(
                    json!({"state":"completed","result":{"ok":false,"error":error.to_string()}}),
                ),
                Err(_) => None,
            }
        } else {
            None
        };
        loop {
            if let Some(result) = receipt
                .as_ref()
                .and_then(|receipt| management_terminal(receipt, accept_before_ms))
            {
                self.update(|book| {
                    book.client_management.get_mut(&operation).unwrap().result =
                        Some(result.clone());
                    Ok(())
                })?;
                return remote::outcome(&result);
            }
            ensure!(
                !cancelled() && !self.stopping.load(std::sync::atomic::Ordering::SeqCst),
                "Management cancelled; original receipt must be reconciled before another operation"
            );
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
            receipt = self
                .call(
                    &binding.environment.runtime_id,
                    Rpc::InspectManagement {
                        operation_id: operation.clone(),
                    },
                )
                .await
                .ok();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::super::tests::{native, resources};
    use super::*;
    use pot_runtime::remote_environment::RemoteEnvironment;
    #[test]
    fn absence_does_not_close_an_inflight_acceptance_window() {
        let receipt = json!({"state":"not_accepted"});
        assert!(management_terminal(&receipt, now() + LEASE_MS).is_none());
        assert!(management_terminal(&receipt, now() - 1).is_some());
        assert!(management_terminal(&json!({"state":"uncertain"}), now() - 1).is_none());
    }
    #[test]
    fn management_receipts_fence_targets_survive_restart_and_keep_control_scope() {
        let root =
            std::env::temp_dir().join(format!("pot-management-receipt-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&root).unwrap();
        let runtime = uuid::Uuid::new_v4().to_string();
        let authority = Authority::open(&root, &runtime).unwrap();
        let target = Target::Sandbox {
            sandbox_id: "managed".into(),
        };
        let path = authority.management_path("control-a", "once").unwrap();
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        let receipt = Receipt {
            schema_version: VERSION,
            request: json!({"target":target,"args":{"action":"delete","sandbox_id":"managed"}}),
            boot_id: "boot".into(),
            result: None,
        };
        private_json(&path, &receipt).unwrap();
        authority.active.lock().unwrap().insert(path.clone());
        let active = ActiveOperation {
            authority: authority.clone(),
            path: path.clone(),
        };
        assert_eq!(
            authority
                .inspect_management("control-a", "once", "boot")
                .unwrap()["state"],
            "accepted"
        );
        assert_eq!(
            authority
                .inspect_management("control-b", "once", "boot")
                .unwrap()["state"],
            "not_accepted"
        );
        assert!(authority.management_busy(&target).is_err());
        assert!(authority.management_busy(&Target::Host {}).is_ok());
        drop(active);
        drop(authority);
        let restored = Authority::open(&root, &runtime).unwrap();
        assert_eq!(
            restored
                .inspect_management("control-a", "once", "next-boot")
                .unwrap()["state"],
            "uncertain"
        );
        assert!(restored.management_busy(&target).is_err());
        let mut receipt = receipt;
        receipt.result = Some(json!({"ok":true,"value":{"sandbox_id":"managed"}}));
        private_json(&path, &receipt).unwrap();
        assert_eq!(
            restored
                .inspect_management("control-a", "once", "next-boot")
                .unwrap()["state"],
            "completed"
        );
        restored.management_busy(&target).unwrap();
        std::fs::remove_dir_all(root).unwrap();
    }
    struct InstalledFixture;
    impl pot_sandbox::Provider for InstalledFixture {
        fn descriptor(&self) -> pot_sandbox::ProviderDescriptor {
            pot_sandbox::ProviderDescriptor {
                id: if cfg!(target_os = "linux") {
                    "linux.podman"
                } else {
                    "macos.apple-containerization"
                }
                .into(),
                os: "linux".into(),
                host_os: std::env::consts::OS.into(),
                engine: "fixture".into(),
                capabilities: pot_sandbox::Capabilities {
                    shell: true,
                    files: true,
                    desktop: false,
                    shared_execution: true,
                },
            }
        }
        fn workspace(&self, root: &Path, id: &str) -> PathBuf {
            root.join("sandboxes").join(id).join("workspace")
        }
        fn guest_workspace(&self) -> &str {
            "/workspace"
        }
        fn lifecycle<'a>(
            &'a self,
            _: &'a pot_sandbox::ProviderContext,
            _: &'a str,
        ) -> futures_util::future::BoxFuture<'a, Result<Value>> {
            Box::pin(async { Ok(json!({"phase":"stopped"})) })
        }
        fn health<'a>(
            &'a self,
            _: &'a pot_sandbox::ProviderContext,
        ) -> futures_util::future::BoxFuture<'a, Result<bool>> {
            Box::pin(async { Ok(true) })
        }
        fn request<'a>(
            &'a self,
            _: &'a pot_sandbox::ProviderContext,
            _: &'a str,
            _: &'a Value,
            _: std::time::Duration,
        ) -> futures_util::future::BoxFuture<'a, Result<Value>> {
            Box::pin(async { anyhow::bail!("Fixture does not execute") })
        }
        fn validate_config(&self, _: &Value) -> Result<()> {
            Ok(())
        }
    }
    #[tokio::test]
    async fn control_management_busy_receipts_conflicts_and_restart() {
        let caller = resources().await;
        let target = resources().await;
        target
            .register_test_provider(Arc::new(InstalledFixture))
            .await;
        let response = native(&target, json!({"request_id":"sandbox","resource":{"kind":"sandbox","sandbox_id":"managed","name":"Managed","provider": if cfg!(target_os="linux") { "linux.podman" } else { "macos.apple-containerization" }}})).await;
        assert!(response.status().is_success(), "{}", response.status());
        super::super::tests::join_nodes(&caller, &target).await;
        let environment = EnvironmentRef::sandbox(&target.control.identity.runtime_id, "managed");
        let binding = RemoteBinding {
            owner: "assistant".into(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            environment: environment.clone(),
        };
        let args = json!({"action":"update","runtime_id":environment.runtime_id,"sandbox_id":"managed","name":"Renamed","config":{"cpus":2,"memory_gib":2,"disk_gib":8}});
        caller
            .control
            .call(&environment.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        let lease = caller
            .control
            .call(
                &environment.runtime_id,
                Rpc::Acquire {
                    owner: "another-owner".into(),
                    run_id: uuid::Uuid::new_v4().to_string(),
                    generation: 1,
                    target: environment.target.clone(),
                },
            )
            .await
            .unwrap();
        assert!(
            caller
                .control
                .manage(
                    binding.clone(),
                    "busy".into(),
                    args.clone(),
                    Arc::new(|| false)
                )
                .await
                .unwrap_err()
                .to_string()
                .contains("active remote execution")
        );
        caller
            .control
            .call(
                &environment.runtime_id,
                Rpc::Cancel {
                    lease_id: lease["id"].as_str().unwrap().into(),
                },
            )
            .await
            .unwrap();
        let result = caller
            .control
            .manage(
                binding.clone(),
                "rename".into(),
                args.clone(),
                Arc::new(|| false),
            )
            .await
            .unwrap();
        assert_eq!(result["sandbox_id"], "managed");
        let inventory = caller
            .control
            .call(&environment.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        assert!(inventory.to_string().contains("Renamed"));
        // Reconciliation and cached results remain available without new effects.
        assert_eq!(
            caller
                .control
                .manage(
                    binding.clone(),
                    "rename".into(),
                    args.clone(),
                    Arc::new(|| false)
                )
                .await
                .unwrap(),
            result
        );
        let mut conflicting = args.clone();
        conflicting["name"] = json!("Conflicting");
        assert!(
            caller
                .control
                .manage(
                    binding.clone(),
                    "rename".into(),
                    conflicting,
                    Arc::new(|| false)
                )
                .await
                .is_err()
        );
        for action in ["create", "select", "desktop", "set_mounts"] {
            let operation = format!("forbidden-{action}");
            assert!(
                caller
                    .control
                    .call(
                        &environment.runtime_id,
                        Rpc::ManageSandbox {
                            operation_id: operation.clone(),
                            accept_before_ms: now() + LEASE_MS,
                            target: environment.target.clone(),
                            args: json!({"action":action,"sandbox_id":"managed"})
                        }
                    )
                    .await
                    .is_err()
            );
            assert_eq!(
                caller
                    .control
                    .call(
                        &environment.runtime_id,
                        Rpc::InspectManagement {
                            operation_id: operation
                        }
                    )
                    .await
                    .unwrap()["state"],
                "not_accepted"
            );
        }
        // A journaled but unsent intent is inspected after restore, never executed.
        let call = "restart-unsent".to_owned();
        let operation = digest(&(&binding, &call)).unwrap();
        caller
            .control
            .update(|book| {
                book.client_management.insert(
                    operation.clone(),
                    Intent {
                        binding: binding.clone(),
                        call: call.clone(),
                        args: args.clone(),
                        accept_before_ms: now() - 1,
                        result: None,
                    },
                );
                Ok(())
            })
            .unwrap();
        let config = pot_runtime::RuntimeConfig {
            data: caller.control.data.clone(),
            workspace: caller.control.data.parent().unwrap().join("workspace"),
        };
        caller.shutdown().await;
        drop(caller);
        let backend = pot_runtime::initialize(config.clone()).await.unwrap();
        let caller = Resources::open(backend, config).await.unwrap();
        let restored = caller.control.clone();
        assert!(
            restored
                .manage(binding.clone(), call, args, Arc::new(|| false))
                .await
                .unwrap_err()
                .to_string()
                .contains("not accepted")
        );
        assert!(
            restored.book.lock().unwrap().client_management[&operation]
                .result
                .is_some()
        );
        caller
            .control
            .call(&environment.runtime_id, Rpc::Inventory)
            .await
            .unwrap();
        let deletion = restored.manage(binding.clone(), "delete".into(), json!({"action":"delete","sandbox_id":"managed","runtime_id":environment.runtime_id}), Arc::new(||false)).await.unwrap();
        assert_eq!(deletion["sandbox_id"], "managed");
        let response = native(&target, json!({"request_id":"reuse-retired","resource":{"kind":"sandbox","sandbox_id":"managed","name":"Replacement","provider": if cfg!(target_os="linux") { "linux.podman" } else { "macos.apple-containerization" }}})).await;
        assert!(!response.status().is_success());
        for node in [caller, target] {
            node.shutdown().await;
            std::fs::remove_dir_all(node.control.data.parent().unwrap()).unwrap();
        }
    }
}
