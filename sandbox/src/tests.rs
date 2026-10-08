use crate::*;
use anyhow::{Result, bail};
use futures::future::BoxFuture;
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::Duration,
};
struct Fake {
    browser: AtomicBool,
    desktop: AtomicBool,
    generation: AtomicUsize,
    running: AtomicBool,
    exclusive: AtomicBool,
    session_only: AtomicBool,
    fail_cleanup: AtomicBool,
    dispatches: AtomicUsize,
}
impl Fake {
    fn new() -> Self {
        Self {
            browser: AtomicBool::new(true),
            desktop: AtomicBool::new(false),
            generation: AtomicUsize::new(1),
            running: AtomicBool::new(false),
            exclusive: AtomicBool::new(false),
            session_only: AtomicBool::new(false),
            fail_cleanup: AtomicBool::new(false),
            dispatches: AtomicUsize::new(0),
        }
    }
}
impl Provider for Fake {
    fn scope_cleanup_policy(&self) -> ScopeCleanupPolicy {
        if self.session_only.load(Ordering::SeqCst) {
            ScopeCleanupPolicy::SessionClosure
        } else {
            ScopeCleanupPolicy::ProcessContainment
        }
    }
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: MACOS_APPLE_CONTAINER_PROVIDER.into(),
            os: "linux".into(),
            host_os: "macos".into(),
            engine: "apple-containerization".into(),
            capabilities: Capabilities {
                shell: true,
                files: true,
                desktop: self.desktop.load(Ordering::SeqCst),
                shared_execution: !self.exclusive.load(Ordering::SeqCst),
            },
        }
    }
    fn desktop_capabilities(&self) -> DesktopCapabilities {
        let available = self.desktop.load(Ordering::SeqCst);
        DesktopCapabilities {
            viewing: available,
            input: available,
            clipboard: false,
            browser: false,
            protocol: available.then_some(DesktopProtocol::Rfb38),
        }
    }
    fn prepare_desktop<'a>(&'a self, _: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            anyhow::ensure!(self.desktop.load(Ordering::SeqCst), "Unsupported desktop");
            Ok(())
        })
    }
    fn open_desktop<'a>(&'a self, _: &'a ProviderContext) -> BoxFuture<'a, Result<DesktopStream>> {
        Box::pin(async {
            let (stream, _) = tokio::io::duplex(1024);
            Ok(Box::pin(stream) as DesktopStream)
        })
    }
    fn workspace(&self, root: &Path, id: &str) -> PathBuf {
        root.join("sandboxes").join(id).join("workspace/workspace")
    }
    fn guest_workspace(&self) -> &str {
        "/data/workspace"
    }
    fn lifecycle<'a>(
        &'a self,
        _: &'a ProviderContext,
        method: &'a str,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            match method {
                "start" => {
                    self.running.store(true, Ordering::SeqCst);
                }
                "stop" => {
                    self.running.store(false, Ordering::SeqCst);
                }
                "status" => {}
                _ => bail!("unsupported"),
            };
            Ok(
                json!({"phase":if self.running.load(Ordering::SeqCst){"running"}else{"stopped"},"generation":self.generation.load(Ordering::SeqCst)}),
            )
        })
    }
    fn health<'a>(&'a self, _: &'a ProviderContext) -> BoxFuture<'a, Result<bool>> {
        Box::pin(async { Ok(true) })
    }
    fn request<'a>(
        &'a self,
        _: &'a ProviderContext,
        route: &'a str,
        _: &'a Value,
        _: Duration,
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            if route == "capabilities" {
                Ok(
                    json!({"protocol_version":1,"plugins":{"protocol_version":1,"runtime":if self.browser.load(Ordering::SeqCst) {"node"} else {"unavailable"},"requirements":["node","chromium"]}}),
                )
            } else if route == "execute" {
                self.dispatches.fetch_add(1, Ordering::SeqCst);
                Ok(json!({"result":{"session_id":"session","stdout":"guest"},"events":[]}))
            } else {
                if self.fail_cleanup.load(Ordering::SeqCst) {
                    bail!("disconnected")
                };
                Ok(json!({"settled":!self.session_only.load(Ordering::SeqCst),"scope_closed":true}))
            }
        })
    }
    fn validate_config(&self, c: &Value) -> Result<()> {
        anyhow::ensure!(
            c["cpus"].as_u64().unwrap_or(0) > 0,
            "Invalid Sandbox resources"
        );
        Ok(())
    }
}
fn grant(owner: &str, worker: bool) -> AccessGrant {
    AccessGrant {
        owner: owner.into(),
        turn: "turn".into(),
        epoch: 1,
        target: "default".into(),
        worker,
        policy: Default::default(),
    }
}
fn fixture() -> (tempfile::TempDir, Arc<Manager>, Arc<Fake>) {
    let dir = tempfile::tempdir().unwrap();
    let provider = Arc::new(Fake::new());
    let manager =
        Arc::new(Manager::open(&dir.path().join("product"), vec![provider.clone()]).unwrap());
    (dir, manager, provider)
}
#[tokio::test]
async fn receipts_are_manager_owned_deduplicated_and_unknown_legacy_calls_never_replay() {
    let (_dir, m, p) = fixture();
    let args = json!({"cmd":"echo guest"});
    let g = grant("main", false);
    let a = m
        .execute(&g, "exec_command", "call", &args, &|| false, None)
        .await
        .unwrap();
    let b = m
        .execute(&g, "exec_command", "call", &args, &|| false, None)
        .await
        .unwrap();
    assert_eq!(a, b);
    assert_eq!(p.dispatches.load(Ordering::SeqCst), 1);
    assert!(
        m.execute(
            &g,
            "exec_command",
            "call",
            &json!({"cmd":"different"}),
            &|| false,
            None
        )
        .await
        .is_err()
    );
    let old = json!({"name":"exec_command","args":args,"status":"accepted"});
    assert!(
        m.execute(&g, "exec_command", "legacy", &args, &|| false, Some(old))
            .await
            .unwrap_err()
            .to_string()
            .contains("unknown")
    );
    assert_eq!(p.dispatches.load(Ordering::SeqCst), 1);
    m.finish("main").await.unwrap();
}
#[tokio::test]
async fn sessions_are_fixed_to_owner_epoch_and_target() {
    let (_dir, m, _) = fixture();
    m.execute(
        &grant("main", false),
        "exec_command",
        "call",
        &json!({"cmd":"true"}),
        &|| false,
        None,
    )
    .await
    .unwrap();
    let err = m
        .execute(
            &grant("worker", true),
            "write_stdin",
            "foreign",
            &json!({"session_id":"session"}),
            &|| false,
            None,
        )
        .await
        .unwrap_err();
    assert!(err.to_string().contains("another task/agent epoch"));
    let err = m
        .execute(
            &grant("worker", true),
            "exec_command",
            "switch",
            &json!({"cmd":"true","sandbox_id":"other"}),
            &|| false,
            None,
        )
        .await
        .unwrap_err();
    assert!(err.to_string().contains("cannot switch"));
    m.finish("main").await.unwrap();
    assert!(
        m.execute(
            &grant("main", false),
            "write_stdin",
            "expired",
            &json!({"session_id":"session"}),
            &|| false,
            None
        )
        .await
        .unwrap_err()
        .to_string()
        .contains("expired")
    );
}
#[tokio::test]
async fn browser_sessions_cannot_retarget_or_cross_owner_and_boot() {
    let (_dir, m, p) = fixture();
    let owner = grant("main", false);
    let open = plugin_args(json!({"code":r#"let tab = await cua.open("https://example.com");"#}));
    m.execute(&owner, "plugin_call", "open", &open, &|| false, None)
        .await
        .unwrap();
    // Completed operation IDs replay the receipt rather than a second navigation.
    m.execute(&owner, "plugin_call", "open", &open, &|| false, None)
        .await
        .unwrap();
    assert_eq!(p.dispatches.load(Ordering::SeqCst), 1);
    assert_eq!(m.session_target("main", 1, "session").unwrap(), "default");
    assert!(m.session_target("worker", 1, "session").is_err());
    assert!(m.session_target("main", 2, "session").is_err());
    let observe = plugin_args(json!({"code":"await tab.observe();","session_id":"session"}));
    assert!(
        m.execute(
            &grant("worker", true),
            "plugin_call",
            "foreign",
            &observe,
            &|| false,
            None
        )
        .await
        .unwrap_err()
        .to_string()
        .contains("another task")
    );
    let mut same_target = observe.clone();
    same_target["sandbox_id"] = json!("default");
    m.execute(
        &owner,
        "plugin_call",
        "same-target",
        &same_target,
        &|| false,
        None,
    )
    .await
    .unwrap();
    let mut retarget = observe.clone();
    retarget["sandbox_id"] = json!("other");
    assert!(
        m.execute(
            &owner,
            "plugin_call",
            "retarget",
            &retarget,
            &|| false,
            None
        )
        .await
        .unwrap_err()
        .to_string()
        .contains("cannot switch")
    );
    p.generation.store(2, Ordering::SeqCst);
    assert!(
        m.execute(&owner, "plugin_call", "boot", &observe, &|| false, None)
            .await
            .unwrap_err()
            .to_string()
            .contains("generation changed")
    );
    m.finish("main").await.unwrap();
    assert!(m.session_target("main", 1, "session").is_err());
    assert!(
        m.execute(&owner, "plugin_call", "expired", &observe, &|| false, None)
            .await
            .unwrap_err()
            .to_string()
            .contains("expired")
    );
}
#[tokio::test]
async fn browser_unavailable_guest_is_rejected_before_action_dispatch() {
    let (_dir, m, p) = fixture();
    p.browser.store(false, Ordering::SeqCst);
    assert!(
        m.execute(
            &grant("main", false),
            "plugin_call",
            "unsupported",
            &plugin_args(json!({"code":r#"await cua.open("https://example.com");"#})),
            &|| false,
            None
        )
        .await
        .unwrap_err()
        .to_string()
        .contains("Plugin runtime unavailable")
    );
    assert_eq!(p.dispatches.load(Ordering::SeqCst), 0);
}
#[tokio::test]
async fn unknown_cleanup_quarantines_persistently_and_busy_stop_cannot_delete_state() {
    let (dir, m, p) = fixture();
    m.execute(
        &grant("main", false),
        "exec_command",
        "call",
        &json!({"cmd":"true"}),
        &|| false,
        None,
    )
    .await
    .unwrap();
    assert!(
        m.management(&json!({"action":"stop"}), false, &|| false, &|_, f| f())
            .await
            .unwrap_err()
            .to_string()
            .contains("busy")
    );
    p.fail_cleanup.store(true, Ordering::SeqCst);
    assert!(m.finish("main").await.is_err());
    assert!(m.acquire("default").await.is_err());
    let reopened = Arc::new(Manager::open(&dir.path().join("product"), vec![p.clone()]).unwrap());
    assert!(reopened.acquire("default").await.is_err());
    p.running.store(false, Ordering::SeqCst);
    reopened
        .management(&json!({"action":"recover"}), false, &|| false, &|_, f| f())
        .await
        .unwrap();
    assert!(reopened.acquire("default").await.is_ok());
}
#[tokio::test]
async fn provider_registration_migration_and_failed_inventory_write_are_safe() {
    let (dir, m, p) = fixture();
    assert!(Manager::open(&dir.path().join("duplicate"), vec![p.clone(), p.clone()]).is_err());
    assert!(
        m.management(
            &json!({"action":"create","sandbox_id":"missing","provider":"unknown"}),
            false,
            &|| false,
            &|_, f| f()
        )
        .await
        .is_err()
    );
    assert!(
        m.management(
            &json!({"action":"create","sandbox_id":"worker"}),
            true,
            &|| false,
            &|_, f| f()
        )
        .await
        .is_err()
    );
    std::fs::create_dir(dir.path().join("product/sandboxes.json")).unwrap();
    assert!(
        m.management(
            &json!({"action":"create","sandbox_id":"unsaved"}),
            false,
            &|| false,
            &|_, f| f()
        )
        .await
        .is_err()
    );
    assert!(m.assignment("unsaved").is_err());
    std::fs::remove_dir(dir.path().join("product/sandboxes.json")).unwrap();
    private_json(&dir.path().join("product/sandboxes.json"),&json!({"selected":"default","items":{"default":{"id":"default","name":"Linux","provider":MACOS_APPLE_CONTAINER_PROVIDER}},"retired":[]})).unwrap();
    let migrated = Manager::open(&dir.path().join("product"), vec![p]).unwrap();
    assert_eq!(migrated.display_name("default").unwrap(), "Linux");
    assert!(migrated.display_name("missing").is_err());
    assert_eq!(
        migrated.describe("default").unwrap().id,
        MACOS_APPLE_CONTAINER_PROVIDER
    );
}

#[tokio::test]
async fn concurrent_duplicate_calls_dispatch_once_and_restart_advances_epoch() {
    let (dir, manager, provider) = fixture();
    let g = grant("main", false);
    let args = json!({"cmd":"true"});
    let (first, second) = tokio::join!(
        manager.execute(&g, "exec_command", "same", &args, &|| false, None),
        manager.execute(&g, "exec_command", "same", &args, &|| false, None)
    );
    assert_eq!(first.unwrap(), second.unwrap());
    assert_eq!(provider.dispatches.load(Ordering::SeqCst), 1);
    manager.finish("main").await.unwrap();
    let path = dir.path().join("product/sandbox-generations.json");
    let before: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    drop(manager);
    let _reopened = Manager::open(&dir.path().join("product"), vec![provider]).unwrap();
    let after: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    assert!(after["default"][0].as_u64().unwrap() > before["default"][0].as_u64().unwrap());
}

#[tokio::test]
async fn exclusive_provider_rejects_another_owner_until_confirmed_closure() {
    let (_directory, manager, provider) = fixture();
    provider.exclusive.store(true, Ordering::SeqCst);
    let args = json!({"cmd":"true"});
    manager
        .execute(
            &grant("first", false),
            "exec_command",
            "one",
            &args,
            &|| false,
            None,
        )
        .await
        .unwrap();
    assert!(
        manager
            .execute(
                &grant("second", false),
                "exec_command",
                "two",
                &args,
                &|| false,
                None
            )
            .await
            .unwrap_err()
            .to_string()
            .contains("exclusive")
    );
    assert_eq!(provider.dispatches.load(Ordering::SeqCst), 1);
    manager.finish("first").await.unwrap();
    manager
        .execute(
            &grant("second", false),
            "exec_command",
            "two",
            &args,
            &|| false,
            None,
        )
        .await
        .unwrap();
    assert_eq!(provider.dispatches.load(Ordering::SeqCst), 2);
    manager.finish("second").await.unwrap();
}

#[test]
fn session_closure_is_not_process_containment() {
    let response = json!({"scope_closed":true,"settled":false,"process_containment":false});
    assert!(ScopeCleanupPolicy::SessionClosure.confirmed(&response));
    assert!(!ScopeCleanupPolicy::ProcessContainment.confirmed(&response));
    assert!(!ScopeCleanupPolicy::SessionClosure.confirmed(&json!({"settled":true})));
}

#[tokio::test]
async fn persistent_scope_closure_releases_ownership_without_stopping_vm() {
    let (_dir, manager, provider) = fixture();
    provider.exclusive.store(true, Ordering::SeqCst);
    provider.session_only.store(true, Ordering::SeqCst);
    manager
        .execute(
            &grant("main", false),
            "exec_command",
            "call",
            &json!({"cmd":"true"}),
            &|| false,
            None,
        )
        .await
        .unwrap();
    manager.finish("main").await.unwrap();
    assert!(provider.running.load(Ordering::SeqCst));
    assert!(manager.acquire("default").await.is_ok());
}

#[tokio::test]
async fn desktop_tickets_are_exclusive_expiring_and_do_not_stop_vm() {
    let (_dir, m, p) = fixture();
    // This fake is command-only: absent adapter must reject display authority.
    assert!(m.open_desktop("default").await.is_err());
    assert!(!p.running.load(Ordering::SeqCst));
    assert!(m.desktop_has_control("invented").is_err());
}

#[tokio::test]
async fn desktop_ownership_release_expiry_and_vm_generation() {
    let (_dir, m, p) = fixture();
    p.desktop.store(true, Ordering::SeqCst);
    let a = m.open_desktop("default").await.unwrap();
    let b = m.open_desktop("default").await.unwrap();
    assert!(!m.desktop_has_control(&a.id).unwrap());
    assert!(m.desktop_control(&a.id, true).await.is_err());
    let _first = m.desktop_stream(&a.id).await.unwrap();
    let _second = m.desktop_stream(&b.id).await.unwrap();
    assert!(m.desktop_stream(&a.id).await.is_err());
    m.desktop_control(&a.id, true).await.unwrap();
    assert!(m.desktop_control(&b.id, true).await.is_err());
    let release = {
        let m = m.clone();
        let id = a.id.clone();
        tokio::spawn(async move { m.desktop_control(&id, false).await })
    };
    tokio::time::sleep(Duration::from_millis(30)).await;
    assert!(m.desktop_control(&b.id, true).await.is_err());
    m.desktop_released(&a.id, m.desktop_revision(&a.id).unwrap());
    release.await.unwrap().unwrap();
    m.desktop_control(&b.id, true).await.unwrap();
    m.close_desktop(&b.id);
    assert!(p.running.load(Ordering::SeqCst));
    p.generation.store(2, Ordering::SeqCst);
    assert!(m.desktop_validate(&a.id).await.is_err());
    m.viewers.lock().unwrap().get_mut(&a.id).unwrap().expires = std::time::Instant::now();
    assert!(m.desktop_session(&a.id).is_err());
    let c = m.open_desktop("default").await.unwrap();
    m.management(
        &json!({"action":"stop","sandbox_id":"default"}),
        false,
        &|| false,
        &|_, f| f(),
    )
    .await
    .unwrap();
    assert!(m.desktop_session(&c.id).is_err());
    assert!(!p.running.load(Ordering::SeqCst));
}

#[tokio::test]
async fn disconnected_viewer_does_not_confirm_input_return() {
    let (_dir, m, p) = fixture();
    p.desktop.store(true, Ordering::SeqCst);
    let a = m.open_desktop("default").await.unwrap();
    let _stream = m.desktop_stream(&a.id).await.unwrap();
    m.desktop_control(&a.id, true).await.unwrap();
    let release = {
        let m = m.clone();
        let id = a.id.clone();
        tokio::spawn(async move { m.desktop_control(&id, false).await })
    };
    tokio::time::sleep(Duration::from_millis(30)).await;
    m.desktop_disconnected(&a.id);
    assert!(release.await.unwrap().is_err());
    assert!(p.running.load(Ordering::SeqCst));
}

#[tokio::test]
async fn approved_mappings_are_persisted_preserved_on_resource_update_and_refused_when_busy() {
    let (dir, manager, provider) = fixture();
    let source = dir.path().canonicalize().unwrap();
    let mappings = json!([{"source":source,"destination":"/mnt/host/project","read_only":true}]);
    let operation = json!({"action":"set_mounts","sandbox_id":"default","mappings":mappings});
    manager
        .management(&operation, false, &|| false, &|_, f| f())
        .await
        .unwrap();
    let path = dir.path().join("sandboxes/default/sandbox.json");
    let saved: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    assert_eq!(saved["mappings"], mappings);
    manager.management(&json!({"action":"update","sandbox_id":"default","name":"Linux","config":{"cpus":2,"memory_gib":2,"disk_gib":8}}),false,&||false,&|_,f|f()).await.unwrap();
    let saved: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    assert_eq!(saved["mappings"], mappings);
    provider.running.store(true, Ordering::SeqCst);
    assert!(
        manager
            .management(&operation, false, &|| false, &|_, f| f())
            .await
            .is_err()
    );
    provider.running.store(false, Ordering::SeqCst);
    let lease = manager.acquire("default").await.unwrap();
    assert!(
        manager
            .management(&operation, false, &|| false, &|_, f| f())
            .await
            .is_err()
    );
    drop(lease);
    manager
        .management(
            &json!({"action":"set_mounts","sandbox_id":"default","mappings":[]}),
            false,
            &|| false,
            &|_, f| f(),
        )
        .await
        .unwrap();
    assert!(
        host_mappings(&serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap())
            .unwrap()
            .is_empty()
    );
}

fn plugin_args(input: Value) -> Value {
    pot_core::plugins::call(
        pot_core::plugins::PluginBundle::builtin("computer-use").unwrap(),
        "cua_repl",
        input,
        false,
    )
    .unwrap()
}
