//! Scoped reservations and forwarding for Pot Agent model adapters.
use crate::*;
use axum::{body::Body, extract::Path, response::Response};
use futures::StreamExt;
use serde::Deserialize;

pub mod codecs;
pub mod setup;
use codecs::{Protocol, UsageObserver};

use pot_gateway::registry::Provider;
fn stored_provider(s: &Service, spec: &agent::ResolvedSpec) -> Result<Provider> {
    #[cfg(test)]
    if spec.auth_ref == "provider:deepseek" && spec.provider == "deepseek" && s.execution.is_some()
    {
        return Ok(Provider {
            id: "deepseek".into(),
            endpoint: s.agent_upstream.clone(),
            allowed_hosts: vec!["api.deepseek.com".into()],
            secret_ref: "fixture".into(),
            protocol: Protocol::OpenaiChat,
        });
    }
    anyhow::ensure!(
        spec.auth_ref == format!("provider:{}", spec.provider),
        "Invalid provider credential reference"
    );
    s.control_config.registry()?.get(&spec.provider)
}
fn credential(s: &Service, provider: &Provider) -> Result<String> {
    #[cfg(test)]
    if provider.secret_ref == "fixture" {
        return Ok(s
            .execution
            .as_ref()
            .ok_or_else(|| anyhow!("Fixture missing"))?
            .api_key
            .clone());
    }
    s.control_config.registry()?.credential(provider)
}
fn provider(s: &Service, spec: &agent::ResolvedSpec) -> Result<Provider> {
    let mut p = stored_provider(s, spec)?;
    #[cfg(test)]
    if spec.auth_ref == "provider:deepseek" && s.execution.is_some() {
        return Ok(p);
    }
    if p.protocol == Protocol::Gemini {
        anyhow::ensure!(
            spec.model
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b)),
            "Unsupported Gemini model path"
        );
        p.endpoint = p.endpoint.replace("{model}", &spec.model);
        let parsed = reqwest::Url::parse(&p.endpoint)?;
        anyhow::ensure!(
            parsed
                .path()
                .ends_with(&format!("/models/{}:streamGenerateContent", spec.model)),
            "Gemini endpoint model differs from thread"
        );
    }
    let url = reqwest::Url::parse(&p.endpoint)?;
    let secure =
        url.scheme() == "https" || (url.scheme() == "http" && url.host_str() == Some("127.0.0.1"));
    anyhow::ensure!(
        secure
            && url
                .host_str()
                .is_some_and(|host| p.allowed_hosts.iter().any(|h| h == host))
            && url.username().is_empty()
            && url.password().is_none()
            && url.fragment().is_none(),
        "Provider endpoint must use an allow-listed HTTPS host"
    );
    Ok(p)
}
/// API profiles keep their own request ledger even when the legacy smoke route exists.
pub fn agent_budget(s: &Service, max_output_tokens: u64) -> Result<budget::BudgetSettings> {
    if let Some(budget) = &s.api_budget
        && budget.namespace != "deepseek-attempts"
    {
        let mut budget = budget.clone();
        budget.max_output_tokens = max_output_tokens;
        return Ok(budget);
    }
    let data = s.control_config.root().to_path_buf();
    Ok(budget::BudgetSettings {
        namespace: "agent-attempts",
        data,
        limit: s.execution.as_ref().map_or(10000, |c| c.request_limit),
        max_output_tokens,
    })
}

pub fn resolve(s: &Service, profile: &agent::Profile) -> Result<agent::ResolvedSpec> {
    let mut spec = s.control_config.models.apply(profile)?.resolve()?;
    let route = provider(s, &spec)?;
    spec.set_gateway_protocol(route.protocol.name())?;
    Ok(spec)
}
/// Brand derives from the executed thread's immutable route identity, never model names.
pub fn executor_icon(s: &Service, spec: &agent::ResolvedSpec) -> String {
    let endpoint = stored_provider(s, spec)
        .ok()
        .map(|p| p.endpoint)
        .unwrap_or_default();
    let url = reqwest::Url::parse(&endpoint).ok();
    match url.as_ref().and_then(|u| u.host_str()) {
        Some("api.anthropic.com") => "anthropic",
        Some("generativelanguage.googleapis.com") => "gemini",
        Some("api.deepseek.com") => "deepseek",
        Some("api.openai.com") => "openai",
        Some("opencode.ai")
            if url
                .as_ref()
                .is_some_and(|u| u.path().starts_with("/zen/go/")) =>
        {
            "opencode-go"
        }
        Some("opencode.ai") if url.as_ref().is_some_and(|u| u.path().starts_with("/zen/")) => {
            "opencode-zen"
        }
        _ => "custom",
    }
    .into()
}

