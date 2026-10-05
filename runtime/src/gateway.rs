//! Protocol-independent reservation and transport; no format translation.
use crate::*;
use axum::{body::Body, extract::Path, response::Response};
use futures::StreamExt;
use serde::{Deserialize, Serialize};

pub mod codecs;
pub mod setup;
use codecs::{Protocol, UsageObserver};

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
struct Provider {
    id: String,
    endpoint: String,
    allowed_hosts: Vec<String>,
    api_key: String,
    protocol: Protocol,
}
fn stored_provider(s: &Service, spec: &agent::ResolvedSpec) -> Result<Provider> {
    if spec.auth_ref == "pi.json:api_key" && spec.provider == "deepseek" {
        return Ok(Provider {
            id: "deepseek".into(),
            endpoint: s.pi_upstream.clone(),
            allowed_hosts: vec!["api.deepseek.com".into()],
            api_key: s
                .pi
                .as_ref()
                .ok_or_else(|| anyhow!("Pi credential missing"))?
                .api_key
                .clone(),
            protocol: Protocol::OpenaiChat,
        });
    }
    anyhow::ensure!(
        spec.auth_ref == format!("providers.json:{}", spec.provider),
        "Invalid provider credential reference"
    );
    // Workers refer to the root private provider configuration, never a file
    // named by model input. Endpoints/hosts are configured by the user.
    let root = if s.is_worker {
        s.data
            .parent()
            .and_then(|p| p.parent())
            .ok_or_else(|| anyhow!("Invalid worker data path"))?
    } else {
        &s.data
    };
    let providers: Vec<Provider> =
        serde_json::from_slice(&std::fs::read(root.join("providers.json"))?)?;
    let p = providers
        .into_iter()
        .find(|p| p.id == spec.provider)
        .ok_or_else(|| anyhow!("Provider unavailable; needs rebind"))?;
    Ok(p)
}
fn provider(s: &Service, spec: &agent::ResolvedSpec) -> Result<Provider> {
    let mut p = stored_provider(s, spec)?;
    if spec.auth_ref == "pi.json:api_key" {
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
            "Gemini endpoint model differs from binding"
        );
    }
    let url = reqwest::Url::parse(&p.endpoint)?;
    let secure = url.scheme() == "https"
        || (cfg!(test) && url.scheme() == "http" && url.host_str() == Some("127.0.0.1"));
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
    anyhow::ensure!(!p.api_key.is_empty(), "Provider credential missing");
    Ok(p)
}
/// API profiles keep their own request ledger even when the legacy smoke route exists.
pub fn pi_budget(s: &Service, max_output_tokens: u64) -> Result<deepseek::BudgetSettings> {
    if let Some(budget) = &s.api_budget {
        if budget.namespace != "deepseek-attempts" {
            let mut budget = budget.clone();
            budget.max_output_tokens = max_output_tokens;
            return Ok(budget);
        }
    }
    let data = if s.is_worker {
        s.data
            .parent()
            .and_then(|p| p.parent())
            .ok_or_else(|| anyhow!("Invalid worker data path"))?
            .to_path_buf()
    } else {
        s.data.clone()
    };
    Ok(deepseek::BudgetSettings {
        namespace: "pi-attempts",
        data,
        limit: s.pi.as_ref().map_or(10000, |c| c.request_limit),
        max_output_tokens,
    })
}

pub fn resolve(s: &Service, profile: &agent::Profile) -> Result<agent::ResolvedSpec> {
    let mut spec = profile.resolve()?;
    if matches!(
        spec.engine,
        agent::Engine::Codex | agent::Engine::Claude | agent::Engine::Agy
    ) {
        return Ok(spec);
    }
    let route = provider(s, &spec)?;
    spec.set_gateway_protocol(route.protocol.name())?;
    Ok(spec)
}
pub fn validate_spec(s: &Service, spec: &agent::ResolvedSpec) -> Result<()> {
    if matches!(
        spec.engine,
        agent::Engine::Codex | agent::Engine::Claude | agent::Engine::Agy
    ) {
        return spec.validate();
    }
    let route = provider(s, spec)?;
    anyhow::ensure!(
        spec.gateway_protocol == route.protocol.name(),
        "Provider protocol changed; needs rebind"
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
    let s = tasks::pi_service(&root, &h)?;
    let binding = agent::binding(&s).map_err(internal)?;
    if binding.spec.provider != provider_id {
        return Err(error(
            StatusCode::FORBIDDEN,
            "provider_mismatch",
            "Provider does not match active binding",
        ));
    }
    forward(State(s), h, body).await
}
/// Pi speaks each provider's native protocol; authenticate its local SDK key only.
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
    let s = tasks::pi_service(&root, &h)?;
    let binding = agent::binding(&s).map_err(internal)?;
    let protocol = Protocol::from_name(&binding.spec.gateway_protocol).map_err(internal)?;
    if binding.spec.provider != provider_id || !protocol.path_matches(&path, &binding.spec.model) {
        return Err(error(
            StatusCode::FORBIDDEN,
            "provider_mismatch",
            "Provider protocol or model path differs from binding",
        ));
    }
    forward(State(s), h, body).await
}

