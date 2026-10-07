use serde_json::Value;
use std::{
    path::PathBuf,
    process::{Command, Stdio},
    time::Duration,
};
struct Process {
    child: std::process::Child,
    root: PathBuf,
}
impl Drop for Process {
    fn drop(&mut self) {
        unsafe {
            libc::kill(self.child.id() as i32, libc::SIGTERM);
        }
        let _ = self.child.wait();
        let _ = std::fs::remove_dir_all(&self.root);
    }
}
fn command(root: &std::path::Path) -> Command {
    let mut c = Command::new(env!("CARGO_BIN_EXE_pot"));
    c.arg("--root").arg(root);
    c
}
#[test]
fn cli_runs_without_app_and_uses_local_os_authority() {
    let root = std::path::PathBuf::from("/tmp").join(format!("pot-cli-{}", uuid::Uuid::new_v4()));
    assert!(
        command(&root)
            .arg("init")
            .output()
            .unwrap()
            .status
            .success()
    );
    let socket = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let port = socket.local_addr().unwrap().port();
    drop(socket);
    let child = command(&root)
        .arg("serve")
        .env("POT_RUNTIME_BIND", "127.0.0.1:0")
        .env("POT_ACCESS_BIND", format!("127.0.0.1:{port}"))
        .env("POT_ACCESS_ORIGIN", format!("http://localhost:{port}"))
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let mut process = Process { child, root };
    for _ in 0..200 {
        if process.root.join("product/control.sock").exists() {
            break;
        }
        assert!(
            process.child.try_wait().unwrap().is_none(),
            "CLI exited during startup"
        );
        std::thread::sleep(Duration::from_millis(10));
    }
    for args in [
        vec!["status"],
        vec!["tasks", "list"],
        vec!["config", "get", "providers"],
        vec!["access", "clients"],
    ] {
        let output = command(&process.root).args(args).output().unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        serde_json::from_slice::<Value>(&output.stdout).unwrap();
    }
    let output = command(&process.root)
        .args(["access", "pair"])
        .output()
        .unwrap();
    assert!(output.status.success());
    let value: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(value["code"].as_str().unwrap().len(), 14);
    assert!(
        command(&process.root)
            .args(["access", "close"])
            .output()
            .unwrap()
            .status
            .success()
    );
    assert!(
        !command(&process.root)
            .arg("serve")
            .env("POT_RUNTIME_BIND", "127.0.0.1:0")
            .env("POT_ACCESS_BIND", "127.0.0.1:0")
            .output()
            .unwrap()
            .status
            .success()
    );
}

#[test]
#[cfg(target_os = "macos")]
fn service_install_is_private_and_does_not_register_or_start_a_daemon() {
    use std::os::unix::fs::PermissionsExt;
    let root = PathBuf::from("/tmp").join(format!("pot-service-test-{}", uuid::Uuid::new_v4()));
    let home = root.join("home");
    let output = command(&root)
        .args(["service", "install"])
        .env("HOME", &home)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let definition = home.join("Library/LaunchAgents/com.jjyr.pot.server.plist");
    let text = std::fs::read_to_string(&definition).unwrap();
    assert!(text.contains("--root"));
    assert!(text.contains(&root.display().to_string()));
    assert_eq!(
        std::fs::metadata(definition).unwrap().permissions().mode() & 0o777,
        0o600
    );
    assert_eq!(
        std::fs::metadata(root.join("server.log"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o600
    );
    assert!(!root.join("product/control.sock").exists());
    assert!(
        !command(&root)
            .args(["service", "install"])
            .env("HOME", &home)
            .output()
            .unwrap()
            .status
            .success()
    );
    std::fs::remove_dir_all(root).unwrap();
}
