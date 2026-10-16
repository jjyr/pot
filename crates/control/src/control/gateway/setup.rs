//! User-authored API connections. Secrets never enter profile snapshots or responses.
use super::*;

#[derive(Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
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
    #[serde(default)]
    max_output_tokens: Option<u64>,
    #[serde(default)]
    context_window: Option<u64>,
    #[serde(default)]
    catalog_provider: Option<String>,
    #[serde(default)]
    reasoning_effort: Option<String>,
    #[serde(default)]
    models: Vec<String>,
}
fn default_profile() -> String {
    "apot-default".into()
}
fn default_name() -> String {
    "API".into()
}

pub async fn get(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let profiles = agent::configured_profiles(&s).map_err(internal)?;
    let mut connections = Vec::new();
    for profile in profiles {
        // Editing a connection must remain available even if refreshed metadata
        // makes a previously saved override exceed a newly advertised limit.
        let models = saved_models(&s, &profile.id).map_err(internal)?;
        let spec = profile.resolve().map_err(internal)?;
        if spec.auth_ref.starts_with("provider:")
            && let Ok(p) = stored_provider(&s, &spec)
        {
            connections.push(json!({"profile_id": profile.id, "endpoint":p.endpoint,
                    "name":profile.name,"default_model":spec.model,"model":spec.model,"models":models,"revision":profile.revision,"protocol":p.protocol,"has_key":!p.secret_ref.is_empty(),"model_settings":profile.model_settings,"reasoning_effort":profile.params.reasoning_effort}));
        }
    }
    Ok(Json(
        json!({"connections":connections,"default_provider_id":agent::default_provider(&s).map_err(internal)?,"directory":s.control_config.models.options(),"model_defaults":s.control_config.models.model_defaults()}),
    ))
}

pub(crate) fn saved_models(s: &Service, id: &str) -> Result<Vec<String>> {
    let owner = agent::configuration_owner(s);
    owner
        .conversation
        .inner
        .lock()
        .unwrap()
        .store
        .get(&format!("providers.models:{id}"))?
        .map(|v| serde_json::from_str(&v).map_err(Into::into))
        .unwrap_or_else(|| Ok(vec![]))
}

#[derive(Deserialize)]
pub struct DefaultProvider {
    provider_id: String,
}
pub async fn set_default(
    State(s): State<Service>,
    h: HeaderMap,
    Json(input): Json<DefaultProvider>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let s = agent::configuration_owner(&s);
    if !agent::configured_profiles(&s)
        .map_err(internal)?
        .iter()
        .any(|p| p.id == input.provider_id)
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "provider",
            "Choose a configured Provider",
        ));
    }
    s.conversation
        .inner
        .lock()
        .unwrap()
        .store
        .set("providers.default", &input.provider_id)
        .map_err(internal)?;
    s.changes().notify("settings");
    Ok(Json(json!({"saved":true})))
}

