//! HTTP Web listeners and external HTTPS origins keep browser and Control authority separate.
use axum::{Json, Router, http::HeaderMap, routing::get};
use serde_json::{Value, json};
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
async fn http_listener_authenticates_with_http_and_external_https_origins() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let control_url = format!("http://{}", listener.local_addr().unwrap());
    let control = tokio::spawn(async move {
        let routes = Router::new()
            .route("/api/v1/runtime/control", get(|headers: HeaderMap| async move {
                assert_eq!(headers["authorization"], "Bearer http-control-fixture");
                Json(json!({"role":"control","identity":{"runtime_id":"00000000-0000-4000-8000-000000000001"}}))
            }))
            .route("/api/v1/runtime", get(|headers: HeaderMap| async move {
                assert_eq!(headers["authorization"], "Bearer http-control-fixture");
                assert!(!headers.contains_key("cookie"));
                assert!(!headers.contains_key("origin"));
                Json(json!({"source":"configured-control"}))
            }));
        axum::serve(listener, routes).await.unwrap();
    });
    let client = reqwest::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    for public_origin in ["http://100.64.0.20:17701", "https://pot.example.com"] {
        let root = PathBuf::from("/tmp").join(format!("pot-access-http-{}", uuid::Uuid::new_v4()));
        let reservation = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let port = reservation.local_addr().unwrap().port();
        drop(reservation);
        let child = Command::new(env!("CARGO_BIN_EXE_pot"))
            .arg("--root")
            .arg(&root)
            .args(["serve", "--ui", "--control", &control_url])
            .env("POT_CONTROL_TOKEN", "http-control-fixture")
            .env("POT_DATA_DIR", root.join("product"))
            .env("POT_WORKSPACE", root.join("workspace"))
            .env("POT_AGENT_HOME", root.join("agent-home"))
            .env("POT_RUNTIME_BIND", "127.0.0.1:0")
            .env("POT_ACCESS_ORIGIN", public_origin)
            .env("POT_ACCESS_BIND", format!("0.0.0.0:{port}"))
            .env_remove("POT_ACCESS_CERT")
            .env_remove("POT_ACCESS_KEY")
            .env_remove("POT_ACCESS_VAPID_KEY")
            .env_remove("POT_TAILSCALE_HOSTNAME")
            .stdout(Stdio::null())
            .stderr(Stdio::inherit())
            .spawn()
            .unwrap();
        let mut process = Process { child, root };
        let base = format!("http://127.0.0.1:{port}");
        tokio::time::timeout(Duration::from_secs(15), async {
            loop {
                assert!(
                    process.child.try_wait().unwrap().is_none(),
                    "Web process exited during startup"
                );
                if let Ok(response) = client.get(format!("{base}/access/status")).send().await
                    && response.status().is_success()
                {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        #[cfg(feature = "web-ui")]
        {
            let response = client.get(format!("{base}/auth.js")).send().await.unwrap();
            assert!(response.status().is_success());
            assert!(
                response
                    .text()
                    .await
                    .unwrap()
                    .contains("body.request_id=requestUUID();")
            );
        }
        let credentials: Value = serde_json::from_slice(
            &std::fs::read(process.root.join("product/access-token.json")).unwrap(),
        )
        .unwrap();
        let token = credentials["token"].as_str().unwrap();
        assert_eq!(
            client
                .get(format!("{base}/api/v1/runtime"))
                .send()
                .await
                .unwrap()
                .status(),
            401
        );
        assert_eq!(
            client
                .post(format!("{base}/access/token"))
                .header("origin", "http://other.example")
                .bearer_auth(token)
                .json(&json!({}))
                .send()
                .await
                .unwrap()
                .status(),
            403
        );
        let response = client
            .post(format!("{base}/access/token"))
            .header("origin", public_origin)
            .bearer_auth(token)
            .json(&json!({}))
            .send()
            .await
            .unwrap();
        assert!(response.status().is_success());
        let cookie = response.headers()["set-cookie"]
            .to_str()
            .unwrap()
            .to_owned();
        let secure = public_origin.starts_with("https://");
        assert_eq!(cookie.contains("; Secure"), secure);
        assert_eq!(cookie.starts_with("__Host-pot-session="), secure);
        assert!(cookie.contains("; HttpOnly; SameSite=Strict"));
        let response = client
            .get(format!("{base}/api/v1/runtime"))
            .header("cookie", cookie.split(';').next().unwrap())
            .header("origin", public_origin)
            .send()
            .await
            .unwrap();
        assert!(response.status().is_success());
        assert_eq!(
            response.json::<Value>().await.unwrap()["source"],
            "configured-control"
        );
        // Proxy headers cannot override the configured browser origin or TLS cookie policy.
        assert_eq!(
            client
                .post(format!("{base}/access/token"))
                .header("origin", "http://other.example")
                .header("x-forwarded-proto", "https")
                .header("x-forwarded-host", "other.example")
                .bearer_auth(token)
                .json(&json!({}))
                .send()
                .await
                .unwrap()
                .status(),
            403
        );
        drop(process);
    }
    control.abort();
}
