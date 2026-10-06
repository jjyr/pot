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
use std::time::{Duration, Instant};
use tower::ServiceExt;
use uuid::Uuid;
use webauthn_rs::prelude::*;
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
    h.get("origin").and_then(|v| v.to_str().ok()) == Some(a.origin.as_str())
}

pub fn public_router(access: Access) -> Router {
    Router::new()
        .route("/access/status", get(status))
        .route("/access/enroll", post(enroll))
        .route("/access/enroll/status", get(enrollment_status))
        .route("/access/register/start", post(register_start))
        .route("/access/register/finish", post(register_finish))
        .route("/access/login/start", post(login_start))
        .route("/access/login/finish", post(login_finish))
        .route("/access/logout", post(logout))
        .route("/access/push", post(super::push::subscribe))
        .route("/access/push/remove", post(super::push::unsubscribe))
        .route("/access/push/preferences", post(super::push::preferences))
        .route("/access/push/heartbeat", post(super::push::heartbeat))
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
async fn boundary(State(a): State<Access>, req: Request, next: Next) -> Response {
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
    {
        return fail(StatusCode::FORBIDDEN, "cross_site").into_response();
    }
    if req.method() == Method::POST && req.uri().path().starts_with("/access/") && !a.rate_limit() {
        return fail(StatusCode::TOO_MANY_REQUESTS, "rate_limited").into_response();
    }
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
    r
}
async fn status(State(a): State<Access>, h: HeaderMap) -> Json<Value> {
    let authenticated = cookie(&h, SESSION_COOKIE).is_some_and(|s| a.valid_session(&s, false));
    let i = a.inner.lock().unwrap();
    Json(
        json!({"authenticated":authenticated,"enrollment_open":i.enrollment_until.is_some_and(|e|e>Instant::now()),"scope":"owner_instance","desktop_available":false,"push_available":a.push.is_some() && a.notifications.is_some(),"push_key":a.push.as_ref().map(|p|&p.public_key),"subscription":if authenticated{cookie(&h,"__Host-pot-installation").and_then(|id|i.credentials.subscriptions.get(&id)).map(|s|json!({"enabled":true,"private_preview":s.private_preview,"locale":s.locale,"failures":s.failures}))}else{None}}),
    )
}
async fn enroll(State(a): State<Access>, Json(body): Json<Value>) -> Result<Response, Failure> {
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    if !i.enrollment_until.is_some_and(|e| e > Instant::now()) {
        return Err(fail(StatusCode::FORBIDDEN, "enrollment_closed"));
    }
    if i.pending.len() >= 16 {
        return Err(fail(StatusCode::TOO_MANY_REQUESTS, "pending_limit"));
    }
    let id = Uuid::new_v4().to_string();
    let code = Uuid::new_v4().simple().to_string()[..8].to_uppercase();
    let name = body["name"]
        .as_str()
        .unwrap_or("Phone")
        .chars()
        .take(64)
        .collect();
    i.pending.insert(
        id.clone(),
        Enrollment {
            code: code.clone(),
            expires: Access::expiry(),
            approved: false,
            name,
        },
    );
    Ok(response(
        json!({"code":code,"expires_in":300}),
        Some((CEREMONY_COOKIE, id, 300)),
    ))
}
async fn enrollment_status(State(a): State<Access>, h: HeaderMap) -> Result<Json<Value>, Failure> {
    let id = cookie(&h, CEREMONY_COOKIE).ok_or_else(invalid)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let p = i.pending.get(&id).ok_or_else(invalid)?;
    Ok(Json(json!({"approved":p.approved,"code":p.code})))
}
async fn register_start(State(a): State<Access>, h: HeaderMap) -> Result<Response, Failure> {
    let id = cookie(&h, CEREMONY_COOKIE).ok_or_else(invalid)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let p = i.pending.get(&id).ok_or_else(invalid)?;
    if !p.approved {
        return Err(fail(StatusCode::FORBIDDEN, "approval_required"));
    }
    let name = p.name.clone();
    let exclude = i
        .credentials
        .keys
        .values()
        .map(|c| c.passkey.cred_id().clone())
        .collect();
    if i.credentials.keys.len() >= 64 {
        return Err(fail(StatusCode::TOO_MANY_REQUESTS, "credential_limit"));
    }
    let (mut options, state) = a
        .webauthn
        .start_passkey_registration(Uuid::new_v4(), &name, &name, Some(exclude))
        .map_err(|_| invalid())?;
    if let Some(selection) = options.public_key.authenticator_selection.as_mut() {
        selection.resident_key = Some(
            serde_json::from_value(json!("preferred")).expect("valid resident key preference"),
        );
    }
    i.pending.remove(&id);
    i.challenges.insert(
        id.clone(),
        Challenge {
            expires: Access::expiry(),
            ceremony: Ceremony::Register { state, name },
        },
    );
    Ok(response(
        serde_json::to_value(options).map_err(|_| invalid())?,
        Some((CEREMONY_COOKIE, id, 300)),
    ))
}
async fn register_finish(
    State(a): State<Access>,
    h: HeaderMap,
    Json(credential): Json<RegisterPublicKeyCredential>,
) -> Result<Response, Failure> {
    let id = cookie(&h, CEREMONY_COOKIE).ok_or_else(invalid)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let c = i.challenges.remove(&id).ok_or_else(invalid)?;
    let Ceremony::Register { state, name } = c.ceremony else {
        return Err(invalid());
    };
    let passkey = a
        .webauthn
        .finish_passkey_registration(&credential, &state)
        .map_err(|_| invalid())?;
    let key = Uuid::new_v4().to_string();
    i.credentials
        .keys
        .insert(key.clone(), Credential { name, passkey });
    if a.persist(&i).is_err() {
        i.credentials.keys.remove(&key);
        return Err(fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "credential_save_failed",
        ));
    }
    let session = Access::session(&mut i, key);
    Ok(response(
        json!({"authenticated":true}),
        Some((SESSION_COOKIE, session, 43200)),
    ))
}
async fn login_start(State(a): State<Access>) -> Result<Response, Failure> {
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    if i.challenges.len() >= 32 {
        return Err(fail(StatusCode::TOO_MANY_REQUESTS, "challenge_limit"));
    }
    if i.credentials.keys.is_empty() {
        return Err(fail(StatusCode::UNAUTHORIZED, "passkey_required"));
    }
    let keys = i
        .credentials
        .keys
        .values()
        .map(|c| c.passkey.clone())
        .collect::<Vec<_>>();
    let (options, state) = a
        .webauthn
        .start_passkey_authentication(&keys)
        .map_err(|_| fail(StatusCode::UNAUTHORIZED, "passkey_required"))?;
    let id = Uuid::new_v4().to_string();
    i.challenges.insert(
        id.clone(),
        Challenge {
            expires: Access::expiry(),
            ceremony: Ceremony::Login(state),
        },
    );
    Ok(response(
        serde_json::to_value(options).map_err(|_| invalid())?,
        Some((CEREMONY_COOKIE, id, 300)),
    ))
}
async fn login_finish(
    State(a): State<Access>,
    h: HeaderMap,
    Json(credential): Json<PublicKeyCredential>,
) -> Result<Response, Failure> {
    let id = cookie(&h, CEREMONY_COOKIE).ok_or_else(invalid)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let c = i.challenges.remove(&id).ok_or_else(invalid)?;
    let Ceremony::Login(state) = c.ceremony else {
        return Err(invalid());
    };
    let result = a
        .webauthn
        .finish_passkey_authentication(&credential, &state)
        .map_err(|_| invalid())?;
    let key = i
        .credentials
        .keys
        .iter()
        .find(|(_, c)| c.passkey.cred_id() == result.cred_id())
        .map(|(k, _)| k.clone())
        .ok_or_else(invalid)?;
    i.credentials
        .keys
        .get_mut(&key)
        .unwrap()
        .passkey
        .update_credential(&result);
    a.persist(&i)
        .map_err(|_| fail(StatusCode::INTERNAL_SERVER_ERROR, "credential_save_failed"))?;
    let session = Access::session(&mut i, key);
    Ok(response(
        json!({"authenticated":true}),
        Some((SESSION_COOKIE, session, 43200)),
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
    if let Some(s) = cookie(&h, SESSION_COOKIE) {
        a.inner.lock().unwrap().sessions.remove(&s);
    }
    response(
        json!({"authenticated":false}),
        Some((SESSION_COOKIE, String::new(), 0)),
    )
}
fn allowed(method: &Method, path: &str) -> bool {
    if *method == Method::GET
        && matches!(
            path,
            "/api/v1/snapshot"
                | "/api/v1/events"
                | "/api/v1/tasks"
                | "/api/v1/tasks/query"
                | "/api/v1/sandboxes"
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
        return (*method == Method::GET && parts.len() == 5)
            || (*method == Method::POST
                && parts.len() == 6
                && matches!(parts[5], "messages" | "cancel"));
    }
    false
}
async fn proxy(State(a): State<Access>, mut req: Request) -> Result<Response, Failure> {
    let session = cookie(req.headers(), SESSION_COOKIE)
        .ok_or_else(|| fail(StatusCode::UNAUTHORIZED, "login_required"))?;
    if !a.valid_session(&session, true) {
        return Err(fail(StatusCode::UNAUTHORIZED, "session_expired"));
    }
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
        if !a.valid_session(&session, false) {
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
                _=tick.tick()=>{ if !a.valid_session(&session,false) {break;} }
                item=stream.next()=> { match item {Some(item)=> {if !a.valid_session(&session,false){break;} yield item;},None=>break} }
            }
        }
    };
    Ok(Response::from_parts(parts, Body::from_stream(guarded)))
}
fn public_inventory(value: &Value) -> Value {
    let items=value["items"].as_array().map(|items|items.iter().map(|item|{
        json!({"id":item["id"],"name":item["name"],"os":item["os"],
            "running_tasks":item["running_tasks"].as_u64(),"active_leases":item["active_leases"].as_u64(),
            "state":{"phase":item["state"]["phase"].as_str().unwrap_or("unknown"),"generation":item["state"]["generation"].as_u64()},
            "config":{"cpus":item["config"]["cpus"].as_u64(),"memory_gib":item["config"]["memory_gib"].as_u64(),"disk_gib":item["config"]["disk_gib"].as_u64()}})
    }).collect::<Vec<_>>()).unwrap_or_default();
    json!({"selected":value["selected"],"items":items})
}
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
    match tokio::fs::read(a.assets.join(path)).await {
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
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    Ok(Json(
        json!({"origin":a.origin,"enabled":i.enabled,"push_available":a.push.is_some(),"subscriptions":i.credentials.subscriptions.iter().map(|(id,s)|json!({"id":id,"credential":s.credential,"failures":s.failures,"paused":s.failures>=7})).collect::<Vec<_>>(),"scope":"owner_instance","enrollment_open":i.enrollment_until.is_some_and(|e|e>Instant::now()),"pending":i.pending.iter().map(|(id,p)|json!({"id":id,"code":p.code,"name":p.name,"approved":p.approved})).collect::<Vec<_>>(),"credentials":i.credentials.keys.iter().map(|(id,c)|json!({"id":id,"name":c.name})).collect::<Vec<_>>(),"sessions":i.sessions.iter().map(|(id,s)|json!({"id":id,"credential":s.key})).collect::<Vec<_>>()}),
    ))
}
async fn admin_command(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Json<Value>, Failure> {
    admin(&a, &h)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    match v["action"].as_str() {
        Some("pair") => {
            if !i.enabled {
                return Err(fail(StatusCode::FORBIDDEN, "access_disabled"));
            }
            i.enrollment_until = Some(Access::expiry());
        }
        Some("approve") => {
            let p = i
                .pending
                .get_mut(v["id"].as_str().ok_or_else(invalid)?)
                .ok_or_else(invalid)?;
            p.approved = true;
        }
        Some("close_enrollment") => {
            i.enrollment_until = None;
            i.pending.clear();
            i.challenges
                .retain(|_, c| matches!(c.ceremony, Ceremony::Login(_)));
        }
        Some("revoke_session") => {
            i.sessions.remove(v["id"].as_str().ok_or_else(invalid)?);
        }
        Some("revoke_credential") => {
            let id = v["id"].as_str().ok_or_else(invalid)?;
            let subscriptions = i.credentials.subscriptions.clone();
            i.credentials
                .subscriptions
                .retain(|_, s| s.credential != id);
            let old = i.credentials.keys.remove(id);
            if a.persist(&i).is_err() {
                i.credentials.subscriptions = subscriptions;
                if let Some(c) = old {
                    i.credentials.keys.insert(id.into(), c);
                }
                return Err(fail(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "credential_save_failed",
                ));
            }
            i.sessions.retain(|_, s| s.key != id);
            i.challenges.clear();
        }
        Some("revoke_subscription") => {
            let id = v["id"].as_str().ok_or_else(invalid)?;
            let old = i.credentials.subscriptions.remove(id);
            if a.persist(&i).is_err() {
                if let Some(sub) = old {
                    i.credentials.subscriptions.insert(id.into(), sub);
                }
                return Err(fail(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "subscription_save_failed",
                ));
            }
        }
        Some("disable") => {
            let old = i.credentials.disabled;
            i.credentials.disabled = true;
            if a.persist(&i).is_err() {
                i.credentials.disabled = old;
                return Err(fail(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "policy_save_failed",
                ));
            }
            i.enabled = false;
            a.policy.send_replace(false);
            i.sessions.clear();
            i.pending.clear();
            i.challenges.clear();
            i.enrollment_until = None;
        }
        Some("enable") => {
            let old = i.credentials.disabled;
            i.credentials.disabled = false;
            if a.persist(&i).is_err() {
                i.credentials.disabled = old;
                return Err(fail(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "policy_save_failed",
                ));
            }
            i.enabled = true;
            a.policy.send_replace(true);
        }
        _ => return Err(invalid()),
    }
    Ok(Json(json!({"ok":true})))
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn allowlist_excludes_management_and_terminal() {
        assert!(allowed(&Method::POST, "/api/v1/messages"));
        assert!(allowed(&Method::GET, "/api/v1/tasks/id"));
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
        assert!(!allowed(&Method::GET, "/api/v1/terminals"));
        assert!(!allowed(&Method::POST, "/api/v1/tasks/id/steer"));
    }
}
