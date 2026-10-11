//! Durable launch and same-process lease recovery for destination-local workers.
use super::*;
use pot_runner::{Journal, Launch, Phase, RunServices};
impl Authority {
    fn journal(&self, host: &pot_control::host::Host) -> Result<Arc<Journal>> {
        let mut journal = self.task_journal.lock().unwrap();
        if let Some(journal) = &*journal {
            return Ok(journal.clone());
        }
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let value = Arc::new(Journal::open(&self.data, &self.identity.runtime_id, &boot)?);
        *journal = Some(value.clone());
        Ok(value)
    }
    pub(super) async fn inspect_task(
        self: &Arc<Self>,
        host: &pot_control::host::Host,
        control: &str,
        task: &str,
        run: &str,
    ) -> Result<Value> {
        let journal = self.journal(host)?;
        if let Some(record) = journal.inspect(control, task, run)?
            && record.phase == Phase::RecoveryRequired
        {
            let leases = self
                .book
                .lock()
                .unwrap()
                .leases
                .values()
                .filter(|l| l.control_id == control && l.owner == task && l.run_id == run)
                .cloned()
                .collect::<Vec<_>>();
            let mut recovered = BTreeMap::new();
            let directory = self.data.join("execution-operations");
            if directory.exists() {
                for path in task_services::receipt_files(&directory)? {
                    let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
                    if leases.iter().any(|l| receipt.request["lease_id"] == l.id) {
                        let Some(result) = receipt.result else {
                            return Ok(
                                json!({"phase":"recovery_required","reason":"unknown_tool_outcome"}),
                            );
                        };
                        if let Some(call) = receipt.request["call_id"]
                            .as_str()
                            .and_then(|id| id.strip_prefix(&format!("{run}:")))
                        {
                            let value = match remote::outcome(&result) {
                                Ok(value) => value["result"].clone(),
                                Err(error) => json!({"isError":true,"error":error.to_string()}),
                            };
                            if let Some(old) = recovered.insert(call.to_owned(), value.clone()) {
                                ensure!(old == value, "Conflicting recovered task tool result");
                            }
                        }
                    }
                }
            }
            let lease = leases
                .first()
                .ok_or_else(|| anyhow::anyhow!("Previous boot lease missing"))?;
            let port = task_services::Port {
                authority: self.clone(),
                host: host.clone(),
                binding: pot_control::remote_environment::RemoteBinding {
                    owner: task.into(),
                    run_id: run.into(),
                    generation: record.launch.generation,
                    environment: record.launch.environment.clone(),
                },
                control: control.into(),
                lease: lease.id.clone(),
                policy: Mutex::new(None),
                cancelled: std::sync::atomic::AtomicBool::new(false),
            };
            let directory = self.data.join("task-service-intents");
            if directory.exists() {
                for path in task_services::receipt_files(&directory)? {
                    let mut intent: Value = serde_json::from_slice(&std::fs::read(&path)?)?;
                    if intent["input"]["binding"] == json!(port.binding)
                        && intent["result"].is_null()
                    {
                        let id = intent["id"]
                            .as_str()
                            .ok_or_else(|| anyhow::anyhow!("Original service identity missing"))?;
                        let receipt = port.send(id, None, false).await?;
                        let Some(result) = remote::terminal_receipt(&receipt) else {
                            return Ok(
                                json!({"phase":"recovery_required","reason":"unknown_service_outcome"}),
                            );
                        };
                        intent["result"] = result;
                        private_json(&path, &intent)?;
                    }
                }
            }
            for lease in leases {
                host.cancel_controlled(&scope(&lease)?, &lease.target)
                    .await?;
            }
            journal.reconcile_recovered_context(control, task, run, &recovered)?;
            journal.interrupt_recovered(control, task, run)?;
        }
        Ok(match journal.inspect(control, task, run)? {
            Some(record) => {
                json!({"phase":record.phase,"result":record.result,"boot_id":record.boot_id})
            }
            None => json!({"phase":"not_accepted"}),
        })
    }
    pub(super) async fn launch_task(
        self: &Arc<Self>,
        host: &pot_control::host::Host,
        control: &str,
        lease_id: String,
        launch: Launch,
    ) -> Result<Value> {
        let _guard = self.operations.lock().await;
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let lease = self.lease(control, &lease_id, &boot, false)?;
        launch.validate(&self.identity.runtime_id)?;
        ensure!(
            lease.owner == launch.task_id
                && lease.run_id == launch.run_id
                && lease.generation == launch.generation
                && lease.target == launch.environment.target
                && lease.resources_revision.as_ref() == Some(&launch.resources.revision()?),
            "Launch differs from fixed lease"
        );
        let journal = self.journal(host)?;
        if journal
            .inspect(control, &launch.task_id, &launch.run_id)?
            .is_none()
        {
            self.lease(control, &lease_id, &boot, true)?;
            require_target(host, &lease.target).await?;
            self.lease(control, &lease_id, &boot, true)?;
        }
        let record = journal.accept(control, launch.clone())?;
        if record.phase == Phase::Accepted {
            let port = Arc::new(task_services::Port {
                authority: self.clone(),
                host: host.clone(),
                binding: pot_control::remote_environment::RemoteBinding {
                    owner: launch.task_id.clone(),
                    run_id: launch.run_id.clone(),
                    generation: launch.generation,
                    environment: launch.environment,
                },
                control: control.into(),
                lease: lease_id,
                policy: Mutex::new(None),
                cancelled: std::sync::atomic::AtomicBool::new(false),
            });
            let host = host.clone();
            let control = control.to_owned();
            self.track(tokio::spawn(async move {
                if let Err(error) = pot_runner::execute(
                    Arc::new(host.clone()),
                    journal,
                    &control,
                    &launch.task_id,
                    &launch.run_id,
                    RunServices {
                        model: port.clone(),
                        control: port.clone(),
                        tools: port,
                    },
                )
                .await
                {
                    eprintln!("Task Agent remains unresolved: {error}");
                }
            }));
        }
        Ok(json!({"phase":record.phase,"result":record.result,"boot_id":record.boot_id}))
    }
    pub(super) async fn recover_task_lease(
        &self,
        host: &pot_control::host::Host,
        control: &str,
        id: &str,
    ) -> Result<Value> {
        let _guard = self.operations.lock().await;
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let old = self.lease(control, id, &boot, false)?;
        ensure!(
            old.boot_id == boot,
            "Execution Runtime restarted; explicit continuation required"
        );
        if let Some(next) = &old.replacement {
            return Ok(json!(self.lease(control, next, &boot, false)?));
        }
        ensure!(!old.cancelled, "Cancelled lease cannot recover");
        ensure!(
            old.expires_at_ms <= now(),
            "Original lease is still live; renew it instead"
        );
        let record = self
            .journal(host)?
            .inspect(control, &old.owner, &old.run_id)?
            .ok_or_else(|| anyhow::anyhow!("Task Agent run missing"))?;
        ensure!(
            record.phase == Phase::Running
                && record.boot_id == boot
                && record.launch.generation == old.generation
                && record.launch.environment.target == old.target
                && old.resources_revision.as_ref() == Some(&record.launch.resources.revision()?),
            "Original Task Agent/configuration is no longer live"
        );
        let directory = self.data.join("execution-operations");
        if directory.exists() {
            for path in task_services::receipt_files(&directory)? {
                let receipt: Receipt = serde_json::from_slice(&std::fs::read(&path)?)?;
                if receipt.request["lease_id"] == id {
                    ensure!(
                        receipt.result.is_some(),
                        "Tool outcome needs reconciliation before lease recovery"
                    );
                }
            }
        }
        let directory = self.data.join("task-service-intents");
        if directory.exists() {
            for path in task_services::receipt_files(&directory)? {
                let intent: Value = serde_json::from_slice(&std::fs::read(&path)?)?;
                if intent["input"]["binding"]["owner"] == old.owner
                    && intent["input"]["binding"]["run_id"] == old.run_id
                {
                    ensure!(
                        !intent["result"].is_null(),
                        "Service reply must be reconciled before lease recovery"
                    );
                }
            }
        }
        self.update(|book| {
            let old = book.leases.get(id).unwrap().clone();
            let binding = book
                .control
                .as_ref()
                .filter(|b| {
                    b.identity.runtime_id == control
                        && digest(&b.token).ok().as_ref() == Some(&old.session)
                })
                .ok_or_else(|| anyhow::anyhow!("Control session changed"))?;
            let _ = binding;
            ensure!(
                !old.cancelled && old.replacement.is_none(),
                "Lease changed during recovery"
            );
            let mut next = old.clone();
            next.id = secret()?;
            next.expires_at_ms = now() + LEASE_MS;
            next.replacement = None;
            let previous = book.leases.get_mut(id).unwrap();
            previous.cancelled = true;
            previous.replacement = Some(next.id.clone());
            book.leases.insert(next.id.clone(), next.clone());
            Ok(json!(next))
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn recovery_requires_a_live_same_boot_run_and_settled_receipts() {
        use super::super::tests::{join_nodes, native, resources};
        let control = resources().await;
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
        join_nodes(&control, &target).await;
        let host = target.host().await;
        let boot = host.describe()["boot_id"].as_str().unwrap().to_owned();
        let control_id = &control.control.identity.runtime_id;
        let config = pot_core::Profile::api(&pot_control::execution::Config {
            model: "fixture".into(),
            ..Default::default()
        })
        .resolve()
        .unwrap();
        let vision = config
            .params
            .agent
            .model_descriptor(&config.provider, &config.model)
            .unwrap()
            .vision();
        let launch = Launch {
            task_id: uuid::Uuid::new_v4().to_string(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            environment: EnvironmentRef::host(&target.control.identity.runtime_id),
            resources: pot_runner::resources::GlobalResources {
                plugins: config.params.agent.plugins.clone(),
                shell: config.params.agent.shell.clone(),
                vision,
                ..Default::default()
            },
            config,
            skills: None,
            text: "fixture".into(),
        };
        let value = target
            .control
            .acquire(
                control_id,
                &launch.task_id,
                &launch.run_id,
                1,
                &launch.environment.target,
                &boot,
            )
            .unwrap();
        let id = value["id"].as_str().unwrap();
        let journal = target.control.journal(&host).unwrap();
        journal.accept(control_id, launch.clone()).unwrap();
        journal
            .claim(control_id, &launch.task_id, &launch.run_id)
            .unwrap();
        target
            .control
            .update(|book| {
                let lease = book.leases.get_mut(id).unwrap();
                lease.resources_revision = Some(launch.resources.revision()?);
                lease.expires_at_ms = now() - 1;
                Ok(())
            })
            .unwrap();
        let lease = target.control.lease(control_id, id, &boot, false).unwrap();
        let path = target.control.receipt_path(&lease, "effect").unwrap();
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        let mut receipt = Receipt {
            schema_version: VERSION,
            request: json!({"lease_id":id,"call_id":"effect"}),
            boot_id: boot.clone(),
            result: None,
        };
        private_json(&path, &receipt).unwrap();
        assert!(
            target
                .control
                .recover_task_lease(&host, control_id, id)
                .await
                .unwrap_err()
                .to_string()
                .contains("Tool outcome")
        );
        receipt.result = Some(json!({"ok":true,"value":{"result":"once"}}));
        private_json(&path, &receipt).unwrap();
        let next = target
            .control
            .recover_task_lease(&host, control_id, id)
            .await
            .unwrap();
        let next_id = next["id"].as_str().unwrap();
        assert_ne!(id, next_id);
        assert_eq!(next["run_id"], launch.run_id);
        assert_eq!(
            next,
            target
                .control
                .recover_task_lease(&host, control_id, id)
                .await
                .unwrap()
        );
        assert!(target.control.lease(control_id, id, &boot, true).is_err());
        assert!(
            target
                .control
                .lease(control_id, next_id, &boot, true)
                .is_ok()
        );
        assert!(
            target
                .control
                .lease(control_id, next_id, &uuid::Uuid::new_v4().to_string(), true)
                .is_err()
        );
        assert_eq!(
            journal
                .inspect(control_id, &launch.task_id, &launch.run_id)
                .unwrap()
                .unwrap()
                .phase,
            Phase::Running
        );
        for node in [control, target] {
            node.shutdown().await;
            std::fs::remove_dir_all(node.control.data.parent().unwrap()).unwrap();
        }
    }

    #[tokio::test]
    async fn reboot_reconciles_known_tool_results_without_restarting_or_replaying() {
        use super::super::tests::{join_nodes, native, resources};
        let control = resources().await;
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
        join_nodes(&control, &target).await;
        let host = target.host().await;
        let boot = uuid::Uuid::new_v4().to_string();
        let control_id = &control.control.identity.runtime_id;
        let config = pot_core::Profile::api(&pot_control::execution::Config {
            model: "fixture".into(),
            ..Default::default()
        })
        .resolve()
        .unwrap();
        let vision = config
            .params
            .agent
            .model_descriptor(&config.provider, &config.model)
            .unwrap()
            .vision();
        let launch = Launch {
            task_id: uuid::Uuid::new_v4().to_string(),
            run_id: uuid::Uuid::new_v4().to_string(),
            generation: 1,
            environment: EnvironmentRef::host(&target.control.identity.runtime_id),
            resources: pot_runner::resources::GlobalResources {
                plugins: config.params.agent.plugins.clone(),
                shell: config.params.agent.shell.clone(),
                vision,
                ..Default::default()
            },
            config,
            skills: None,
            text: "fixture".into(),
        };

        let value = target
            .control
            .acquire(
                control_id,
                &launch.task_id,
                &launch.run_id,
                1,
                &launch.environment.target,
                &boot,
            )
            .unwrap();
        let id = value["id"].as_str().unwrap();
        let old = Journal::open(
            &target.control.data,
            &target.control.identity.runtime_id,
            &boot,
        )
        .unwrap();
        old.accept(control_id, launch.clone()).unwrap();
        assert!(
            old.claim(control_id, &launch.task_id, &launch.run_id)
                .unwrap()
        );
        let calls = json!([
            {"id":"known","name":"exec_command","arguments":{"cmd":"printf X >> once"}},
            {"id":"unknown","name":"exec_command","arguments":{"cmd":"other"}}
        ]);
        old.save_context(
            control_id,
            &launch.task_id,
            &launch.run_id,
            json!({
                "type":"context", "version":1,
                "messages":[{"role":"assistant","text":"","calls":calls}],
                "pending":calls, "started":"known", "last_request":"original"
            }),
        )
        .unwrap();
        drop(old);
        let lease = target.control.lease(control_id, id, &boot, false).unwrap();
        let path = target.control.receipt_path(&lease, "known").unwrap();
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        let mut receipt = Receipt {
            schema_version: VERSION,
            request: json!({"lease_id":id,"call_id":format!("{}:known",launch.run_id)}),
            boot_id: boot,
            result: None,
        };
        private_json(&path, &receipt).unwrap();
        assert_eq!(
            target
                .control
                .inspect_task(&host, control_id, &launch.task_id, &launch.run_id)
                .await
                .unwrap()["phase"],
            "recovery_required"
        );
        receipt.result = Some(json!({"ok":true,"value":{"result":{"stdout":"X","exit_code":0}}}));
        private_json(&path, &receipt).unwrap();
        let result = target
            .control
            .inspect_task(&host, control_id, &launch.task_id, &launch.run_id)
            .await
            .unwrap();
        assert_eq!(result["phase"], "interrupted");
        let journal = target.control.journal(&host).unwrap();
        let record = journal
            .inspect(control_id, &launch.task_id, &launch.run_id)
            .unwrap()
            .unwrap();
        let context = record.context.unwrap();
        assert_eq!(context["pending"], json!([calls[1]]));
        assert_eq!(context["messages"][1]["call_id"], "known");
        assert_eq!(
            serde_json::from_str::<Value>(context["messages"][1]["text"].as_str().unwrap())
                .unwrap(),
            json!({"stdout":"X","exit_code":0})
        );
        assert_eq!(context["last_request"], "original");
        assert!(
            !journal
                .claim(control_id, &launch.task_id, &launch.run_id)
                .unwrap()
        );
        assert_eq!(
            target
                .control
                .inspect_task(&host, control_id, &launch.task_id, &launch.run_id)
                .await
                .unwrap(),
            result
        );
        assert_eq!(
            journal
                .inspect(control_id, &launch.task_id, &launch.run_id)
                .unwrap()
                .unwrap()
                .context
                .unwrap(),
            context
        );
        for node in [control, target] {
            node.shutdown().await;
            std::fs::remove_dir_all(node.control.data.parent().unwrap()).unwrap();
        }
    }
}
