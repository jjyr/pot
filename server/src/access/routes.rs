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
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use ring::{digest, signature};
use serde_json::{Value, json};
use std::time::{Duration, Instant};
use tower::ServiceExt;
use uuid::Uuid;
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
        .route("/access/pair/start", post(pair_start))
        .route("/access/pair/finish", post(pair_finish))
        .route("/access/login/start", post(login_start))
        .route("/access/login/finish", post(login_finish))
        .route("/access/logout", post(logout))
        .route("/access/push", post(super::push::subscribe))
        .route("/access/push/remove", post(super::push::unsubscribe))
        .route("/access/push/preferences", post(super::push::preferences))
        .route("/access/push/heartbeat", post(super::push::heartbeat))
        .route("/api/{*path}", get(proxy).post(proxy))
        .route("/desktop/{*path}", get(proxy).post(proxy))
        .route("/desktop-assets/{*path}", get(proxy))
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
    let desktop = req.uri().path().starts_with("/desktop/");
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
        if k == "content-security-policy" && desktop {
            continue;
        }
        r.headers_mut().insert(k, HeaderValue::from_static(v));
    }
    r
}
async fn status(State(a): State<Access>, h: HeaderMap) -> Json<Value> {
    let authenticated = cookie(&h, SESSION_COOKIE).is_some_and(|s| a.valid_session(&s, false));
    let i = a.inner.lock().unwrap();
    Json(
        json!({"authenticated":authenticated,"enrollment_open":i.pairing.as_ref().is_some_and(|e|e.expires>Instant::now()),"scope":"owner_instance","desktop_available":true,"server_id":i.credentials.server_id,"api_version":pot_protocol::API_VERSION,"push_available":a.push.is_some() && a.notifications.is_some(),"push_key":a.push.as_ref().map(|p|&p.public_key),"subscription":if authenticated{cookie(&h,"__Host-pot-installation").and_then(|id|i.credentials.subscriptions.get(&id)).map(|s|json!({"enabled":true,"private_preview":s.private_preview,"locale":s.locale,"failures":s.failures}))}else{None}}),
    )
}
async fn pair_start(State(a): State<Access>, Json(body): Json<Value>) -> Result<Response, Failure> {
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    if i.pairing.is_none() {
        return Err(fail(StatusCode::FORBIDDEN, "enrollment_closed"));
    }
    let public_key = body["public_key"].as_str().ok_or_else(invalid)?;
    let bytes = URL_SAFE_NO_PAD.decode(public_key).map_err(|_| invalid())?;
    if bytes.len() != 65 || bytes[0] != 4 {
        return Err(invalid());
    }
    challenge(
        &a,
        &mut i,
        Uuid::new_v4().to_string(),
        public_key.into(),
        body["name"]
            .as_str()
            .unwrap_or("Browser")
            .chars()
            .take(64)
            .collect(),
        true,
    )
}
fn challenge(
    a: &Access,
    i: &mut Inner,
    key: String,
    public_key: String,
    name: String,
    pairing: bool,
) -> Result<Response, Failure> {
    if i.challenges.len() >= 32 {
        return Err(fail(StatusCode::TOO_MANY_REQUESTS, "challenge_limit"));
    }
    let id = Uuid::new_v4().to_string();
    let message = format!(
        "Pot client authorization\n{}\n{}\n{}\n{}\n{}",
        a.origin,
        if pairing { "pair" } else { "login" },
        key,
        id,
        i.credentials.server_id
    );
    i.challenges.insert(
        id.clone(),
        Challenge {
            expires: Access::expiry(),
            message: message.clone(),
            key: key.clone(),
            name,
            public_key,
            pairing,
        },
    );
    Ok(response(
        json!({"challenge":message,"client_id":key,"server_id":i.credentials.server_id}),
        Some((CEREMONY_COOKIE, id, 300)),
    ))
}
fn verify(c: &Challenge, v: &Value) -> Result<(), Failure> {
    let key = URL_SAFE_NO_PAD
        .decode(&c.public_key)
        .map_err(|_| invalid())?;
    let sig = URL_SAFE_NO_PAD
        .decode(v["signature"].as_str().ok_or_else(invalid)?)
        .map_err(|_| invalid())?;
    signature::UnparsedPublicKey::new(&signature::ECDSA_P256_SHA256_FIXED, key)
        .verify(c.message.as_bytes(), &sig)
        .map_err(|_| invalid())
}
async fn pair_finish(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Response, Failure> {
    let id = cookie(&h, CEREMONY_COOKIE).ok_or_else(invalid)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let c = i.challenges.remove(&id).ok_or_else(invalid)?;
    if !c.pairing {
        return Err(invalid());
    }
    verify(&c, &v)?;
    let code = v["code"]
        .as_str()
        .unwrap_or("")
        .replace('-', "")
        .to_uppercase();
    let hash = digest::digest(&digest::SHA256, code.as_bytes());
    let p = i
        .pairing
        .as_mut()
        .ok_or_else(|| fail(StatusCode::FORBIDDEN, "enrollment_closed"))?;
    p.attempts += 1;
    // Fixed-size hashes avoid a comparison that leaks the matching code prefix.
    let matches = hash
        .as_ref()
        .iter()
        .zip(&p.digest)
        .fold(0u8, |diff, (a, b)| diff | (a ^ b))
        == 0;
    if !matches {
        if p.attempts >= 5 {
            i.pairing = None;
        }
        return Err(fail(StatusCode::FORBIDDEN, "invalid_pairing_code"));
    }
    if i.credentials.keys.len() >= 64 {
        return Err(fail(StatusCode::TOO_MANY_REQUESTS, "credential_limit"));
    }
    let key = c.key.clone();
    i.credentials.keys.insert(
        key.clone(),
        Credential {
            name: c.name,
            public_key: c.public_key,
        },
    );
    if a.persist(&i).is_err() {
        i.credentials.keys.remove(&key);
        return Err(fail(
            StatusCode::INTERNAL_SERVER_ERROR,
            "credential_save_failed",
        ));
    }
    i.pairing = None;
    let session = Access::session(&mut i, key.clone());
    Ok(response(
        json!({"authenticated":true,"client_id":key}),
        Some((SESSION_COOKIE, session, 43200)),
    ))
}
async fn login_start(State(a): State<Access>, Json(v): Json<Value>) -> Result<Response, Failure> {
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let key = v["client_id"].as_str().ok_or_else(invalid)?.to_owned();
    let c = i
        .credentials
        .keys
        .get(&key)
        .ok_or_else(|| fail(StatusCode::UNAUTHORIZED, "client_revoked"))?;
    let (public_key, name) = (c.public_key.clone(), c.name.clone());
    challenge(&a, &mut i, key, public_key, name, false)
}
async fn login_finish(
    State(a): State<Access>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> Result<Response, Failure> {
    let id = cookie(&h, CEREMONY_COOKIE).ok_or_else(invalid)?;
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    let c = i.challenges.remove(&id).ok_or_else(invalid)?;
    if c.pairing || !i.credentials.keys.contains_key(&c.key) {
        return Err(invalid());
    }
    verify(&c, &v)?;
    let session = Access::session(&mut i, c.key);
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
async fn proxy(State(a): State<Access>, mut req: Request) -> Result<Response, Failure> {
    let session = cookie(req.headers(), SESSION_COOKIE)
        .ok_or_else(|| fail(StatusCode::UNAUTHORIZED, "login_required"))?;
    if !a.valid_session(&session, true) {
        return Err(fail(StatusCode::UNAUTHORIZED, "session_expired"));
    }
    let desktop_open = req.uri().path() == "/api/v1/desktop/sessions";
    // Never forward client-provided authorization, cookies or native-only route access.
    req.headers_mut().remove("cookie");
    req.headers_mut().remove("origin");
    req.headers_mut().insert(
        "authorization",
        HeaderValue::from_str(&format!("Bearer {}", a.root_token)).unwrap(),
    );
    let runtime = a.runtime.clone().merge(admin_router(a.clone()));
    let r = runtime
        .oneshot(req)
        .await
        .map_err(|_| fail(StatusCode::BAD_GATEWAY, "runtime_unavailable"))?;
    if desktop_open && r.status().is_success() {
        let bytes = axum::body::to_bytes(r.into_body(), 1024 * 1024)
            .await
            .map_err(|_| fail(StatusCode::BAD_GATEWAY, "invalid_desktop"))?;
        let value: Value = serde_json::from_slice(&bytes)
            .map_err(|_| fail(StatusCode::BAD_GATEWAY, "invalid_desktop"))?;
        if let Some(id) = value["session"]["id"].as_str() {
            let id = id.to_owned();
            let access = a.clone();
            let session = session.clone();
            tokio::spawn(async move {
                loop {
                    tokio::time::sleep(Duration::from_secs(1)).await;
                    if !access.valid_session(&session, false) {
                        if let Some(service) = &access.notifications {
                            service.close_access_desktop(&id).await;
                        }
                        break;
                    }
                    if let Some(service) = &access.notifications
                        && !service.access_desktop_exists(&id)
                    {
                        break;
                    }
                }
            });
        }
        return Ok(Json(value).into_response());
    }
    // Upgraded sockets own their transport and are closed by desktop ticket revocation.
    if r.status() == StatusCode::SWITCHING_PROTOCOLS {
        return Ok(r);
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
    let mut i = a.inner.lock().unwrap();
    Access::cleanup(&mut i);
    Ok(Json(
        json!({"origin":a.origin,"enabled":i.enabled,"push_available":a.push.is_some(),"subscriptions":i.credentials.subscriptions.iter().map(|(id,s)|json!({"id":id,"credential":s.credential,"failures":s.failures,"paused":s.failures>=7})).collect::<Vec<_>>(),"scope":"owner_instance","enrollment_open":i.pairing.as_ref().is_some_and(|e|e.expires>Instant::now()),"credentials":i.credentials.keys.iter().map(|(id,c)|json!({"id":id,"name":c.name})).collect::<Vec<_>>(),"sessions":i.sessions.iter().map(|(id,s)|json!({"id":id,"credential":s.key})).collect::<Vec<_>>()}),
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
            let code = Uuid::new_v4().simple().to_string()[..12].to_uppercase();
            i.pairing = Some(Pairing {
                digest: digest::digest(&digest::SHA256, code.as_bytes())
                    .as_ref()
                    .to_vec(),
                expires: Access::expiry(),
                attempts: 0,
            });
            return Ok(Json(
                json!({"code":format!("{}-{}-{}",&code[..4],&code[4..8],&code[8..]),"expires_in":300,"origin":a.origin}),
            ));
        }
        Some("close_enrollment") => {
            i.pairing = None;
            i.challenges.retain(|_, c| !c.pairing);
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
            i.challenges.clear();
            i.pairing = None;
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
