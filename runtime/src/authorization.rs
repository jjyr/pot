//! Ephemeral native-only browser authorization. Credentials stay with the official agent.
use crate::*;
use agent::LaunchSpec;
use std::time::{Duration, Instant};

#[derive(Default)]
pub(crate) struct Hub {
    attempt: Option<Attempt>,
}
struct Attempt {
    id: String,
    phase: String,
    url: String,
    code: String,
    callback: Option<reqwest::Url>,
    state: String,
    started: Instant,
    models: Vec<String>,
    cancel: Arc<tokio::sync::Notify>,
}
impl Attempt {
    fn public(&self) -> Value {
        json!({"attempt_id":self.id,"phase":self.phase,"authorization_url":self.url,
            "user_code":self.code,"callback_url":self.callback.as_ref().map(|u|u.as_str()),"models":self.models})
    }
    fn finish(&mut self, phase: &str) {
        self.phase = phase.into();
        self.url.clear();
        self.code.clear();
        self.callback = None;
        self.state.clear();
    }
}
fn native(s: &Service, h: &HeaderMap) -> ApiResult<()> {
    s.authorized(h)?;
    if s.is_worker || h.contains_key("origin") || s.stopping() {
        return Err(error(
            StatusCode::FORBIDDEN,
            "native_only",
            "Authorization is available only to the native app",
        ));
    }
    Ok(())
}
fn unavailable() -> (StatusCode, Json<ApiError>) {
    error(
        StatusCode::BAD_GATEWAY,
        "authorization_unavailable",
        "Official authorization is unavailable; retry or update the environment",
    )
}
fn browser_url(raw: &str) -> Result<(reqwest::Url, Option<reqwest::Url>, String)> {
    official_url(raw, "codex")
}
fn official_url(raw: &str, engine: &str) -> Result<(reqwest::Url, Option<reqwest::Url>, String)> {
    let url = reqwest::Url::parse(raw)?;
    let official = if engine == "grok" {
        matches!(url.host_str(), Some("auth.x.ai" | "accounts.x.ai"))
    } else {
        url.host_str() == Some("auth.openai.com")
    };
    anyhow::ensure!(
        url.scheme() == "https"
            && url.port_or_known_default() == Some(443)
            && official
            && url.username().is_empty()
            && url.password().is_none(),
        "Unsupported authorization URL"
    );
    let params: std::collections::HashMap<_, _> = url.query_pairs().into_owned().collect();
    let callback = params
        .get("redirect_uri")
        .map(|v| reqwest::Url::parse(v))
        .transpose()?;
    if let Some(u) = &callback {
        anyhow::ensure!(
            u.scheme() == "http"
                && matches!(u.host_str(), Some("localhost" | "127.0.0.1"))
                && u.port().is_some()
                && u.path()
                    == if engine == "grok" {
                        "/callback"
                    } else {
                        "/auth/callback"
                    }
                && u.query().is_none()
                && u.fragment().is_none()
                && u.username().is_empty()
                && u.password().is_none(),
            "Unsupported callback"
        );
    }
    Ok((
        url,
        callback,
        params.get("state").cloned().unwrap_or_default(),
    ))
}
#[derive(Deserialize)]
pub struct Start {
    engine: String,
    #[serde(default)]
    attempt_id: Option<String>,
    #[serde(default)]
    device: bool,
}
#[derive(Deserialize)]
pub struct ID {
    attempt_id: String,
}
#[derive(Deserialize)]
pub struct Callback {
    attempt_id: String,
    query: String,
}
pub async fn start(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<Start>,
) -> ApiResult<Json<Value>> {
    native(&s, &h)?;
    if !matches!(req.engine.as_str(), "codex" | "grok") {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "unsupported_engine",
            "Unsupported account authorization",
        ));
    }
    let attempt_id = req.attempt_id.clone().unwrap_or_else(id);
    if uuid::Uuid::parse_str(&attempt_id).is_err() {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_attempt",
            "Invalid authorization attempt identity",
        ));
    }
    let cancel = Arc::new(tokio::sync::Notify::new());
    {
        let mut hub = s.authorization.lock().await;
        if hub
            .attempt
            .as_ref()
            .is_some_and(|a| a.phase == "waiting" || a.id == attempt_id)
        {
            return Err(error(
                StatusCode::CONFLICT,
                "authorization_pending",
                "Cancel the current authorization or use a fresh attempt identity",
            ));
        }
        hub.attempt = Some(Attempt {
            id: attempt_id.clone(),
            phase: "waiting".into(),
            url: String::new(),
            code: String::new(),
            callback: None,
            state: String::new(),
            started: Instant::now(),
            models: vec![],
            cancel: cancel.clone(),
        });
    }
    let result = begin(s.clone(), req, attempt_id.clone(), cancel).await;
    if result.is_err() {
        let mut hub = s.authorization.lock().await;
        if let Some(a) = hub
            .attempt
            .as_mut()
            .filter(|a| a.id == attempt_id && a.phase == "waiting")
        {
            a.finish("failed");
        }
    }
    result
}
async fn begin(
    s: Service,
    req: Start,
    attempt_id: String,
    cancel: Arc<tokio::sync::Notify>,
) -> ApiResult<Json<Value>> {
    if req.engine == "grok" {
        return start_grok(s, req.device, attempt_id, cancel).await;
    }
    let host = s
        .codex
        .connect(&LaunchSpec::codex())
        .await
        .map_err(|_| unavailable())?;
    let mut events = host.events.subscribe();
    let reply = host
        .request(
            "account/login/start",
            json!({"type":if req.device {"chatgptDeviceCode"} else {"chatgpt"}}),
        )
        .await
        .map_err(|_| unavailable())?;
    let native_id = reply["loginId"]
        .as_str()
        .ok_or_else(unavailable)?
        .to_owned();
    let raw = reply[if req.device {
        "verificationUrl"
    } else {
        "authUrl"
    }]
    .as_str()
    .unwrap_or("");
    let (url, callback, state) = match browser_url(raw) {
        Ok(v) => v,
        Err(_) => {
            let _ = host
                .request("account/login/cancel", json!({"loginId":native_id}))
                .await;
            return Err(unavailable());
        }
    };
    if !req.device && (callback.is_none() || state.is_empty()) {
        let _ = host
            .request("account/login/cancel", json!({"loginId":native_id}))
            .await;
        return Err(unavailable());
    }
    let mut hub = s.authorization.lock().await;
    if !hub
        .attempt
        .as_ref()
        .is_some_and(|a| a.id == attempt_id && a.phase == "waiting")
    {
        drop(hub);
        let _ = host
            .request("account/login/cancel", json!({"loginId":native_id}))
            .await;
        return Ok(Json(json!({"attempt_id":attempt_id,"phase":"cancelled"})));
    }
    hub.attempt = Some(Attempt {
        id: attempt_id.clone(),
        phase: "waiting".into(),
        url: url.to_string(),
        code: reply["userCode"].as_str().unwrap_or("").into(),
        callback,
        state,
        started: Instant::now(),
        models: vec![],
        cancel: cancel.clone(),
    });
    let public = hub.attempt.as_ref().unwrap().public();
    drop(hub);
    tokio::spawn(async move {
        let phase = loop {
            tokio::select! {
                _ = cancel.notified() => break "cancelled",
                _ = s.stop_requested() => break "cancelled",
                _ = tokio::time::sleep_until(tokio::time::Instant::from_std({ let hub=s.authorization.lock().await; hub.attempt.as_ref().map(|a|a.started+Duration::from_secs(300)).unwrap_or(Instant::now()) })) => break "expired",
                event = events.recv() => match event {
                    Ok(v) if v["method"]=="account/login/completed" && v["params"]["loginId"]==native_id => break if v["params"]["success"]==true {"authenticated"} else {"failed"},
                    Ok(v) if v["method"]=="pot/hostLost" => break "failed",
                    Err(_) => break "failed", _ => {},
                }
            }
        };
        let mut models = vec![];
        let mut final_phase = phase;
        if phase == "authenticated" {
            match host
                .request("account/read", json!({"refreshToken":false}))
                .await
            {
                Ok(v) if !v["account"].is_null() => {}
                _ => final_phase = "failed",
            }
            if final_phase == "authenticated" {
                if let Ok(v) = host.request("model/list", json!({})).await {
                    models = v["data"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .filter_map(|m| {
                            m["model"]
                                .as_str()
                                .or_else(|| m["id"].as_str())
                                .map(str::to_owned)
                        })
                        .collect();
                }
            }
        } else {
            let _ = host
                .request("account/login/cancel", json!({"loginId":native_id}))
                .await;
        }
        let mut hub = s.authorization.lock().await;
        if let Some(a) = hub
            .attempt
            .as_mut()
            .filter(|a| a.id == attempt_id && a.phase == "waiting")
        {
            a.finish(final_phase);
            a.models = models;
        }
    });
    Ok(Json(public))
}
async fn start_grok(
    s: Service,
    device: bool,
    attempt_id: String,
    cancel: Arc<tokio::sync::Notify>,
) -> ApiResult<Json<Value>> {
    if device {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "device_unavailable",
            "Grok device authorization is not available through this integration; retry browser authorization",
        ));
    }
    let host = pot_driver::grok::connect()
        .await
        .map_err(|_| unavailable())?;
    let auth_host = host.clone();
    let mut authenticate = tokio::spawn(async move {
        auth_host.request_timeout("authenticate",json!({"methodId":"grok.com","_meta":{"headless":false,"use_oauth":true,"force_interactive":false,"request_seq":1}}),Duration::from_secs(300)).await
    });
    let deadline = tokio::time::Instant::now() + Duration::from_secs(30);
    let mut completed = false;
    let raw = loop {
        tokio::select! {
            _=cancel.notified()=>{let _=host.request("_x.ai/auth/cancel",json!({"request_seq":1})).await;authenticate.abort();return Ok(Json(json!({"attempt_id":attempt_id,"phase":"cancelled"})));},
            reply=&mut authenticate=>{
                if !matches!(reply,Ok(Ok(_))) {return Err(unavailable());}
                completed=true;break String::new();
            },
            _=tokio::time::sleep_until(deadline)=>{
                let _=host.request("_x.ai/auth/cancel",json!({"request_seq":1})).await;authenticate.abort();return Err(unavailable());
            },
            reply=host.request("_x.ai/auth/get_url",json!({}))=>{
                if let Ok(reply)=reply {if let Some(raw)=reply["auth_url"].as_str() {break raw.to_owned();}}
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
        }
    };
    let (url, callback, state) = if completed {
        (String::new(), None, String::new())
    } else {
        match official_url(&raw, "grok") {
            Ok((url, Some(callback), state)) if !state.is_empty() => {
                (url.to_string(), Some(callback), state)
            }
            _ => {
                let _ = host
                    .request("_x.ai/auth/cancel", json!({"request_seq":1}))
                    .await;
                authenticate.abort();
                return Err(unavailable());
            }
        }
    };
    let mut hub = s.authorization.lock().await;
    if !hub
        .attempt
        .as_ref()
        .is_some_and(|a| a.id == attempt_id && a.phase == "waiting")
    {
        drop(hub);
        let _ = host
            .request("_x.ai/auth/cancel", json!({"request_seq":1}))
            .await;
        authenticate.abort();
        return Ok(Json(json!({"attempt_id":attempt_id,"phase":"cancelled"})));
    }
    hub.attempt = Some(Attempt {
        id: attempt_id.clone(),
        phase: "waiting".into(),
        url,
        callback,
        state,
        code: String::new(),
        started: Instant::now(),
        models: vec![],
        cancel: cancel.clone(),
    });
    let public = hub.attempt.as_ref().unwrap().public();
    drop(hub);
    tokio::spawn(async move {
        let mut phase = if completed {
            "authenticated"
        } else {
            tokio::select! {
                reply=&mut authenticate=>if matches!(reply,Ok(Ok(_))) {"authenticated"} else {"failed"},
                _=cancel.notified()=>"cancelled",
                _=s.stop_requested()=>"cancelled",
                _=tokio::time::sleep(Duration::from_secs(300))=>"expired",
            }
        };
        let mut models = vec![];
        if phase == "authenticated" {
            let readiness = pot_driver::grok::readiness(&host).await.unwrap_or("failed");
            if readiness != "authenticated" {
                phase = readiness;
            } else if let Ok(session) = host
                .request("session/new", json!({"cwd":s.workspace,"mcpServers":[]}))
                .await
            {
                models = pot_driver::grok::models(&session);
            }
        } else {
            let _ = host
                .request("_x.ai/auth/cancel", json!({"request_seq":1}))
                .await;
            authenticate.abort();
        }
        let mut hub = s.authorization.lock().await;
        if let Some(a) = hub
            .attempt
            .as_mut()
            .filter(|a| a.id == attempt_id && a.phase == "waiting")
        {
            a.finish(phase);
            a.models = models;
        }
    });
    Ok(Json(public))
}
pub async fn status(
    State(s): State<Service>,
    h: HeaderMap,
    Query(req): Query<ID>,
) -> ApiResult<Json<Value>> {
    native(&s, &h)?;
    let hub = s.authorization.lock().await;
    let a = hub
        .attempt
        .as_ref()
        .filter(|a| a.id == req.attempt_id)
        .ok_or_else(|| {
            error(
                StatusCode::NOT_FOUND,
                "stale_attempt",
                "Authorization attempt no longer exists",
            )
        })?;
    Ok(Json(a.public()))
}
pub async fn cancel(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<ID>,
) -> ApiResult<Json<Value>> {
    native(&s, &h)?;
    let mut hub = s.authorization.lock().await;
    if let Some(a) = hub
        .attempt
        .as_mut()
        .filter(|a| a.id == req.attempt_id && a.phase == "waiting")
    {
        a.finish("cancelled");
        a.cancel.notify_one();
    }
    Ok(Json(json!({"cancelled":true})))
}
pub async fn callback(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<Callback>,
) -> ApiResult<Json<Value>> {
    native(&s, &h)?;
    let target = {
        let hub = s.authorization.lock().await;
        let a = hub
            .attempt
            .as_ref()
            .filter(|a| {
                a.id == req.attempt_id
                    && a.phase == "waiting"
                    && a.started.elapsed() < Duration::from_secs(300)
            })
            .ok_or_else(|| {
                error(
                    StatusCode::CONFLICT,
                    "stale_attempt",
                    "Authorization expired or cancelled",
                )
            })?;
        let mut target = a.callback.clone().ok_or_else(unavailable)?;
        if req.query.len() > 16384 || req.query.contains(['\r', '\n', '#']) {
            return Err(error(
                StatusCode::BAD_REQUEST,
                "invalid_callback",
                "Invalid authorization callback",
            ));
        }
        target.set_query(Some(&req.query));
        let params: std::collections::HashMap<_, _> = target.query_pairs().into_owned().collect();
        if params.get("state") != Some(&a.state)
            || (!params.contains_key("code") && !params.contains_key("error"))
        {
            return Err(error(
                StatusCode::BAD_REQUEST,
                "invalid_callback",
                "Invalid authorization callback",
            ));
        }
        // Only the registered guest loopback destination, never a client-supplied URL.
        target
            .set_host(Some("127.0.0.1"))
            .map_err(|_| unavailable())?;
        target
    };
    let response = reqwest::Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(20))
        .build()
        .map_err(|_| unavailable())?
        .get(target)
        .send()
        .await
        .map_err(|_| unavailable())?;
    if !response.status().is_success() && !response.status().is_redirection() {
        return Err(unavailable());
    }
    Ok(Json(json!({"received":true})))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn rejects_foreign_and_non_loopback_callbacks() {
        for url in [
            "https://evil.test/login",
            "https://auth.openai.com/login?redirect_uri=http%3A%2F%2Fevil.test%3A1455%2Fauth%2Fcallback",
            "https://auth.openai.com/login?redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fadmin",
        ] {
            assert!(browser_url(url).is_err());
        }
        let (_,callback,state)=browser_url("https://auth.openai.com/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&state=test").unwrap();
        assert_eq!(callback.unwrap().port(), Some(1455));
        assert_eq!(state, "test");
    }
    fn headers() -> HeaderMap {
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        h
    }
    fn attempt() -> Attempt {
        Attempt {
            id: "current".into(),
            phase: "waiting".into(),
            url: "secret-auth-url".into(),
            code: "device-code".into(),
            callback: Some(reqwest::Url::parse("http://localhost:1455/auth/callback").unwrap()),
            state: "expected".into(),
            started: Instant::now(),
            models: vec![],
            cancel: Arc::new(tokio::sync::Notify::new()),
        }
    }
    #[tokio::test]
    async fn native_boundary_rejects_browser_worker_and_missing_token() {
        let mut s = crate::tests::service();
        assert_eq!(
            native(&s, &HeaderMap::new()).unwrap_err().0,
            StatusCode::UNAUTHORIZED
        );
        let mut h = headers();
        h.insert("origin", "http://localhost".parse().unwrap());
        assert_eq!(native(&s, &h).unwrap_err().0, StatusCode::FORBIDDEN);
        s.is_worker = true;
        assert_eq!(native(&s, &headers()).unwrap_err().0, StatusCode::FORBIDDEN);
    }
    #[tokio::test]
    async fn stale_cancel_cannot_cancel_successor_and_cancel_erases_transient_data() {
        let s = crate::tests::service();
        s.authorization.lock().await.attempt = Some(attempt());
        let _ = cancel(
            State(s.clone()),
            headers(),
            Json(ID {
                attempt_id: "older".into(),
            }),
        )
        .await
        .unwrap();
        assert_eq!(
            s.authorization.lock().await.attempt.as_ref().unwrap().phase,
            "waiting"
        );
        let _ = cancel(
            State(s.clone()),
            headers(),
            Json(ID {
                attempt_id: "current".into(),
            }),
        )
        .await
        .unwrap();
        let hub = s.authorization.lock().await;
        let a = hub.attempt.as_ref().unwrap();
        assert_eq!(a.phase, "cancelled");
        assert!(
            a.url.is_empty() && a.code.is_empty() && a.state.is_empty() && a.callback.is_none()
        );
    }
    #[tokio::test]
    async fn callback_rejects_stale_expired_and_wrong_state_without_network() {
        let s = crate::tests::service();
        s.authorization.lock().await.attempt = Some(attempt());
        for (id, query, status) in [
            ("older", "state=expected&code=secret", StatusCode::CONFLICT),
            (
                "current",
                "state=wrong&code=secret",
                StatusCode::BAD_REQUEST,
            ),
            ("current", "state=expected", StatusCode::BAD_REQUEST),
        ] {
            assert_eq!(
                callback(
                    State(s.clone()),
                    headers(),
                    Json(Callback {
                        attempt_id: id.into(),
                        query: query.into()
                    })
                )
                .await
                .unwrap_err()
                .0,
                status
            );
        }
        s.authorization
            .lock()
            .await
            .attempt
            .as_mut()
            .unwrap()
            .started = Instant::now() - Duration::from_secs(301);
        assert_eq!(
            callback(
                State(s),
                headers(),
                Json(Callback {
                    attempt_id: "current".into(),
                    query: "state=expected&code=secret".into()
                })
            )
            .await
            .unwrap_err()
            .0,
            StatusCode::CONFLICT
        );
    }
    #[tokio::test]
    async fn relay_preserves_query_does_not_follow_redirect_or_mark_authenticated() {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let receiver = tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut buffer = vec![0; 4096];
            let n = socket.read(&mut buffer).await.unwrap();
            let request = String::from_utf8_lossy(&buffer[..n]);
            assert!(
                request
                    .starts_with("GET /auth/callback?state=expected&code=fixture%2Bcode HTTP/1.1")
            );
            socket.write_all(b"HTTP/1.1 303 See Other\r\nLocation: http://127.0.0.1:1/never-follow\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").await.unwrap();
        });
        let s = crate::tests::service();
        let mut a = attempt();
        a.callback =
            Some(reqwest::Url::parse(&format!("http://localhost:{port}/auth/callback")).unwrap());
        s.authorization.lock().await.attempt = Some(a);
        let result = callback(
            State(s.clone()),
            headers(),
            Json(Callback {
                attempt_id: "current".into(),
                query: "state=expected&code=fixture%2Bcode".into(),
            }),
        )
        .await
        .unwrap();
        assert_eq!(result.0["received"], true);
        receiver.await.unwrap();
        assert_eq!(
            s.authorization.lock().await.attempt.as_ref().unwrap().phase,
            "waiting"
        );
    }
}