pub fn validate_spec(s: &Service, spec: &agent::ResolvedSpec) -> Result<()> {
    let route = provider(s, spec)?;
    anyhow::ensure!(
        spec.gateway_protocol == route.protocol.name(),
        "Provider protocol differs from the Thread configuration"
    );
    Ok(())
}

#[cfg(test)]
pub async fn routed(
    State(root): State<Service>,
    Path(provider_id): Path<String>,
    h: HeaderMap,
    body: Json<Value>,
) -> ApiResult<Response> {
    let s = tasks::execution_service(&root, &h)?;
    let thread = agent::thread(&s).map_err(internal)?;
    if thread.config.provider != provider_id {
        return Err(error(
            StatusCode::FORBIDDEN,
            "provider_mismatch",
            "Provider does not match active thread",
        ));
    }
    forward(State(s), h, body).await
}
/// Pot speaks each provider's native protocol; authenticate its local SDK key only.
pub async fn routed_protocol(
    State(root): State<Service>,
    Path((provider_id, path)): Path<(String, String)>,
    mut h: HeaderMap,
    body: Json<Value>,
) -> ApiResult<Response> {
    if !h.contains_key("authorization") {
        let key = h
            .get("x-api-key")
            .or_else(|| h.get("x-goog-api-key"))
            .and_then(|v| v.to_str().ok())
            .ok_or_else(|| {
                error(
                    StatusCode::UNAUTHORIZED,
                    "unauthorized",
                    "Local gateway token missing",
                )
            })?;
        let bearer = format!("Bearer {key}")
            .parse()
            .map_err(|_| error(StatusCode::UNAUTHORIZED, "unauthorized", "Invalid token"))?;
        h.insert("authorization", bearer);
    }
    let s = tasks::execution_service(&root, &h)?;
    let thread = agent::thread(&s).map_err(internal)?;
    let protocol = Protocol::from_name(&thread.config.gateway_protocol).map_err(internal)?;
    if thread.config.provider != provider_id || !protocol.path_matches(&path, &thread.config.model)
    {
        return Err(error(
            StatusCode::FORBIDDEN,
            "provider_mismatch",
            "Provider protocol or model path differs from thread",
        ));
    }
    forward(State(s), h, body).await
}

// The gateway has a fixed upstream and never follows redirects. The real API
// key is not passed to Pot or included in its session files.
pub async fn forward(
    State(s): State<Service>,
    h: HeaderMap,
    Json(body): Json<Value>,
) -> ApiResult<Response> {
    s.authorized(&h)?;
    call(
        &s,
        body,
        h.get("x-pot-compaction").and_then(|v| v.to_str().ok()),
        h.get("anthropic-beta"),
    )
    .await
}

