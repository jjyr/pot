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
