//! Provider networking and wire codecs; no product state or database access.
pub mod codecs;
use anyhow::{Result, anyhow};
use codecs::Protocol;
use reqwest::header::HeaderValue;
use serde_json::Value;
use std::time::Duration;

/// Call only after Runtime has durably reserved and claimed the request.
pub async fn forward(
    endpoint: &str,
    key: &str,
    protocol: &Protocol,
    body: &Value,
    beta: Option<&HeaderValue>,
) -> Result<reqwest::Response> {
    let client = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(120))
        .build()?;
    let request = client.post(endpoint);
    let request = match protocol {
        Protocol::AnthropicMessages => {
            let mut request = request
                .header("x-api-key", key)
                .header("anthropic-version", "2023-06-01");
            if let Some(beta) = beta {
                request = request.header("anthropic-beta", beta);
            }
            request
        }
        Protocol::Gemini => request
            .header("x-goog-api-key", key)
            .query(&[("alt", "sse")]),
        _ => request.bearer_auth(key),
    };
    request
        .json(body)
        .send()
        .await
        .map_err(|_| anyhow!("Model provider connection failed"))
}

pub mod model;
