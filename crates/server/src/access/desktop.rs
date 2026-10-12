//! Browser desktop tickets are bound to one authenticated login session.
use super::{
    routes::{Failure, fail},
    state::*,
};
use axum::{
    body::Body,
    extract::{Path, Request, State},
    http::{HeaderValue, Method, StatusCode},
    response::{IntoResponse, Response},
};
use serde_json::Value;
use std::{
    sync::Arc,
    time::{Duration, Instant},
};
use tower::ServiceExt;

fn session(a: &Access, req: &Request) -> Result<String, Failure> {
    let id = super::state::cookie(req.headers(), SESSION_COOKIE)
        .ok_or_else(|| fail(StatusCode::UNAUTHORIZED, "login_required"))?;
    if !a.valid_session(&id, true) {
        return Err(fail(StatusCode::UNAUTHORIZED, "session_expired"));
    }
    Ok(id)
}
fn owns(a: &Access, ticket: &str, session: &str) -> bool {
    a.desktops
        .lock()
        .unwrap()
        .get(ticket)
        .is_some_and(|ticket| {
            ticket.owner == session
                && ticket.live()
                && Arc::ptr_eq(&ticket.endpoint, &a.endpoint_epoch)
        })
        && a.valid_session(session, false)
}
pub(super) async fn proxy(
    State(mut a): State<Access>,
    mut req: Request,
) -> Result<Response, Failure> {
    if a.fixed_control {
        return Err(fail(StatusCode::NOT_IMPLEMENTED, "remote_desktop_pending"));
    }
    a.capture_endpoint();
    let session = session(&a, &req)?;
    let opening = req.uri().path() == "/api/v1/desktop/sessions" && req.method() == Method::POST;
    let path = req.uri().path().to_owned();
    let parts = path.split('/').collect::<Vec<_>>();
    let ticket = if opening {
        String::new()
    } else {
        if parts.len() < 4 || parts[1] != "desktop" || !owns(&a, parts[2], &session) {
            return Err(fail(StatusCode::FORBIDDEN, "desktop_session"));
        }
        let suffix = parts[3..].join("/");
        let allowed = match *req.method() {
            Method::GET => matches!(
                suffix.as_str(),
                "" | "rfb" | "api/status" | "api/metrics" | "i18n.js" | "locales.json"
            ),
            Method::POST => matches!(suffix.as_str(), "api/control" | "api/browser"),
            _ => false,
        };
        if !allowed {
            return Err(fail(StatusCode::FORBIDDEN, "route_not_allowed"));
        }
        parts[2].to_owned()
    };
    let _opening_guard = if opening {
        Some(a.desktop_open.lock().await)
    } else {
        None
    };
    if !a.valid_session(&session, false) {
        return Err(fail(StatusCode::UNAUTHORIZED, "session_expired"));
    }
    let mut closing = false;
    if !opening && req.method() == Method::POST && path.ends_with("/api/control") {
        let body = std::mem::replace(req.body_mut(), Body::empty());
        let bytes = axum::body::to_bytes(body, 1024 * 1024)
            .await
            .map_err(|_| fail(StatusCode::BAD_REQUEST, "invalid_control"))?;
        closing = serde_json::from_slice::<Value>(&bytes).is_ok_and(|v| v["action"] == "close");
        *req.body_mut() = Body::from(bytes);
    }
    if opening {
        let mut tickets = a.desktops.lock().unwrap();
        tickets.retain(|_, ticket| ticket.live() && a.valid_session(&ticket.owner, false));
        if tickets.len() >= 64
            || tickets
                .values()
                .filter(|ticket| ticket.owner == session)
                .count()
                >= 16
        {
            return Err(fail(StatusCode::TOO_MANY_REQUESTS, "desktop_limit"));
        }
    }
    let upgrade = req.extensions_mut().remove::<hyper::upgrade::OnUpgrade>();
    req.extensions_mut().clear();
    if let Some(upgrade) = upgrade {
        req.extensions_mut().insert(upgrade);
    }
    if !opening {
        let access = a.clone();
        let owner = session.clone();
        let id = ticket.clone();
        req.extensions_mut()
            .insert(pot_control::desktop::DesktopAccessGuard(Arc::new(
                move || owns(&access, &id, &owner),
            )));
    }
    req.headers_mut().remove("cookie");
    // The public boundary has already checked the configured exact Origin.
    req.headers_mut().remove("origin");
    req.headers_mut().insert(
        "authorization",
        HeaderValue::from_str(&format!("Bearer {}", a.root_token)).unwrap(),
    );
    let r = a
        .runtime
        .clone()
        .oneshot(req)
        .await
        .map_err(|_| fail(StatusCode::BAD_GATEWAY, "runtime_unavailable"))?;
    if closing && r.status().is_success() {
        a.desktops.lock().unwrap().remove(&ticket);
    }
    if !opening || !r.status().is_success() {
        return Ok(r);
    }
    let (parts, body) = r.into_parts();
    let bytes = axum::body::to_bytes(body, 1024 * 1024)
        .await
        .map_err(|_| fail(StatusCode::BAD_GATEWAY, "invalid_desktop"))?;
    let value: Value = serde_json::from_slice(&bytes)
        .map_err(|_| fail(StatusCode::BAD_GATEWAY, "invalid_desktop"))?;
    if let Some(id) = value["session"]["id"].as_str() {
        // A revocation during startup must not create authority for an expired login.
        if !a.valid_session(&session, false) {
            return Err(fail(StatusCode::UNAUTHORIZED, "session_expired"));
        }
        a.desktops.lock().unwrap().insert(
            id.into(),
            DesktopTicket {
                owner: session,
                expires: Instant::now() + Duration::from_secs(3600),
                endpoint: a.endpoint_epoch.clone(),
                generation: a.endpoint_generation(),
            },
        );
    }
    Ok(Response::from_parts(parts, Body::from(bytes)))
}
pub(super) async fn asset(
    State(a): State<Access>,
    Path(path): Path<String>,
    req: Request,
) -> Result<Response, Failure> {
    session(&a, &req)?;
    if !path.ends_with(".js")
        || path
            .split('/')
            .any(|p| p.is_empty() || p == "." || p == ".." || p.contains('\\'))
    {
        return Err(fail(StatusCode::NOT_FOUND, "desktop_asset"));
    }
    let name = format!("desktop-assets/{path}");
    if let Some(response) =
        super::assets::response(&name, "text/javascript; charset=utf-8", req.headers())
    {
        return Ok(response);
    }
    let data = {
        let root = a
            .assets
            .join("desktop-assets")
            .canonicalize()
            .map_err(|_| fail(StatusCode::NOT_FOUND, "desktop_asset"))?;
        let file = root
            .join(path)
            .canonicalize()
            .map_err(|_| fail(StatusCode::NOT_FOUND, "desktop_asset"))?;
        if !file.starts_with(root) {
            return Err(fail(StatusCode::NOT_FOUND, "desktop_asset"));
        }
        tokio::fs::read(file)
            .await
            .map_err(|_| fail(StatusCode::NOT_FOUND, "desktop_asset"))?
    };
    Ok(([("content-type", "text/javascript; charset=utf-8")], data).into_response())
}
