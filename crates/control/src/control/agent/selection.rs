//! Concrete per-assistant model choices, independent of shared Provider defaults.
use super::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Selection {
    pub provider_id: String,
    pub model: String,
    #[serde(default)]
    pub context_window: Option<u64>,
    #[serde(default)]
    pub max_output_tokens: Option<u64>,
    #[serde(default)]
    pub reasoning_effort: Option<String>,
}
impl Selection {
    pub(crate) fn profile(&self, s: &Service) -> Result<Profile> {
        let mut profile = configured_profiles(s)?
            .into_iter()
            .find(|p| p.id == self.provider_id)
            .ok_or_else(|| anyhow!("Provider unavailable"))?;
        let model = self.model.trim().to_owned();
        let changed = model != profile.model;
        profile.model = model;
        let settings = profile.model_settings.get_or_insert_with(Default::default);
        if changed {
            settings.context_window = None;
            settings.max_output_tokens = None;
        }
        if self.context_window.is_some() {
            settings.context_window = self.context_window;
        }
        if self.max_output_tokens.is_some() {
            settings.max_output_tokens = self.max_output_tokens;
        }
        if let Some(effort) = self
            .reasoning_effort
            .as_ref()
            .map(|s| s.trim())
            .filter(|s| !s.is_empty())
        {
            profile.params.reasoning_effort = Some(effort.into());
        }
        Ok(profile)
    }
    pub(crate) fn resolve(&self, s: &Service) -> Result<(Profile, ResolvedSpec)> {
        let profile = self.profile(s)?;
        let spec = crate::gateway::resolve(s, &profile)?;
        crate::gateway::validate_spec(s, &spec)?;
        Ok((profile, spec))
    }
}
pub(crate) fn current_selection(s: &Service) -> Result<Option<Selection>> {
    s.conversation
        .inner
        .lock()
        .unwrap()
        .store
        .get("assistant.model_selection")?
        .map(|v| serde_json::from_str(&v).map_err(Into::into))
        .transpose()
}

