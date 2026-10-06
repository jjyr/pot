//! User-authored API connections. Secrets never enter profile snapshots or responses.
use super::*;

#[derive(Deserialize)]
pub struct Setup {
    request_id: String,
    expected_revision: u64,
    endpoint: String,
    model: String,
    protocol: Protocol,
    #[serde(default)]
    api_key: String,
    #[serde(default = "default_profile")]
    profile_id: String,
    #[serde(default = "default_name")]
    name: String,
    #[serde(default = "follow_main")]
    follow_main: bool,
    #[serde(default = "output_limit")]
    max_output_tokens: u64,
}
fn default_profile() -> String {
    "apot-default".into()
}
fn default_name() -> String {
    "API".into()
}
fn follow_main() -> bool {
    true
}
fn output_limit() -> u64 {
    8192
}

pub async fn get(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let profiles = agent::profiles(&s.inner.lock().unwrap().store).map_err(internal)?;
    let mut connections = Vec::new();
    for profile in profiles {
        let spec = profile.resolve().map_err(internal)?;
        if spec.auth_ref.starts_with("providers.json:") {
            if let Ok(p) = stored_provider(&s, &spec) {
                connections.push(json!({"profile_id": profile.id, "endpoint":p.endpoint,
                    "model":spec.model,"protocol":p.protocol,"has_key":!p.api_key.is_empty()}));
            }
        }
    }
    let binding = agent::current(&s.inner.lock().unwrap().store).map_err(internal)?;
    let connection = binding.and_then(|b| {
        connections
            .iter()
            .find(|c| c["profile_id"] == b.spec.source_profile_id)
            .cloned()
    });
    Ok(Json(
        json!({"connection":connection,"connections":connections}),
    ))
}

