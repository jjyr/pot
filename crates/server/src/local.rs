//! Trusted in-process dispatch shared by the native App and OS-protected CLI adapter.
use anyhow::Result;
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use serde_json::Value;
use tower::ServiceExt;
#[derive(Clone)]
pub struct LocalClient {
    router: Router,
    token: String,
    _lock: std::sync::Arc<std::fs::File>,
}
pub struct LocalReply {
    pub status: StatusCode,
    pub body: Vec<u8>,
}
impl LocalClient {
    pub(crate) fn new(router: Router, token: String, lock: std::sync::Arc<std::fs::File>) -> Self {
        Self {
            router,
            token,
            _lock: lock,
        }
    }
    pub async fn stream(&self, path: &str) -> Result<crate::client::ByteStream> {
        anyhow::ensure!(
            path.starts_with("/api/") && !path.starts_with("//") && !path.contains(['#', '\\']),
            "Invalid local API path"
        );
        let request = Request::builder()
            .uri(path)
            .header("authorization", format!("Bearer {}", self.token))
            .body(Body::empty())?;
        let response = self.router.clone().oneshot(request).await?;
        anyhow::ensure!(
            response.status().is_success(),
            "Subscription rejected: {}",
            response.status()
        );
        use futures_util::StreamExt;
        anyhow::ensure!(
            response
                .headers()
                .get("content-type")
                .and_then(|v| v.to_str().ok())
                .is_some_and(|v| v.starts_with("text/event-stream")),
            "Expected event stream"
        );
        Ok(Box::pin(
            response
                .into_body()
                .into_data_stream()
                .map(|chunk| chunk.map_err(Into::into)),
        ))
    }
    pub async fn request(&self, path: &str, body: Option<Value>) -> Result<LocalReply> {
        anyhow::ensure!(
            path.starts_with("/api/") && !path.starts_with("//"),
            "Invalid local API path"
        );
        let req = Request::builder()
            .method(if body.is_some() { "POST" } else { "GET" })
            .uri(path)
            .header("authorization", format!("Bearer {}", self.token))
            .header("content-type", "application/json")
            .body(
                body.map(|v| Body::from(v.to_string()))
                    .unwrap_or_else(Body::empty),
            )?;
        let response = self.router.clone().oneshot(req).await?;
        Ok(LocalReply {
            status: response.status(),
            body: to_bytes(response.into_body(), 8 * 1024 * 1024)
                .await?
                .to_vec(),
        })
    }
}
