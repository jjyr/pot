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
    #[serde(default = "output_limit")]
    max_output_tokens: u64,
}
fn output_limit() -> u64 {
    8192
}

pub async fn get(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let binding = agent::current(&s.inner.lock().unwrap().store).map_err(internal)?;
    let Some(binding) = binding.filter(|b| b.spec.auth_ref.starts_with("providers.json:")) else {
        return Ok(Json(json!({"connection":null})));
    };
    let p = provider(&s, &binding.spec).map_err(internal)?;
    Ok(Json(
        json!({"connection":{"endpoint":p.endpoint,"model":binding.spec.model,"protocol":p.protocol,"has_key":!p.api_key.is_empty()}}),
    ))
}

pub async fn save(
    State(s): State<Service>,
    h: HeaderMap,
    Json(input): Json<Setup>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
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
    if !(url.scheme() == "https"
        || (cfg!(test) && url.scheme() == "http" && url.host_str() == Some("127.0.0.1")))
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
        let current = agent::current(&s.inner.lock().unwrap().store).map_err(internal)?;
        if let Some(binding) = current.filter(|b| b.spec.auth_ref.starts_with("providers.json:")) {
            let previous = provider(&s, &binding.spec).map_err(internal)?;
            if previous.endpoint == input.endpoint {
                key = previous.api_key;
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
    let config = pi::Config {
        model: input.model.clone(),
        max_tokens: input.max_output_tokens,
        ..Default::default()
    };
    let mut profile = agent::Profile::legacy(&config);
    profile.id = "api-default".into();
    profile.name = "API".into();
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
        pi::private_json(
            &path,
            &serde_json::to_value(&all).map_err(|e| internal(e.into()))?,
        )
        .map_err(internal)?;
    }
    let settings=serde_json::from_value(json!({"profile":profile,"expected_revision":input.expected_revision,"request_id":input.request_id,"follow_main":true})).map_err(|e|internal(e.into()))?;
    agent::migration::settings(State(s), h, Json(settings)).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;
    #[tokio::test]
    async fn api_setup_is_private_idempotent_and_does_not_need_legacy_pi() {
        let dir = std::env::temp_dir().join(format!("pot-api-setup-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.pi = None;
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
        assert_eq!(agent::pi_config(&s).unwrap().max_requests_per_turn, 100);
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
        save(
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