#[cfg(test)]
mod tests {
    use super::*;
    async fn provider(s: &Service, id: &str, model: &str, revision: u64) {
        let mut headers = HeaderMap::new();
        headers.insert(
            "authorization",
            format!("Bearer {}", s.token).parse().unwrap(),
        );
        let _ = crate::gateway::setup::save(State(s.clone()), headers, Json(serde_json::from_value(json!({
            "request_id":uuid::Uuid::new_v4().to_string(),"expected_revision":revision,"profile_id":id,"name":id,
            "endpoint":"https://example.invalid/v1/responses","protocol":"openai_responses","model":model,"models":[model,"task-model"],"api_key":"fixture-private"
        })).unwrap())).await.unwrap();
    }
    fn hub() -> Arc<crate::assistants::Hub> {
        let mut root = crate::tests::service();
        root.data = root.control_config.root().to_path_buf();
        root.workspace = root.data.join("workspace");
        root.conversation.inner.lock().unwrap().store =
            Store::open(&root.data.join("pot-v2.sqlite")).unwrap();
        let hub = Arc::new(crate::assistants::Hub::open(root.clone()).unwrap());
        *root.agent_service.resources.assistants.lock().unwrap() = Arc::downgrade(&hub);
        hub
    }
    #[tokio::test]
    async fn shared_provider_defaults_and_autonomous_routes_preserve_assistant_choices() {
        let hub = hub();
        let root = hub.root.clone();
        provider(&root, "first", "opus5.5", 0).await;
        assert!(hub.definitions().unwrap().is_empty());
        assert!(
            current(&root.conversation.inner.lock().unwrap().store)
                .unwrap()
                .is_none()
        );
        assert_eq!(default_provider(&root).unwrap().as_deref(), Some("first"));
        let config = Selection {
            provider_id: "first".into(),
            model: "opus5.5".into(),
            ..Default::default()
        };
        let a = hub
            .create_configured("create-a", "A", &config, "Use task-model for coding.")
            .unwrap();
        let b = hub
            .create_configured(
                "create-b",
                "B",
                &Selection {
                    model: "other-model".into(),
                    ..config.clone()
                },
                "",
            )
            .unwrap();
        let sa = hub.service(&a.id).unwrap();
        let sb = hub.service(&b.id).unwrap();
        let original = thread(&sa).unwrap().config;
        assert_eq!(thread(&sb).unwrap().config.model, "other-model");
        assert_eq!(
            hub.create_configured("create-a", "A", &config, "Use task-model for coding.")
                .unwrap()
                .id,
            a.id
        );
        assert!(
            hub.create_configured(
                "create-a",
                "A",
                &Selection {
                    model: "changed".into(),
                    ..config.clone()
                },
                "Use task-model for coding."
            )
            .is_err()
        );
        provider(&sb, "second", "small-model", 0).await;
        assert_eq!(configured_profiles(&sa).unwrap().len(), 2);
        let catalog = catalog::inspect(&sa).unwrap();
        assert!(
            catalog["profiles"]
                .as_array()
                .unwrap()
                .iter()
                .all(|p| p["eligible"] == true)
        );
        let (chosen, route) = routing::resolve(
            &sa,
            Some("second"),
            Some("task-model"),
            routing::Source::Tool,
            &TaskRequirements::default(),
        )
        .unwrap();
        assert_eq!(chosen.model, "task-model");
        assert_eq!(route.profile, "second");
        assert_eq!(
            routing::resolve(
                &sa,
                None,
                None,
                routing::Source::Tool,
                &TaskRequirements::default()
            )
            .unwrap()
            .0,
            original
        );
        provider(&root, "first", "new-provider-default", 1).await;
        settings::refresh_model_defaults(&sa).unwrap();
        assert_eq!(thread(&sa).unwrap().config, original);
        assert_eq!(thread(&sb).unwrap().config.model, "other-model");
        let c = hub.create("create-c", "C").unwrap();
        assert_eq!(
            thread(&hub.service(&c.id).unwrap()).unwrap().config.model,
            "new-provider-default"
        );
        assert!(!catalog.to_string().contains("fixture-private"));
        root.stop.send_replace(true);
        hub.shutdown().await;
    }
    #[tokio::test]
    async fn assistant_configuration_saves_validate_before_editing_and_retries_are_idempotent() {
        let hub = hub();
        let root = hub.root.clone();
        provider(&root, "first", "opus5.5", 0).await;
        let config = Selection {
            provider_id: "first".into(),
            model: "opus5.5".into(),
            ..Default::default()
        };
        let a = hub.create_configured("create", "A", &config, "").unwrap();
        let service = hub.service(&a.id).unwrap();
        let rules = hub.rules(&a.id).unwrap();
        let mut request = json!({"request_id":"edit","name":"Renamed","expected_revision":a.revision,"text":"Coding uses task-model.","rules_revision":rules["revision"],"configuration":{"provider_id":"first","model":"chat-model","context_window":64000,"max_output_tokens":100000}});
        assert!(hub.edit_configured(&a.id, &request).is_err());
        assert_eq!(hub.rules(&a.id).unwrap(), rules);
        assert_eq!(hub.definitions().unwrap()[0].name, "A");
        request["configuration"]["max_output_tokens"] = json!(1024);
        let saved = hub.edit_configured(&a.id, &request).unwrap();
        assert_eq!(saved["definition"]["name"], "Renamed");
        assert_eq!(thread(&service).unwrap().config.model, "chat-model");
        assert_eq!(
            thread(&service).unwrap().config.params.max_output_tokens,
            1024
        );
        assert_eq!(configured_profiles(&service).unwrap()[0].model, "opus5.5");
        assert_eq!(hub.edit_configured(&a.id, &request).unwrap(), saved);
        request["configuration"]["model"] = json!("conflicting");
        assert!(hub.edit_configured(&a.id, &request).is_err());
        assert_eq!(thread(&service).unwrap().config.model, "chat-model");
        root.stop.send_replace(true);
        hub.shutdown().await;
    }
}