pub async fn save(
    State(s): State<Service>,
    h: HeaderMap,
    Json(input): Json<Setup>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let s = agent::configuration_owner(&s);
    let mut h = h;
    h.insert(
        "authorization",
        format!("Bearer {}", s.token).parse().unwrap(),
    );
    if input.protocol == Protocol::Unknown {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "protocol",
            "Select Chat Completions, Responses, Anthropic Messages or Gemini",
        ));
    }
    static SAVE: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());
    let _guard = SAVE.lock().await;
    use sha2::Digest;
    let fingerprint: String =
        sha2::Sha256::digest(serde_json::to_vec(&input).map_err(|e| internal(e.into()))?)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
    let receipt_key = format!("providers.save:{}", input.request_id);
    if let Some(saved) = s
        .conversation
        .inner
        .lock()
        .unwrap()
        .store
        .get(&receipt_key)
        .map_err(internal)?
    {
        if saved != fingerprint {
            return Err(error(
                StatusCode::CONFLICT,
                "provider_request",
                "Conflicting Provider save request",
            ));
        }
        return Ok(Json(json!({"saved":true,"duplicate":true})));
    }

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
            "Enter a full HTTP or HTTPS model endpoint",
        )
    })?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "endpoint",
            "Endpoint must use HTTP or HTTPS without embedded credentials or a fragment",
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
        let profiles = agent::configured_profiles(&s).map_err(internal)?;
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
    let previous = agent::configured_profiles(&s)
        .map_err(internal)?
        .into_iter()
        .find(|p| p.id == input.profile_id);
    if previous.as_ref().map_or(0, |p| p.revision) != input.expected_revision {
        return Err(error(
            StatusCode::CONFLICT,
            "provider_revision",
            "Provider changed; reload before saving",
        ));
    }
    let previous_route = previous
        .as_ref()
        .and_then(|p| p.resolve().ok())
        .and_then(|spec| stored_provider(&s, &spec).ok());
    let id = previous_route
        .filter(|p| {
            p.endpoint == input.endpoint
                && p.protocol == input.protocol
                && credential(&s, p).ok().as_deref() == Some(key.as_str())
        })
        .map(|p| p.id)
        .unwrap_or_else(|| format!("api-{request}"));
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
    let mut profile = previous.unwrap_or_else(|| agent::Profile::api(&config));
    if input.models.len() > 500
        || input
            .models
            .iter()
            .any(|m| m.is_empty() || m.len() > 256 || m.chars().any(char::is_control))
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "models",
            "Invalid Provider model IDs",
        ));
    }
    let mut models = input.models;
    models.push(input.model.clone());
    models.sort();
    models.dedup();
    profile.model = input.model;
    profile.params.reasoning_effort = input
        .reasoning_effort
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty());
    profile.model_settings = Some(pot_core::ModelSettings {
        catalog_provider: input.catalog_provider.filter(|id| !id.is_empty()),
        context_window: input.context_window,
        max_output_tokens: input.max_output_tokens,
    });
    profile.id = input.profile_id.clone();
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
    {
        let mut inner = s.conversation.inner.lock().unwrap();
        let mut profiles = agent::profiles(&inner.store).map_err(internal)?;
        if profiles
            .iter()
            .find(|p| p.id == profile.id)
            .map_or(0, |p| p.revision)
            != input.expected_revision
        {
            return Err(error(
                StatusCode::CONFLICT,
                "provider_revision",
                "Provider changed; reload before saving",
            ));
        }
        profiles.retain(|p| p.id != profile.id);
        profiles.push(profile);
        let default = inner.store.get("providers.default").map_err(internal)?;
        let tx = inner
            .store
            .0
            .transaction()
            .map_err(|e| internal(e.into()))?;
        agent::set(
            &tx,
            agent::PROFILES,
            &serde_json::to_string(&profiles).map_err(|e| internal(e.into()))?,
        )
        .map_err(internal)?;
        agent::set(
            &tx,
            &format!("providers.models:{}", input.profile_id),
            &serde_json::to_string(&models).map_err(|e| internal(e.into()))?,
        )
        .map_err(internal)?;
        if default.is_none() {
            agent::set(&tx, "providers.default", &input.profile_id).map_err(internal)?;
        }
        agent::set(&tx, &receipt_key, &fingerprint).map_err(internal)?;
        tx.commit().map_err(|e| internal(e.into()))?;
    }
    s.changes().notify("settings");
    Ok(Json(json!({"saved":true})))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    #[tokio::test]
    async fn http_gateway_and_free_text_effort_round_trip_and_clear() {
        let s = crate::tests::service();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        for endpoint in [
            "http://localhost:8080/v1/responses",
            "http://192.168.1.20:8080/v1/responses",
            "http://[::1]:8080/v1/responses",
        ] {
            let profile_id = id();
            for (revision, effort, expected) in [
                (
                    0,
                    " provider-specific-mode ",
                    Some("provider-specific-mode"),
                ),
                (1, "   ", None),
            ] {
                let input = json!({"request_id":id(),"expected_revision":revision,"profile_id":profile_id,"name":"HTTP gateway","endpoint":endpoint,"protocol":"openai_responses","model":"custom-model","api_key":"fixture-secret","reasoning_effort":effort});
                let _ = save(
                    State(s.clone()),
                    h.clone(),
                    Json(serde_json::from_value(input).unwrap()),
                )
                .await
                .unwrap();
                let public = get(State(s.clone()), h.clone()).await.unwrap().0;
                let connection = public["connections"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .find(|p| p["profile_id"] == profile_id)
                    .unwrap();
                assert_eq!(connection["reasoning_effort"], json!(expected));
                let profile = agent::configured_profiles(&s)
                    .unwrap()
                    .into_iter()
                    .find(|p| p.id == profile_id)
                    .unwrap();
                let spec = resolve(&s, &profile).unwrap();
                assert_eq!(spec.params.reasoning_effort.as_deref(), expected);
                assert_eq!(provider(&s, &spec).unwrap().endpoint, endpoint);
            }
        }
    }
    #[tokio::test]
    async fn custom_api_keeps_its_identity_and_uses_model_defaults() {
        let s = crate::tests::service();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let request = json!({"request_id":id(),"expected_revision":0,"profile_id":"custom","name":"Custom API","endpoint":"https://api.anthropic.com/v1/messages","protocol":"anthropic_messages","model":"claude-sonnet-4-5","api_key":"fixture-secret","catalog_provider":null});
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request).unwrap()),
        )
        .await
        .unwrap();
        let public = get(State(s.clone()), h).await.unwrap().0;
        let connection = public["connections"]
            .as_array()
            .unwrap()
            .iter()
            .find(|p| p["profile_id"] == "custom")
            .unwrap();
        assert_eq!(
            connection["model_settings"]["catalog_provider"],
            Value::Null
        );
        assert_eq!(
            connection["endpoint"],
            "https://api.anthropic.com/v1/messages"
        );
        assert_eq!(
            connection["model_settings"]["max_output_tokens"],
            Value::Null
        );
        assert_eq!(
            public["model_defaults"]["claude-sonnet-4-5"]["output"],
            64000
        );
        let profile = agent::configured_profiles(&s)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "custom")
            .unwrap();
        let config = resolve(&s, &profile).unwrap();
        assert_eq!(config.context_window, 200000);
        assert_eq!(config.params.max_output_tokens, 64000);
    }
    #[tokio::test]
    async fn defaults_overrides_and_provider_updates_do_not_change_existing_assistants() {
        let s = crate::tests::service();
        let h = crate::control::tasks::tests::headers();
        let request = |revision, context: Value, output: Value| json!({"request_id":id(),"expected_revision":revision,"profile_id":"fixture","name":"Claude","endpoint":"https://api.anthropic.com/v1/messages","protocol":"anthropic_messages","model":"claude-sonnet-4-5","api_key":"fixture-secret","catalog_provider":"anthropic","context_window":context,"max_output_tokens":output});
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(0, Value::Null, Value::Null)).unwrap()),
        )
        .await
        .unwrap();
        let base = agent::configured_profiles(&s)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "fixture")
            .unwrap();
        let inherited = resolve(&s, &base).unwrap();
        assert_eq!(inherited.context_window, 200000);
        assert_eq!(inherited.params.max_output_tokens, 64000);
        let before = agent::current(&s.conversation.inner.lock().unwrap().store).unwrap();
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(1, json!(64000), json!(4096))).unwrap()),
        )
        .await
        .unwrap();
        assert_eq!(
            agent::current(&s.conversation.inner.lock().unwrap().store).unwrap(),
            before
        );
        let base = agent::configured_profiles(&s)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "fixture")
            .unwrap();
        let overridden = resolve(&s, &base).unwrap();
        assert_eq!(overridden.context_window, 64000);
        assert_eq!(overridden.params.max_output_tokens, 4096);
        assert_eq!(
            overridden.provider, inherited.provider,
            "model defaults do not reset connection quotas"
        );
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(request(2, Value::Null, Value::Null)).unwrap()),
        )
        .await
        .unwrap();
        let base = agent::configured_profiles(&s)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "fixture")
            .unwrap();
        assert_eq!(resolve(&s, &base).unwrap().context_window, 200000);
        assert_eq!(overridden.params.max_output_tokens, 4096);
        assert!(
            save(
                State(s),
                h,
                Json(serde_json::from_value(request(3, json!(1000), json!(2000))).unwrap())
            )
            .await
            .is_err()
        );
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
        let before = agent::configured_profiles(&s).unwrap();
        let response=check(State(s.clone()),h,Json(serde_json::from_value(json!({"endpoint":format!("http://localhost:{}/v1/responses", address.port()),"api_key":"check-only","protocol":"openai_responses"})).unwrap())).await.unwrap().0;
        assert_eq!(response["models"], json!(["available-model"]));
        assert_eq!(agent::configured_profiles(&s).unwrap(), before);
        assert!(!response.to_string().contains("check-only"));
        host.abort();
    }
    #[tokio::test]
    async fn provider_setup_is_private_idempotent_and_never_creates_a_thread() {
        let dir = std::env::temp_dir().join(format!("pot-provider-only-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.execution = None;
        s.control_config = Arc::new(crate::model_config::ControlConfig::new(dir.clone()));
        let h = crate::control::tasks::tests::headers();
        let input = json!({"request_id":id(),"profile_id":"first","name":"First","expected_revision":0,"endpoint":"https://example.com/v1/responses","protocol":"openai_responses","model":"opus5.5","models":["other-model"],"api_key":"fixture-private-key"});
        assert!(
            save(
                State(s.clone()),
                HeaderMap::new(),
                Json(serde_json::from_value(input.clone()).unwrap())
            )
            .await
            .is_err()
        );
        for _ in 0..2 {
            let _ = save(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(input.clone()).unwrap()),
            )
            .await
            .unwrap();
        }
        assert!(
            agent::current(&s.conversation.inner.lock().unwrap().store)
                .unwrap()
                .is_none()
        );
        let public = get(State(s.clone()), h.clone()).await.unwrap().0;
        assert_eq!(public["default_provider_id"], "first");
        assert_eq!(public["connections"][0]["default_model"], "opus5.5");
        assert_eq!(
            public["connections"][0]["models"],
            json!(["opus5.5", "other-model"])
        );
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
        conflict["api_key"] = json!("changed");
        assert!(
            save(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(conflict).unwrap())
            )
            .await
            .is_err()
        );
        let profile = agent::configured_profiles(&s)
            .unwrap()
            .into_iter()
            .find(|p| p.id == "first")
            .unwrap();
        assert_eq!(
            credential(
                &s,
                &stored_provider(&s, &profile.resolve().unwrap()).unwrap()
            )
            .unwrap(),
            "fixture-private-key"
        );
        let mut update = input;
        update["request_id"] = json!(id());
        update["expected_revision"] = json!(1);
        update["model"] = json!("new-default");
        update["api_key"] = json!("");
        let _ = save(
            State(s.clone()),
            h.clone(),
            Json(serde_json::from_value(update).unwrap()),
        )
        .await
        .unwrap();
        assert!(
            agent::current(&s.conversation.inner.lock().unwrap().store)
                .unwrap()
                .is_none()
        );
        assert!(
            set_default(
                State(s.clone()),
                h.clone(),
                Json(DefaultProvider {
                    provider_id: "missing".into()
                })
            )
            .await
            .is_err()
        );
        let _ = set_default(
            State(s.clone()),
            h,
            Json(DefaultProvider {
                provider_id: "first".into(),
            }),
        )
        .await
        .unwrap();
        assert_eq!(
            agent::default_provider(&s).unwrap().as_deref(),
            Some("first")
        );
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn connection_check_rejects_unsafe_endpoints_before_networking() {
        let s = crate::tests::service();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        for endpoint in [
            "ftp://example.com/v1/responses",
            "not a URL",
            "https://example.com/v1/responses#fragment",
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
    let mut url = reqwest::Url::parse(&input.endpoint).map_err(|_| {
        error(
            StatusCode::BAD_REQUEST,
            "endpoint",
            "请输入完整 HTTP 或 HTTPS 地址",
        )
    })?;
    if !matches!(url.scheme(), "http" | "https")
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
            "连接检查需要不含凭据或查询参数的 HTTP 或 HTTPS 地址",
        ));
    }
    let mut key = input.api_key;
    if key.is_empty() {
        let profiles = agent::configured_profiles(&s).map_err(internal)?;
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
