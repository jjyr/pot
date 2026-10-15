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
    #[serde(default)]
    max_output_tokens: Option<u64>,
    #[serde(default)]
    context_window: Option<u64>,
    #[serde(default)]
    catalog_provider: Option<String>,
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

pub async fn get(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let profiles =
        agent::profiles(&s.conversation.inner.lock().unwrap().store).map_err(internal)?;
    let mut connections = Vec::new();
    for profile in profiles {
        // Editing a connection must remain available even if refreshed metadata
        // makes a previously saved override exceed a newly advertised limit.
        let spec = profile.resolve().map_err(internal)?;
        if spec.auth_ref.starts_with("provider:")
            && let Ok(p) = stored_provider(&s, &spec)
        {
            connections.push(json!({"profile_id": profile.id, "endpoint":p.endpoint,
                    "model":spec.model,"protocol":p.protocol,"has_key":!p.secret_ref.is_empty(),"model_settings":profile.model_settings}));
        }
    }
    let thread = agent::current(&s.conversation.inner.lock().unwrap().store).map_err(internal)?;
    let connection = thread.and_then(|b| {
        connections
            .iter()
            .find(|c| c["profile_id"] == b.config.source_profile_id)
            .cloned()
    });
    Ok(Json(
        json!({"connection":connection,"connections":connections,"directory":s.control_config.models.options()}),
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
        let profiles =
            agent::profiles(&s.conversation.inner.lock().unwrap().store).map_err(internal)?;
        if let Some(profile) = profiles.iter().find(|p| p.id == input.profile_id)
            && let Ok(previous) = stored_provider(&s, &profile.resolve().map_err(internal)?)
            && previous.endpoint == input.endpoint
        {
            key = credential(&s, &previous).map_err(internal)?;
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
        endpoint: input.endpoint.clone(),
        allowed_hosts: vec![url.host_str().unwrap().into()],
        secret_ref: format!("provider/{id}"),
        protocol: input.protocol,
    };
    let config = execution::Config {
        model: input.model.clone(),
        max_tokens: input.max_output_tokens.unwrap_or(8192),
        ..Default::default()
    };
    let previous = agent::profiles(&s.conversation.inner.lock().unwrap().store)
        .map_err(internal)?
        .into_iter()
        .find(|p| p.id == input.profile_id);
    let mut profile = previous.unwrap_or_else(|| agent::Profile::api(&config));
    profile.model = input.model;
    profile.model_settings = Some(pot_core::ModelSettings {
        catalog_provider: input
            .catalog_provider
            .filter(|id| !id.is_empty())
            .or_else(|| s.control_config.models.infer(&input.endpoint)),
        context_window: input.context_window,
        max_output_tokens: input.max_output_tokens,
    });
    profile.id = input.profile_id;
    profile.name = input.name;
    profile.revision = input
        .expected_revision
        .checked_add(1)
        .ok_or_else(|| error(StatusCode::BAD_REQUEST, "revision", "Invalid revision"))?;
    profile.provider = id.clone();
    profile.auth_ref = format!("provider:{id}");
    s.control_config
        .models
        .apply(&profile)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "profile", &e.to_string()))?;
    s.control_config
        .registry()
        .map_err(internal)?
        .save(&next, key.as_bytes())
        .map_err(internal)?;
    let settings=serde_json::from_value(json!({"profile":profile,"expected_revision":input.expected_revision,"request_id":input.request_id,"follow_main":input.follow_main,"follow_worker":false})).map_err(|e|internal(e.into()))?;
    agent::settings::settings(State(s), h, Json(settings)).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    #[tokio::test]
    async fn model_limits_inherit_override_clear_and_pin_execution_snapshots() {
        let mut s = crate::tests::service();
        let root = std::env::temp_dir().join(format!("pot-model-settings-{}", id()));
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(root.clone()));
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let request = |revision, context: Value, output: Value, follow| json!({"request_id":id(),"expected_revision":revision,"profile_id":"fixture","name":"Claude","endpoint":"https://api.anthropic.com/v1/messages","protocol":"anthropic_messages","model":"claude-sonnet-4-5","api_key":"fixture-secret","catalog_provider":"anthropic","context_window":context,"max_output_tokens":output,"follow_main":follow});
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(0, Value::Null, Value::Null, true)).unwrap()),
        )
        .await
        .unwrap();
        let initial = agent::thread(&s).unwrap();
        assert_eq!(initial.config.context_window, 200000);
        assert_eq!(initial.config.params.max_output_tokens, 8192);
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(1, json!(64000), json!(4096), false)).unwrap()),
        )
        .await
        .unwrap();
        assert_eq!(agent::thread(&s).unwrap().config, initial.config);
        let public = get(State(s.clone()), h.clone()).await.unwrap().0;
        assert_eq!(
            public["connections"][0]["model_settings"]["context_window"],
            64000
        );
        assert_eq!(
            public["connections"][0]["model_settings"]["max_output_tokens"],
            4096
        );
        assert!(!public.to_string().contains("fixture-secret"));
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(2, json!(64000), json!(4096), true)).unwrap()),
        )
        .await
        .unwrap();
        assert_eq!(agent::thread(&s).unwrap().config.context_window, 64000);
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(3, Value::Null, Value::Null, true)).unwrap()),
        )
        .await
        .unwrap();
        assert_eq!(agent::thread(&s).unwrap().config.context_window, 200000);
        let pinned = agent::thread(&s).unwrap().config;
        let mut catalog: Value =
            serde_json::from_str(include_str!("../../../assets/model-directory.json")).unwrap();
        catalog["providers"]["anthropic"]["models"]["claude-sonnet-4-5"]["limit"]["context"] =
            json!(300000);
        std::fs::create_dir_all(root.join("models-dev")).unwrap();
        std::fs::write(
            root.join("models-dev/catalog.json"),
            serde_json::to_vec(&catalog).unwrap(),
        )
        .unwrap();
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(root.clone()));
        assert_eq!(agent::thread(&s).unwrap().config, pinned);
        agent::settings::refresh_model_defaults(&s).unwrap();
        assert_eq!(agent::thread(&s).unwrap().config.context_window, 300000);
        assert_eq!(pinned.context_window, 200000);
        assert!(
            save(
                State(s.clone()),
                h,
                Json(serde_json::from_value(request(4, json!(1000), json!(2000), true)).unwrap())
            )
            .await
            .is_err()
        );
        assert_eq!(agent::thread(&s).unwrap().config.context_window, 300000);
        let _ = std::fs::remove_dir_all(root);
    }
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
        let before = agent::profiles(&s.conversation.inner.lock().unwrap().store).unwrap();
        let response=check(State(s.clone()),h,Json(serde_json::from_value(json!({"endpoint":format!("http://{address}/v1/responses"),"api_key":"check-only","protocol":"openai_responses"})).unwrap())).await.unwrap().0;
        assert_eq!(response["models"], json!(["available-model"]));
        assert_eq!(
            agent::profiles(&s.conversation.inner.lock().unwrap().store).unwrap(),
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
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(s.data.clone()));
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
            agent::current(&s.conversation.inner.lock().unwrap().store)
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
        let active = agent::thread(&s).unwrap();
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(make("first", 1, "a2", "", false)).unwrap()),
        )
        .await
        .unwrap();
        assert_eq!(agent::thread(&s).unwrap(), active);
        assert!(
            s.conversation
                .inner
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
        let first = agent::profiles(&s.conversation.inner.lock().unwrap().store)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "first")
            .unwrap();
        assert_eq!(
            credential(&s, &stored_provider(&s, &first.resolve().unwrap()).unwrap()).unwrap(),
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
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(s.data.clone()));
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
        let current = agent::thread(&s).unwrap();
        assert_eq!(current.config.model, "custom-model");
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
            std::fs::metadata(dir.join("control.sqlite"))
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
        assert_eq!(agent::thread(&s).unwrap(), current);
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
        let updated = agent::thread(&s).unwrap();
        assert_ne!(updated.config.provider, current.config.provider);
        assert_eq!(updated.generation, current.generation + 1);
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
        let profiles =
            agent::profiles(&s.conversation.inner.lock().unwrap().store).map_err(internal)?;
        if let Some(profile) = profiles.iter().find(|p| p.id == input.profile_id)
            && let Ok(previous) = stored_provider(&s, &profile.resolve().map_err(internal)?)
            && previous.endpoint == input.endpoint
        {
            key = credential(&s, &previous).map_err(internal)?;
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
