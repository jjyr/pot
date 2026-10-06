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
    started: Instant,
    models: Vec<String>,
    cancel: Arc<tokio::sync::Notify>,
}
impl Attempt {
    fn public(&self) -> Value {
        json!({"attempt_id":self.id,"phase":self.phase,"authorization_url":self.url,
            "user_code":self.code,"models":self.models})
    }
    fn finish(&mut self, phase: &str) {
        self.phase = phase.into();
        self.url.clear();
        self.code.clear();
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
    official_url(raw)
}
fn official_url(raw: &str) -> Result<(reqwest::Url, Option<reqwest::Url>, String)> {
    let url = reqwest::Url::parse(raw)?;
    anyhow::ensure!(
        url.scheme() == "https"
            && url.port_or_known_default() == Some(443)
            && url.host_str() == Some("auth.openai.com")
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
                && u.path() == "/auth/callback"
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
pub async fn start(
    State(s): State<Service>,
    h: HeaderMap,
    Json(req): Json<Start>,
) -> ApiResult<Json<Value>> {
    native(&s, &h)?;
    if req.engine != "codex" {
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
                    Ok(v) if completion(&v, &native_id).is_some() => break completion(&v, &native_id).unwrap(),
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
fn completion(event: &Value, login_id: &str) -> Option<&'static str> {
    if event["method"] != "account/login/completed" || event["params"]["loginId"] != login_id {
        return None;
    }
    Some(if event["params"]["success"] == true {
        "authenticated"
    } else {
        "failed"
    })
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
    #[test]
    fn only_matching_official_completion_can_settle_login() {
        assert_eq!(
            completion(
                &json!({"method":"account/updated","params":{"account":{"type":"chatgpt"}}}),
                "current"
            ),
            None
        );
        assert_eq!(
            completion(
                &json!({"method":"account/login/completed","params":{"loginId":"old","success":true}}),
                "current"
            ),
            None
        );
        assert_eq!(
            completion(
                &json!({"method":"account/login/completed","params":{"loginId":"current","success":true}}),
                "current"
            ),
            Some("authenticated")
        );
        assert_eq!(
            completion(
                &json!({"method":"account/login/completed","params":{"loginId":"current","success":false}}),
                "current"
            ),
            Some("failed")
        );
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
        assert!(a.url.is_empty() && a.code.is_empty());
    }
}