/// Trusted Control entry point. Network adapters authenticate before calling it.
pub(crate) async fn call(
    s: &Service,
    mut body: Value,
    compaction: Option<&str>,
    beta: Option<&axum::http::HeaderValue>,
) -> ApiResult<Response> {
    let c = agent::execution_config_for(s).map_err(internal)?;
    let thread = agent::thread(s).map_err(internal)?;
    validate_spec(s, &thread.config).map_err(internal)?;
    let provider = provider(s, &thread.config).map_err(internal)?;
    if (provider.protocol != Protocol::Gemini
        && (body["model"] != c.model || body["stream"] != true))
        || body.to_string().len() > execution::MAX_MODEL_BODY
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_model_request",
            "Model, stream or context limit exceeded",
        ));
    }
    let max_output = provider
        .protocol
        .clamp(&mut body, c.max_tokens)
        .map_err(internal)?;
    if provider.id == "deepseek" {
        body["thinking"] = json!({"type":"disabled"});
    }
    {
        let inner = s.conversation.inner.lock().unwrap();
        let turn = inner
            .turn
            .as_ref()
            .ok_or_else(|| error(StatusCode::CONFLICT, "no_turn", "No active turn"))?;
        if turn.cancelling {
            return Err(error(
                StatusCode::CONFLICT,
                "cancelling",
                "Turn is stopping",
            ));
        }
        if inner.model_requests >= c.max_requests_per_turn {
            return Err(error(
                StatusCode::TOO_MANY_REQUESTS,
                "turn_budget",
                "Turn request limit reached",
            ));
        }
    }
    let turn_id = s
        .conversation
        .inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .unwrap()
        .user_message_id
        .clone();
    let request_id = id();
    // UTF-8 bytes are a deliberately conservative input estimate, not billed tokens.
    let input_estimate = body.to_string().len() as u64 + 1024;
    let reserved = {
        let ledger = s.usage.lock().unwrap();
        let operation = compaction.filter(|id| *id != "conversation");
        if let Some(operation) = operation {
            ledger
                .validate_compaction(&s.usage_scope, &turn_id, operation)
                .map_err(internal)?;
        }
        let output = ledger
            .reserve_routed(
                &request_id,
                &s.usage_scope,
                &turn_id,
                now(),
                &thread.config,
                thread.generation,
                input_estimate,
                max_output,
            )
            .map_err(internal)?;
        if output.is_some()
            && let Some(operation) = operation
        {
            ledger
                .tag_compaction(&request_id, operation)
                .map_err(internal)?;
        }
        output
    };
    let Some(output) = reserved else {
        s.usage
            .lock()
            .unwrap()
            .wait_routed(
                &s.usage_scope,
                &turn_id,
                now(),
                input_estimate + 1,
                &thread.config.provider,
            )
            .map_err(internal)?;
        return Err(error(
            StatusCode::TOO_MANY_REQUESTS,
            "pot_token_limit",
            "Pot token allowance is unavailable. This turn is paused before sending the model request.",
        ));
    };
    provider
        .protocol
        .clamp(&mut body, output)
        .map_err(internal)?;
    {
        let mut inner = s.conversation.inner.lock().unwrap();
        if inner
            .turn
            .as_ref()
            .is_none_or(|turn| turn.cancelling || turn.user_message_id != turn_id)
            || inner.model_requests >= c.max_requests_per_turn
        {
            s.usage
                .lock()
                .unwrap()
                .rejected(&request_id)
                .map_err(internal)?;
            return Err(error(
                StatusCode::CONFLICT,
                "turn_changed",
                "Turn changed before request claim",
            ));
        }
        let budget = agent_budget(s, c.max_tokens).map_err(internal)?;
        if budget::reserve_in(&budget.data.join(budget.namespace), budget.limit).is_err() {
            s.usage
                .lock()
                .unwrap()
                .rejected(&request_id)
                .map_err(internal)?;
            return Err(error(
                StatusCode::TOO_MANY_REQUESTS,
                "budget_exhausted",
                "Request budget exhausted",
            ));
        }
        inner.model_requests += 1;
    }
    s.publish_budget().map_err(internal)?;
    let api_key = credential(s, &provider).map_err(internal)?;
    let response = pot_gateway::forward(
        &provider.endpoint,
        &api_key,
        &provider.protocol,
        &body,
        beta,
        thread.config.params.agent.request_timeout_ms,
    )
    .await
    .map_err(|_| {
        error(
            StatusCode::BAD_GATEWAY,
            "provider_connection",
            "Model provider connection failed",
        )
    })?;
    if !response.status().is_success() {
        s.usage
            .lock()
            .unwrap()
            .rejected(&request_id)
            .map_err(internal)?;
        return Err(error(
            StatusCode::BAD_GATEWAY,
            "provider_rejected",
            &format!(
                "Model provider returned HTTP {}",
                response.status().as_u16()
            ),
        ));
    }
    let content_type = response
        .headers()
        .get("content-type")
        .cloned()
        .unwrap_or_else(|| "application/octet-stream".parse().unwrap());
    let mut incoming = response.bytes_stream();
    let service = s.clone();
    let stream = async_stream::stream! {
        let mut pending = String::new();
        let mut observer = UsageObserver::default();
        while let Some(chunk) = incoming.next().await {
            match chunk {
                Ok(bytes) => {
                    // Usage fields are ASCII; partial UTF-8 text is only forwarded,
                    // never reconstructed here. Bound diagnostics independently.
                    pending.push_str(&String::from_utf8_lossy(&bytes));
                    while let Some(end) = pending.find('\n') {
                        let line = pending[..end].trim().to_owned(); pending.drain(..=end);
                        if let Some(data) = line.strip_prefix("data:")
                            && let Ok(value) = serde_json::from_str::<Value>(data.trim()) {
                                let result = observer.observe(&provider.protocol,&value).and_then(|usage| {
                                    if let Some(usage) = usage { service.usage.lock().unwrap().settle(&request_id,&usage)?; }
                                    Ok(())
                                });
                                if result.is_err() {
                                    yield Err(std::io::Error::other("Could not persist model usage"));
                                    return;
                                }
                            }
                    }
                    if pending.len()>1024*1024 { pending.clear(); }
                    yield Ok::<_,std::io::Error>(bytes);
                }
                Err(_) => {yield Err(std::io::Error::other("Provider stream interrupted")); break;}
            }
        }
    };
    Ok(Response::builder()
        .header("content-type", content_type)
        .header("cache-control", "no-cache")
        .body(Body::from_stream(stream))
        .unwrap())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn codecs_only_clamp_output_and_unknown_usage_stays_unknown() {
        let mut body =
            json!({"messages":[{"role":"user","content":"test"}],"max_completion_tokens":20});
        assert_eq!(Protocol::OpenaiChat.clamp(&mut body, 10).unwrap(), 10);
        assert_eq!(body["max_completion_tokens"], 10);
        assert!(body.get("max_tokens").is_none());
        assert_eq!(body["messages"][0]["content"], "test");
        let original = body.clone();
        Protocol::Unknown.clamp(&mut body, 1).unwrap();
        assert_eq!(body, original);
        let ledger = usage::Ledger::open(std::path::Path::new(":memory:")).unwrap();
        ledger
            .reserve("unknown", "main", "turn", now(), "model", 100, 10)
            .unwrap();
        let (input, output, reserved): (Option<u64>, Option<u64>, u64) = ledger
            .0
            .query_row("SELECT input,output,reserved FROM requests", [], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?))
            })
            .unwrap();
        assert_eq!((input, output, reserved), (None, None, 110));
    }
}