pub async fn save(
    State(s): State<Service>,
    h: HeaderMap,
    Json(input): Json<Setup>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    if input.protocol == Protocol::Unknown {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "protocol",
            "Select Chat Completions, Responses, Anthropic Messages or Gemini",
        ));
    }
    static SAVE: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());
    let _guard = SAVE.lock().await;
    let request = uuid::Uuid::parse_str(&input.request_id).map_err(|_| {
        error(
            StatusCode::BAD_REQUEST,
            "request_id",
            "Expected a UUID request ID",
        )
    })?;
    let url = reqwest::Url::parse(&input.endpoint).map_err(|_| {
        error(
            StatusCode::BAD_REQUEST,
            "endpoint",
            "Enter a full HTTPS model endpoint",
        )
    })?;
    if !(url.scheme() == "https" || (url.scheme() == "http" && url.host_str() == Some("127.0.0.1")))
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "endpoint",
            "Endpoint must use HTTPS without embedded credentials or a fragment",
        ));
    }
    if input.endpoint.len() > 4096 || input.api_key.len() > 16384 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "connection_size",
            "API connection values are too long",
        ));
    }
    let mut key = input.api_key;
    if key.is_empty() {
        let profiles = agent::profiles(&s.inner.lock().unwrap().store).map_err(internal)?;
        if let Some(profile) = profiles.iter().find(|p| p.id == input.profile_id) {
            if let Ok(previous) = stored_provider(&s, &profile.resolve().map_err(internal)?) {
                if previous.endpoint == input.endpoint {
                    key = previous.api_key;
                }
            }
        }
    }
    if key.is_empty() {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "credential",
            "Enter an API key for this endpoint",
        ));
    }
    let id = format!("api-{request}");
    let next = Provider {
        id: id.clone(),
        endpoint: input.endpoint,
        allowed_hosts: vec![url.host_str().unwrap().into()],
        api_key: key,
        protocol: input.protocol,
    };
    let config = execution::Config {
        model: input.model.clone(),
        max_tokens: input.max_output_tokens,
        ..Default::default()
    };
    let mut profile = agent::Profile::api(&config);
    profile.id = input.profile_id;
    profile.name = input.name;
    profile.revision = input
        .expected_revision
        .checked_add(1)
        .ok_or_else(|| error(StatusCode::BAD_REQUEST, "revision", "Invalid revision"))?;
    profile.provider = id.clone();
    profile.auth_ref = format!("providers.json:{id}");
    profile
        .validate()
        .map_err(|e| error(StatusCode::BAD_REQUEST, "profile", &e.to_string()))?;
    let path = s.data.join("providers.json");
    let mut all: Vec<Provider> = if path.exists() {
        serde_json::from_slice(&std::fs::read(&path).map_err(|e| internal(e.into()))?)
            .map_err(|e| internal(e.into()))?
    } else {
        vec![]
    };
    if let Some(old) = all.iter().find(|p| p.id == id) {
        if old != &next {
            return Err(error(
                StatusCode::CONFLICT,
                "request_conflict",
                "API request ID was reused with different values",
            ));
        }
    } else {
        all.push(next);
        // Persist the immutable credential route first. A failed profile save can
        // leave an unused route, but cannot change any existing binding's endpoint.
        execution::private_json(
            &path,
            &serde_json::to_value(&all).map_err(|e| internal(e.into()))?,
        )
        .map_err(internal)?;
    }
    let settings=serde_json::from_value(json!({"profile":profile,"expected_revision":input.expected_revision,"request_id":input.request_id,"follow_main":input.follow_main,"follow_worker":false})).map_err(|e|internal(e.into()))?;
    agent::migration::settings(State(s), h, Json(settings)).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    #[tokio::test]
    async fn connection_check_reads_metadata_only_and_does_not_mutate_profiles() {
        use axum::{Router, routing::get};
        let app = Router::new().route(
            "/v1/models",
            get(|h: HeaderMap| async move {
                assert_eq!(h["authorization"], "Bearer check-only");
                Json(json!({"data":[{"id":"available-model"}]}))
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let host = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let s = crate::tests::service();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let before = agent::profiles(&s.inner.lock().unwrap().store).unwrap();
        let response=check(State(s.clone()),h,Json(serde_json::from_value(json!({"endpoint":format!("http://{address}/v1/responses"),"api_key":"check-only","protocol":"openai_responses"})).unwrap())).await.unwrap().0;
        assert_eq!(response["models"], json!(["available-model"]));
        assert_eq!(
            agent::profiles(&s.inner.lock().unwrap().store).unwrap(),
            before
        );
        assert!(!response.to_string().contains("check-only"));
        host.abort();
    }
    #[tokio::test]
    async fn saved_connections_do_not_select_chat_and_can_be_edited_while_another_is_active() {
        let dir = std::env::temp_dir().join(format!("pot-api-connections-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.execution = None;
        s.api_budget = None;
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let make = |profile: &str, revision: u64, model: &str, key: &str, follow: bool| json!({"request_id":id(),"profile_id":profile,"name":profile,"expected_revision":revision,"endpoint":"https://example.com/v1/chat/completions","model":model,"protocol":"openai_chat","api_key":key,"follow_main":follow});
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(make("first", 0, "a", "private-one", false)).unwrap()),
        )
        .await
        .unwrap();
        assert!(
            agent::current(&s.inner.lock().unwrap().store)
                .unwrap()
                .is_none()
        );
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(make("second", 0, "b", "private-two", true)).unwrap()),
        )
        .await
        .unwrap();
        let active = agent::binding(&s).unwrap();
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(make("first", 1, "a2", "", false)).unwrap()),
        )
        .await
        .unwrap();
        assert_eq!(agent::binding(&s).unwrap(), active);
        assert!(
            s.inner
                .lock()
                .unwrap()
                .store
                .get("agent.default.worker")
                .unwrap()
                .is_none()
        );
        let public = get(State(s.clone()), h.clone()).await.unwrap().0;
        assert_eq!(public["connections"].as_array().unwrap().len(), 2);
        assert_eq!(public["connection"]["profile_id"], "second");
        assert!(!public.to_string().contains("private-"));
        let first = agent::profiles(&s.inner.lock().unwrap().store)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "first")
            .unwrap();
        assert_eq!(
            stored_provider(&s, &first.resolve().unwrap())
                .unwrap()
                .api_key,
            "private-one"
        );
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn connection_check_rejects_unsafe_endpoints_before_networking() {
        let s = crate::tests::service();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        for endpoint in [
            "http://example.com/v1/responses",
            "https://user:secret@example.com/v1/responses",
            "https://example.com/v1/responses?key=secret",
        ] {
            let input =
                json!({"endpoint":endpoint,"api_key":"fixture","protocol":"openai_responses"});
            assert!(
                check(
                    State(s.clone()),
                    h.clone(),
                    Json(serde_json::from_value(input).unwrap())
                )
                .await
                .is_err()
            );
        }
        let unsupported=check(State(s),h,Json(serde_json::from_value(json!({"endpoint":"https://example.com/custom","api_key":"fixture","protocol":"unknown"})).unwrap())).await.unwrap().0;
        assert_eq!(unsupported["supported"], false);
    }
    #[tokio::test]
    async fn api_setup_is_private_idempotent_and_does_not_need_legacy_pi() {
        let dir = std::env::temp_dir().join(format!("pot-api-setup-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.execution = None;
        s.api_budget = None;
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let input = json!({"request_id":id(),"expected_revision":0,"endpoint":"https://example.com/v1/chat/completions","model":"custom-model","protocol":"openai_chat","api_key":"fixture-private-key"});
        assert!(
            save(
                State(s.clone()),
                HeaderMap::new(),
                Json(serde_json::from_value(input.clone()).unwrap())
            )
            .await
            .is_err()
        );
        let saved = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(input.clone()).unwrap()),
        )
        .await
        .unwrap()
        .0;
        assert_eq!(saved["saved"], true);
        assert_eq!(
            save(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(input.clone()).unwrap())
            )
            .await
            .unwrap()
            .0["duplicate"],
            true
        );
        let current = agent::binding(&s).unwrap();
        assert_eq!(current.spec.model, "custom-model");
        assert_eq!(
            agent::execution_config_for(&s)
                .unwrap()
                .max_requests_per_turn,
            100
        );
        assert!(
            !serde_json::to_string(&current)
                .unwrap()
                .contains("fixture-private-key")
        );
        let public = get(State(s.clone()), h.clone()).await.unwrap().0;
        assert_eq!(public["connection"]["has_key"], true);
        assert!(!public.to_string().contains("fixture-private-key"));
        assert_eq!(
            std::fs::metadata(dir.join("providers.json"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
        let mut conflict = input.clone();
        conflict["api_key"] = json!("different");
        assert!(
            save(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(conflict).unwrap())
            )
            .await
            .is_err()
        );
        let mut stale = input.clone();
        stale["request_id"] = json!(id());
        stale["model"] = json!("different");
        assert!(
            save(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(stale).unwrap())
            )
            .await
            .is_err()
        );
        assert_eq!(agent::binding(&s).unwrap(), current);
        let mut update = input;
        update["request_id"] = json!(id());
        update["expected_revision"] = json!(1);
        update["model"] = json!("second-model");
        update["api_key"] = json!("");
        let _ = save(
            State(s.clone()),
            h,
            Json(serde_json::from_value(update).unwrap()),
        )
        .await
        .unwrap();
        let all = agent::bindings(&s.inner.lock().unwrap().store).unwrap();
        assert_eq!(all.len(), 2);
        assert_eq!(all[0].spec, current.spec);
        assert_ne!(all[0].spec.provider, all[1].spec.provider);
        std::fs::remove_dir_all(dir).unwrap();
    }
}

