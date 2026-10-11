//! Owner-created display tickets; the browser receives no Provider credentials.
use super::*;
use axum::{
    extract::{
        Path, WebSocketUpgrade,
        ws::{Message as WsMessage, WebSocket},
    },
    response::{IntoResponse, Response},
};
use futures::SinkExt;
use std::time::Duration;
/// Attached only by the authenticated browser boundary, never by HTTP headers.
#[derive(Clone)]
pub struct DesktopAccessGuard(pub std::sync::Arc<dyn Fn() -> bool + Send + Sync>);
use tokio::io::{AsyncReadExt, AsyncWriteExt};

pub async fn open(
    State(s): State<Service>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if s.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "desktop",
            "Owner access required",
        ));
    }
    let id = s
        .environment_service
        .sandboxes
        .assignment(args["sandbox_id"].as_str().unwrap_or(""))
        .map_err(internal)?;
    if let Some(hub) = &s.environment_service.sandboxes.resources
        && let Some(key) = hub
            .inspect(&s.environment_service.sandboxes, &id, &s.usage_scope, None)
            .await
            .map_err(internal)?
    {
        return Ok(Json(
            json!({"resource_request_id":key,"phase":"awaiting-resources"}),
        ));
    }
    let session = s
        .environment_service
        .sandboxes
        .open_desktop(args["sandbox_id"].as_str().unwrap_or(""))
        .await
        .map_err(internal)?;
    Ok(Json(
        json!({"session":session,"path":format!("/desktop/{}/",session.id)}),
    ))
}
pub async fn close(
    State(s): State<Service>,
    h: HeaderMap,
    Json(args): Json<Value>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if s.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "desktop",
            "Owner access required",
        ));
    }
    let _guard = s
        .environment_service
        .sandboxes
        .desktop_serialization()
        .await;
    s.environment_service
        .sandboxes
        .close_desktop(args["session_id"].as_str().unwrap_or(""));
    Ok(Json(json!({"closed":true})))
}
fn origin(h: &HeaderMap) -> ApiResult<()> {
    if let Some(value) = h.get("origin") {
        let host = h.get("host").and_then(|v| v.to_str().ok()).unwrap_or("");
        if value.to_str().ok() != Some(&format!("http://{host}"))
            && value.to_str().ok() != Some(&format!("https://{host}"))
        {
            return Err(error(
                StatusCode::FORBIDDEN,
                "desktop_origin",
                "Same-origin desktop access required",
            ));
        }
    }
    Ok(())
}
fn ticket(s: &Service, h: &HeaderMap, id: &str) -> ApiResult<pot_sandbox::DesktopSession> {
    origin(h)?;
    s.environment_service
        .sandboxes
        .desktop_session(id)
        .map_err(|_| {
            error(
                StatusCode::FORBIDDEN,
                "desktop_session",
                "Desktop session expired or revoked",
            )
        })
}
fn private_response(body: impl IntoResponse) -> Response {
    let mut response = body.into_response();
    response
        .headers_mut()
        .insert("cache-control", "no-store".parse().unwrap());
    response
        .headers_mut()
        .insert("referrer-policy", "no-referrer".parse().unwrap());
    response.headers_mut().insert("content-security-policy","default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'".parse().unwrap());
    response
}
pub async fn page(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> ApiResult<Response> {
    ticket(&s, &h, &id)?;
    Ok(private_response((
        [("content-type", "text/html; charset=utf-8")],
        include_str!(concat!(env!("OUT_DIR"), "/desktop/index.html")),
    )))
}
/// Page resources retain the desktop ticket's authorization and cache policy.
pub async fn localization_asset(
    State(s): State<Service>,
    h: HeaderMap,
    Path((id, name)): Path<(String, String)>,
) -> ApiResult<Response> {
    ticket(&s, &h, &id)?;
    let (content_type, body) = match name.as_str() {
        "i18n.js" => (
            "text/javascript; charset=utf-8",
            include_str!(concat!(env!("OUT_DIR"), "/desktop/i18n.js")),
        ),
        "locales.json" => (
            "application/json; charset=utf-8",
            include_str!(concat!(env!("OUT_DIR"), "/desktop/locales.json")),
        ),
        _ => {
            return Err(error(
                StatusCode::NOT_FOUND,
                "desktop_asset",
                "Unknown desktop resource",
            ));
        }
    };
    Ok(private_response(([("content-type", content_type)], body)))
}
pub async fn status(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> ApiResult<Response> {
    let session = ticket(&s, &h, &id)?;
    let human = s
        .environment_service
        .sandboxes
        .desktop_has_control(&id)
        .map_err(internal)?;
    Ok(private_response(Json(
        json!({"owner":if human {"human"} else {"none"},"epoch":s.environment_service.sandboxes.desktop_revision(&id).map_err(internal)?,"capabilities":session.capabilities,"sandbox_id":session.sandbox_id}),
    )))
}
pub async fn control(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
    Json(body): Json<Value>,
) -> ApiResult<Response> {
    ticket(&s, &h, &id)?;
    let action = body["action"].as_str().unwrap_or("");
    if !["take", "return", "close"].contains(&action) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "desktop_action",
            "Invalid control action",
        ));
    }
    if action == "close" {
        let _guard = s
            .environment_service
            .sandboxes
            .desktop_serialization()
            .await;
        s.environment_service.sandboxes.close_desktop(&id);
    } else {
        s.environment_service
            .sandboxes
            .desktop_control(&id, action == "take")
            .await
            .map_err(|e| error(StatusCode::CONFLICT, "desktop_control", &e.to_string()))?;
    }
    Ok(private_response(Json(json!({"ok":true}))))
}
pub async fn browser(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> ApiResult<Response> {
    ticket(&s, &h, &id)?;
    s.environment_service
        .sandboxes
        .desktop_browser(&id)
        .await
        .map_err(|e| error(StatusCode::CONFLICT, "desktop_browser", &e.to_string()))?;
    Ok(private_response(Json(json!({"opened":true}))))
}
pub async fn metrics(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> ApiResult<Response> {
    ticket(&s, &h, &id)?;
    Ok(private_response(Json(
        s.environment_service
            .sandboxes
            .desktop_metrics(&id)
            .await
            .map_err(internal)?,
    )))
}
pub async fn connect(
    State(s): State<Service>,
    h: HeaderMap,
    Path(id): Path<String>,
    ws: WebSocketUpgrade,
    guard: Option<axum::Extension<DesktopAccessGuard>>,
) -> ApiResult<Response> {
    ticket(&s, &h, &id)?;
    let upstream = s
        .environment_service
        .sandboxes
        .desktop_stream(&id)
        .await
        .map_err(|_| {
            error(
                StatusCode::SERVICE_UNAVAILABLE,
                "desktop_transport",
                "Guest desktop unavailable; verify its configured RFB service",
            )
        })?;
    let failed_service = s.clone();
    let failed_id = id.clone();
    Ok(ws
        .on_failed_upgrade(move |_| {
            failed_service
                .environment_service
                .sandboxes
                .desktop_disconnected(&failed_id)
        })
        .protocols(["binary"])
        .max_message_size(4 * 1024 * 1024)
        .on_upgrade(move |socket| relay(s, id, socket, upstream, guard.map(|g| g.0))))
}
async fn relay(
    s: Service,
    id: String,
    mut socket: WebSocket,
    mut upstream: pot_sandbox::DesktopStream,
    access: Option<DesktopAccessGuard>,
) {
    let mut gate = pot_sandbox::RfbGate::default();
    let mut revision = s
        .environment_service
        .sandboxes
        .desktop_revision(&id)
        .unwrap_or(0);
    let mut buf = [0; 65536];
    let mut poll = tokio::time::interval(Duration::from_secs(1));
    loop {
        tokio::select! {
            _ = poll.tick() => {
                if access.as_ref().is_some_and(|g| !(g.0)()) || s.stopping() || s.environment_service.sandboxes.desktop_validate(&id).await.is_err() { break; }
                let _guard = s.environment_service.sandboxes.desktop_serialization().await;
                let Ok(current) = s.environment_service.sandboxes.desktop_revision(&id) else { break; };
                if current != revision {
                    if gate.control_changed().is_err() { break; }
                    if !matches!(tokio::time::timeout(Duration::from_secs(5),upstream.write_all(&gate.release_input())).await,Ok(Ok(()))) { break; }
                    s.environment_service.sandboxes.desktop_released(&id,current);
                    revision = current;
                }
            }
            data = upstream.read(&mut buf) => {
                match data { Ok(n) if n>0 => { if !matches!(tokio::time::timeout(Duration::from_secs(5),socket.send(WsMessage::Binary(buf[..n].to_vec().into()))).await,Ok(Ok(()))) { break; } }, _=>break }
            }
            message = socket.recv() => {
                if access.as_ref().is_some_and(|g| !(g.0)()) { break; }
                let data = match message { Some(Ok(WsMessage::Binary(data)))=>data, Some(Ok(WsMessage::Ping(_) | WsMessage::Pong(_)))=>continue, _=>break };
                let _guard = s.environment_service.sandboxes.desktop_serialization().await;
                let Ok(current) = s.environment_service.sandboxes.desktop_revision(&id) else { break; };
                if current != revision {
                    if gate.control_changed().is_err() { break; }
                    if !matches!(tokio::time::timeout(Duration::from_secs(5),upstream.write_all(&gate.release_input())).await,Ok(Ok(()))) { break; }
                    s.environment_service.sandboxes.desktop_released(&id,current);
                    revision = current;
                }
                let Ok(control) = s.environment_service.sandboxes.desktop_has_control(&id) else { break; };
                let Ok(session) = s.environment_service.sandboxes.desktop_session(&id) else { break; };
                let Ok(bytes) = gate.push(&data,control,session.capabilities.clipboard) else { break; };
                if !matches!(tokio::time::timeout(Duration::from_secs(5),upstream.write_all(&bytes)).await,Ok(Ok(()))) { break; }
            }
        }
    }
    let _ = tokio::time::timeout(
        Duration::from_secs(2),
        upstream.write_all(&gate.release_input()),
    )
    .await;
    s.environment_service.sandboxes.desktop_disconnected(&id);
    let _ = socket.close().await;
}
/// noVNC is a pinned packaging resource; requests cannot select arbitrary host paths.
pub async fn asset(Path(path): Path<String>) -> ApiResult<Response> {
    if path
        .split('/')
        .any(|p| p.is_empty() || p == ".." || p == "." || p.contains('\\'))
        || !path.ends_with(".js")
    {
        return Err(error(
            StatusCode::NOT_FOUND,
            "desktop_asset",
            "Asset unavailable",
        ));
    }
    let base = std::env::var_os("POT_RUNTIME_RESOURCES")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../.local/d1/resources")
        });
    let root = base
        .join("novnc")
        .canonicalize()
        .map_err(|e| internal(e.into()))?;
    let file = root
        .join(path)
        .canonicalize()
        .map_err(|e| internal(e.into()))?;
    if !file.starts_with(&root) {
        return Err(error(
            StatusCode::NOT_FOUND,
            "desktop_asset",
            "Asset unavailable",
        ));
    }
    let data = tokio::fs::read(file)
        .await
        .map_err(|e| internal(e.into()))?;
    Ok((
        [
            ("content-type", "text/javascript; charset=utf-8"),
            ("x-content-type-options", "nosniff"),
        ],
        data,
    )
        .into_response())
}

