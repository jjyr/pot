//! Real HTTPS process boundary, owner certificate and TCP-only relay qualification.
use pot_protocol::RuntimeEndpoint;
use serde_json::json;
use std::os::unix::fs::PermissionsExt;
use std::{
    path::PathBuf,
    process::{Child, Command, Stdio},
    time::Duration,
};
struct Process {
    child: Child,
    root: PathBuf,
}
impl Drop for Process {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
        let _ = std::fs::remove_dir_all(&self.root);
    }
}
#[tokio::test]
async fn tls_passthrough_certificate_validation_disable_and_reenable() {
    let root = PathBuf::from("/tmp").join(format!("pot-access-tls-{}", uuid::Uuid::new_v4()));
    std::fs::create_dir_all(&root).unwrap();
    let rcgen::CertifiedKey { cert, signing_key } =
        rcgen::generate_simple_self_signed(vec!["localhost".into()]).unwrap();
    let certificate = cert.pem();
    std::fs::write(root.join("cert.pem"), &certificate).unwrap();
    std::fs::write(root.join("key.pem"), signing_key.serialize_pem()).unwrap();
    std::fs::set_permissions(root.join("key.pem"), std::fs::Permissions::from_mode(0o600)).unwrap();
    let reservation = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let port = reservation.local_addr().unwrap().port();
    drop(reservation);
    let control_listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let control_url = format!("http://{}", control_listener.local_addr().unwrap());
    let control_task = tokio::spawn(async move {
        let router = axum::Router::new().route("/api/v1/runtime/control", axum::routing::get(|headers: axum::http::HeaderMap| async move {
            assert_eq!(headers["authorization"], "Bearer tls-control-fixture");
            axum::Json(json!({"role":"control","identity":{"runtime_id":"00000000-0000-4000-8000-000000000001"}}))
        })).route("/api/v1/runtime", axum::routing::get(|headers: axum::http::HeaderMap| async move {
            assert_eq!(headers["authorization"], "Bearer tls-control-fixture");
            axum::Json(json!({"runtime_id":"00000000-0000-4000-8000-000000000001"}))
        }));
        axum::serve(control_listener, router).await.unwrap();
    });
    let child = Command::new(env!("CARGO_BIN_EXE_pot"))
        .arg("--root")
        .arg(&root)
        .args(["serve", "--ui", "--control", &control_url])
        .env("POT_CONTROL_TOKEN", "tls-control-fixture")
        .env("POT_DATA_DIR", root.join("product"))
        .env("POT_WORKSPACE", root.join("workspace"))
        .env("POT_AGENT_HOME", root.join("agent-home"))
        .env("POT_RUNTIME_BIND", "127.0.0.1:0")
        .env("POT_ACCESS_ORIGIN", format!("https://localhost:{port}"))
        .env("POT_ACCESS_BIND", format!("127.0.0.1:{port}"))
        .env("POT_ACCESS_CERT", root.join("cert.pem"))
        .env("POT_ACCESS_KEY", root.join("key.pem"))
        .env_remove("POT_ACCESS_VAPID_KEY")
        .env_remove("POT_TAILSCALE_HOSTNAME")
        .stdout(Stdio::null())
        .stderr(Stdio::inherit())
        .spawn()
        .unwrap();
    let mut process = Process { child, root };
    let endpoint: RuntimeEndpoint = tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(bytes) = std::fs::read(process.root.join("product/runtime.json"))
                && let Ok(e) = serde_json::from_slice(&bytes)
            {
                break e;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let client = reqwest::Client::builder()
        .no_proxy()
        .add_root_certificate(reqwest::Certificate::from_pem(certificate.as_bytes()).unwrap())
        .min_tls_version(reqwest::tls::Version::TLS_1_3)
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    let origin = format!("https://localhost:{port}");
    let response = client
        .get(format!("{origin}/access/status"))
        .send()
        .await
        .unwrap();
    assert!(response.status().is_success());
    assert!(
        client
            .get(format!("https://127.0.0.1:{port}/access/status"))
            .send()
            .await
            .is_err()
    );
    // Resolver directs the *owner hostname* to a relay; the relay has no TLS key.
    let relay = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let relay_addr = relay.local_addr().unwrap();
    let forwarding = tokio::spawn(async move {
        let (mut incoming, _) = relay.accept().await.unwrap();
        let mut owner = tokio::net::TcpStream::connect(("127.0.0.1", port))
            .await
            .unwrap();
        tokio::io::copy_bidirectional(&mut incoming, &mut owner)
            .await
            .unwrap();
    });
    let via_relay = reqwest::Client::builder()
        .no_proxy()
        .resolve("localhost", relay_addr)
        .add_root_certificate(reqwest::Certificate::from_pem(certificate.as_bytes()).unwrap())
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    // reqwest uses the URL port; use the relay port with the same owner certificate.
    let status = via_relay
        .get(format!(
            "https://localhost:{}/access/status",
            relay_addr.port()
        ))
        .send()
        .await
        .unwrap()
        .json::<serde_json::Value>()
        .await
        .unwrap();
    assert_eq!(status["scope"], "owner_instance");
    drop(via_relay);
    forwarding.abort();
    let admin = format!("http://127.0.0.1:{}/api/v1/access", endpoint.port);
    let access: serde_json::Value = client
        .get(&admin)
        .bearer_auth(&endpoint.token)
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    let directory: serde_json::Value = client
        .get(format!("{origin}/api/v1/runtime"))
        .bearer_auth(access["token"].as_str().unwrap())
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(
        directory["runtime_id"],
        "00000000-0000-4000-8000-000000000001"
    );
    assert!(!process.root.join("product/pot-v2.sqlite").exists());
    let denied = client
        .get(format!("{origin}/api/v1/runtime/control"))
        .bearer_auth(access["token"].as_str().unwrap())
        .send()
        .await
        .unwrap();
    assert_eq!(denied.status(), reqwest::StatusCode::FORBIDDEN);
    for action in ["disable", "enable"] {
        assert!(
            client
                .post(&admin)
                .bearer_auth(&endpoint.token)
                .json(&json!({"action":action}))
                .send()
                .await
                .unwrap()
                .status()
                .is_success()
        );
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                let connected = tokio::net::TcpStream::connect(("127.0.0.1", port))
                    .await
                    .is_ok();
                if connected == (action == "enable") {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
    }
    assert!(
        client
            .get(format!("{origin}/access/status"))
            .send()
            .await
            .unwrap()
            .status()
            .is_success()
    );
    unsafe {
        libc::kill(process.child.id() as i32, libc::SIGTERM);
    }
    assert!(process.child.wait().unwrap().success());
    control_task.abort();
}
