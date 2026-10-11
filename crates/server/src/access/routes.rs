use super::state::*;
use axum::{
    Json, Router,
    body::Body,
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, Method, StatusCode},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde_json::{Value, json};
use std::time::Duration;
use tower::ServiceExt;
pub(super) type Failure = (StatusCode, Json<Value>);
pub(super) fn fail(status: StatusCode, code: &str) -> Failure {
    (status, Json(json!({"code":code})))
}
pub(super) fn invalid() -> Failure {
    fail(StatusCode::BAD_REQUEST, "invalid_ceremony")
}
pub(super) fn response(value: Value, cookie: Option<(&str, String, usize)>) -> Response {
    let mut r = Json(value).into_response();
    if let Some((name, value, age)) = cookie {
        r.headers_mut().insert(
            "set-cookie",
            HeaderValue::from_str(&format!(
                "{name}={value}; Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age={age}"
            ))
            .unwrap(),
        );
    }
    r
}
fn origin(a: &Access, h: &HeaderMap) -> bool {
    h.get("origin").and_then(|v| v.to_str().ok()) == Some(a.origin().as_str())
}

pub fn public_router(access: Access) -> Router {
    Router::new()
        .route("/access/status", get(status))
        .route("/access/token", post(configure_token))
        .route("/access/logout", post(logout))
        .route("/access/push", post(super::push::subscribe))
        .route("/access/push/remove", post(super::push::unsubscribe))
        .route("/access/push/preferences", post(super::push::preferences))
        .route("/access/push/heartbeat", post(super::push::heartbeat))
        .route("/api/v1/desktop/sessions", post(super::desktop::proxy))
        .route(
            "/desktop/{*path}",
            get(super::desktop::proxy).post(super::desktop::proxy),
        )
        .route("/desktop-assets/{*path}", get(super::desktop::asset))
        .route("/api/{*path}", get(proxy).post(proxy))
        .fallback(assets)
        .layer(axum::extract::DefaultBodyLimit::max(1024 * 1024))
        .layer(middleware::from_fn_with_state(access.clone(), boundary))
        .with_state(access)
}
pub fn admin_router(access: Access) -> Router {
    Router::new()
        .route("/api/v1/access", get(admin_status).post(admin_command))
        .with_state(access)
}
// The public login shell is safe to open from a link/bookmark. API reads,
// downloads, embedded frames and every mutation retain the cross-site boundary.
fn public_shell_navigation(req: &Request) -> bool {
    req.method() == Method::GET
        && req.uri().path() == "/"
        && req
            .headers()
            .get("sec-fetch-mode")
            .is_some_and(|v| v == "navigate")
        && req
            .headers()
            .get("sec-fetch-dest")
            .is_some_and(|v| v == "document")
}
async fn boundary(State(a): State<Access>, mut req: Request, next: Next) -> Response {
    let secure = a.origin().starts_with("https://");
    if !secure {
        let cookies = req
            .headers()
            .get_all("cookie")
            .iter()
            .filter_map(|v| v.to_str().ok())
            .flat_map(|v| v.split(';'))
            .map(|item| {
                let item = item.trim();
                if item.starts_with("pot-session=") || item.starts_with("pot-installation=") {
                    format!("__Host-{item}")
                } else {
                    item.to_owned()
                }
            })
            .collect::<Vec<_>>()
            .join("; ");
        if !cookies.is_empty()
            && let Ok(value) = HeaderValue::from_str(&cookies)
        {
            req.headers_mut().insert("cookie", value);
        }
    }
    if !a.inner.lock().unwrap().enabled {
        return fail(StatusCode::SERVICE_UNAVAILABLE, "access_disabled").into_response();
    }
    // Mutations need exact Origin; reads reject cross-site browser requests too.
    if (req.method() != Method::GET || req.headers().contains_key("origin"))
        && !origin(&a, req.headers())
    {
        return fail(StatusCode::FORBIDDEN, "origin_rejected").into_response();
    }
    if req
        .headers()
        .get("sec-fetch-site")
        .is_some_and(|v| v == "cross-site")
        && !public_shell_navigation(&req)
    {
        return fail(StatusCode::FORBIDDEN, "cross_site").into_response();
    }
    if req.method() == Method::POST && req.uri().path().starts_with("/access/") && !a.rate_limit() {
        return fail(StatusCode::TOO_MANY_REQUESTS, "rate_limited").into_response();
    }
    let desktop_page = req.uri().path().starts_with("/desktop/") && req.uri().path().ends_with('/');
    let mut r = next.run(req).await;
    for (k, v) in [
        ("cache-control", "no-store"),
        ("referrer-policy", "no-referrer"),
        ("cross-origin-opener-policy", "same-origin"),
        ("cross-origin-embedder-policy", "require-corp"),
        ("x-content-type-options", "nosniff"),
        (
            "content-security-policy",
            "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
        ),
        ("strict-transport-security", "max-age=31536000"),
    ] {
        r.headers_mut().insert(k, HeaderValue::from_static(v));
    }
    if !secure {
        r.headers_mut().remove("strict-transport-security");
        if url::Url::parse(&a.origin())
            .ok()
            .and_then(|url| url.host_str().map(str::to_owned))
            .as_deref()
            != Some("localhost")
        {
            r.headers_mut().remove("cross-origin-opener-policy");
            r.headers_mut().remove("cross-origin-embedder-policy");
        }
        let cookies = r
            .headers()
            .get_all("set-cookie")
            .iter()
            .filter_map(|v| v.to_str().ok())
            .map(|v| v.trim_start_matches("__Host-").replace("; Secure", ""))
            .collect::<Vec<_>>();
        r.headers_mut().remove("set-cookie");
        for cookie in cookies {
            if let Ok(value) = HeaderValue::from_str(&cookie) {
                r.headers_mut().append("set-cookie", value);
            }
        }
    }
    if desktop_page {
        r.headers_mut().insert("content-security-policy", HeaderValue::from_static("default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'"));
    }
    r
}
async fn status(State(a): State<Access>, h: HeaderMap) -> Json<Value> {
    let authenticated = a.authorization(&h).is_some();
    let i = a.inner.lock().unwrap();
    Json(
        json!({"authenticated":authenticated,"scope":"owner_instance","desktop_available":true,"push_available":a.push.is_some() && a.notifications.lock().unwrap().is_some(),"push_key":a.push.as_ref().map(|p|&p.public_key),"subscription":if authenticated{cookie(&h,"__Host-pot-installation").and_then(|id|i.credentials.subscriptions.get(&id)).map(|s|json!({"enabled":true,"private_preview":s.private_preview,"locale":s.locale,"failures":s.failures}))}else{None}}),
    )
}
async fn configure_token(State(a): State<Access>, h: HeaderMap) -> Result<Response, Failure> {
    let token = h
        .get("authorization")
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.strip_prefix("Bearer "));
    if !token.is_some_and(|token| a.valid_token(token)) {
        return Err(fail(StatusCode::UNAUTHORIZED, "invalid_token"));
    }
    let browser = cookie(&h, SESSION_COOKIE)
        .filter(|id| a.valid_session(id, false))
        .unwrap_or_else(|| a.browser_cookie());
    Ok(response(
        json!({"authenticated":true}),
        Some((SESSION_COOKIE, browser, 31536000)),
    ))
}
async fn logout(State(a): State<Access>, h: HeaderMap) -> Response {
    if super::push::remove(&a, &h).is_err() {
        return fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "subscription_save_failed",
        )
        .into_response();
    }
    response(
        json!({"authenticated":false}),
        Some((SESSION_COOKIE, String::new(), 0)),
    )
}
fn allowed(method: &Method, path: &str) -> bool {
    if matches!(path, "/api/v1/runtime" | "/api/v1/machines") {
        return *method == Method::GET;
    }
    if path == "/api/v1/machine-connections" {
        return *method == Method::GET || *method == Method::POST;
    }
    if path == "/api/v1/assistants" {
        return *method == Method::GET || *method == Method::POST;
    }
    if path == "/api/v1/assistant-tasks" {
        return *method == Method::GET;
    }
    if let Some(rest) = path.strip_prefix("/api/v1/assistants/") {
        let Some((owner, operation)) = rest.split_once('/') else {
            return false;
        };
        if owner != "main" && uuid::Uuid::parse_str(owner).is_err() {
            return false;
        }
        if operation == "instructions" {
            return *method == Method::GET || *method == Method::POST;
        }
        if operation == "definition" || operation == "agent/config" {
            return *method == Method::POST;
        }
        if operation == "agent" {
            return *method == Method::GET;
        }
        if operation.starts_with("assistants")
            || operation == "assistant-tasks"
            || operation == "machine-connections"
        {
            return false;
        }
        return allowed(method, &format!("/api/v1/{operation}"));
    }

    if *method == Method::POST && path == "/api/v1/permissions" {
        return true;
    }
    if *method == Method::GET
        && matches!(
            path,
            "/api/v1/snapshot"
                | "/api/v1/events"
                | "/api/v1/tasks"
                | "/api/v1/tasks/query"
                | "/api/v1/sandboxes"
                | "/api/v1/permissions"
                | "/api/v1/usage"
                | "/api/v1/usage/summary"
                | "/api/v1/schedules"
        )
    {
        return true;
    }
    if *method == Method::POST && matches!(path, "/api/v1/messages" | "/api/v1/turn/cancel") {
        return true;
    }
    let parts = path.split('/').collect::<Vec<_>>();
    if parts.len() >= 5 && parts[..4] == ["", "api", "v1", "tasks"] && !parts[4].is_empty() {
        return (*method == Method::GET
            && (parts.len() == 5
                || (parts.len() == 6 && matches!(parts[5], "files" | "file-download"))))
            || (*method == Method::POST
                && parts.len() == 6
                && matches!(parts[5], "messages" | "cancel" | "interrupt"));
    }
    false
}
async fn proxy(State(mut a): State<Access>, mut req: Request) -> Result<Response, Failure> {
    a.capture_endpoint();
    let session = a
        .authorization(req.headers())
        .ok_or_else(|| fail(StatusCode::UNAUTHORIZED, "invalid_token"))?;
    if !allowed(req.method(), req.uri().path()) {
        return Err(fail(StatusCode::FORBIDDEN, "route_not_allowed"));
    }
    let inventory = req.uri().path() == "/api/v1/sandboxes";
    // Never forward client-provided authorization, cookies or native-only route access.
    req.headers_mut().remove("cookie");
    req.headers_mut().remove("origin");
    req.headers_mut().insert(
        "authorization",
        HeaderValue::from_str(&format!("Bearer {}", a.root_token)).unwrap(),
    );
    // The outer wildcard match must not contaminate Runtime Path extractors.
    // Runtime establishes its own route extensions during this fresh dispatch.
    req.extensions_mut().clear();
    let r = a
        .runtime
        .clone()
        .oneshot(req)
        .await
        .map_err(|_| fail(StatusCode::BAD_GATEWAY, "runtime_unavailable"))?;
    if inventory && r.status().is_success() {
        let bytes = axum::body::to_bytes(r.into_body(), 1024 * 1024)
            .await
            .map_err(|_| fail(StatusCode::BAD_GATEWAY, "invalid_inventory"))?;
        let value: Value = serde_json::from_slice(&bytes)
            .map_err(|_| fail(StatusCode::BAD_GATEWAY, "invalid_inventory"))?;
        if !a.valid_authorization(&session) {
            return Err(fail(StatusCode::UNAUTHORIZED, "session_expired"));
        }
        return Ok(Json(public_inventory(&value)).into_response());
    }
    let (parts, body) = r.into_parts();
    // Validate revocation during streams, not just at their opening.
    use futures_util::StreamExt;
    let mut stream = body.into_data_stream();
    let guarded = async_stream::stream! {
        let mut tick=tokio::time::interval(Duration::from_secs(1));
        loop {
            tokio::select! {
                _=tick.tick()=>{ if !a.valid_authorization(&session) {break;} }
                item=stream.next()=> { match item {Some(item)=> {if !a.valid_authorization(&session){break;} yield item;},None=>break} }
            }
        }
    };
    Ok(Response::from_parts(parts, Body::from_stream(guarded)))
}
fn public_inventory(value: &Value) -> Value {
    let items=value["items"].as_array().map(|items|items.iter().map(|item|{
        json!({"id":item["id"],"name":item["name"],"os":item["os"],"host_os":item["host_os"],"engine":item["engine"],"provider":item["provider"],
            "running_tasks":item["running_tasks"].as_u64(),"active_leases":item["active_leases"].as_u64(),
            "state":{"phase":item["state"]["phase"].as_str().unwrap_or("unknown"),"generation":item["state"]["generation"].as_u64()},
            "capabilities":item["capabilities"],"desktop_capabilities":item["desktop_capabilities"],
            "config":{"cpus":item["config"]["cpus"].as_u64(),"memory_gib":item["config"]["memory_gib"].as_u64(),"disk_gib":item["config"]["disk_gib"].as_u64()}})
    }).collect::<Vec<_>>()).unwrap_or_default();
    json!({"selected":value["selected"],"items":items})
}
include!(concat!(env!("OUT_DIR"), "/web_assets.rs"));
async fn assets(State(a): State<Access>, req: Request) -> Response {
    if req.method() != Method::GET {
        return StatusCode::METHOD_NOT_ALLOWED.into_response();
    }
    let path = req.uri().path();
    let (file, mime) = match path {
        "/" => ("index.html", "text/html; charset=utf-8"),
        "/locales.json" => ("locales.json", "application/json"),
        "/OFL.txt" => ("OFL.txt", "text/plain; charset=utf-8"),
        "/sw.js" => ("sw.js", "text/javascript"),
        "/manifest.json" => ("manifest.json", "application/manifest+json"),
        "/icon.png" => ("icon.png", "image/png"),
        "/icon-192.png" => ("icon-192.png", "image/png"),
        "/icon-512.png" => ("icon-512.png", "image/png"),
        "/install.js" => ("install.js", "text/javascript"),
        "/auth.js" => ("auth.js", "text/javascript"),
        "/style.css" => ("style.css", "text/css"),
        "/pkg/pot_mobile.js" => ("pkg/pot_mobile.js", "text/javascript"),
        "/pkg/pot_mobile_bg.wasm" => ("pkg/pot_mobile_bg.wasm", "application/wasm"),
        "/font.ttf" => ("font.ttf", "font/ttf"),
        _ => return StatusCode::NOT_FOUND.into_response(),
    };
    let gzip = req
        .headers()
        .get("accept-encoding")
        .and_then(|v| v.to_str().ok())
        .is_some_and(|v| v.split(',').any(|v| v.trim() == "gzip"))
        && matches!(file, "pkg/pot_mobile_bg.wasm" | "pkg/pot_mobile.js");
    let path = if gzip {
        format!("{file}.gz")
    } else {
        file.into()
    };
    let bytes = if let Some(bytes) = embedded_asset(&path) {
        Ok(bytes.to_vec())
    } else {
        tokio::fs::read(a.assets.join(path)).await
    };
    match bytes {
        Ok(bytes) => {
            let mut r =
                ([("content-type", mime), ("vary", "accept-encoding")], bytes).into_response();
            if gzip {
                r.headers_mut()
                    .insert("content-encoding", HeaderValue::from_static("gzip"));
            }
            r
        }
        Err(_) => fail(StatusCode::SERVICE_UNAVAILABLE, "mobile_assets_missing").into_response(),
    }
}
fn admin(a: &Access, h: &HeaderMap) -> Result<(), Failure> {
    if h.contains_key("origin")
        || h.get("authorization").and_then(|s| s.to_str().ok())
            != Some(format!("Bearer {}", a.root_token).as_str())
    {
        return Err(fail(StatusCode::FORBIDDEN, "trusted_admin_required"));
    }
    Ok(())
}
async fn admin_status(State(a): State<Access>, h: HeaderMap) -> Result<Json<Value>, Failure> {
    admin(&a, &h)?;
    let ts = if let Some(ts) = &a.tailscale {
        ts.status().await
    } else {
        json!({"available":false})
    };
    let web = if let Some(web) = &a.web {
        web.status(&a).await
    } else {
        Value::Null
    };
    let i = a.inner.lock().unwrap();
    Ok(Json(
        json!({"origin":a.origin(),"enabled":i.enabled,"tailscale":ts,"web":web,"token":i.credentials.token,"scope":"owner_instance"}),
    ))
}
async fn admin_command(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Json<Value>, Failure> {
    admin(&a, &h)?;
    let action = v["action"].as_str().ok_or_else(invalid)?;
    if matches!(action, "lan" | "tailscale" | "https") {
        let web = a.web.as_ref().ok_or_else(invalid)?;
        let enabled = v["enabled"].as_bool().ok_or_else(invalid)?;
        web.configure(&a, action, enabled)
            .await
            .map_err(|e| fail(StatusCode::BAD_REQUEST, &e.to_string()))?;
        return Ok(Json(json!({"ok":true})));
    }
    if action == "connect" || action == "disconnect" {
        let ts = a
            .tailscale
            .as_ref()
            .ok_or_else(|| fail(StatusCode::BAD_REQUEST, "tailscale_unavailable"))?;
        if action == "disconnect" {
            if let Some(web) = &a.web {
                web.disconnect(&a)
                    .await
                    .map_err(|e| fail(StatusCode::BAD_GATEWAY, &e.to_string()))?;
            } else {
                set_enabled(&a, false)?;
                ts.disconnect()
                    .await
                    .map_err(|_| fail(StatusCode::BAD_GATEWAY, "tailscale_disconnect_failed"))?;
            }
        } else {
            if let Some(web) = &a.web {
                web.connect(&a)
                    .await
                    .map_err(|e| fail(StatusCode::BAD_GATEWAY, &e.to_string()))?;
            } else {
                ts.connect()
                    .await
                    .map_err(|e| fail(StatusCode::BAD_GATEWAY, &e.to_string()))?;
            }
        }
        return Ok(Json(json!({"ok":true})));
    }
    if matches!(action, "enable" | "disable")
        && let Some(web) = &a.web
    {
        web.configure(&a, "tailscale", action == "enable")
            .await
            .map_err(|e| fail(StatusCode::BAD_REQUEST, &e.to_string()))?;
        if action == "disable" {
            web.configure(&a, "lan", false)
                .await
                .map_err(|e| fail(StatusCode::BAD_REQUEST, &e.to_string()))?;
        }
        return Ok(Json(json!({"ok":true})));
    }
    if action == "enable" {
        if let Some(ts) = &a.tailscale {
            let origin = ts
                .enable()
                .await
                .map_err(|e| fail(StatusCode::BAD_GATEWAY, &e.to_string()))?;
            *a.origin.write().unwrap() = origin;
        }
        set_enabled(&a, true)?;
    } else if action == "disable" {
        set_enabled(&a, false)?;
    } else if action == "token" || action == "regenerate_token" {
        let mut i = a.inner.lock().unwrap();
        if action == "regenerate_token" {
            let previous = i.credentials.clone();
            i.credentials.token = Access::generate_token()
                .map_err(|_| fail(StatusCode::INTERNAL_SERVER_ERROR, "token_generation_failed"))?;
            i.credentials.subscriptions.clear();
            if a.persist(&i).is_err() {
                i.credentials = previous;
                return Err(fail(StatusCode::INTERNAL_SERVER_ERROR, "token_save_failed"));
            }
        }
        let token = i.credentials.token.clone();
        drop(i);
        if action == "regenerate_token" {
            a.desktops.lock().unwrap().clear();
            a.foreground.lock().unwrap().clear();
        }
        return Ok(Json(
            json!({"token":token,"connection_url":format!("{}/#access_token={token}",a.origin())}),
        ));
    } else {
        return Err(invalid());
    }
    Ok(Json(json!({"ok":true})))
}
fn set_enabled(a: &Access, enabled: bool) -> Result<(), Failure> {
    let mut i = a.inner.lock().unwrap();
    let old = i.credentials.disabled;
    i.credentials.disabled = !enabled;
    if a.persist(&i).is_err() {
        i.credentials.disabled = old;
        return Err(fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "policy_save_failed",
        ));
    }
    i.enabled = enabled;
    a.policy.send_replace(enabled);
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_top_level_public_shell_navigation_can_be_cross_site() {
        for (method, path, dest, expected) in [
            ("GET", "/?page=chat", "document", true),
            ("GET", "/api/v1/snapshot", "document", false),
            ("GET", "/api/v1/tasks/id/file-download", "document", false),
            ("GET", "/", "iframe", false),
            ("POST", "/", "document", false),
        ] {
            let req = axum::http::Request::builder()
                .method(method)
                .uri(path)
                .header("sec-fetch-mode", "navigate")
                .header("sec-fetch-dest", dest)
                .body(Body::empty())
                .unwrap();
            assert_eq!(public_shell_navigation(&req), expected);
        }
    }
    #[test]
    fn allowlist_excludes_management_and_terminal() {
        assert!(allowed(&Method::POST, "/api/v1/messages"));
        assert!(allowed(&Method::POST, "/api/v1/machine-connections"));
        assert!(!allowed(&Method::POST, "/api/v1/runtime/control"));
        assert!(!allowed(
            &Method::POST,
            "/api/v1/assistants/main/machine-connections"
        ));
        assert!(allowed(&Method::GET, "/api/v1/tasks/id"));
        assert!(allowed(
            &Method::GET,
            "/api/v1/assistants/main/tasks/id/file-download"
        ));
        assert!(allowed(
            &Method::GET,
            "/api/v1/assistants/main/tasks/id/files"
        ));
        assert!(!allowed(&Method::POST, "/api/v1/tasks/id/files"));
        for p in [
            "/api/v1/execution",
            "/api/v1/terminals",
            "/api/v1/providers",
            "/api/v1/access",
            "/api/v1/authorization/start",
            "/api/v1/sandboxes",
        ] {
            assert!(!allowed(&Method::POST, p), "{p}");
        }
        assert!(allowed(
            &Method::POST,
            "/api/v1/assistants/00000000-0000-0000-0000-000000000001/messages"
        ));
        assert!(allowed(
            &Method::GET,
            "/api/v1/assistants/00000000-0000-0000-0000-000000000001/agent"
        ));
        for suffix in [
            "providers",
            "execution",
            "terminals",
            "assistants/b/messages",
        ] {
            assert!(!allowed(
                &Method::POST,
                &format!("/api/v1/assistants/00000000-0000-0000-0000-000000000001/{suffix}")
            ));
        }
        assert!(!allowed(&Method::GET, "/api/v1/terminals"));
        assert!(!allowed(&Method::POST, "/api/v1/tasks/id/steer"));
    }
}
