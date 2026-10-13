//! Only Brotli bytes are embedded. Decode once for clients that require identity.
use axum::{
    http::{HeaderMap, HeaderValue, StatusCode},
    response::{IntoResponse, Response},
};
use std::{io::Read, sync::OnceLock};

struct EmbeddedAsset {
    compressed: &'static [u8],
    decoded: OnceLock<Vec<u8>>,
}
include!(concat!(env!("OUT_DIR"), "/web_assets.rs"));

// Explicit entries override wildcards, including q=0. Missing Accept-Encoding
// uses identity; identity remains acceptable unless explicitly excluded.
fn quality(headers: &HeaderMap, encoding: &str) -> f32 {
    let mut explicit = None;
    let mut wildcard = None;
    for header in headers.get_all("accept-encoding") {
        let Ok(header) = header.to_str() else {
            continue;
        };
        for item in header.split(',') {
            let mut parts = item.trim().split(';');
            let name = parts.next().unwrap_or("").trim();
            let mut q = 1.0;
            for parameter in parts {
                if let Some((key, value)) = parameter.trim().split_once('=')
                    && key.trim().eq_ignore_ascii_case("q")
                {
                    q = value
                        .trim()
                        .parse::<f32>()
                        .ok()
                        .filter(|v| v.is_finite() && (0.0..=1.0).contains(v))
                        .unwrap_or(0.0);
                }
            }
            if name.eq_ignore_ascii_case(encoding) {
                explicit = Some(q);
            }
            if name == "*" {
                wildcard = Some(q);
            }
        }
    }
    explicit.unwrap_or_else(|| {
        if encoding == "identity" {
            if wildcard == Some(0.0) { 0.0 } else { 1.0 }
        } else {
            wildcard.unwrap_or(0.0)
        }
    })
}

pub(super) fn response(name: &str, mime: &'static str, headers: &HeaderMap) -> Option<Response> {
    let asset = embedded_asset(name)?;
    let br = quality(headers, "br");
    let identity = quality(headers, "identity");
    let mut response = if br > 0.0 && br >= identity {
        let mut response = ([("content-type", mime)], asset.compressed).into_response();
        response
            .headers_mut()
            .insert("content-encoding", HeaderValue::from_static("br"));
        response
    } else if identity > 0.0 {
        if asset.decoded.get().is_none() {
            let mut decoded = Vec::new();
            if brotli_decompressor::Decompressor::new(asset.compressed, 4096)
                .read_to_end(&mut decoded)
                .is_err()
            {
                return Some(StatusCode::INTERNAL_SERVER_ERROR.into_response());
            }
            let _ = asset.decoded.set(decoded);
        }
        (
            [("content-type", mime)],
            asset.decoded.get().unwrap().clone(),
        )
            .into_response()
    } else {
        StatusCode::NOT_ACCEPTABLE.into_response()
    };
    response
        .headers_mut()
        .insert("vary", HeaderValue::from_static("Accept-Encoding"));
    Some(response)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn encoding_preferences() {
        let mut headers = HeaderMap::new();
        assert_eq!(quality(&headers, "br"), 0.0);
        assert_eq!(quality(&headers, "identity"), 1.0);
        headers.insert(
            "accept-encoding",
            HeaderValue::from_static("gzip, br;q=0, *;q=1"),
        );
        assert_eq!(quality(&headers, "br"), 0.0);
        headers.insert(
            "accept-encoding",
            HeaderValue::from_static("BR;q=0.8, identity;q=0.2"),
        );
        assert_eq!(quality(&headers, "br"), 0.8);
        assert_eq!(quality(&headers, "identity"), 0.2);
        headers.insert("accept-encoding", HeaderValue::from_static("*;q=0"));
        assert_eq!(quality(&headers, "identity"), 0.0);
    }

    #[cfg(feature = "web-ui")]
    #[tokio::test]
    async fn embedded_response_round_trip() {
        let mut headers = HeaderMap::new();
        headers.insert("accept-encoding", HeaderValue::from_static("gzip, br"));
        let encoded = response("pkg/pot_web_ui_bg.wasm", "application/wasm", &headers).unwrap();
        assert_eq!(encoded.headers()["content-encoding"], "br");
        assert_eq!(encoded.headers()["vary"], "Accept-Encoding");
        let bytes = axum::body::to_bytes(encoded.into_body(), 32 * 1024 * 1024)
            .await
            .unwrap();
        let mut decoded = Vec::new();
        brotli_decompressor::Decompressor::new(&bytes[..], 4096)
            .read_to_end(&mut decoded)
            .unwrap();
        assert!(decoded.starts_with(b"\0asm"));
        headers.insert("accept-encoding", HeaderValue::from_static("br;q=0, gzip"));
        let raw = response("pkg/pot_web_ui_bg.wasm", "application/wasm", &headers).unwrap();
        assert!(raw.headers().get("content-encoding").is_none());
        assert_eq!(
            axum::body::to_bytes(raw.into_body(), 32 * 1024 * 1024)
                .await
                .unwrap()
                .as_ref(),
            decoded
        );
        headers.insert(
            "accept-encoding",
            HeaderValue::from_static("br;q=0, identity;q=0"),
        );
        assert_eq!(
            response("index.html", "text/html", &headers)
                .unwrap()
                .status(),
            StatusCode::NOT_ACCEPTABLE
        );
        assert!(response("missing", "text/plain", &headers).is_none());
    }
}
