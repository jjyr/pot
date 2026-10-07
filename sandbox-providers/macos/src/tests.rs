use super::*;
use std::os::unix::fs::{PermissionsExt, symlink};
fn fixture() -> tempfile::TempDir {
    let dir = tempfile::tempdir().unwrap();
    std::fs::set_permissions(dir.path(), std::fs::Permissions::from_mode(0o700)).unwrap();
    for name in [
        "hardware-model",
        "machine-identifier",
        "auxiliary-storage",
        "disk.img",
        "guest-ca.pem",
        "executor-token",
    ] {
        std::fs::write(dir.path().join(name), "fixture").unwrap();
        std::fs::set_permissions(
            dir.path().join(name),
            std::fs::Permissions::from_mode(0o600),
        )
        .unwrap();
    }
    pot_sandbox::private_json(&dir.path().join("manifest.json"),&json!({"schema_version":1,"provider":PROVIDER_ID,"guest_build":"test","xcode_version":"test","bootstrap":"pot-macos-executor-v3","guest_ip":"192.168.64.3","mac_address":"02:00:00:00:00:01"})).unwrap();
    dir
}
#[test]
fn enrollment_requires_complete_private_platform_and_never_overwrites() {
    let source = fixture();
    assert!(Manifest::load(source.path()).is_ok());
    let manifest_path = source.path().join("manifest.json");
    let original: Value = serde_json::from_slice(&std::fs::read(&manifest_path).unwrap()).unwrap();
    let mut old = original.clone();
    old["bootstrap"] = json!("pot-macos-executor-v2");
    pot_sandbox::private_json(&manifest_path, &old).unwrap();
    assert!(
        Manifest::load(source.path())
            .err()
            .unwrap()
            .to_string()
            .contains("Update the macOS guest bootstrap")
    );
    pot_sandbox::private_json(&manifest_path, &original).unwrap();
    let root = tempfile::tempdir().unwrap();
    enroll(root.path(), "mac-dev", source.path()).unwrap();
    assert!(enroll(root.path(), "mac-dev", source.path()).is_err());
    assert!(enroll(root.path(), "../escape", source.path()).is_err());
    std::fs::set_permissions(
        source.path().join("executor-token"),
        std::fs::Permissions::from_mode(0o644),
    )
    .unwrap();
    assert!(Manifest::load(source.path()).is_err());
    std::fs::remove_file(source.path().join("executor-token")).unwrap();
    symlink(
        source.path().join("disk.img"),
        source.path().join("executor-token"),
    )
    .unwrap();
    assert!(Manifest::load(source.path()).is_err());
}
#[tokio::test]
async fn missing_optional_helper_does_not_start_or_fake_execution_cleanup() {
    let root = tempfile::tempdir().unwrap();
    let provider = MacOSProvider::new(root.path().join("absent"));
    let context = ProviderContext {
        root: root.path().into(),
        sandbox_id: "mac".into(),
    };
    assert_eq!(
        provider.lifecycle(&context, "status").await.unwrap()["phase"],
        "unavailable"
    );
    assert!(provider.lifecycle(&context, "start").await.is_err());
    assert!(!provider.descriptor().capabilities.shared_execution);
    assert!(provider.descriptor().capabilities.desktop);
    assert!(provider.prepare_desktop(&context).await.is_err());
    assert!(
        provider
            .request(&context, "desktop", &json!({}), Duration::from_secs(1))
            .await
            .is_err()
    );
    let directory = MacOSProvider::directory(&context);
    std::fs::create_dir_all(&directory).unwrap();
    std::fs::write(directory.join("macos-helper.sock"), "stale").unwrap();
    assert!(
        provider
            .request(
                &context,
                "finish",
                &json!({"id":"scope"}),
                Duration::from_secs(1)
            )
            .await
            .is_err()
    );
}
#[tokio::test]
async fn helper_response_must_match_protocol_and_request_identity() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("socket");
    let listener = tokio::net::UnixListener::bind(&path).unwrap();
    let task = tokio::spawn(async move {
        let (mut stream, _) = listener.accept().await.unwrap();
        let mut line = String::new();
        BufReader::new(&mut stream)
            .read_line(&mut line)
            .await
            .unwrap();
        stream
            .write_all(b"{\"version\":1,\"id\":\"wrong\",\"result\":{\"phase\":\"stopped\"}}\n")
            .await
            .unwrap();
    });
    assert!(MacOSProvider::rpc(&path, "stop").await.is_err());
    task.await.unwrap();
}

#[cfg(all(target_os = "macos", target_arch = "aarch64"))]
#[tokio::test]
async fn persistent_vm_is_reused_and_explicit_shutdown_stops_owned_helper() {
    let source = fixture();
    let root = tempfile::tempdir().unwrap();
    enroll(root.path(), "mac", source.path()).unwrap();
    let script = root.path().join("fake-helper");
    std::fs::write(&script, r#"#!/usr/bin/env python3
import json, socket, sys
from pathlib import Path
root=Path(sys.argv[1]); s=socket.socket(socket.AF_UNIX); s.bind(str(root/'macos-helper.sock')); s.listen()
phase='stopped'
while True:
 c,_=s.accept(); request=json.loads(c.makefile().readline()); method=request['method']
 if method=='start': phase='running'
 if method=='stop':
  phase='stopped'
  with (root/'stops').open('a') as f: f.write('confirmed\n')
 c.sendall((json.dumps({'version':1,'id':request['id'],'result':{'phase':phase,'generation':1}})+'\n').encode()); c.close()
"#).unwrap();
    std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o700)).unwrap();
    let provider = MacOSProvider::new(script);
    let context = ProviderContext {
        root: root.path().into(),
        sandbox_id: "mac".into(),
    };
    assert_eq!(
        provider.lifecycle(&context, "start").await.unwrap()["phase"],
        "running"
    );
    // Reusing the running VM does not reset platform state or invoke stop.
    assert_eq!(
        provider.lifecycle(&context, "start").await.unwrap()["phase"],
        "running"
    );
    assert!(!MacOSProvider::directory(&context).join("stops").exists());
    assert!(matches!(
        provider.scope_cleanup_policy(),
        pot_sandbox::ScopeCleanupPolicy::SessionClosure
    ));
    assert!(Manifest::load(&MacOSProvider::directory(&context).join("macos")).is_ok());
    provider.lifecycle(&context, "start").await.unwrap();
    provider.shutdown(root.path()).await.unwrap();
    assert!(
        !MacOSProvider::directory(&context)
            .join("macos-helper.sock")
            .exists()
    );
}
