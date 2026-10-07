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
    running: AtomicBool,
    fail_cleanup: AtomicBool,
    dispatches: AtomicUsize,
}
impl Fake {
    fn new() -> Self {
        Self {
            running: AtomicBool::new(false),
            fail_cleanup: AtomicBool::new(false),
            dispatches: AtomicUsize::new(0),
        }
    }
}
impl Provider for Fake {
    fn descriptor(&self) -> ProviderDescriptor {
        ProviderDescriptor {
            id: LINUX_PROVIDER.into(),
            os: "linux".into(),
            capabilities: Capabilities {
                shell: true,
                files: true,
                desktop: false,
                shared_execution: true,
            },
        }
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
                json!({"phase":if self.running.load(Ordering::SeqCst){"running"}else{"stopped"},"generation":1}),
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
            if route == "execute" {
                self.dispatches.fetch_add(1, Ordering::SeqCst);
                Ok(json!({"result":{"session_id":"session","stdout":"guest"},"events":[]}))
            } else {
                if self.fail_cleanup.load(Ordering::SeqCst) {
                    bail!("disconnected")
                };
                Ok(json!({"settled":true}))
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
async fn sessions_are_fixed_to_owner_turn_and_target() {
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
    assert!(err.to_string().contains("another task/turn"));
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
    private_json(&dir.path().join("product/sandboxes.json"),&json!({"selected":"default","items":{"default":{"id":"default","name":"Linux"}},"retired":[]})).unwrap();
    let migrated = Manager::open(&dir.path().join("product"), vec![p]).unwrap();
    assert_eq!(migrated.describe("default").unwrap().id, LINUX_PROVIDER);
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