#[derive(Deserialize)]
pub struct Check {
    endpoint: String,
    protocol: Protocol,
    #[serde(default)]
    api_key: String,
    #[serde(default)]
    profile_id: String,
}
pub async fn check(
    State(s): State<Service>,
    h: HeaderMap,
    Json(input): Json<Check>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let mut url = reqwest::Url::parse(&input.endpoint)
        .map_err(|_| error(StatusCode::BAD_REQUEST, "endpoint", "请输入完整 HTTPS 地址"))?;
    if !(url.scheme() == "https" || (url.scheme() == "http" && url.host_str() == Some("127.0.0.1")))
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
        || url.query().is_some()
        || input.api_key.len() > 16384
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "endpoint",
            "连接检查需要不含凭据或查询参数的 HTTPS 地址",
        ));
    }
    let mut key = input.api_key;
    if key.is_empty() {
        let profiles = agent::profiles(&s.inner.lock().unwrap().store).map_err(internal)?;
        if let Some(profile) = profiles.iter().find(|p| p.id == input.profile_id) {
            if let Ok(previous) = stored_provider(&s, &profile.resolve().map_err(internal)?) {
                if previous.endpoint == input.endpoint {
                    key = previous.api_key;
                }
            }
        }
    }
    if key.trim().is_empty() || key.contains(['\n', '\r']) {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "credential",
            "请提供 API key",
        ));
    }
    let path = url.path().to_owned();
    let model_path = match input.protocol {
        Protocol::OpenaiChat => path
            .strip_suffix("/chat/completions")
            .map(|base| format!("{base}/models")),
        Protocol::OpenaiResponses => path
            .strip_suffix("/responses")
            .map(|base| format!("{base}/models")),
        Protocol::AnthropicMessages => path
            .strip_suffix("/messages")
            .map(|base| format!("{base}/models")),
        Protocol::Gemini => path
            .split_once("/models/")
            .map(|(base, _)| format!("{base}/models")),
        _ => None,
    };
    let Some(model_path) = model_path else {
        return Ok(Json(
            json!({"supported":false,"models":[],"message":"此服务无法自动查询模型；请填写服务商提供的模型标识。保存只检查配置，不调用模型。"}),
        ));
    };
    url.set_path(&model_path);
    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(12))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|e| internal(e.into()))?;
    let request = client.get(url);
    let request = match input.protocol {
        Protocol::AnthropicMessages => request
            .header("x-api-key", key)
            .header("anthropic-version", "2023-06-01"),
        Protocol::Gemini => request.header("x-goog-api-key", key),
        _ => request.bearer_auth(key),
    };
    let mut response = request.send().await.map_err(|_| {
        error(
            StatusCode::BAD_GATEWAY,
            "connection",
            "无法连接服务，请检查地址或网络后重试。",
        )
    })?;
    if !response.status().is_success() {
        return Err(error(
            StatusCode::BAD_GATEWAY,
            "connection",
            match response.status().as_u16() {
                401 | 403 => "服务拒绝授权，请检查 API key 与访问权限。",
                404 | 405 => "服务未提供模型列表接口；可手动填写模型，保存配置不代表调用已验证。",
                429 => "服务暂时限流，请稍后重试。",
                _ => "服务检查失败，请稍后重试。",
            },
        ));
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| error(StatusCode::BAD_GATEWAY, "response", "模型列表读取失败"))?
    {
        if bytes.len() + chunk.len() > 262144 {
            return Err(error(StatusCode::BAD_GATEWAY, "response", "模型列表过大"));
        }
        bytes.extend_from_slice(&chunk);
    }
    let value: Value = serde_json::from_slice(&bytes).map_err(|_| {
        error(
            StatusCode::BAD_GATEWAY,
            "response",
            "服务未返回有效模型列表",
        )
    })?;
    let items = if input.protocol == Protocol::Gemini {
        &value["models"]
    } else {
        &value["data"]
    };
    let Some(items) = items.as_array() else {
        return Err(error(
            StatusCode::BAD_GATEWAY,
            "response",
            "服务未返回模型列表",
        ));
    };
    let models: Vec<&str> = items
        .iter()
        .filter_map(|v| {
            v["id"].as_str().or_else(|| {
                v["name"]
                    .as_str()
                    .map(|s| s.strip_prefix("models/").unwrap_or(s))
            })
        })
        .filter(|id| id.len() <= 256 && !id.chars().any(char::is_control))
        .take(500)
        .collect();
    Ok(Json(
        json!({"supported":true,"models":models,"message":"已连接并读取模型列表；未发送模型请求。"}),
    ))
}
