//! Fixed Control transport for explicit Web hosting. No local fallback or browser credentials.
use crate::{RemoteClient, WebControlConfig};
use anyhow::{Result, ensure};
use axum::{
    Router,
    body::Body,
    extract::{Request, State},
    http::StatusCode,
    response::{IntoResponse, Response},
};
use std::{sync::Arc, time::Duration};

struct Destination {
    origin: url::Url,
    token: String,
    http: reqwest::Client,
}
pub(crate) async fn connect(
    config: &WebControlConfig,
) -> Result<(Router, crate::access::push::NotificationSource)> {
    let client =
        RemoteClient::with_ca(&config.url, config.token.clone(), config.ca_pem.as_deref())?;
    let metadata = tokio::time::timeout(
        Duration::from_secs(10),
        client.request("/api/v1/runtime/control", None),
    )
    .await??;
    ensure!(
        metadata.status.is_success(),
        "Control rejected Web hosting credential"
    );
    let value: serde_json::Value = serde_json::from_slice(&metadata.body)?;
    ensure!(
        value["role"] == "control",
        "Web UI requires a standalone Control"
    );
    uuid::Uuid::parse_str(
        value["identity"]["runtime_id"]
            .as_str()
            .ok_or_else(|| anyhow::anyhow!("Control identity missing"))?,
    )?;
    let mut http = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(10));
    if let Some(ca) = &config.ca_pem {
        http = http.add_root_certificate(reqwest::Certificate::from_pem(ca.as_bytes())?);
    }
    let destination = Arc::new(Destination {
        origin: url::Url::parse(&config.url)?,
        token: config.token.clone(),
        http: http.build()?,
    });
    let source = crate::access::push::NotificationSource {
        control_id: value["identity"]["runtime_id"].as_str().unwrap().into(),
        client,
    };
    Ok((
        Router::new().fallback(forward).with_state(destination),
        source,
    ))
}
async fn forward(State(destination): State<Arc<Destination>>, request: Request) -> Response {
    match relay(&destination, request).await {
        Ok(response) => response,
        Err(_) => (
            StatusCode::BAD_GATEWAY,
            axum::Json(serde_json::json!({"code":"control_unavailable"})),
        )
            .into_response(),
    }
}
async fn relay(destination: &Destination, request: Request) -> Result<Response> {
    let path = request
        .uri()
        .path_and_query()
        .ok_or_else(|| anyhow::anyhow!("API path required"))?
        .as_str();
    ensure!(
        path.starts_with("/api/") && !path.contains('\\'),
        "Invalid Control API path"
    );
    let url = destination.origin.join(path)?;
    ensure!(
        url.origin() == destination.origin.origin(),
        "Control origin changed"
    );
    let mut outgoing = destination
        .http
        .request(request.method().clone(), url)
        .bearer_auth(&destination.token);
    for name in ["content-type", "accept", "last-event-id"] {
        if let Some(value) = request.headers().get(name) {
            outgoing = outgoing.header(name, value);
        }
    }
    let bytes = axum::body::to_bytes(request.into_body(), 1024 * 1024).await?;
    let upstream =
        tokio::time::timeout(Duration::from_secs(15), outgoing.body(bytes).send()).await??;
    ensure!(
        !upstream.status().is_redirection(),
        "Control redirect rejected"
    );
    if upstream.status() == StatusCode::UNAUTHORIZED {
        return Ok((
            StatusCode::BAD_GATEWAY,
            axum::Json(serde_json::json!({"code":"control_authentication_failed"})),
        )
            .into_response());
    }
    let mut response = Response::builder().status(upstream.status());
    for name in [
        "content-type",
        "cache-control",
        "retry-after",
        "content-disposition",
        "content-length",
        "x-content-type-options",
    ] {
        if let Some(value) = upstream.headers().get(name) {
            response = response.header(name, value);
        }
    }
    Ok(response.body(Body::from_stream(upstream.bytes_stream()))?)
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{http::HeaderMap, routing::get};
    use futures_util::StreamExt;
    use tower::ServiceExt;
    async fn fixture(role: &'static str) -> (WebControlConfig, tokio::task::JoinHandle<()>) {
        let upstream = Router::new()
            .route("/api/v1/runtime/control", get(move || async move {
                axum::Json(serde_json::json!({"role":role,"identity":{"runtime_id":"00000000-0000-4000-8000-000000000001"}}))
            }))
            .route("/api/v1/assistant-notifications", axum::routing::post(|headers: HeaderMap, axum::Json(args): axum::Json<serde_json::Value>| async move {
                assert_eq!(headers["authorization"], "Bearer fixture-control");
                assert!(!headers.contains_key("cookie"));
                let mut feed = pot_protocol::AssistantNotifications::default();
                feed.cursors.insert("assistant-b".into(), 7);
                if args["initialize"] != true && args["after"]["assistant-b"].as_i64().unwrap_or(0) < 7 {
                    feed.messages.push(pot_protocol::AssistantNotification { assistant_id: "assistant-b".into(), sequence: 7, message: pot_protocol::Message { id: "final-reply".into(), conversation_id: "assistant-b".into(), role: pot_protocol::Role::Assistant, text: "Verified assistant response".into(), status: pot_protocol::MessageStatus::Complete, created_at_ms: 0, request_id: None } });
                }
                axum::Json(feed)
            }))
            .route("/api/v1/snapshot", get(|headers: HeaderMap| async move {
                assert_eq!(headers["authorization"], "Bearer fixture-control");
                assert!(!headers.contains_key("cookie"));
                assert!(!headers.contains_key("origin"));
                Response::builder().header("content-type", "application/json")
                    .header("set-cookie", "secret=control").body(Body::from("{\"owner\":\"control\"}")).unwrap()
            }))
            .route("/api/v1/events", get(|| async {
                Response::builder().header("content-type", "text/event-stream")
                    .body(Body::from_stream(async_stream::stream! {
                        yield Ok::<_,std::io::Error>("data: control\n\n");
                        std::future::pending::<()>().await;
                    })).unwrap()
            }))
            .route("/api/v1/rejected", get(|| async { StatusCode::UNAUTHORIZED }));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let config = WebControlConfig {
            url: format!("http://{}", listener.local_addr().unwrap()),
            token: "fixture-control".into(),
            ca_pem: None,
        };
        let task = tokio::spawn(async move { axum::serve(listener, upstream).await.unwrap() });
        (config, task)
    }
    #[tokio::test]
    async fn remote_notifications_bind_control_and_preserve_owner_cursors() {
        let (config, task) = fixture("control").await;
        let (_, source) = connect(&config).await.unwrap();
        assert_eq!(source.control_id, "00000000-0000-4000-8000-000000000001");
        let initial = source
            .feed(serde_json::json!({"initialize":true}))
            .await
            .unwrap();
        assert!(initial.messages.is_empty());
        let feed = source.feed(serde_json::json!({"after":{}})).await.unwrap();
        assert_eq!(feed.messages.len(), 1);
        assert_eq!(feed.messages[0].assistant_id, "assistant-b");
        assert!(
            source
                .feed(serde_json::json!({"after":initial.cursors}))
                .await
                .unwrap()
                .messages
                .is_empty()
        );
        task.abort();
        let _ = task.await;
        assert!(source.feed(serde_json::json!({"after":{}})).await.is_err());
    }
    #[tokio::test]
    async fn fixed_control_streams_and_never_forwards_browser_credentials() {
        let (config, task) = fixture("control").await;
        let (router, _) = connect(&config).await.unwrap();
        let rejected = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/rejected")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(rejected.status(), StatusCode::BAD_GATEWAY);
        assert_eq!(
            axum::body::to_bytes(rejected.into_body(), 1024)
                .await
                .unwrap(),
            "{\"code\":\"control_authentication_failed\"}"
        );
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/snapshot")
                    .header("authorization", "Bearer browser")
                    .header("cookie", "session=browser")
                    .header("origin", "https://browser.example")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert!(!response.headers().contains_key("set-cookie"));
        assert_eq!(
            axum::body::to_bytes(response.into_body(), 1024)
                .await
                .unwrap(),
            "{\"owner\":\"control\"}"
        );
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/events")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let mut stream = response.into_body().into_data_stream();
        let chunk = tokio::time::timeout(Duration::from_secs(1), stream.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        assert_eq!(chunk, "data: control\n\n");
        drop(stream);
        task.abort();
        let _ = task.await;
        let response = router
            .oneshot(
                Request::builder()
                    .uri("/api/v1/snapshot")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
        let text = axum::body::to_bytes(response.into_body(), 1024)
            .await
            .unwrap();
        assert_eq!(text, "{\"code\":\"control_unavailable\"}");
    }
    #[tokio::test]
    async fn web_destination_requires_control_role() {
        let (config, task) = fixture("runtime").await;
        assert!(connect(&config).await.is_err());
        assert!(!format!("{config:?}").contains("fixture-control"));
        task.abort();
    }
}