// The gateway has a fixed upstream and never follows redirects. The real API
// key is not passed to Pi or included in its session files.
pub async fn forward(
    State(s): State<Service>,
    h: HeaderMap,
    Json(mut body): Json<Value>,
) -> ApiResult<Response> {
    s.authorized(&h)?;
    let c = agent::pi_config(&s).map_err(internal)?;
    let binding = agent::binding(&s).map_err(internal)?;
    validate_spec(&s, &binding.spec).map_err(internal)?;
    let provider = provider(&s, &binding.spec).map_err(internal)?;
    if (provider.protocol != Protocol::Gemini
        && (body["model"] != c.model || body["stream"] != true))
        || body.to_string().len() > pi::MAX_MODEL_BODY
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
        let inner = s.inner.lock().unwrap();
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
        let operation = h
            .get("x-pot-compaction")
            .and_then(|v| v.to_str().ok())
            .filter(|id| *id != "conversation");
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
                &binding.spec,
                binding.epoch,
                input_estimate,
                max_output,
            )
            .map_err(internal)?;
        if output.is_some() {
            if let Some(operation) = operation {
                ledger
                    .tag_compaction(&request_id, operation)
                    .map_err(internal)?;
            }
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
                &binding.spec.provider,
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
        let mut inner = s.inner.lock().unwrap();
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
        let budget = pi_budget(&s, c.max_tokens).map_err(internal)?;
        if deepseek::reserve_in(&budget.data.join(budget.namespace), budget.limit).is_err() {
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
    let response = pot_gateway::forward(
        &provider.endpoint,
        &provider.api_key,
        &provider.protocol,
        &body,
        h.get("anthropic-beta"),
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
                        if let Some(data) = line.strip_prefix("data:") {
                            if let Ok(value) = serde_json::from_str::<Value>(data.trim()) {
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
    use axum::response::sse::Event as WireEvent;
    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    #[ignore = "Runs installed Pi against two local mock providers; set POT_TEST_PI_BIN"]
    async fn real_pi_migrates_to_second_provider_without_losing_old_session() {
        let dir = std::env::temp_dir().join(format!("pot-providers-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.pi_program = std::env::var("POT_TEST_PI_BIN").unwrap();
        s.pi = Some(pi::Config {
            api_key: "first-secret".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 10,
            max_tokens: 128,
            max_requests_per_turn: 2,
            max_parallel_tasks: 5,
        });
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: dir.clone(),
            limit: 10,
            max_output_tokens: 128,
        });
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        s.pi_upstream = format!("http://127.0.0.1:{port}/first");
        pi::private_json(
            &dir.join(RUNTIME_ENDPOINT_FILE),
            &json!({"api_version":API_VERSION,"port":port,"token":s.token,"boot_id":s.boot}),
        )
        .unwrap();
        pi::private_json(&dir.join("providers.json"),&json!([{"id":"mock","endpoint":format!("http://127.0.0.1:{port}/second"),"allowed_hosts":["127.0.0.1"],"api_key":"second-secret","protocol":"openai_chat"}])).unwrap();
        let seen = Arc::new(Mutex::new(Vec::new()));
        let capture = seen.clone();
        let mock = move |h: HeaderMap, Json(body): Json<Value>| {
            let capture = capture.clone();
            async move {
                capture.lock().unwrap().push((
                    h["authorization"].to_str().unwrap().to_string(),
                    body.clone(),
                ));
                let events = vec![
                    json!({"id":"test","choices":[{"index":0,"delta":{"role":"assistant","content":"done"},"finish_reason":null}]}),
                    json!({"id":"test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2}}),
                ];
                Sse::new(futures::stream::iter(events.into_iter().map(|value| {
                    Ok::<_, Infallible>(WireEvent::default().data(value.to_string()))
                })))
            }
        };
        let router = Router::new()
            .route("/api/pi/v1/chat/completions", post(tasks::gateway))
            .route("/gw/{provider}/v1/chat/completions", post(routed))
            .with_state(s.clone())
            .route("/first", post(mock.clone()))
            .route("/second", post(mock));
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        for turn in ["first", "second"] {
            if turn == "second" {
                let mut profile = agent::Profile::legacy(s.pi.as_ref().unwrap());
                profile.id = "second-profile".into();
                profile.provider = "mock".into();
                profile.auth_ref = "providers.json:mock".into();
                profile.model = "mock-model".into();
                let settings = serde_json::from_value(
                    json!({"profile":profile,"expected_revision":0,"request_id":"switch"}),
                )
                .unwrap();
                let mut h = HeaderMap::new();
                h.insert("authorization", "Bearer test-token".parse().unwrap());
                let _ = agent::migration::settings(State(s.clone()), h, Json(settings))
                    .await
                    .unwrap();
            }
            s.save(Message {
                id: turn.into(),
                conversation_id: "main".into(),
                role: Role::User,
                text: format!("real {turn} request"),
                status: MessageStatus::Accepted,
                created_at_ms: now(),
                request_id: Some(turn.into()),
            })
            .unwrap();
            s.turn(Some(TurnState {
                user_message_id: turn.into(),
                cancelling: false,
            }));
            s.inner.lock().unwrap().model_requests = 0;
            agent::run(
                &s,
                SendMessage {
                    request_id: turn.into(),
                    conversation_id: "main".into(),
                    text: format!("real {turn} request"),
                },
            )
            .await
            .unwrap();
            assert_eq!(
                s.inner
                    .lock()
                    .unwrap()
                    .store
                    .message(turn)
                    .unwrap()
                    .unwrap()
                    .status,
                MessageStatus::Complete
            );
            s.turn(None);
        }
        let captures = seen.lock().unwrap();
        assert_eq!(captures.len(), 2);
        assert_eq!(captures[0].0, "Bearer first-secret");
        assert_eq!(captures[1].0, "Bearer second-secret");
        assert_eq!(captures[1].1["model"], "mock-model");
        assert!(captures[1].1.to_string().contains("lossy handoff"));
        drop(captures);
        let all = agent::bindings(&s.inner.lock().unwrap().store).unwrap();
        assert_eq!(all.len(), 2);
        assert_eq!(all[0].state, agent::BindingState::Archived);
        assert_eq!(all[1].epoch, 2);
        let agent::NativeRef::Pi {
            file,
            session_id: Some(native),
        } = &all[0].native_ref
        else {
            panic!()
        };
        agent::validate_pi_session(file, native).unwrap();
        let providers: Vec<String> = s
            .usage
            .lock()
            .unwrap()
            .0
            .prepare("SELECT provider FROM request_routes ORDER BY rowid")
            .unwrap()
            .query_map([], |r| r.get(0))
            .unwrap()
            .collect::<rusqlite::Result<_>>()
            .unwrap();
        assert_eq!(providers, vec!["deepseek", "mock"]);
        server.abort();
        std::fs::remove_dir_all(dir).unwrap();
    }
}

#[cfg(test)]
mod opaque_tests {
    use super::*;
    #[tokio::test]
    async fn unknown_protocol_forwards_without_translation_and_never_reports_zero() {
        let dir = std::env::temp_dir().join(format!("pot-opaque-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.pi = Some(pi::Config {
            api_key: "legacy".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 10,
            max_tokens: 20,
            max_requests_per_turn: 2,
            max_parallel_tasks: 5,
        });
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: dir.clone(),
            limit: 10,
            max_output_tokens: 20,
        });
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        pi::private_json(&dir.join("providers.json"),&json!([{"id":"opaque","endpoint":format!("http://127.0.0.1:{port}/model"),"allowed_hosts":["127.0.0.1"],"api_key":"upstream-secret","protocol":"unknown"}])).unwrap();
        let original = json!({"model":"opaque-model","stream":true,"max_tokens":99,"messages":[{"role":"user","content":"hello"}]});
        let expected = original.clone();
        let server = tokio::spawn(async move {
            axum::serve(listener,Router::new().route("/model",post(move |h:HeaderMap,Json(body):Json<Value>|{let expected=expected.clone();async move{assert_eq!(body,expected);assert_eq!(h["authorization"],"Bearer upstream-secret");([("content-type","text/event-stream")],"data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")}}))).await.unwrap()
        });
        let mut profile = agent::Profile::legacy(s.pi.as_ref().unwrap());
        profile.id = "opaque".into();
        profile.provider = "opaque".into();
        profile.auth_ref = "providers.json:opaque".into();
        profile.model = "opaque-model".into();
        let settings = serde_json::from_value(
            json!({"profile":profile,"expected_revision":0,"request_id":"select"}),
        )
        .unwrap();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let _ = agent::migration::settings(State(s.clone()), h.clone(), Json(settings))
            .await
            .unwrap();
        assert!(
            !agent::binding(&s)
                .unwrap()
                .spec
                .capabilities
                .request_level_budget_enforcement
        );
        assert!(
            !agent::binding(&s)
                .unwrap()
                .spec
                .capabilities
                .safe_checkpoint_resume
        );
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

#[cfg(test)]
mod native_tests;