#[cfg(test)]
mod tests {
    use super::*;
    use futures::future::BoxFuture;
    use futures::{SinkExt, StreamExt};
    use pot_sandbox::{
        Capabilities, DesktopCapabilities, DesktopStream, Provider, ProviderContext,
        ProviderDescriptor,
    };
    use std::path::Path as FsPath;
    struct Display(tokio::sync::mpsc::UnboundedSender<Vec<u8>>);
    impl Provider for Display {
        fn descriptor(&self) -> ProviderDescriptor {
            ProviderDescriptor {
                id: "test.display".into(),
                os: "test-display".into(),
                host_os: "test".into(),
                engine: "test".into(),
                capabilities: Capabilities {
                    shell: true,
                    files: true,
                    desktop: true,
                    shared_execution: true,
                },
            }
        }
        fn desktop_capabilities(&self) -> DesktopCapabilities {
            DesktopCapabilities {
                viewing: true,
                input: true,
                clipboard: false,
                browser: false,
                protocol: Some(pot_sandbox::DesktopProtocol::Rfb38),
            }
        }
        fn workspace(&self, root: &FsPath, _: &str) -> PathBuf {
            root.join("workspace")
        }
        fn guest_workspace(&self) -> &str {
            "/data/workspace"
        }
        fn lifecycle<'a>(
            &'a self,
            _: &'a ProviderContext,
            _: &'a str,
        ) -> BoxFuture<'a, Result<Value>> {
            Box::pin(async { Ok(json!({"phase":"running","generation":1})) })
        }
        fn health<'a>(&'a self, _: &'a ProviderContext) -> BoxFuture<'a, Result<bool>> {
            Box::pin(async { Ok(true) })
        }
        fn request<'a>(
            &'a self,
            _: &'a ProviderContext,
            _: &'a str,
            _: &'a Value,
            _: Duration,
        ) -> BoxFuture<'a, Result<Value>> {
            Box::pin(async { Ok(json!({})) })
        }
        fn prepare_desktop<'a>(&'a self, _: &'a ProviderContext) -> BoxFuture<'a, Result<()>> {
            Box::pin(async { Ok(()) })
        }
        fn open_desktop<'a>(
            &'a self,
            _: &'a ProviderContext,
        ) -> BoxFuture<'a, Result<DesktopStream>> {
            Box::pin(async move {
                let (client, mut up) = tokio::io::duplex(65536);
                let tx = self.0.clone();
                tokio::spawn(async move {
                    up.write_all(b"RFB 003.008\n").await.unwrap();
                    let mut data = [0; 65536];
                    loop {
                        match up.read(&mut data).await {
                            Ok(n) if n > 0 => {
                                let _ = tx.send(data[..n].to_vec());
                            }
                            _ => break,
                        }
                    }
                });
                Ok(Box::pin(client) as DesktopStream)
            })
        }
        fn validate_config(&self, _: &Value) -> Result<()> {
            Ok(())
        }
    }
    #[tokio::test]
    async fn real_websocket_enforces_view_only_control_origin_and_revocation() {
        let mut s = tasks::tests::fixture();
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel();
        s.environment_service.sandboxes = Arc::new(
            sandbox::Manager::with_providers(&s.data, vec![Arc::new(Display(tx))]).unwrap(),
        );
        let routes = Router::new()
            .route("/open", axum::routing::post(open))
            .route("/desktop/{id}/rfb", axum::routing::get(connect))
            .route("/desktop/{id}/api/control", axum::routing::post(control))
            .with_state(s.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            axum::serve(listener, routes).await.unwrap();
        });
        let http = reqwest::Client::new();
        let base = format!("http://{addr}");
        assert_eq!(
            http.post(format!("{base}/open"))
                .json(&json!({}))
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::UNAUTHORIZED
        );
        let opened: Value = http
            .post(format!("{base}/open"))
            .bearer_auth(&s.token)
            .json(&json!({}))
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        let session = opened["session"]["id"].as_str().unwrap();
        use tokio_tungstenite::tungstenite::{Message, client::IntoClientRequest};
        let url = format!("ws://{addr}/desktop/{session}/rfb");
        let mut bad = url.clone().into_client_request().unwrap();
        bad.headers_mut()
            .insert("Origin", "https://attacker.invalid".parse().unwrap());
        assert!(tokio_tungstenite::connect_async(bad).await.is_err());
        let (mut ws, _) = tokio_tungstenite::connect_async(url).await.unwrap();
        assert_eq!(
            ws.next().await.unwrap().unwrap().into_data(),
            b"RFB 003.008\n".as_slice()
        );
        ws.send(Message::Binary(b"RFB 003.008\n\x01\x01".to_vec().into()))
            .await
            .unwrap();
        assert_eq!(rx.recv().await.unwrap(), b"RFB 003.008\n\x01\x01");
        let key = vec![4, 1, 0, 0, 0, 0, 0, 65];
        ws.send(Message::Binary(key.clone().into())).await.unwrap();
        assert!(
            tokio::time::timeout(Duration::from_millis(80), rx.recv())
                .await
                .is_err()
        );
        let control_url = format!("{base}/desktop/{session}/api/control");
        http.post(&control_url)
            .json(&json!({"action":"take"}))
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap();
        ws.send(Message::Binary(key.clone().into())).await.unwrap();
        assert_eq!(rx.recv().await.unwrap(), key);
        // Clipboard is rejected even after taking control when the adapter lacks it.
        ws.send(Message::Binary(vec![6, 0, 0, 0, 0, 0, 0, 1, 65].into()))
            .await
            .unwrap();
        assert!(
            tokio::time::timeout(Duration::from_millis(80), rx.recv())
                .await
                .is_err()
        );
        http.post(&control_url)
            .json(&json!({"action":"return"}))
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap();
        assert_eq!(rx.recv().await.unwrap(), vec![4, 0, 0, 0, 0, 0, 0, 65]);
        ws.send(Message::Binary(key.into())).await.unwrap();
        assert!(
            tokio::time::timeout(Duration::from_millis(80), rx.recv())
                .await
                .is_err()
        );
        http.post(&control_url)
            .json(&json!({"action":"close"}))
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap();
        let result = tokio::time::timeout(Duration::from_secs(3), ws.next())
            .await
            .unwrap();
        assert!(result.is_none() || matches!(result, Some(Ok(Message::Close(_)))));
        assert!(
            s.environment_service
                .sandboxes
                .desktop_session(session)
                .is_err()
        );
        server.abort();
        std::fs::remove_dir_all(s.data.parent().unwrap()).unwrap();
    }
}