#[cfg(test)]
mod integration_tests {
    use super::*;

    #[tokio::test]
    async fn unknown_protocol_forwards_without_translation_and_never_reports_zero() {
        let dir = std::env::temp_dir().join(format!("pot-opaque-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(s.data.clone()));
        s.workspace = dir.clone();
        s.execution = Some(execution::Config {
            api_key: "legacy".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 10,
            max_tokens: 20,
            max_requests_per_turn: 2,
            max_parallel_tasks: 5,
        });
        s.api_budget = Some(budget::BudgetSettings {
            namespace: "agent-attempts",
            data: dir.clone(),
            limit: 10,
            max_output_tokens: 20,
        });
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        crate::tests::providers(
            &s,
            json!([{"id":"opaque","endpoint":format!("http://127.0.0.1:{port}/model"),"allowed_hosts":["127.0.0.1"],"api_key":"upstream-secret","protocol":"unknown"}]),
        );
        let original = json!({"model":"opaque-model","stream":true,"max_tokens":99,"messages":[{"role":"user","content":"hello"}]});
        let expected = original.clone();
        let server = tokio::spawn(async move {
            axum::serve(listener,Router::new().route("/model",post(move |h:HeaderMap,Json(body):Json<Value>|{let expected=expected.clone();async move{assert_eq!(body,expected);assert_eq!(h["authorization"],"Bearer upstream-secret");([("content-type","text/event-stream")],"data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")}}))).await.unwrap()
        });
        let mut profile = agent::Profile::api(s.execution.as_ref().unwrap());
        profile.id = "opaque".into();
        profile.provider = "opaque".into();
        profile.auth_ref = "provider:opaque".into();
        profile.model = "opaque-model".into();
        let settings = serde_json::from_value(
            json!({"profile":profile,"expected_revision":0,"request_id":"select"}),
        )
        .unwrap();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let _ = agent::settings::settings(State(s.clone()), h.clone(), Json(settings))
            .await
            .unwrap();
        assert!(!agent::thread(&s).unwrap().config.supports_checkpoint());
        assert!(!agent::thread(&s).unwrap().config.supports_checkpoint());
        s.turn(Some(TurnState {
            user_message_id: "turn".into(),
            cancelling: false,
        }));
        let response = forward(State(s.clone()), h, Json(original)).await.unwrap();
        let _ = axum::body::to_bytes(response.into_body(), 65536)
            .await
            .unwrap();
        let (input, output, reserved): (Option<u64>, Option<u64>, u64) = s
            .usage
            .lock()
            .unwrap()
            .0
            .query_row("SELECT input,output,reserved FROM requests", [], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?))
            })
            .unwrap();
        assert_eq!((input, output), (None, None));
        assert!(reserved > 0);
        server.abort();
        std::fs::remove_dir_all(dir).unwrap();
    }
}
