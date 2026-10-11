//! Client adapters share bounded replies; client and machine credentials are separate.
use crate::{LocalClient, LocalReply};
use anyhow::{Result, ensure};
use serde_json::Value;

#[derive(Clone)]
pub enum ClientTransport {
    Local(LocalClient),
    Remote(RemoteClient),
}
impl ClientTransport {
    pub async fn request(&self, path: &str, body: Option<Value>) -> Result<LocalReply> {
        match self {
            Self::Local(client) => client.request(path, body).await,
            Self::Remote(client) => client.request(path, body).await,
        }
    }
}

#[derive(Clone)]
pub struct RemoteClient {
    endpoint: url::Url,
    token: String,
    http: reqwest::Client,
}
impl RemoteClient {
    pub fn new(endpoint: &str, token: String) -> Result<Self> {
        Self::with_ca(endpoint, token, None)
    }
    pub fn with_ca(endpoint: &str, token: String, ca_pem: Option<&str>) -> Result<Self> {
        let endpoint = url::Url::parse(endpoint)?;
        ensure!(
            endpoint.scheme() == "https"
                || (endpoint.scheme() == "http"
                    && matches!(
                        endpoint.host_str(),
                        Some("localhost" | "127.0.0.1" | "[::1]")
                    )),
            "Remote clients require HTTPS or loopback HTTP"
        );
        ensure!(
            endpoint.username().is_empty()
                && endpoint.password().is_none()
                && endpoint.path() == "/"
                && endpoint.query().is_none()
                && endpoint.fragment().is_none(),
            "Use a Runtime origin without credentials, path, query or fragment"
        );
        ensure!(!token.is_empty(), "Client credential required");
        let mut builder = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(std::time::Duration::from_secs(120));
        if let Some(ca) = ca_pem {
            builder = builder.add_root_certificate(reqwest::Certificate::from_pem(ca.as_bytes())?);
        }
        Ok(Self {
            endpoint,
            token,
            http: builder.build()?,
        })
    }
    pub async fn request(&self, path: &str, body: Option<Value>) -> Result<LocalReply> {
        ensure!(
            path.starts_with("/api/") && !path.contains('#') && !path.contains('\\'),
            "Invalid API path"
        );
        let endpoint = self.endpoint.join(path)?;
        ensure!(
            endpoint.origin() == self.endpoint.origin(),
            "Client request cannot change origin"
        );
        let method = if body.is_some() {
            reqwest::Method::POST
        } else {
            reqwest::Method::GET
        };
        let mut request = self.http.request(method, endpoint).bearer_auth(&self.token);
        if let Some(body) = body {
            request = request.json(&body);
        }
        let mut response = request.send().await?;
        let status = response.status();
        let mut bytes = Vec::new();
        while let Some(chunk) = response.chunk().await? {
            ensure!(
                bytes.len() + chunk.len() <= 8 * 1024 * 1024,
                "Client reply exceeds limit"
            );
            bytes.extend_from_slice(&chunk);
        }
        Ok(LocalReply {
            status,
            body: bytes,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn remote_adapter_rejects_unsafe_origins_and_paths() {
        for origin in [
            "http://remote.example",
            "https://user:secret@example.com",
            "https://example.com/path",
            "https://example.com/?token=x",
        ] {
            assert!(RemoteClient::new(origin, "test".into()).is_err());
        }
        let client = RemoteClient::new("https://example.com", "test".into()).unwrap();
        for path in [
            "//evil.example/api/",
            "https://evil.example/api/",
            "/api/\\evil",
            "/api/v1/snapshot#fragment",
        ] {
            assert!(client.request(path, None).await.is_err());
        }
    }
}
